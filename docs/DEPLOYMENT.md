# Deployment and rollback

Phase 4 said CI is real but **staging does not exist, so deploy and rollback are unproven**. That is still
half true. What changed is the half that can be proved without a hosting account: the *procedure* is now
executed on every pull request, against a real PostgreSQL database and two real binaries.

```bash
python3 scripts/deploy-rehearsal.py --check-only        # refuse unsafe migrations (no database needed)
python3 scripts/deploy-rehearsal.py --dsn 'postgres://…'  # deploy forward, then roll back
```

## The rehearsal

`scripts/deploy-rehearsal.py` builds the **previous release from a git ref** and **this release from the
working tree**, then:

| # | Stage | What it proves |
| --- | --- | --- |
| 1 | Previous release onto an empty database, full journey | The release being rolled back to actually works |
| 2 | **Migrate as a separate step** (`-migrate-only`) | One process touches the schema, not N replicas racing — and the replicas themselves are covered by `fleet-start.py` |
| 3 | Previous release against the *migrated* database | The earliest possible signal that a rollout is unsafe — before anything new serves traffic |
| 4 | This release over it | The new binary works on a database migrated while it was not running |
| 5 | This release writes an order | The write path works on the new schema |
| 6 | **Roll back**: previous release against the migrated database | **The gate.** Old code, new schema |
| 7 | Read stage 5's order with the old code | A rollback that boots but loses the last hour is not a rollback |
| 8 | Compare 9 endpoint statuses against the pre-deploy baseline | The rollback restores the *whole* surface, not just the routes the journey touches |

Stage 6 is the whole point. The database is one release *ahead* of the binary serving it — precisely the
state a bad deploy leaves behind — and the old code has to keep working. That holds only if migrations are
**expand-only**: add the new shape, ship code that tolerates both, and drop the old shape in a later release
once nothing reads it.

Stage 8 is why the script records a **fingerprint** — the status code of nine endpoints, public and
authenticated — rather than asserting "this release has search". A rollback is a **feature** rollback as well
as an operational one, and hardcoding which features exist would make the script wrong the moment either ref
changes. The first draft did exactly that: it asserted `/v1/search` returned 404 under the previous release,
which passed against an older ref and then failed the moment the two refs were the same commit. The
fingerprint works for any pair, including a pair that is identical.

### The migration gate

Before any of that, the script scans every migration for DDL a rollback cannot survive:

```
FAIL  postgres/0099_x.sql:2 DROP TABLE — a rollback cannot survive this
```

`DROP TABLE`, `DROP COLUMN`, `RENAME` and `DROP CONSTRAINT` fail the build unless the file carries

```sql
-- rehearsal: destructive: <why>
```

which is not a bypass but a declaration: it says *this release is forward-only*, and the rehearsal will show
what that costs. It runs in a fraction of a second with no database, so it gates every PR.

### Verified to fail

A gate that has only ever passed is not a gate. Both were exercised:

- A probe migration containing `DROP TABLE` was rejected, naming the file and line.
- A probe carrying `ALTER TABLE products RENAME …` **with** the marker was allowed by the scan, and the
  rehearsal then refused at the deploy stage with `GET /v1/products returned 500` — before deploy, not after.
- The SQLite path initially failed with `unknown driver "sqlite"` because the store calls that driver
  `sqlite3`. A nice illustration of why the rehearsal runs on both engines.

One honest caveat: with the current code that probe broke *both* releases, because this release is the
previous release plus additive features. The rollback stage is designed for the case where new code has
adapted to a destructive migration — the case you cannot reach without actually writing that code. Stage 4
has been shown to **pass** (at 0011, adding the search tables); its failure path is the same code as
stage 2's, which has been shown to fail loudly with the server's own log.

## A fleet of replicas

`scripts/fleet-start.py` starts six API processes against one fresh database and checks three scenarios:

| Scenario | Property it checks |
| --- | --- |
| **boot** — all replicas migrate on boot | All become ready; every migration is recorded **exactly once**; every replica reports the same applied count |
| **migrate-first** — `-migrate-only`, then replicas with `EDUMART_MIGRATE_ON_BOOT=false` | All become ready without migrating |
| **fail-closed** — replicas that may not migrate, against an unmigrated database | **None** becomes ready, they stay alive rather than crashlooping, and readiness names the pending migrations |

```bash
EDUMART_API_BIN=./edumart-api python3 scripts/fleet-start.py --dsn 'postgres://…/throwaway'
```

The Go test that covers the same race in-process (`TestConcurrentOpensOnPostgresApplyEachMigrationOnce`, gated on
`EDUMART_TEST_POSTGRES_DSN`) creates its own throwaway database, because `go test ./...` runs packages in
parallel and the API tests wipe `public` on the shared DSN. It skips with a message rather than failing when the
role has no `CREATEDB`. Do not "fix" a collision between packages by asking each test to clean up in the right
order — the isolation has to be structural or the next test added to either package reintroduces it.

### What it found the first time it ran

Six processes started within ~20 ms of each other, against an empty database:

```
replica-1  ERROR  create migration ledger: pq: duplicate key value violates unique constraint "pg_type_typname_nsp_index" (23505)
replica-2  INFO   api listening
replica-3  ERROR  apply migration 0001_init: pq: duplicate key ... "pg_type_typname_nsp_index" (23505)
replica-4  ERROR  apply migration 0001_init: pq: duplicate key ... "pg_type_typname_nsp_index" (23505)
```

**Three of four replicas exited with status 1 and would have crashlooped** until their restart backoff
happened to put them behind the winner. The ledger did not prevent it: it is written *after* the DDL has run,
so by the time it could arbitrate, `CREATE TABLE IF NOT EXISTS` had already raced and PostgreSQL had already
refused the loser. A constraint cannot serialise something that happens before it.

The fix is a lock taken **before the first statement**:

- **PostgreSQL** — a session-level advisory lock (`pg_advisory_lock`, constant key) held for the whole run on a
  connection pinned with `db.Conn`. Pinning matters: `database/sql` is a pool, so issuing the lock and the
  migration as separate calls can put them on different connections, leaving the lock on a connection nobody
  is using and *looking* correct on an idle pool.
- **SQLite** — an exclusive `flock` on the database file, because SQLite releases its write lock at every
  commit, which is far too fine-grained for "one migrator at a time".

After acquiring, the applied set is **re-read**: a process that queued for three seconds has been holding a
stale list the entire time, and applying from it would re-run everything the winner just finished — the
original bug wearing a different hat.

### Two more bugs the tests found

- **A shadowed variable silently disabled the SQLite lock.** `path := dsn` inside the branch shadowed the
  field the lock reads, so `flock` was called with an empty path and did nothing. The concurrent-open test
  failed on its first run; without a test that starts several opens, the code would have looked correct.
- **A short ledger checksum panicked the process.** `recorded[:12]` on a corrupted or truncated ledger entry
  crashed with `slice bounds out of range` — so "your ledger is corrupt" became a crashloop with no
  explanation. Both error paths now abbreviate safely.

## Replica rollout

```bash
EDUMART_DSN=… EDUMART_SEED=false ./edumart-api -migrate-only   # once, before any replica starts
EDUMART_DSN=… EDUMART_MIGRATE_ON_BOOT=false ./edumart-api       # every replica
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `EDUMART_MIGRATE_ON_BOOT` | `true` | Whether a replica applies migrations when it opens the database |
| `EDUMART_MIGRATION_LOCK_TIMEOUT` | `60s` | How long to wait for another migrator before failing with an explanation |

A replica with migrations disabled still **verifies** the ledger: if an applied migration's bytes have changed,
or the database is ahead of the binary, it refuses to start rather than serving a schema it does not
understand. Readiness is the second line — pending migrations make `/readyz` fail, which removes the replica
from the load balancer.

## Migrations are expand-only

The rule the rehearsal enforces, stated once:

1. **Add** the new column or table. Nothing reads it yet. (Deployable, rollback-safe.)
2. **Ship code** that writes both shapes and reads either.
3. **Stop reading** the old shape. (Only now is a rollback still safe.)
4. **Drop** the old shape in a *later* release, after nothing can roll back that far.

Steps 1–3 are rollback-safe; step 4 is forward-only. That is why the scan blocks destructive DDL by default
and why the marker is a declaration rather than a bypass.

## Runbook: shipping a release

Nothing here is automated end to end, because **there is no hosting target yet**. This is what the procedure
will be, and each step is either proven or marked.

1. **CI green**, including `Deploy and rollback rehearsal`. The rehearsal compares against `origin/main`, so
   it is always rehearsing *this* code against *what is running*.
2. **Take a backup** — `make backup` and confirm the heartbeat. Untested until a restore drill passes
   (`docs/BACKUP_AND_RECOVERY.md`).
3. **Migrate, then deploy.** Run the one-shot migration job before any replica starts:

   ```bash
   EDUMART_DSN=… EDUMART_SEED=false ./edumart-api -migrate-only
   ```

   It applies pending migrations, reports what is applied, and exits 0. Replicas still migrate on boot, but
   after this step they find nothing pending and only verify checksums — so the race a fleet would otherwise
   create (all N see the same pending migration; the losers exit on a duplicate ledger row and crashloop)
   does not happen. `EDUMART_SEED=false` is not optional: a migration job that also writes demo fixtures is
   a job that does two things, and the second is not safe against production.
4. **Watch the signals** for ten minutes: error rate, p95 latency, reconciliation contradictions, webhook
   failures. `docs/OBSERVABILITY.md` names the alert for each.
5. **If it goes wrong, roll back the binary** — do not roll back the schema. The schema stays ahead; that is
   what the rehearsal proves is survivable. Deploying an older migration set is a data-loss operation
   dressed as a rollback.

## What is still missing, and is not claimed

- **No staging environment.** Nothing has ever been deployed to a host. The rehearsal proves the *schema and
  binary* transition; it proves nothing about DNS, TLS, a load balancer, container orchestration, secrets
  management, or a rolling restart with two replicas in flight.
- **No container image.** There is no `Dockerfile`; the rehearsal ships raw binaries, which is honest about
  what has been tested but is not how this will run in production.
- **No orchestrator.** The fleet check starts processes; nothing yet starts *containers* with a rolling
  update, readiness probe and grace period. `EDUMART_MIGRATE_ON_BOOT=false` is what makes that rollout safe,
  and it is verified — but the deployment controller itself does not exist.
- **No automated rollback trigger.** Deciding to roll back is a human judgement on the signals above.
- **No data rollback.** Reverting a schema change loses whatever the newer code wrote into it. The rehearsal
  proves the *old code* can read the *old columns*; it says nothing about a column added and populated by the
  release being rolled back.
