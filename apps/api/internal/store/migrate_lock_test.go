package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests exist because the failure they describe is invisible to a single-process suite: it needs two
// database opens to happen at overlapping moments. The in-process version below is the cheap one that runs
// everywhere; `scripts/fleet-start.py` starts real processes against one database and is the end-to-end
// proof.

// concurrentOpens opens the store from `n` goroutines released together, and reports every error.
func concurrentOpens(t *testing.T, driver, dsn string, n int, opts Options) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			db, err := OpenWith(driver, dsn, opts)
			errs[index] = err
			if err == nil {
				_ = db.Close()
			}
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

func requireNoErrors(t *testing.T, errs []error) {
	t.Helper()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("open %d failed: %v", index, err)
		}
	}
}

// TestConcurrentOpensOnSQLiteApplyEachMigrationOnce covers the development engine. SQLite serialises writers,
// but "serialised" is not "exclusive for the whole run": the lock is released at every commit, so without an
// flock two processes interleave between migrations and the second fails on an already-inserted ledger row.
func TestConcurrentOpensOnSQLiteApplyEachMigrationOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	errs := concurrentOpens(t, "sqlite3", path, 6, Options{})
	requireNoErrors(t, errs)

	db, err := Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	assertEachMigrationRecordedOnce(t, db.DB)
}

// TestConcurrentOpensOnPostgresApplyEachMigrationOnce is the real one: this is the engine a fleet runs, and
// the reproduction was 3 of 4 replicas exiting with
// `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`.
func TestConcurrentOpensOnPostgresApplyEachMigrationOnce(t *testing.T) {
	dsn := os.Getenv("EDUMART_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set EDUMART_TEST_POSTGRES_DSN to exercise the PostgreSQL migration lock")
	}
	dsn = isolatedDatabase(t, dsn)

	// Eight, not two: the lock has to hold under a startup storm, and a two-goroutine version can pass by
	// luck on a fast machine.
	errs := concurrentOpens(t, "postgres", dsn, 8, Options{})
	requireNoErrors(t, errs)

	db, err := Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	assertEachMigrationRecordedOnce(t, db.DB)
}

func assertEachMigrationRecordedOnce(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT version, count(*) FROM schema_migrations GROUP BY version`)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer func() { _ = rows.Close() }()
	seen := 0
	for rows.Next() {
		var version string
		var count int
		if err := rows.Scan(&version, &count); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if count != 1 {
			t.Fatalf("migration %s recorded %d times", version, count)
		}
		seen++
	}
	if seen == 0 {
		// A green test over an empty ledger would be a test of nothing.
		t.Fatal("no migrations were recorded, so the concurrency check proved nothing")
	}
}

// TestMigrateOnBootFalseLeavesTheSchemaAlone: a replica that may not migrate has to be a *replica*, not a
// second migrator. If this ever starts applying migrations, the fleet behaves differently from the runbook
// and nobody finds out until the day two deploys overlap.
func TestMigrateOnBootFalseLeavesTheSchemaAlone(t *testing.T) {
	off := false
	path := filepath.Join(t.TempDir(), "replica.db")

	db, err := OpenWith("sqlite3", path, Options{MigrateOnBoot: &off})
	if err != nil {
		t.Fatalf("open with migrations disabled: %v", err)
	}
	defer func() { _ = db.Close() }()

	if db.MigrateOnBoot() {
		t.Fatal("MigrateOnBoot reports true after being opened with it disabled")
	}
	status, err := db.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.UpToDate {
		t.Fatal("an unmigrated database reported itself up to date")
	}
	if len(status.Pending) == 0 || status.Pending[0] != "0001_init" {
		t.Fatalf("expected every migration to be pending, got %v", status.Pending)
	}

	// And the tables really are absent: a replica that quietly created them anyway would pass the status
	// check above while defeating the point.
	var count int
	if err := db.DB.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if count != 0 {
		t.Fatalf("a replica with migrations disabled created %d tables", count)
	}
}

// TestMigrateOnBootFalseRefusesAnEditedMigration: the checksum guard has to survive the move from "applied by
// this process" to "applied by a job", or a replica would happily serve a schema that no longer matches the
// migrations bundled in the binary.
func TestMigrateOnBootFalseRefusesAnEditedMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drift.db")
	first, err := Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := first.DB.Exec(
		`UPDATE schema_migrations SET checksum = 'deadbeef' WHERE version = '0001_init'`); err != nil {
		t.Fatalf("tamper with the ledger: %v", err)
	}
	_ = first.Close()

	off := false
	_, err = OpenWith("sqlite3", path, Options{MigrateOnBoot: &off})
	if err == nil {
		t.Fatal("a replica opened a database whose applied migration had been edited")
	}
	if !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("the error does not explain the problem: %v", err)
	}
}

// TestMissingLedgerIsPendingNotBroken: an operator reading `relation "schema_migrations" does not exist`
// concludes the database is broken. The truthful reading is "the migration job has not run", and readiness
// now says that in those words.
func TestMissingLedgerIsPendingNotBroken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	off := false
	db, err := OpenWith("sqlite3", path, Options{MigrateOnBoot: &off})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	status, err := db.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("a missing ledger was reported as an error: %v", err)
	}
	if status.UpToDate {
		t.Fatal("a database with no schema reported itself up to date")
	}
	if len(status.Pending) < 10 {
		t.Fatalf("expected every migration to be pending, got %d", len(status.Pending))
	}
}

// isolatedDatabase creates a throwaway database and returns a DSN pointing at it.
//
// The API tests already run against EDUMART_TEST_POSTGRES_DSN and wipe `public` before every test, and
// `go test ./...` runs packages in parallel. Sharing one database therefore means this package's migration
// tests and that package's reset race each other — the first full `-race` sweep showed up as
// `schema "public" already exists` and, worse, `relation "users" does not exist` halfway through 0002 in a
// test that had nothing to do with migrations. A per-test database makes the two packages independent
// instead of asking every future test in either package to remember the ordering.
func isolatedDatabase(t *testing.T, dsn string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse EDUMART_TEST_POSTGRES_DSN: %v", err)
	}
	name := fmt.Sprintf("edumart_lock_%d_%d", os.Getpid(), time.Now().UnixNano())

	connector, err := NewPostgresConnector(dsn)
	if err != nil {
		t.Fatalf("postgres connector: %v", err)
	}
	admin := sql.OpenDB(connector)
	defer func() { _ = admin.Close() }()
	if _, err := admin.Exec(`CREATE DATABASE ` + quoteIdent(name)); err != nil {
		// Not every role may create a database; say so instead of reporting a broken migration lock.
		t.Skipf("cannot create an isolated database (%v); run the suite against a role with CREATEDB", err)
	}
	t.Cleanup(func() {
		dropper, err := NewPostgresConnector(dsn)
		if err != nil {
			return
		}
		db := sql.OpenDB(dropper)
		defer func() { _ = db.Close() }()
		_, _ = db.Exec(`DROP DATABASE IF EXISTS ` + quoteIdent(name) + ` WITH (FORCE)`)
	})

	parsed.Path = "/" + name
	return parsed.String()
}

// quoteIdent quotes a PostgreSQL identifier for DDL that cannot take a placeholder.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
