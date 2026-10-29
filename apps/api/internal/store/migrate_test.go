package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

func open(t *testing.T, dsn string) *store.Store {
	t.Helper()
	db, err := store.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return db
}

func TestMigrationsApplyOnceAndReportReady(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "migrate.db")

	db := open(t, dsn)
	status, err := db.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	if !status.UpToDate || len(status.Pending) != 0 {
		t.Fatalf("a fresh database should be up to date, got %+v", status)
	}
	if len(status.Applied) == 0 {
		t.Fatal("expected at least one recorded migration")
	}
	first := len(status.Applied)
	for _, m := range status.Applied {
		if len(m.Checksum) != 64 {
			t.Fatalf("migration %s has no sha256 checksum: %q", m.Version, m.Checksum)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Booting again must be a no-op: this is what makes restarts and rolling deploys safe.
	again := open(t, dsn)
	defer again.Close()
	status, err = again.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("migration status after restart: %v", err)
	}
	if len(status.Applied) != first {
		t.Fatalf("restart re-recorded migrations: %d → %d", first, len(status.Applied))
	}
	if !status.UpToDate {
		t.Fatal("database should still be up to date")
	}
}

func TestEditingAnAppliedMigrationIsRefused(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "drift.db")

	db := open(t, dsn)
	// Simulate someone editing a migration file after it ran: the ledger keeps the old checksum.
	if _, err := db.DB.Exec(`UPDATE schema_migrations SET checksum = 'edited-on-the-server'`); err != nil {
		t.Fatalf("tamper ledger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := store.Open("sqlite3", dsn); err == nil {
		t.Fatal("expected the runner to refuse a changed migration, it started anyway")
	} else if !strings.Contains(err.Error(), "add a new migration") {
		t.Fatalf("error should say how to fix it, got: %v", err)
	}
}

func TestSchemaCoversTheDocumentedTables(t *testing.T) {
	db := open(t, filepath.Join(t.TempDir(), "schema.db"))
	defer db.Close()

	// The tables whose absence would be a real incident, not a cosmetic gap.
	required := []string{
		"users", "sessions", "password_reset_tokens", "signup_drafts",
		"questions", "question_versions", "assessment_sessions", "assessment_responses",
		"points_ledger_entries", "commission_ledger_entries",
		"orders", "provider_events", "entitlements",
	}
	for _, table := range required {
		var name string
		err := db.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q missing from the migrated schema: %v", table, err)
		}
	}
}

func TestAdoptsADatabaseThatPredatesTheLedger(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "adopt.db")

	// Reproduce a database created by the runner that predates the ledger: the baseline schema
	// exists, `schema_migrations` does not. Later migrations (0002 onwards) legitimately are not in
	// this database, which is why only the baseline has to replay cleanly.
	baseline, err := os.ReadFile(filepath.Join("migrations", "sqlite", "0001_init.sql"))
	if err != nil {
		t.Fatalf("read baseline migration: %v", err)
	}
	legacy, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := legacy.Exec(string(baseline)); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	// Booting must adopt it: the baseline's statements are idempotent, so re-running records what is
	// already there, and the migrations that come after it apply for real.
	adopted := open(t, dsn)
	defer adopted.Close()
	status, err := adopted.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	if !status.UpToDate || len(status.Applied) < 2 {
		t.Fatalf("expected an adopted database with the baseline and everything after it, got %+v", status)
	}
	var column string
	if err := adopted.DB.QueryRow(
		`SELECT name FROM pragma_table_info('users') WHERE name = 'phone_verified_at'`).Scan(&column); err != nil {
		t.Fatalf("an adopted database must receive later migrations too: %v", err)
	}
}
