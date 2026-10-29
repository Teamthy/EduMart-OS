// Package store owns database connectivity, migrations and the transaction helper.
//
// One query layer serves SQLite (local dev, CI, demo) and PostgreSQL (production). Queries are
// written in portable SQL and timestamps are ISO-8601 UTC text, so switching drivers is a config
// change plus the matching migration directory.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrations embed.FS

// Store wraps the connection pool and exposes small helpers used by every module.
type Store struct {
	DB     *sql.DB
	Driver string
}

// ErrNotFound is returned by Collect helpers when a row does not exist.
var ErrNotFound = errors.New("not found")

// Open connects to the database, applies the schema for the selected driver and returns the store.
// Supported drivers: "sqlite3" (default, the tested path) and "postgres" (the production target;
// needs github.com/jackc/pgx/v5/stdlib registered here, which this build does not link yet — asking
// for it fails loudly with that instruction rather than silently falling back).
func Open(driver, dsn string) (*Store, error) {
	if driver == "" {
		driver = "sqlite3"
	}
	if driver == "sqlite3" {
		// Ensure the parent directory exists so a fresh checkout works without manual setup.
		path := strings.TrimPrefix(dsn, "file:")
		if i := strings.Index(path, "?"); i >= 0 {
			path = path[:i]
		}
		if path != "" && path != ":memory:" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, fmt.Errorf("create database directory: %w", err)
			}
			dsn = "file:" + path + "?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL"
		}
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		if driver == "postgres" || driver == "pgx" {
			// Naming the missing piece beats "unknown driver" at 2am.
			return nil, fmt.Errorf("open postgres: %w — this build links only the SQLite driver; "+
				"register github.com/jackc/pgx/v5/stdlib in this package to enable PostgreSQL "+
				"(see docs/PRODUCTION_READINESS.md)", err)
		}
		return nil, fmt.Errorf("open database: %w", err)
	}
	// SQLite serialises writes anyway; a single connection avoids "database is locked" churn.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	s := &Store{DB: db, Driver: driver}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases the pool.
func (s *Store) Close() error { return s.DB.Close() }

// Tx runs fn inside a transaction, rolling back on error or panic.
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Now returns the canonical timestamp format used across the schema.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// TS formats a time in the canonical schema format.
func TS(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ParseTS parses a stored timestamp, tolerating both RFC3339 and RFC3339Nano.
func ParseTS(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, ErrNotFound
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, v)
}

// ParseTSOr parses a nullable timestamp column.
func ParseTSOr(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := ParseTS(v.String)
	if err != nil {
		return nil
	}
	return &t
}

// NewID returns a random UUID v4 rendered as text (no external dependency).
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("entropy failure: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// MetaGet reads a value from schema_meta.
func (s *Store) MetaGet(ctx context.Context, key string) (string, bool) {
	var value string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", false
	}
	return value, true
}

// MetaSet writes a value into schema_meta.
func (s *Store) MetaSet(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO schema_meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
