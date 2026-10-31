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
| 2 | **Migrate as a separate step** (`-migrate-only`) | One process touches the schema, not N replicas racing |
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
- **Replicas still migrate on boot.** `-migrate-only` gives the rollout the right *order*, but the binary
  still applies migrations when it opens the database, so a replica started before the job would try to
  migrate too. The ledger stops two processes diverging (the loser fails on the duplicate row), but that is
  a crashloop, not a queue — there is no lock, and the flag does not remove the capability from replicas.
- **No automated rollback trigger.** Deciding to roll back is a human judgement on the signals above.
- **No data rollback.** Reverting a schema change loses whatever the newer code wrote into it. The rehearsal
  proves the *old code* can read the *old columns*; it says nothing about a column added and populated by the
  release being rolled back.
