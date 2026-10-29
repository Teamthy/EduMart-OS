# Edumart OS

Exam preparation for Nigerian learners: timed mocks, adaptive practice, class assessments, a
verified-payment store, learning analytics and an operations console.

Three deliverables, one product:

| Surface | Stack | Path |
| --- | --- | --- |
| **API** | Go 1.24+, chi router, SQLite (dev/CI) or PostgreSQL (prod) | `apps/api` |
| **Web / PWA** | Next.js 15 App Router, Tailwind CSS v4, shadcn/ui primitives | `apps/web` |
| **Mobile app** | React Native + Expo (Router), shipping alongside the PWA | `apps/mobile` |
| **Shared contract** | TypeScript wire types, validation, design tokens | `packages/shared` |

The server owns session state, the deadline, scoring and authorisation. Both shells are thin clients
of the same versioned REST API (`/v1`), which is what lets the web app and the native app ship from
one commit without drifting.

---

## Quick start

Requirements: **Go 1.24+**, **Node 20.11+**. No database server is needed — SQLite is embedded.

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

Neither is required to run the product: the API's default and CI-tested driver is SQLite, and rate
limiting is in-process. The file exists for the production-shaped path, and
`infra/docker/README.md` states exactly what does and does not talk to those services yet —
`EDUMART_DRIVER=postgres` fails at startup with an explicit "register the pgx driver" message
rather than pretending to work.

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
docs/                       architecture, API, data model, signup flow, mobile, readiness
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
| `EDUMART_WEBHOOK_SECRET` | `dev_sandbox_webhook_secret` | HMAC secret for payment webhooks |
| `EDUMART_ALLOW_SANDBOX_PAYMENTS` | `true` | Exposes the sandbox "simulate webhook" endpoint |
| `EDUMART_ALLOWED_ORIGINS` | `http://localhost:3000,http://localhost:8081` | CORS allowlist for native clients |
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

- The API is a single static binary: `npm run build:api` → `dist/edumart-api`.
- The web app is a standard Next.js build (`npm run build`); it needs `EDUMART_API_URL` at runtime.
- PostgreSQL is the production system of record. Apply `apps/api/internal/store/migrations/postgres`,
  then set `EDUMART_DRIVER=postgres` and `EDUMART_DSN`.
- Run at least one API instance with the expiry sweep enabled (it is built into `cmd/api`); it closes
  attempts that passed their deadline even when nobody is polling.
- The mobile app is built with EAS (`apps/mobile/eas.json`); see `docs/MOBILE.md`.

See `docs/PRODUCTION_READINESS.md` for the honest list of what is and is not ready.
