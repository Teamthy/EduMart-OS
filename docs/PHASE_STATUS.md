# Phase status

Where Edumart actually stands against the thirty phases in
[`BUILD_BLUEPRINT.md`](./BUILD_BLUEPRINT.md). Every claim points at code, a test or a document.
Nothing here is a projection, and a phase is never "done" because its screens exist.

**Legend** — **Done**: implemented and covered by a test or CI check. **Partial**: real code works but
the phase's exit gate is not met. **Not started**: nothing beyond scaffolding or design.

Last updated with the email/phone verification work (Phase 7).

## The whole board

| # | Phase | Status | The gap that matters |
| --- | --- | --- | --- |
| 1 | Product specification | Partial | PRD exists; no recorded sign-off, no owners |
| 2 | Architecture and standards | Partial | Boundaries and ADR-style notes exist; no formal ADR index |
| 3 | Repository and developer environment | Done | — |
| 4 | Infrastructure and CI/CD | Partial | CI is real; staging **does not exist**, so deploy *and* rollback are unproven |
| 5 | Database and persistence | Partial | Both engines tested; **no backup or restore drill** |
| 6 | API platform and contracts | Done | 53 routes, contract checked in CI |
| 7 | Identity and account lifecycle | Partial | Verification now works; **no refresh-token rotation**, no account suspension |
| 8 | Authorization and multi-tenancy | Partial | Roles + class scoping; **no organisations, no audit trail** |
| 9 | Design system and shells | Partial | Tokens and components shared; **no accessibility verification** |
| 10 | Student onboarding and profiles | Partial | Five-step wizard works; **verification is not a mandatory step** |
| 11 | Taxonomy and syllabus | Partial | Exam → subject → topic; **no syllabus versioning** |
| 12 | Question bank and content ops | Partial | Authoring, review, publishing; **no import pipeline, no withdrawal** |
| 13 | Assessment configuration and snapshots | Partial | Snapshots and versioned scoring; **no assessment templates/sections** |
| 14 | CBT attempt lifecycle | Partial | Durable, idempotent, deadline-authoritative; **no load test** |
| 15 | Scoring and results | Done for practice and mocks | Re-scoring policy and adjustment audit trail missing |
| 16 | Student dashboard | Partial | Dashboard, weak topics, streaks; no syllabus-coverage view |
| 17 | PWA and offline | Partial | Installable and cache-aware; **no pending-write queue, no offline exams** |
| 18 | Native mobile | Partial | Journeys work; **no crash reporting, no signed release builds** |
| 19 | Classrooms and assignments | Partial | Classes, join codes, rosters, assignments; no expiry/usage limits on invites |
| 20 | Institutions and administration | Not started | No `organizations` table; tenancy is per-class today |
| 21 | Digital library and publications | Partial | Catalogue + entitlements; **no publisher onboarding, no versions** |
| 22 | Commerce and payments | Partial | Sandbox provider only; **no refunds, disputes or reconciliation** |
| 23 | Rewards and referrals | Partial | Two ledgers, dedupe, attribution; **no payouts or reversal tooling** |
| 24 | Notifications | Partial | Adapter + templates for codes; **no retries, no delivery status, no SMS provider** |
| 25 | Analytics and reporting | Partial | Learner aggregates; no institution reports, no metric definitions |
| 26 | Search and discovery | Not started | No search endpoint; lists only |
| 27 | AI capabilities | Not started | Deliberately last, per the blueprint |
| 28 | Security and resilience | Not started as a phase | Controls exist; **no threat model, no scanning, no drills** |
| 29 | Performance and QA | Not started | No load tests, no device matrix, no accessibility audit |
| 30 | Pilot and launch | Not started | No staging, pilot, on-call or incident process |

**Nine of thirty phases are done or effectively done.** The remaining twenty-one are real work, not
polish.

---

## Detail: the phases with substance

### Phase 3 — Repository and developer environment · Done

`apps/api` (Go), `apps/web` (Next.js), `apps/mobile` (Expo), `packages/shared`, `packages/contracts`,
`infra`, `docs`, `scripts`. Pinned toolchains, `make` targets for every routine task, `.env.example`
covering both engines, and `README.md` carrying the commands CI actually runs. Verified by CI doing a
clean install on every push.

### Phase 4 — Infrastructure and CI/CD · Partial

| Item | Status |
| --- | --- |
| CI quality gates | gofmt, vet, contract check, Go tests (`-race`) on **SQLite and PostgreSQL 17**, web typecheck/tests/`next build`, mobile typecheck + Metro bundle, HTTP smoke against both engines |
| Migration execution policy | `store/migrate.go`: checksummed, applied once, one transaction per migration, refuses an edited-after-apply file |
| Environments | Development and CI are real. **Staging and production are not provisioned** |
| Rollback | Documented in `docs/PRODUCTION_READINESS.md`; never exercised, because there is nothing to roll back |
| Secret/dependency scanning | **Not started** |

### Phase 5 — Database and persistence foundation · Partial

Both dialects are exercised by the whole suite. Constraints exist where they protect a real invariant
(unique email, FK cascades, ledger append-only triggers where applicable). Timestamps are UTC RFC3339
text. The gap is operational, not structural: no backup, no PITR, no restore rehearsal — so the exit
gate ("a restore has been demonstrated") is not met.

### Phase 6 — API platform and shared contracts · Done

53 routes in `packages/contracts/openapi.yaml`, enforced in both directions against the router by
`scripts/check-contract.py`. One error envelope with stable codes (`VALIDATION_ERROR`,
`UNAUTHENTICATED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `RATE_LIMITED`, `ASSESSMENT_NOT_OPEN`,
`SESSION_EXPIRED`, `SESSION_CLOSED`, `PAYMENT_PENDING`, `EMAIL_NOT_VERIFIED`, `INTERNAL_ERROR`).
Request ids on every response. Idempotency keys on retry-sensitive writes. `packages/shared` is one
TypeScript client used by web and mobile.

### Phase 7 — Identity and account lifecycle · Partial

| Item | Status |
| --- | --- |
| Registration, login, logout | Done |
| Password hashing | PBKDF2, not Argon2id. Argon2id needs `golang.org/x/crypto/argon2`; drop-in in `internal/auth/password.go` |
| Password recovery | Done, enumeration-safe, invalidates every session |
| Sessions and revocation | Done — opaque tokens, server-side revocation, `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}` |
| **Email and phone verification** | **Done** — `POST /v1/auth/verification/request` and `/confirm`; HMAC-protected, single-use, 15-minute, attempt-limited codes; E.164 normalisation; per-user throttling; delivery through a `notify.Sender` adapter |
| Verification gate | Checkout (`POST /v1/orders`) refuses an unverified address with `EMAIL_NOT_VERIFIED` |
| Rate limits | Per-route, **in-process** — resets on restart and does not span instances |
| Refresh tokens / rotation | **Not started** — one 7-day opaque session |
| Account suspension and recovery beyond password | **Not started** |

Verification is proven by `TestEmailVerificationJourney`, `TestCheckoutRequiresVerifiedEmail`,
`TestVerificationAttemptsAreLimited`, `TestVerificationResendIsThrottled`, `TestPhoneVerification`,
`TestVerificationRejectsBadInput`, `TestUndeliveredCodeIsNotUsable`, plus the live smoke run.

**What verification does not yet do:** it is not a required onboarding step, so an unverified account
can still practise, take mocks and hold points. Only money is gated. That is a deliberate, documented
scope decision — not an oversight.

### Phase 8 — Authorization and multi-tenancy · Partial

Roles (`student`, `teacher`, `org_admin`, `content_editor`, `finance`, `support`, `platform_admin`) and
class-scoped checks are implemented and covered by `TestTenantAndRoleAuthorisation`: a learner cannot
read another learner's attempt, a teacher cannot reach a class they do not teach, and a self-selected
signup role never becomes a privileged role. There is no `organizations` table, no membership model
and no privileged-action audit stream, so the phase's exit gate (cross-tenant isolation at the
organisation level) cannot be met yet.

### Phase 9 — Design system and shells · Partial

One token set (`packages/shared/src/design-tokens.ts`) drives Tailwind on web and React Native styles
on mobile. `apps/web/components/ui` follows shadcn conventions; `apps/mobile/src/components/ui.tsx`
mirrors it. Loading, empty, error, offline and permission-denied states exist on every P0 screen.
Accessibility is designed for WCAG 2.2 AA but **has not been verified with assistive technology**, so
the exit gate is unmet.

### Phase 10 — Student onboarding and profiles · Partial

The five-step wizard is implemented on web and mobile, with server-persisted drafts
(`signup_drafts`) that exclude passwords and verification secrets. An interrupted signup resumes at the
same step. The blueprint's step 2 is *verification*; today verification is offered after account
creation (settings) rather than inside the wizard, and the account is created at the end of step 5.
That ordering difference is the main gap.

### Phases 13–16 — The learning engine · Partial, and the strongest part of the build

Server-owned deadlines; immutable per-attempt question snapshots; versioned scoring rules; idempotent
answer writes with `409` + `current_version` on stale writes; acknowledgement only after commit;
transactional submission with re-check inside the transaction; double-submit awarding points once;
practice-only corrections that leak no correctness; deterministic re-scoring from stored responses.

Gaps: no assessment templates or sections (a mock is assembled from subject/topic rules), no negative
marking, no load test of concurrent starts or autosaves, and no syllabus-coverage view on the
dashboard.

### Phase 17 — PWA and offline · Partial

Installable manifest, branded icons, cache-versioned service worker, offline fallback page, and a
connection indicator. There is **no pending-write queue**: a save that fails is retried by the client
while the screen is open, and an answer that never reaches the server is visibly unsaved rather than
silently queued. Offline exams are explicitly out of scope until the separate design exists.

### Phases 22–23 — Commerce and rewards · Partial

Webhook-only payment truth with HMAC verification, provider-event dedupe and amount/currency checks.
Entitlements granted in the same transaction as the verified event. Purchase now also requires a
verified email. Points and commission ledgers are separate and append-only, with balances derived from
entries.

Gaps: sandbox provider only (no live Paystack adapter), no refunds, disputes or reconciliation job, no
commission approval or payout workflow, no reversal tooling for refunds.

### Phase 24 — Notifications · Partial

`internal/notify` defines the adapter (`Sender`), a development `LogSender` that redacts codes in
production, and a test `Recorder`. Verification codes and password reset links use it. There is no
provider integration, no template store, no retry/backoff, no delivery-status tracking, and no SMS or
push channel — so the blueprint's exit gate is not met.

---

## Recommended next three steps

1. **Phase 20 (institutions) or Phase 19 hardening.** Tenancy is per-class; every institutional
   feature — org dashboards, reports, renewals — is blocked on an `organizations` model. It is the
   largest structural absence.
2. **Phase 28's first slice: a threat model, dependency/secret scanning and a restore drill.** These
   are cheap, and they convert "we think it is safe and recoverable" into evidence.
3. **Phase 24: a real email provider with retries and delivery status.** Verification now depends on
   delivery, and today delivery is a log line.

## Feature completion checklist

Applied to email/phone verification as a worked example of the standard:

| # | Condition | Verification |
| --- | --- | --- |
| 1 | Requirements and acceptance criteria documented | yes — blueprint Phase 7 + contract |
| 2 | UX covers loading/empty/error/success | yes — settings card with sending, sent, error, verified states |
| 3 | Contract documented and validated | yes — two routes, CI-checked |
| 4 | Server-side authn/authz | yes — authenticated endpoints, codes bound to user + channel |
| 5 | DB constraints and migration | yes — 0002 on both dialects, CHECK on channel |
| 6 | Input validation, safe errors | yes — one error shape, no code or destination leakage |
| 7 | Idempotency and concurrency defined | yes — single-use claimed by affected-row count |
| 8 | Unit and integration tests pass | yes — 7 API tests + phone/mask unit tests, both engines |
| 9 | Web and mobile integration | web: yes; **mobile: not yet** (API and shared client are ready) |
| 10 | Accessibility checked | partial — labels, 44px targets, no screen-reader pass |
| 11 | Logs, metrics, audit events | logs yes; no metric, no audit stream |
| 12 | Documentation updated | yes — README, ARCHITECTURE, API, PHASE_STATUS, contract |
| 13 | QA acceptance and release owner approval | no |
