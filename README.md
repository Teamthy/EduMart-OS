# Edumart OS

Exam preparation for Nigerian learners: timed mocks, adaptive practice, class assessments, a
verified-payment store, learning analytics and an operations console.

Three deliverables, one product:

| Surface | Stack | Path |
| --- | --- | --- |
| **API** | Go 1.27+, chi router, SQLite (dev/CI) **or PostgreSQL (production)** — same query layer | `apps/api` |
| **Web / PWA** | Next.js 15 App Router, Tailwind CSS v4, shadcn/ui primitives | `apps/web` |
| **Mobile app** | React Native + Expo (Router), shipping alongside the PWA | `apps/mobile` |
| **Shared contract** | TypeScript wire types, validation, design tokens | `packages/shared` |

The server owns session state, the deadline, scoring and authorisation. Both shells are thin clients
of the same versioned REST API (`/v1`), which is what lets the web app and the native app ship from
one commit without drifting.

---

## Quick start

Requirements: **Go 1.27+**, **Node 20.11+**. No database server is needed — SQLite is embedded.

```bash
npm install                 # web + shared workspaces
npm run dev:api             # http://localhost:8080  (seeds demo content on first boot)
npm run dev                 # http://localhost:3000  (proxies /v1 to the API)
```

The web app proxies `/v1/*` to the Go API, so the session cookie stays first-party and the browser
never needs to know the API origin.

For the native app:

```bash
npm run install:mobile      # installs Expo's dependencies into apps/mobile
npm run dev:mobile          # Expo dev server (simulator or Expo Go)
```

### Demo accounts

Seeded by `EDUMART_SEED=true` (the default in development).

| Role | Email | Password |
| --- | --- | --- |
| Student | `student@edumart.ng` | `Student123!` |
| Teacher | `teacher@edumart.ng` | `Teacher123!` |
| Content editor | `editor@edumart.ng` | `Editor123!` |
| Platform admin | `admin@edumart.ng` | `Admin123!` |

Other seeded handles: class join code `LEKKI24`, referral code `AMARA-2026`.

---

## The API contract

`packages/contracts/openapi.yaml` describes every route the Go service serves — 48 path+method
pairs — and is checked against `apps/api/internal/api/router.go` in both directions on every CI run:

```bash
npm run contract
# contract matches the router: 48 documented route(s)
```

An undocumented handler and a documented-but-missing endpoint both fail the build, so the contract
cannot drift from the code while the web and mobile clients are built against it.

## Health, readiness and schema

| Endpoint | Purpose |
| --- | --- |
| `GET /healthz` | Liveness only — no dependencies, so a database blip cannot restart a healthy process |
| `GET /readyz` | Readiness — probes the database (ping **and** a real read) and checks every migration is applied. 503 with a per-check breakdown when not |
| `GET /v1/me/sessions` | The caller's live devices, exactly one marked `current` |
| `DELETE /v1/me/sessions/{id}` | Revoke one device, scoped by user id in the query itself |

```json
{ "status": "ok", "driver": "sqlite3",
  "checks": [ { "name": "database", "status": "ok", "latency": "36µs" },
              { "name": "migrations", "status": "ok", "detail": "1 applied" } ] }
```

Schema changes go through a real migration runner (`apps/api/internal/store/migrate.go`): a
`schema_migrations` ledger with sha256 checksums, one transaction per migration, applied exactly
once. Editing a migration that has already run makes the process refuse to start and tells you to
add a new one — the failure mode that silently diverges environments otherwise. A database created
before the ledger existed is adopted on next boot.

## Local infrastructure

`infra/docker/compose.yml` starts PostgreSQL 17 and Redis 7:

```bash
docker compose -f infra/docker/compose.yml up -d     # or: make infra
```

PostgreSQL is fully supported and tested; Redis is not used yet.

```bash
docker compose -f infra/docker/compose.yml up -d
EDUMART_DRIVER=postgres \
EDUMART_DSN='postgres://edumart:local_only_change_me@localhost:5432/edumart?sslmode=disable' \
npm run dev:api
```

The whole suite runs on both engines — locally with `EDUMART_TEST_POSTGRES_DSN=… go test ./...`,
and in CI, where a PostgreSQL service container runs the same 33 tests and the same HTTP smoke
journey a second time. `infra/docker/README.md` records what is and is not wired to Redis.

## One query layer, two engines

The database code is written once, in portable SQL with `?` placeholders. SQLite speaks that
natively; PostgreSQL wants `$1, $2`, so the translation happens at the driver boundary in
`apps/api/internal/store/pg.go` — a `database/sql` connector that rewrites every statement on its way
through, skipping string literals, quoted identifiers, dollar-quoted bodies and comments. Nothing
reaches PostgreSQL without passing through it, so no query can be forgotten.

That wrapper is also what makes the *tests* meaningful: the same 33 tests, the same migrations and
the same seed data run against a real PostgreSQL 17 in CI. Writing this uncovered a genuine
cross-engine bug — the webhook handler wrote rows while a result set was still open on the same
connection, which SQLite tolerates and PostgreSQL rejects. It is fixed, and the pattern is documented
in the code.

Driver: `lib/pq`, not `pgx`. pgx is the more modern client, but its module graph pulls
`gopkg.in/yaml.v3` (through a test dependency of `pgservicefile`) and gopkg.in is unreachable from
the environment this was built in. `lib/pq` has no external dependencies and supports SCRAM-SHA-256;
swapping to pgx later is a change to one file.

## Verifying an address

Codes are six digits, single-use, valid for 15 minutes, and stored only as an HMAC — a leaked
database hands out nothing usable. The endpoint is throttled per user and channel, and a code that
fails to deliver is discarded rather than left valid.

```bash
# Requires a session; the code never appears in the response.
curl -X POST localhost:8080/v1/auth/verification/request \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"channel":"email"}'
# → 202 {"sent":true,"destination":"a••••@example.com","resend_after":60,...}
```

In development the code is printed by the log sender, so the flow works on a laptop with no mail
server. Buying anything requires a verified address: `POST /v1/orders` answers `403
EMAIL_NOT_VERIFIED` until then, because a receipt and a refund both need a mailbox that exists.

## Institutions

An organisation is created by a verified account and starts `pending_verification` until a platform
administrator approves it. Members are added by invitation — single-use, email-locked, seven days —
and authority comes from membership rather than from the role someone picked at signup.

```bash
# As an owner: invite a teacher. The token is returned once, and never again.
curl -X POST localhost:8080/v1/organizations/$ORG_ID/invitations \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"email":"teacher@school.edu.ng","role":"teacher"}'
```

Removing a membership ends the access it carried in the same transaction, including the teaching role
when no other organisation still grants it. See `apps/web/app/(app)/institutions`.

## What is actually enforced

These are the invariants the product depends on; each has a test.

| Invariant | Where | Test |
| --- | --- | --- |
| The server sets the deadline; the client only displays it | `assessment.CanAcceptAnswer`, `DeadlineAt` | `TestCanAcceptAnswer`, `TestAttemptsExpireAtTheServerDeadline` |
| Answer saves are idempotent and version-checked | `assessment.SaveResponse` | `TestPracticeFlowHidesAnswerKeyUntilReveal` |
| Answer keys never travel before release | `assessment.ViewFor` + `domain.ResultReleased` | same, plus the web `AttemptRunner` test |
| Attempts are served from an immutable snapshot | `assessment_sessions.snapshot_json` | `TestPracticeFlow…`, `TestScoringPolicyVersionIsRecorded` |
| Scoring is deterministic and versioned | `domain.ScoreAttempt`, `scoring_policy_json` | `TestScoreAttemptDeterministic` |
| Points and commission are separate append-only ledgers | `points_ledger_entries`, `commission_ledger_entries` | `TestPracticeFlow…` (duplicate submit award), `rewards` service |
| Payment truth comes only from a verified webhook | `commerce.HandleWebhook` | `TestWebhookIsTheOnlySourceOfPaymentTruth` |
| Money is integer minor units | schema + `ServerApi` + `formatMoney` | `formatMoney` tests |
| Authorisation is server-side and tenant-scoped | `auth.RequireRole`, `assessment.owned`, `classroom.Roster` | `TestTenantAndRoleAuthorisation`, `TestSessionListAndRevocation` |
| Migrations apply once, and an edited migration stops the boot | `store.Migrate` + `schema_migrations` | `TestMigrationsApplyOnceAndReportReady`, `TestEditingAnAppliedMigrationIsRefused`, `TestAdoptsADatabaseThatPredatesTheLedger` |
| Readiness reflects real dependencies, liveness does not | `api.readyzHandler` | `TestReadinessChecksRealDependencies` |
| Signup progress is server-persisted across five pages | `signup_drafts` + `POST/PATCH /v1/signup-drafts` | `TestSignupWizardResumesFromServerState`, `mergeDraft` tests |
| Graded attempts freeze on submit; only practice accepts corrections | `assessment.EditResponse` | `TestGradedAssessmentSessionsAreFrozenOnSubmit`, `TestCorrectionsRescoreWithoutRevealingAnswers` |

Run everything:

```bash
npm test                    # Go tests + web tests
npm run typecheck           # shared + web
npm run typecheck:mobile    # Expo app (after npm run install:mobile)
npm run smoke               # learner journey against a running API
make drill                  # destroy a database and prove the snapshot brings it back
make rollback               # refuse unsafe migrations, then deploy forward and roll back
make fleet                  # start six replicas against one database and prove they all come up
make rollout                # replace a fleet under live traffic, then abort the rollout and recover
make image                  # build both container images (API + web)
make manifests              # check the deployment manifests, then prove every check can fail
npm run contract            # OpenAPI spec vs Go router, both directions
npm run assets              # regenerate the app icons + splash
```

Current status: **Go 39 tests** (33 top-level + subtests), **web 25 tests**, `go vet` clean, `tsc --noEmit` clean on web,
shared and mobile, `next build` clean.

---

## Repository layout

```
apps/
  api/                      Go service
    cmd/api/                entrypoint (HTTP server + expiry worker)
    internal/
      domain/               pure rules: scoring, deadlines, release policy
      assessment/           practice + timed attempts, answer writes, finalisation
      auth/                 identities, sessions (PBKDF2), roles
      signup/               server-persisted multi-step signup drafts
      content/              pathways, subjects, topics
      classroom/            classes, join codes, rosters, assignments
      commerce/             products, orders, webhook-verified payments, entitlements
      rewards/              dual ledgers, referral qualification
      insights/             dashboard, analytics, study plan (read models)
      admin/                content review, support cases, metrics
      httpx/                error envelope, request ids, rate limiting, middleware
      store/                connection, migrations (SQLite + PostgreSQL), helpers
      seed/                 idempotent demo content (`content.json`)
  web/                      Next.js app
    app/                    routes; `app/(app)` is the authenticated shell
    components/ui/          vendored shadcn/ui primitives
    components/attempt/     the attempt runner (save states, countdown, submit)
    lib/                    server API client, session, signup actions
  mobile/                   Expo app (Router) — same steps, same contract
packages/shared/            wire types, API client, signup definition, design tokens
legacy/web-sqlite/          the previous Next.js + node:sqlite prototype (reference only)
docs/                       architecture, API, data model, signup flow, mobile, readiness, threat model,
                            backup and recovery, observability and alerting, performance, accessibility,
                            search
```

---

## Configuration

Copy `.env.example` to `.env` for local overrides; both services read environment variables only.

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | API listen port |
| `EDUMART_DRIVER` | `sqlite3` | `sqlite3` or `postgres` |
| `EDUMART_DSN` | `data/edumart.db` | Database path or connection string |
| `EDUMART_SEED` | `true` | Load taxonomy, products and demo accounts |
| `EDUMART_WEBHOOK_SECRET` | `dev_sandbox_webhook_secret` | HMAC secret for payment webhooks (the sandbox scheme; the API's own envelope) |
| `EDUMART_PAYMENT_PROVIDER` | `sandbox` | Which provider moves money. `paystack` sends refunds to the provider's API and verifies its callbacks with `EDUMART_PAYSTACK_SECRET_KEY` |
| `EDUMART_PAYSTACK_SECRET_KEY` | unset | Payments/refunds credential **and** the key Paystack signs its webhooks with. Unset with `paystack` selected means refunds are recorded as failed, never as money moved |
| `EDUMART_PAYSTACK_BASE_URL` | `https://api.paystack.co` | Overridden in tests, which point it at a fake server |
| `EDUMART_ALLOW_SANDBOX_PAYMENTS` | `true` | Exposes the sandbox "simulate webhook" endpoint |
| `EDUMART_ALLOWED_ORIGINS` | `http://localhost:3000,http://localhost:8081` | CORS allowlist for native clients |
| `EDUMART_TRUST_PROXY` | `false` | Believe `X-Forwarded-For`. Set `true` **only** behind a load balancer or proxy — otherwise every rate limit is keyed on a value the caller chooses |
| `EDUMART_ACCESS_TOKEN_TTL` | `1h` | Access-token lifetime; clients renew with the single-use refresh token |
| `EDUMART_MIGRATE_ON_BOOT` | `true` | A replica sets this `false` and lets a migration job (`api -migrate-only`) own the schema |
| `EDUMART_MIGRATION_LOCK_TIMEOUT` | `60s` | How long to wait for another process to finish migrating |
| `EDUMART_DB_MAX_OPEN_CONNS` | `20` | PostgreSQL connection ceiling per process. Unbounded is Go's default and fails only under pressure |
| `EDUMART_DB_MAX_IDLE_CONNS` | half of the above | Idle connections kept warm; clamped to the ceiling |
| `EDUMART_METRICS_TOKEN` | unset | Bearer token required by `GET /metrics`. Unset means loopback-only scraping, which is what local development wants and the internet does not |
| `EDUMART_BACKUP_HEARTBEAT` | unset | Path to the backup job's heartbeat file. Set it, or backup freshness cannot be alerted on |
| `EDUMART_WEB_URL` | `http://localhost:3000` | Origin used in links sent by email (invitations) |
| `EDUMART_NOTIFY` | `log` | `log` writes messages to the structured log; `smtp` delivers them |
| `EDUMART_SMTP_HOST` / `_PORT` | — / `587` | Relay host and port (required when `EDUMART_NOTIFY=smtp`) |
| `EDUMART_SMTP_USERNAME` / `_PASSWORD` | — | Relay credentials; set both or neither |
| `EDUMART_SMTP_FROM` | — | Envelope and header sender, e.g. `no-reply@edumart.ng` |
| `EDUMART_SMTP_TLS` | `starttls` | `starttls` (587), `tls` (465, implicit) or `none` |
| `EDUMART_SMTP_ALLOW_INSECURE` | `false` | Permits `tls=none`; refused when `EDUMART_ENV=production` |
| `EDUMART_API_URL` | `http://127.0.0.1:8080` | Where the Next.js server proxies `/v1` |
| `EXPO_PUBLIC_API_URL` | `http://localhost:8080` | Absolute API base for the native app |

---

## Payments in development

An order is created as `pending_payment`. Nothing on the client can mark it paid:

1. `POST /v1/orders` → order + provider reference.
2. The provider calls `POST /v1/webhooks/payments/sandbox` with `x-edumart-signature`
   (HMAC-SHA256 of the raw body).
3. The API verifies the signature, checks the amount and currency against the order, records the
   event in `provider_events` (unique per provider + event id) and only then flips the order to
   `paid` and grants entitlements.

In development the checkout screen offers **Simulate provider webhook (sandbox)**, which signs a
real event through the same code path, so the demo can never diverge from production behaviour.

---

## Deployment notes

- The API is a single binary: `npm run build:api` → `dist/edumart-api`. Both parts ship as container images
  (`make image`: `apps/api/Dockerfile`, `apps/web/Dockerfile`), built and run as one stack against a real
  database in CI.
- `infra/k8s` is the deployment: namespace with restricted Pod Security, migration Job, rolling Deployments,
  probes, PDBs, TLS Ingress, default-deny network policy. It is checked by `make manifests` and **has never
  been applied to a cluster** — see `docs/DEPLOYMENT.md`.
- The web app is a standard Next.js build (`npm run build`); it needs `EDUMART_API_URL` at runtime.
- PostgreSQL is the production system of record. Apply `apps/api/internal/store/migrations/postgres`,
  then set `EDUMART_DRIVER=postgres` and `EDUMART_DSN`.
- Run at least one API instance with the expiry sweep enabled (it is built into `cmd/api`); it closes
  attempts that passed their deadline even when nobody is polling.
- The mobile app is built with EAS (`apps/mobile/eas.json`); see `docs/MOBILE.md`.

- Behind a load balancer, set `EDUMART_TRUST_PROXY=true` so rate limits key on the real client address.
  Left `false` in front of a proxy, every request looks like it came from the proxy and shares one budget.

- Find things: `curl 'http://localhost:8080/v1/search?q=algebra'` — prefix search over the catalogue and
  syllabus, public, with visibility re-checked against the source tables on every query. `docs/SEARCH.md`
  explains the ranking and what it deliberately does not do.

- Load it before believing it: `python3 scripts/loadtest.py both --dsn "$DSN" --concurrency 96` drives the
  whole attempt lifecycle and then checks seven integrity invariants against the database it just wrote to.
  `docs/PERFORMANCE.md` records the numbers and what they do not cover.

- Scrape `GET /metrics` with a token set, and load `infra/observability/alerts.yml`. CI runs
  `scripts/check-alerts.py`, which fails when a rule names a metric the API does not emit — the failure mode
  of an alerting setup is silence, not noise. See `docs/OBSERVABILITY.md`.

- Back up with `python3 scripts/backup.py --engine postgres --dsn "$DSN" --dest /var/backups/edumart
  --mirror /mnt/offsite`; the systemd and CronJob templates in `infra/backup/` run it nightly with
  `--verify`, and `docs/BACKUP_AND_RECOVERY.md` covers WAL archiving and restoring to a point in time.

See `docs/PRODUCTION_READINESS.md` for the honest list of what is and is not ready,
`docs/THREAT_MODEL.md` for the assets, adversaries, controls and residual risks with owners, and
`docs/BACKUP_AND_RECOVERY.md` for what to do when the disk dies, and `docs/OBSERVABILITY.md` for what is
measured, what alerts on it, and what that alert means.
