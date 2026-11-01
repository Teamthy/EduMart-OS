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

## Rolling rollout: replacing replicas, not just starting them

Starting a fleet and *replacing* it are different claims. A rollout has a failure mode a cold start cannot
have: the service is up the whole time, and every second of it is a second a user can be served by the wrong
replica — or by none.

`scripts/rollout-rehearsal.py` runs four replicas of the previous release behind a small load balancer, then
performs the swap while a client thread requests `/v1/subjects` continuously:

| Scenario | What it does | Gate |
| --- | --- | --- |
| **rolling** | Surging replacement, one replica at a time (`maxSurge=1`) | Every replica ends on the new build; **no client request fails**; capacity never reaches zero |
| **no-surge** | Stop the old replica, then start its replacement on the same port | Capacity dips to three of four — allowed. Zero capacity, or an error — not |
| **abort** | Begin the rollout again, stop it half way, put every replaced replica back on the old binary *under traffic* | Nothing is left on the new build, and the session and data written before the rollout still work |
| **fault: early** | A replica joins the rotation before its process is listening | The run **must fail** |
| **fault: orphan** | A retired replica is stopped but left in the rotation | The run **must fail** |

```bash
python3 scripts/rollout-rehearsal.py --dsn 'postgres://…/throwaway'      # 4 replicas
python3 scripts/rollout-rehearsal.py --fault early --dsn '…'             # prove the gate still fails
```

Recorded on one run: 182 client requests, zero failed, p95 4 ms; every replica reporting
`new-e6ab8d7` after the rollout; the fleet back to nothing-on-the-new-build after the abort.

### The gate is checked at registration, not by waiting to be unlucky

The first version of this script asserted only "no client request failed", and its **`early` fault passed** —
the window in which a replica is listening-but-not-ready is tens of milliseconds, and whether a client lands
in it is a coin flip. A test that passes when the machine is fast is not a gate. Two checks are now
deterministic:

- **At registration**: a replica is only accepted into the rotation after its own `/readyz` returns 200, and
  any replica that fails that check at registration is recorded by name. `early` now fails immediately with
  `4 replica(s) were put in the rotation before they were ready: ['new-1', 'new-2', 'new-3', 'new-4']`.
- **At the end of each scenario**: every replica still in the rotation must answer `/readyz`. `orphan` now
  fails with `stopped or unready replicas are still in the rotation: ['old-4']`.

Two more things the script had to get right, both found by running it:

- **Drain before stop.** Removing a replica from the rotation and killing it in the same breath drops a
  request that was already routed to it. The rehearsal waits a drain delay (`DRAIN_SECONDS`), which is the
  process-level stand-in for a termination grace period.
- **Stop everything on failure.** A failed run used to leave four API processes listening; the *next* run then
  collided on a port and reported "the replica never became ready". Every replica and balancer is now stopped
  in `finally`.

What this still does not prove: a real container runtime, a real ingress with its own health checks, DNS,
TLS, or a deployment controller. It is the ordering and the signals an orchestrator would use, exercised
against real processes.

## The container image

`apps/api/Dockerfile` builds the API; the `Container image` CI job builds it, checks the container does not
run as root, starts it against PostgreSQL, and waits for `/readyz`.

```bash
make image                                   # edumart-api:local, version = current commit
docker run --rm -p 8080:8080 -e EDUMART_DRIVER=postgres -e EDUMART_DSN='postgres://…' edumart-api:local
```

| Decision | Why |
| --- | --- |
| `golang:1.27-bookworm` → `debian:bookworm-slim` | `EDUMART_DRIVER=sqlite3` is cgo. Built on Debian and run on a musl or `scratch` base, it would compile and then fail at the first request with a missing loader |
| `USER 10001:10001` | Numeric, so a `runAsNonRoot` policy can verify it without resolving a name; the process writes nothing to disk |
| `ca-certificates` | Refunds call the payment provider over TLS. A container without a trust store fails them with an opaque x509 error |
| `HEALTHCHECK` on `/healthz` | Liveness must not depend on the database. If it did, a database blip would restart every healthy process and turn one outage into two |
| `-X main.version` build stamp | `/healthz` and `/readyz` report the release, so "is the rollout finished?" is answered by asking a replica — see `TestTheProbesReportWhichBuildIsRunning` |

The image is larger than it needs to be because a distroless runtime has not been attempted: **no registry, no
image signing, no SBOM.** See "What is still missing" at the end of this document for the full list.

## The deployment manifests

`infra/k8s/` is the deployment described as objects rather than prose: a namespace with Pod Security
Admission set to `restricted`, a ConfigMap, a Secret *shape*, the migration Job, the API Deployment and
Service, the web Deployment and Service, an Ingress with TLS, PodDisruptionBudgets, and a default-deny
NetworkPolicy with three permitted flows.

`scripts/check-manifests.py` checks them, because everything that makes those files safe is an invariant
Kubernetes does not enforce for you:

| Check | The mistake it catches |
| --- | --- |
| `probes` | API readiness on `/readyz` (schema + database) and liveness on `/healthz` (cheap); **web readiness on a static file**, never on `/healthz` or a page — those fail when the API fails, which would take every web pod out of rotation during the API's bad minute |
| `rollout` | `replicas >= 2`, `maxSurge >= 1`, `maxUnavailable == 0`, and a `terminationGracePeriodSeconds` longer than the API's own 10s drain |
| `migration` | a Job running `-migrate-only` with `backoffLimit: 0`, `restartPolicy: Never`, `EDUMART_SEED=false` — and `EDUMART_MIGRATE_ON_BOOT=false` on the replicas, which is the line that stopped three of four replicas dying on a fresh database |
| `images` | anything on `:latest`, anything unpinned — and **the Job and the replicas running different builds** |
| `environment` | names checked against the Go source and the web source, in both directions: a key the code does not read is a typo that silently falls back to a default (`EDUMART_TRUST_PROX`), and a variable the API needs to boot must be set somewhere |
| `secrets` | a token-shaped key in a ConfigMap, a credential-shaped value in a committed file, a `REPLACE_ME` placeholder replaced by something that looks real |
| `pod-security` | non-root, seccomp, no privilege escalation, all capabilities dropped, read-only root filesystem, and CPU/memory requests and limits on every container |
| `topology` | a Deployment nothing selects, a missing PodDisruptionBudget, an Ingress without TLS or pointing at a Service that does not exist |
| `proxy` | `EDUMART_API_URL` naming a Service and port that are not in the directory |
| `network` | no default-deny policy, which would make the other two decoration |

```bash
python3 scripts/check-manifests.py --verbose      # check
python3 scripts/check-manifests.py --self-test    # break it on purpose
```

`--self-test` applies fourteen real faults — readiness moved to `/healthz`, `maxUnavailable: 1`, one replica,
`EDUMART_MIGRATE_ON_BOOT=true`, a Job on a different image, `:latest`, a secret in the ConfigMap, a renamed
settings key, a proxy pointed at a missing Service, a writable root filesystem, a deleted PDB, a five-second
grace period, no default-deny — and requires the corresponding check to fire. The renamed key is there
because the first version of the checker **missed it**: the ConfigMap was never inspected, only the inline
`env:` blocks, so a typo in the file everything reads from passed every check.

## The web image, and the trap it exposed

`apps/web/Dockerfile` builds the Next.js app with `output: "standalone"` and runs `node server.js` as UID
10002. Building it and running it against a live API found a defect that no test in the repository could have
caught, because it is a property of *configuration lifetime*:

| How it was built | How it was run | Pages | `/v1/subjects` |
| --- | --- | --- | --- |
| default (`127.0.0.1:8080`) | `EDUMART_API_URL=http://127.0.0.1:8091` | 200 | **500 — went to 8080** |
| `EDUMART_API_URL=http://127.0.0.1:8091` | nothing set | **500 — "the API is unreachable"** | 200 |

The `/v1` proxy was a `next build` rewrite, so its destination was frozen into the routes manifest, while
server-rendered pages read the same variable at run time. One variable, two lifetimes — which produces a
container that **half works**, and which half depends on how the image was built rather than on how it is run.
An image that must be rebuilt to point at another environment also cannot be promoted from staging.

The rewrite is now a route handler (`apps/web/lib/api-proxy.ts`, mounted at `app/v1/[...path]`), so
`EDUMART_API_URL` is read per request everywhere. Measured after the change, on a standalone build made with
no URL at all: `/`, `/login`, `/v1/subjects`, `/v1/subjects?exam=jamb` and `/healthz` all 200 against an API on
8092; `set-cookie: edumart_session=…; HttpOnly; SameSite=Lax` passes through intact; the API's 401 and 404
envelopes pass through with their `request_id`; with the API stopped, `/v1/*` answers 502 with the API's own
error envelope and **`/manifest.webmanifest` still answers 200**, which is exactly why the web probes that and
not `/healthz`.

The CI job `Container images` builds both images, starts them on one Docker network so the web container
reaches the API **by name** (what a Service does in-cluster), and then: renders a page, proxies `/v1`,
expects a 401 through the proxy, and requires the session cookie to survive the hop.

## Vercel: the web app only

The web app is deployed to Vercel as project `edumart-os` (team `teamthy`), linked to `Teamthy/EduMart-OS`
and building **`main`**. The settings are not defaults, and each one earns its place:

| Setting | Value | Why |
| --- | --- | --- |
| Root Directory | `apps/web` | The Next.js app lives in a workspace, not at the repo root |
| Install Command | `cd ../.. && npm ci` | `@edumart/shared` and the root `package.json` workspaces are two levels up |
| Build Command | `cd ../.. && npm run build --workspace @edumart/web` | `next build` must run through the workspace so `transpilePackages` resolves |
| Framework | Next.js | — |
| `EDUMART_API_URL` | **must be set** to a reachable API origin | Read per request by `lib/api-proxy.ts` and `lib/server-api.ts` |

**The Go API cannot be deployed to Vercel.** It is not a limitation of effort: the server needs a process
that outlives a request, and Vercel's functions do not provide one.

- `apps/api` links `mattn/go-sqlite3` (cgo) for the single-binary mode; Vercel's Go runtime is not the
  build target this repository compiles for, and the container images (not functions) are what CI proves.
- The 50-seat class room is an in-process WebRTC SFU plus a TURN server holding long-lived UDP/TCP sockets
  and per-user ephemeral credentials (`internal/media`). Serverless functions cannot accept that traffic.
- Live rooms, whiteboards and synchronised exams hold WebSocket state across minutes; a function instance
  is frozen between invocations.

So a Vercel deployment of `apps/web` is a **frontend**, and it is only as useful as the `EDUMART_API_URL` it
points at. With that variable unset, the deployment still renders: public pages treat an unreachable API as
"signed out" (`getCurrentUser` returns `null` for a transport failure, tested in
`apps/web/tests/session.test.ts`) and every `/v1/*` call answers `503` with the proxy's own error envelope.
Sign-in, signup, wallet, classes and exams all need a real API host — a VPS, Fly, Render, or the k8s
manifests in `infra/k8s`.

### What the first production deployment showed

- Build: 2m04s, 19 static pages, `READY`, promoted to `edumart-os.vercel.app`.
- `output: "standalone"` is in `next.config.ts` for the container image; Vercel ignores it and uses its own
  output, so one config serves both without a conditional.
- The workspace install has to reach **outside** the root directory. Vercel clones the whole repository, so
  `cd ../..` works there; a client-only upload of `apps/web` would not build.

## Session lifetime in a deployment

| Variable | Default | Meaning |
| --- | --- | --- |
| `EDUMART_ACCESS_TOKEN_TTL` | `1h` | How long an access token lives. Both clients renew with the single-use refresh token |

Shortening the access token is only safe because something renews it. If a deployment sets a TTL and the web
middleware cannot reach the API, learners are signed out hourly rather than weekly — which is the correct
failure direction, but it is a failure, so `Rolling rollout` and `Container images` both run
`scripts/session-lifecycle.py` against a five-second TTL to prove renewal works in the shipped image.

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

1. **CI green**, including `Deploy and rollback rehearsal`, `Rolling rollout` and `Container image`. The
   rehearsals compare against `origin/main`, so they are always rehearsing *this* code against *what is
   running*.
2. **Take a backup** — `make backup` and confirm the heartbeat. Untested until a restore drill passes
   (`docs/BACKUP_AND_RECOVERY.md`).
3. **Migrate, then deploy.** Run the one-shot migration job before any replica starts:

   ```bash
   EDUMART_DSN=… EDUMART_SEED=false ./edumart-api -migrate-only
   ```

   It applies pending migrations, reports what is applied, and exits 0. Replicas then start with
   `EDUMART_MIGRATE_ON_BOOT=false` and only verify checksums, so exactly one process ever owns the schema
   during a rollout. (Boot-time migration stays on for a single instance and for every developer, and the
   migration lock makes it safe even if several replicas do it — `scripts/fleet-start.py` proves that too.) `EDUMART_SEED=false` is not optional: a migration job that also writes demo fixtures is
   a job that does two things, and the second is not safe against production.
4. **Watch the signals** for ten minutes: error rate, p95 latency, reconciliation contradictions, webhook
   failures. `docs/OBSERVABILITY.md` names the alert for each.
5. **Apply the manifests.** `kubectl apply -f infra/k8s/` (or the GitOps equivalent). The migration Job must
   complete before the Deployment is updated — the Job, not the replicas, owns the schema. The manifests are
   checked in CI, but **they have never been applied to a cluster from this repository**, so the first apply
   is a real step performed by a human who watches it.
6. **If it goes wrong, roll back the binary** — do not roll back the schema. The schema stays ahead; that is
   what the rehearsal proves is survivable. Deploying an older migration set is a data-loss operation
   dressed as a rollback.

## What is still missing, and is not claimed

- **No staging environment, and only the web app is hosted.** `apps/web` runs on Vercel (above); **the Go
  API does not run anywhere yet**, so the hosted site has no backend until `EDUMART_API_URL` names a real
  host. Nothing has exercised a deployment of the API. The rehearsal proves the *schema and binary*
  transition; it proves nothing about DNS, TLS, a real load balancer, secrets management, or an orchestrator
  performing the rolling restart it rehearses.
- **Images, but no registry.** Both `apps/api/Dockerfile` and `apps/web/Dockerfile` build and run as
  non-root in CI, together, against PostgreSQL. Nothing is pushed to a registry, nothing is signed, there is
  no SBOM and no image scanning step; the mobile app is not containerised at all.
- **Manifests, but no cluster.** `infra/k8s` is checked by `scripts/check-manifests.py` (ten checks, fourteen
  injected faults) and parsed by `kubectl --dry-run=client` in CI — **it has never been applied to a real
  cluster.** No TLS certificate is issued, no ingress controller is configured beyond annotations, no
  external-secrets operator or vault is wired to the placeholder Secret, and there is **no autoscaling, no
  multi-region, and no cluster upgrade story.**
- **No automated rollback trigger.** Deciding to roll back is a human judgement on the signals above.
- **No data rollback.** Reverting a schema change loses whatever the newer code wrote into it. The rehearsal
  proves the *old code* can read the *old columns*; it says nothing about a column added and populated by the
  release being rolled back.
