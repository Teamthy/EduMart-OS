# Phase plan status

Where Edumart actually stands against the phase-by-phase build plan. Every claim points at code, a
test or a document; nothing here is a projection.

**Legend**

- **Done** — implemented and covered by a test or CI check.
- **Partial** — real code exists and works, but the phase's exit criteria are not all met.
- **Not started** — nothing beyond scaffolding or design.

Last updated with the migration-runner, `/readyz` and session-management work.

---

## Phase 0 — Product definition and governance · **Partial**

| Deliverable | Status |
| --- | --- |
| Product requirements | `edumart-prd/` (provider document, 107 KB) |
| User journeys (student, teacher, institution, publisher, admin) | Student and teacher journeys are implemented end to end; institution and publisher journeys are **not** implemented |
| Responsive design system | `packages/shared` tokens + `apps/web/components/ui` (hand-written, shadcn conventions) and `apps/mobile/src/components/ui.tsx` |
| Architecture and domain boundaries | `docs/ARCHITECTURE.md` |
| API conventions and OpenAPI contract | `packages/contracts/openapi.yaml`, **enforced** by `scripts/check-contract.py` (51 routes) |
| Data classification and threat model | `docs/SECURITY_AND_DURABILITY.md` has the data classification and the consistency model; a formal threat model has **not** been written |
| Backlog, owners, acceptance criteria | `docs/BACKLOG.md` (backlog only — no owners assigned) |
| Definition of done | Below, in "Feature completion checklist" |

**Exit criteria:** scope approved ✗ (no formal sign-off recorded), measurable acceptance criteria
partial, contract agreed ✓, owners ✗.

---

## Phase 1 — Repository, infrastructure and CI/CD · **Partial**

| Item | Status |
| --- | --- |
| Monorepo, toolchains (Go / Next / Expo / TS) | Done — `apps/api`, `apps/web`, `apps/mobile`, `packages/shared` |
| Migrations **and a migration runner** | Done — `store/migrate.go`: `schema_migrations` ledger, sha256 checksums, one transaction per migration, applied-once, refuses a edited-after-apply file, adopts a pre-ledger database |
| PostgreSQL migrations | Written (`migrations/postgres/0001_init.sql`) but **never executed** — the pgx driver is not linked, so `EDUMART_DRIVER=postgres` fails at startup with an explicit message |
| Environments (dev/test/staging/prod) | Dev and CI are real; staging and production are documented (`docs/PRODUCTION_READINESS.md`) but not provisioned |
| Docker local dependencies | `infra/docker/compose.yml` (Postgres 17 + Redis 7). Neither is used by the app yet — SQLite is the tested driver, rate limiting is in-process |
| Structured logging, request IDs, error envelope | Done — `internal/httpx` |
| CI | Done — `.github/workflows/ci.yml`: gofmt, vet, contract check, Go tests with `-race`, web typecheck/tests/build, mobile typecheck + Metro bundle (Android/iOS/web), HTTP smoke |
| Secret/dependency scanning | **Not started** |
| Deployment + rollback procedure | Documented only; no deployment pipeline exists |
| `/readyz` checks real dependencies | **Done** — `internal/api/health.go`: database ping + real read, migration state, per-check breakdown, 503 on failure; `TestReadinessChecksRealDependencies` |

**Exit criteria:** clean checkout builds ☑, migrations create a working database from scratch ☑
(SQLite; **PostgreSQL unproven**), CI blocks failing merges ☑, secrets not committed ☑ (nothing
sensitive is in the tree), staging deployable ✗, `/readyz` real ☑.

---

## Phase 2 — Identity, signup and authorization · **Partial**

| Item | Status |
| --- | --- |
| Registration, login, logout | Done — `POST /v1/auth/{register,login,logout}` |
| Five-step stateful signup | Done on both clients; progress is server-persisted in `signup_drafts` and no account exists until the final step |
| Password hashing | **PBKDF2**, not Argon2id as the plan prefers. Argon2id needs `golang.org/x/crypto/argon2`; it is a drop-in change in `internal/auth/password.go` |
| Sessions, revocation | Done — opaque tokens, server-side revocation, `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}` (**new**), scoped by user id |
| Password reset | Done, with enumeration-safe responses and full session invalidation on reset |
| Email / phone verification | **Not started** — accounts are usable immediately. This is the largest identity gap and gates launch |
| Role-based authorization | Done — roles + class-scoped teacher access; `TestTenantAndRoleAuthorisation` |
| Refresh tokens / session rotation | Not started — single 7-day opaque session |
| Account recovery beyond password (lost email) | Not started |

**Exit criteria:** registration/login against PostgreSQL ✗ (SQLite only), verification tested ✗,
expired/revoked sessions rejected ☑ (`TestSessionListAndRevocation`,
`TestPasswordResetInvalidatesSessions`), horizontal escalation tests ☑, duplicate registration retry
safe ☑ (`TestDuplicateRegistrationIsRejected`).

---

## Phase 3 — Catalogue and content management · **Partial**

| Item | Status |
| --- | --- |
| Exam categories (JAMB/UTME, WAEC, NECO) | Pathways + subjects + topics seeded from `internal/seed/content.json` |
| Question bank, options, explanations | Done — `questions`, `question_versions`, options and explanation per version |
| Immutable question versions | Done — attempts snapshot the version used |
| Editorial review → published | Done — `POST /v1/admin/question-versions/{id}/review`, `TestContentReviewPublishesQuestions` |
| Content import pipeline | Seed JSON only; no validated CSV/JSON import tool or duplicate-key validation report |
| Provenance / rights metadata | Schema has source fields; no rights workflow |
| Withdrawal / supersede | Not started (publishing exists, withdrawal does not) |

**Exit criteria:** repeatable imports partial, no unpublished publication ☑, version history ☑,
permission-controlled access ☑, rights documented ✗, reviewed bank for pilot ✗ (seed content is
synthetic).

---

## Phase 4 — CBT and assessment engine · **Mostly done**

| Reliability rule | Status |
| --- | --- |
| Server owns start, deadline, final state | Done — `domain.CanAcceptAnswer`; client timer is display-only |
| Immutable snapshots + versioned scoring | Done — `assessment_sessions.snapshot_json`, `scoring_policy_json` |
| Idempotent answer mutations | Done — replay returns the stored version, stale writes `409` with `current_version` |
| Durable acknowledgement | Done — "saved" only after commit; the UI has distinct pending/saved/failed states |
| Transactional submission and scoring | Done — re-checked inside the transaction; double submit awards points once |
| Concurrent edits / timeout-after-commit | Covered by tests (replay + stale version); **not** load-tested |
| Cross-user attempt access | Done — `TestTenantAndRoleAuthorisation` |
| Corrections after submit | Practice only, rescored from stored responses, leaks no correctness |
| Offline queue on clients | Partial — mobile retries a failed save; there is no persistent offline queue |
| Load testing (start bursts, autosave, deadline spikes) | **Not started** |

**Exit criteria:** web/mobile journey ☑, duplicate submissions safe ☑, concurrency tested ☑,
deadlines enforced ☑, restart keeps acknowledged answers ☑, scoring reproducible ☑, **load tests ✗**.

---

## Phase 5 — Web, PWA and native mobile · **Partial**

| Item | Status |
| --- | --- |
| Web app (Next.js, Tailwind, shadcn-style UI) | Done — 20+ routes, authenticated shell, signup wizard |
| PWA | Manifest, icons, service worker, offline page — but no offline exam support or answer queue |
| Native app (Expo Router) | Done — same five signup steps, attempt runner, results; bundles for Android/iOS/web verified in CI |
| Shared contract + tokens | Done — desktop and native compile the same TypeScript and render the same tokens |
| Loading / empty / error / offline / permission-denied states | Implemented on P0 screens |
| Institution and admin route groups from the plan | **Not started** — the plan's `(institution)` and `(publisher)` areas do not exist |
| Accessibility audit | Designed to WCAG 2.2 AA; **not** verified with assistive technology |

---

## Phase 6 — Classrooms and institutions · **Partial**

Classes, join codes, rosters, assignments, teacher dashboards: done (`internal/classroom`,
`TestClassJoinByCode`). Organisations, organisation memberships, invitations with expiry,
institution reports and report exports: **not started**. There is no `organizations` table; tenancy
today is per-class.

---

## Phase 7 — Commerce · **Partial**

| Item | Status |
| --- | --- |
| Catalogue, orders, entitlements | Done |
| Verified webhook only | Done — HMAC-SHA256, `provider_events` dedupe, amount/currency check; `TestWebhookIsTheOnlySourceOfPaymentTruth` |
| Live payment provider adapter | **Not started** — sandbox only |
| Refunds, partial refunds, disputes, reconciliation | **Not started** |
| Publisher onboarding and revenue | **Not started** |
| Integer minor units end to end | Done, with formatting tests |

---

## Phase 8 — Rewards and analytics · **Partial**

Two separate append-only ledgers (points vs commission), deduplicated awards, referral validation:
done. Payouts, commission approval workflow, reversal tooling and published reward rules: **not
started**. Analytics are rebuildable read models derived from stored responses — no separate event
pipeline or transactional outbox yet.

---

## Phase 9 — Security, resilience, performance · **Not started (as a phase)**

Individual controls exist (rate limits, HMAC verification, tenant-scoped authorisation, security
headers, password hashing). The phase itself has not run: no threat model, no penetration test, no
load test, no backup or restore drill, no observability beyond structured logs, no alerts.

---

## Phase 10 — Staging, pilot, launch · **Not started**

No staging environment, no pilot, no production deployment, no on-call or incident process.

---

## Recommended next three steps

1. **Link the PostgreSQL driver and run the suite against it in CI.** The schema is ported and the
   migrations are now versioned; this is the single biggest gap between "works locally" and "works
   in production". Everything in Phase 2's exit criteria that says "against PostgreSQL" depends on it.
2. **Email verification (Phase 2).** The only remaining blocker to a public launch in identity:
   accounts currently become usable without proving ownership of the address.
3. **A backup + restore drill and metrics (Phase 9).** Cheap to do, and the difference between an
   incident and an outage.

---

## Feature completion checklist

The plan defines thirteen conditions for "done". Applied to the assessment engine as a worked
example:

| # | Condition | Assessment engine |
| --- | --- | --- |
| 1 | Requirements and acceptance criteria approved | partial — documented, not formally signed off |
| 2 | UX covers loading/empty/error/success | yes |
| 3 | API contract documented and validated | yes — CI-checked |
| 4 | Server-side authn/authz | yes — tested |
| 5 | DB constraints and migration | yes — versioned migrations now |
| 6 | Input validation, safe errors | yes — one error envelope |
| 7 | Idempotency and concurrency defined | yes — versions + replay |
| 8 | Unit and integration tests pass | yes — 33+ Go tests, `-race` in CI |
| 9 | Web and mobile integration tested | web: yes; mobile: bundle-verified, not UI-tested on a device |
| 10 | Accessibility and responsive checked | partial — unverified with assistive tech |
| 11 | Logs, metrics, audit events | logs yes; metrics and audit stream no |
| 12 | Documentation updated | yes |
| 13 | QA acceptance and release owner approval | no |
