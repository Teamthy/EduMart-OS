package api_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/store"
)

// openTestStore returns an isolated, migrated database.
//
// Default: a throwaway SQLite file, which is how CI and local development run.
// With EDUMART_TEST_POSTGRES_DSN set: a real PostgreSQL server, wiped to an empty `public` schema
// first so each test starts from nothing. Running the same suite on both engines is what keeps the
// claim "one query layer serves both" honest — otherwise the production driver is only ever
// exercised by the first request that reaches it.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()

	dsn := os.Getenv("EDUMART_TEST_POSTGRES_DSN")
	if dsn == "" {
		db, err := store.Open("sqlite3", filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		return db
	}

	resetPostgresSchema(t, dsn)
	db, err := store.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	return db
}

// resetPostgresSchema drops everything a previous test created.
func resetPostgresSchema(t *testing.T, dsn string) {
	t.Helper()
	connector, err := store.NewPostgresConnector(dsn)
	if err != nil {
		t.Fatalf("postgres connector: %v", err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()

	if _, err := db.Exec(`DROP SCHEMA IF EXISTS public CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := db.Exec(`CREATE SCHEMA public`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
}
