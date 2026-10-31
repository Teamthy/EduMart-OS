package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Migration is one applied schema change, as recorded in the ledger.
type Migration struct {
	Version   string
	Checksum  string
	AppliedAt string
}

// MigrationStatus is what /readyz reports: is the schema the one this binary expects?
type MigrationStatus struct {
	Applied  []Migration
	Pending  []string
	UpToDate bool
}

// ledgerDDL records which migrations ran. It is deliberately not part of any migration file: the
// runner must be able to create it on a database that predates it.
const ledgerDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version    TEXT PRIMARY KEY,
  checksum   TEXT NOT NULL,
  applied_at TEXT NOT NULL
)`

// migrationFS returns the embedded directory for the active dialect.
func (s *Store) migrationDir() string {
	if s.Driver == "postgres" {
		return "migrations/postgres"
	}
	return "migrations/sqlite"
}

// Adoption contract: the baseline migration (0001) is replayed against databases created before the
// ledger existed, so its statements must stay idempotent (CREATE ... IF NOT EXISTS). Later migrations
// run exactly once against a recorded database and do not need to be idempotent — 0002 alters a
// table, which SQLite cannot express defensively.
//
// migrationFiles lists embedded migrations in version order (filenames sort lexicographically, so
// they are zero-padded for exactly this reason).
func (s *Store) migrationFiles() ([]fs.DirEntry, error) {
	entries, err := migrations.ReadDir(s.migrationDir())
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// migrate applies pending migrations exactly once and records their checksums.
//
// Two properties matter more than speed here:
//
//   - A migration that already ran is never re-executed, and if its bytes changed afterwards the
//     runner refuses to start. Editing an applied migration is how environments silently diverge;
//     the fix is a new migration, and the error says so.
//   - Each migration runs in its own transaction with its ledger row, so a failure leaves the
//     database at a known version rather than half-way through a file.
func (s *Store) migrate() error {
	// The lock has to be taken *before* the ledger is created, not after: `CREATE TABLE IF NOT EXISTS` is not
	// atomic against a concurrent creator, and the duplicate-type error it raises is exactly how three of
	// four replicas died on their first boot. See migratelock.go.
	unlock, err := s.lockMigrations(context.Background())
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := s.DB.Exec(ledgerDDL); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	// Re-read *after* acquiring the lock. A process that queued behind another has been staring at a stale
	// list the whole time it waited; applying from that list would re-run every migration the winner just
	// finished, which is the entire bug this lock exists to prevent.
	applied, err := s.appliedMigrations(context.Background())
	if err != nil {
		return err
	}

	files, err := s.migrationFiles()
	if err != nil {
		return err
	}

	for _, entry := range files {
		version := strings.TrimSuffix(entry.Name(), ".sql")
		raw, err := migrations.ReadFile(s.migrationDir() + "/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sum := checksum(raw)

		if previous, ok := applied[version]; ok {
			if previous != sum {
				return fmt.Errorf(
					"migration %s changed after it was applied (recorded %s, file %s): "+
						"add a new migration instead of editing an applied one",
					version, short(previous), short(sum))
			}
			continue
		}

		if err := s.applyMigration(version, sum, raw); err != nil {
			return err
		}
	}
	return nil
}

// verifyApplied checks the ledger against the embedded files without changing anything.
//
// This is the replica's job when a separate migration job owns the schema: it must still refuse to serve a
// database whose applied migrations have been edited, because that check is what keeps environments from
// silently diverging.
func (s *Store) verifyApplied() error {
	applied, err := s.appliedMigrations(context.Background())
	if err != nil {
		// No ledger at all means nothing has migrated yet — which readiness reports as pending, and which
		// this function must not treat as a hard failure, or a replica could never start to report why.
		return nil
	}
	files, err := s.migrationFiles()
	if err != nil {
		return err
	}
	onDisk := map[string]string{}
	for _, entry := range files {
		version := strings.TrimSuffix(entry.Name(), ".sql")
		raw, err := migrations.ReadFile(s.migrationDir() + "/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		onDisk[version] = checksum(raw)
	}
	for version, recorded := range applied {
		current, ok := onDisk[version]
		if !ok {
			return fmt.Errorf(
				"migration %s was applied but is no longer in this build: the database is ahead of the binary",
				version)
		}
		if current != recorded {
			return fmt.Errorf(
				"migration %s changed after it was applied (recorded %s, file %s): add a new migration "+
					"instead of editing an applied one", version, short(recorded), short(current))
		}
	}
	return nil
}

// applyMigration runs one file and records it in the same transaction, so a failure leaves the
// database at the previous version rather than half-way through a file.
func (s *Store) applyMigration(version, sum string, raw []byte) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(string(raw)); err != nil {
		return fmt.Errorf("apply migration %s: %w", version, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, checksum, applied_at) VALUES (?, ?, ?)`,
		version, sum, Now()); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	return nil
}

// appliedMigrations reads the ledger as a version → checksum map.
func (s *Store) appliedMigrations(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var version, sum string
		if err := rows.Scan(&version, &sum); err != nil {
			return nil, fmt.Errorf("scan migration ledger: %w", err)
		}
		out[version] = sum
	}
	return out, rows.Err()
}

// ledgerExists reports whether the ledger table itself is there.
//
// A missing ledger is not an error: it is the truthful statement "nothing has ever migrated". Reporting it as
// a SQL error (`relation "schema_migrations" does not exist`) makes an operator read a readiness failure as a
// broken database instead of "the migration job has not run", which are two very different incidents.
func (s *Store) ledgerExists(ctx context.Context) (bool, error) {
	if s.Driver == "postgres" || s.Driver == "pgx" {
		var present bool
		err := s.DB.QueryRowContext(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&present)
		return present, err
	}
	var count int
	err := s.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&count)
	return count > 0, err
}

// MigrationStatus reports applied and pending migrations for readiness.
func (s *Store) MigrationStatus(ctx context.Context) (MigrationStatus, error) {
	var status MigrationStatus

	exists, err := s.ledgerExists(ctx)
	if err != nil {
		return status, fmt.Errorf("read migration ledger: %w", err)
	}
	if !exists {
		// Everything is pending, and readiness says so by name rather than by SQL error.
		files, err := s.migrationFiles()
		if err != nil {
			return status, err
		}
		for _, entry := range files {
			status.Pending = append(status.Pending, strings.TrimSuffix(entry.Name(), ".sql"))
		}
		return status, nil
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT version, checksum, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return status, fmt.Errorf("read migration ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m Migration
		if err := rows.Scan(&m.Version, &m.Checksum, &m.AppliedAt); err != nil {
			return status, err
		}
		status.Applied = append(status.Applied, m)
	}
	if err := rows.Err(); err != nil {
		return status, err
	}

	files, err := s.migrationFiles()
	if err != nil {
		return status, err
	}
	seen := map[string]bool{}
	for _, m := range status.Applied {
		seen[m.Version] = true
	}
	for _, entry := range files {
		version := strings.TrimSuffix(entry.Name(), ".sql")
		if !seen[version] {
			status.Pending = append(status.Pending, version)
		}
	}
	status.UpToDate = len(status.Pending) == 0
	return status, nil
}

// short abbreviates a checksum for an error message without assuming it is a full-length hex digest.
//
// The ledger is a file on disk that can be edited, truncated or restored from a partial backup; slicing the
// first twelve characters of whatever is in it panicked the process on a short value, turning "your ledger is
// corrupt" into a crash loop with no explanation. A message about a damaged ledger must not be the thing that
// damages the process.
func short(sum string) string {
	if len(sum) <= 12 {
		return sum
	}
	return sum[:12]
}

func checksum(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
