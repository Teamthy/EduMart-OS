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
	if _, err := s.DB.Exec(ledgerDDL); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

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
					version, previous[:12], sum[:12])
			}
			continue
		}

		if err := s.applyMigration(version, sum, raw); err != nil {
			return err
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

// MigrationStatus reports applied and pending migrations for readiness.
func (s *Store) MigrationStatus(ctx context.Context) (MigrationStatus, error) {
	var status MigrationStatus

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

func checksum(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
