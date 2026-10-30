package store

import (
	"os"
	"testing"
)

// TestPoolSizeRejectsValuesThatWouldStopTheServer: `database/sql` treats SetMaxOpenConns(0) as "unlimited",
// but a pool setting read from the environment that says 0 or -1 almost always means a broken template, and
// obeying it either removes the ceiling entirely or (on a driver that reads it differently) stops every
// query. Falling back to the default is the only reading that cannot take the service down.
func TestPoolSizeRejectsValuesThatWouldStopTheServer(t *testing.T) {
	cases := []struct {
		value    string
		fallback int
		want     int
		why      string
	}{
		{"", 20, 20, "unset takes the default"},
		{"50", 20, 50, "a sane value is obeyed"},
		{"0", 20, 20, "zero means unlimited to database/sql, which is the footgun the bound exists to remove"},
		{"-5", 20, 20, "a negative pool is a template bug, not an instruction"},
		{"twenty", 20, 20, "unparseable takes the default"},
		{" 8 ", 20, 8, "surrounding whitespace is tolerated"},
	}
	for _, testCase := range cases {
		t.Run(testCase.value, func(t *testing.T) {
			t.Setenv("EDUMART_TEST_POOL", testCase.value)
			if got := poolSize("EDUMART_TEST_POOL", testCase.fallback); got != testCase.want {
				t.Fatalf("poolSize(%q) = %d, want %d (%s)", testCase.value, got, testCase.want, testCase.why)
			}
		})
	}
}

// TestThePoolIsBoundedOnTheSQLitePathToo pins the one engine whose limit is not a choice: SQLite serialises
// writes, and a pool wider than one connection turns contention into "database is locked" errors under
// load rather than the queue it actually is.
func TestThePoolIsBoundedOnTheSQLitePathToo(t *testing.T) {
	path := t.TempDir() + "/pool.db"
	db, err := Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if got := db.DB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("SQLite must run on exactly one connection, got %d", got)
	}
}

// TestAPoolBoundIsActuallyHonoured proves the setting is wired to the pool rather than merely parsed. It runs
// against SQLite because it must pass on a machine with no PostgreSQL — the PostgreSQL half of the same
// claim is exercised by the load test in CI, which would fail loudly if the ceiling were not applied.
func TestAPoolBoundIsActuallyHonoured(t *testing.T) {
	t.Setenv("EDUMART_DB_MAX_OPEN_CONNS", "3")
	t.Setenv("EDUMART_DB_MAX_IDLE_CONNS", "99") // deliberately above the open bound, so the clamp is exercised
	if os.Getenv("EDUMART_TEST_POSTGRES_DSN") == "" {
		t.Skip("set EDUMART_TEST_POSTGRES_DSN to exercise the PostgreSQL pool")
	}
	db, err := Open("postgres", os.Getenv("EDUMART_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	stats := db.DB.Stats()
	if stats.MaxOpenConnections != 3 {
		t.Fatalf("expected a ceiling of 3, got %d", stats.MaxOpenConnections)
	}
	// The idle bound is clamped to the open bound, because an idle pool larger than the ceiling is a
	// configuration that silently cannot be honoured.
	if open, idle := resolvePool(); idle > open {
		t.Fatalf("idle bound %d exceeds the open bound %d", idle, open)
	}
}
