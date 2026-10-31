package store

// Cross-process migration locking.
//
// The problem this exists for, observed rather than imagined: four API processes starting together against
// an empty PostgreSQL database — a first deploy, a new environment, or a rolling restart — each open the
// database, each see zero applied migrations, and each try to create `schema_migrations` and run 0001_init.
// PostgreSQL rejects three of them with `duplicate key value violates unique constraint
// "pg_type_typname_nsp_index"`, because `CREATE TABLE IF NOT EXISTS` is not atomic against a concurrent
// creator. Three replicas exit with status 1 and crashloop until their restart backoff happens to put them
// behind the winner's completion.
//
// Nothing in the ledger prevented that: the ledger stops two processes *diverging*, but it is written after
// the DDL has already run, so by the time it could arbitrate the damage is done. A migration runner needs a
// lock taken *before* the first statement, not a constraint applied after the last one.
//
// Two mechanisms, because the two engines have different primitives:
//
//   - PostgreSQL: a session-level advisory lock, held on a pinned connection for the whole run. Session
//     scope is what lets it survive the per-migration transactions, which each commit.
//   - SQLite: an exclusive `flock` on the database file. SQLite has no advisory locks, and its own write
//     lock is released at every commit — too fine-grained for "one migrator at a time".

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// migrationLockKey is the advisory lock id for schema migrations. A constant so every version of every
// replica agrees: it is the one number that has to stay stable forever, because a replica running an older
// build must contend for the same lock as a newer one.
const migrationLockKey int64 = 0x4564756D61727401 // "Edumart\x01"

// defaultMigrationLockTimeout is how long a replica waits for another to finish migrating before giving up.
// Long enough to cover a slow migration, short enough that a stuck migrator does not hold every replica in
// a pending state until Kubernetes restarts them all anyway.
func migrationLockTimeout() time.Duration {
	raw := os.Getenv("EDUMART_MIGRATION_LOCK_TIMEOUT")
	if raw == "" {
		return 60 * time.Second
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 60 * time.Second
	}
	return parsed
}

// lockMigrations blocks until this process is the only one migrating, and returns the release function.
func (s *Store) lockMigrations(ctx context.Context) (func(), error) {
	if s.lockTimeout <= 0 {
		s.lockTimeout = migrationLockTimeout()
	}
	if s.Driver == "postgres" || s.Driver == "pgx" {
		return s.lockAdvisory(ctx)
	}
	return s.lockFile()
}

// lockAdvisory takes a PostgreSQL advisory lock on a dedicated connection.
//
// The connection is pinned with db.Conn on purpose. `database/sql` is a pool: `db.Exec("SELECT
// pg_advisory_lock(...)")` may run on one connection and the migration on another, which would leave the
// lock held on a connection nobody is using — and, worse, would look like it worked. A test with six
// goroutines would still pass on an idle pool, so this is precisely the kind of mistake that survives
// testing and fails in a fleet.
func (s *Store) lockAdvisory(ctx context.Context) (func(), error) {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("migration lock: connection: %w", err)
	}

	deadline := time.Now().Add(s.lockTimeout)
	waited := false
	for {
		var acquired bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, migrationLockKey).Scan(&acquired); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("migration lock: %w", err)
		}
		if acquired {
			break
		}
		waited = true
		if time.Now().After(deadline) {
			holder := s.migrationLockHolder(ctx)
			_ = conn.Close()
			return nil, fmt.Errorf(
				"migration lock: still held after %s%s. Either another replica is running a long migration "+
					"(wait, or start replicas with EDUMART_MIGRATE_ON_BOOT=false and run the migration job first), "+
					"or a previous process died holding it",
				s.lockTimeout, holder)
		}
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if waited {
		// Recorded because "the first boot took 4s and the rest took 40ms" is the difference between a
		// fleet that looks slow to start and one that is actually stuck.
		s.migrationWaitLogged = true
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			// Best effort: if the connection is already gone, the lock goes with it. Failing here would
			// turn a successful migration into a failed boot for a reason that no longer matters.
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
			_ = conn.Close()
		})
	}, nil
}

// migrationLockHolder names whoever is holding the lock, so an operator does not have to guess. Best effort:
// a missing or forbidden pg_stat_activity must not replace the real error with a different one.
func (s *Store) migrationLockHolder(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var pid int
	var application, client string
	var since sql.NullTime
	err := s.DB.QueryRowContext(ctx, `
		SELECT l.pid, coalesce(a.application_name, ''), coalesce(host(a.client_addr), 'local'), a.query_start
		FROM pg_locks l LEFT JOIN pg_stat_activity a ON a.pid = l.pid
		WHERE l.locktype = 'advisory' AND l.objid = $1`, migrationLockKey).
		Scan(&pid, &application, &client, &since)
	if err != nil {
		return ""
	}
	age := ""
	if since.Valid {
		age = fmt.Sprintf(", held for %s", time.Since(since.Time).Round(time.Second))
	}
	if application == "" {
		application = "an unnamed session"
	}
	return fmt.Sprintf(" (pid %d, %s from %s%s)", pid, application, client, age)
}

// lockFile takes an exclusive flock on the SQLite database file.
//
// SQLite is the development and CI engine, where "a fleet" means one process — but the concurrent-open test
// runs several against one file, and the failure mode without a lock is the same one PostgreSQL had.
func (s *Store) lockFile() (func(), error) {
	if s.path == "" || s.path == ":memory:" {
		// An in-memory database cannot be shared between processes, so there is nothing to contend with.
		return func() {}, nil
	}
	file, err := os.OpenFile(s.path+".migrate.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("migration lock: %w", err)
	}
	deadline := time.Now().Add(s.lockTimeout)
	for {
		locked, err := tryFlock(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("migration lock: %w", err)
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("migration lock: still held after %s by another process on %s",
				s.lockTimeout, s.path)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() {
		_ = file.Close() // closing the descriptor releases the flock
	}, nil
}

// migrationLockTimeoutSeconds is used by the readiness detail, so an operator can see the setting that
// produced the behaviour rather than inferring it from source.
func migrationLockTimeoutSeconds() string {
	return strconv.Itoa(int(migrationLockTimeout().Seconds()))
}
