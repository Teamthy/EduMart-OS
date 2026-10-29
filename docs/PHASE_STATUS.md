# Phase status

Where Edumart actually stands against the thirty phases in
[`BUILD_BLUEPRINT.md`](./BUILD_BLUEPRINT.md). Every claim points at code, a test or a document.
Nothing here is a projection, and a phase is never "done" because its screens exist.

**Legend** — **Done**: implemented and covered by a test or CI check. **Partial**: real code works but
the phase's exit gate is not met. **Not started**: nothing beyond scaffolding or design.

Last updated with the outbound-delivery work (Phase 24, plus the Phase 7 delivery path).

## The whole board

| # | Phase | Status | The gap that matters |
| --- | --- | --- | --- |
| 1 | Product specification | Partial | PRD exists; no recorded sign-off, no owners |
| 2 | Architecture and standards | Partial | Boundaries and ADR-style notes exist; no formal ADR index |
| 3 | Repository and developer environment | Done | — |
| 4 | Infrastructure and CI/CD | Partial | CI is real; staging **does not exist**, so deploy *and* rollback are unproven |
| 5 | Database and persistence | Partial | Both engines tested; **no backup or restore drill** |
| 6 | API platform and contracts | Done | 64 routes, contract checked in CI |
| 7 | Identity and account lifecycle | Partial | Verification and delivery are solid, and accounts can now be suspended with their sessions; **no refresh-token rotation**, PBKDF2 rather than Argon2id |
| 8 | Authorization and multi-tenancy | Partial | Organisations, memberships, invitations, approval and audit events exist, and classes can belong to an institution; **no org-scoped reporting, suspension or ownership transfer** |
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
| 20 | Institutions and administration | Partial | Creation, membership, invitations, an approval queue with refusal and audit all work; **no institution dashboards, data export, retention rules or console** |
| 21 | Digital library and publications | Partial | Catalogue + entitlements; **no publisher onboarding, no versions** |
| 22 | Commerce and payments | Partial | Sandbox provider only; **no refunds, disputes or reconciliation** |
| 23 | Rewards and referrals | Partial | Two ledgers, dedupe, attribution; **no payouts or reversal tooling** |
| 24 | Notifications | Partial | Durable queue, retries, SMTP delivery, per-message delivery records for codes, resets, invitations and receipts; **no SMS/push provider, no reminders** |
| 25 | Analytics and reporting | Partial | Learner aggregates; no institution reports, no metric definitions |
| 26 | Search and discovery | Not started | No search endpoint; lists only |
| 27 | AI capabilities | Not started | Deliberately last, per the blueprint |
| 28 | Security and resilience | Not started as a phase | Controls exist; **no threat model, no scanning, no drills** |
| 29 | Performance and QA | Not started | No load tests, no device matrix, no accessibility audit |
| 30 | Pilot and launch | Not started | No staging, pilot, on-call or incident process |

**Nine of thirty phases are done or effectively done.** The remaining twenty-one are real work, not
polish. Phase 8 and Phase 20 are now partially done: the model exists and is enforced, the reports do
not.

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

64 routes in `packages/contracts/openapi.yaml`, enforced in both directions against the router by
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
| **Email and phone verification** | **Done** — `POST /v1/auth/verification/request` and `/confirm`; HMAC-protected, single-use, 15-minute, attempt-limited codes; E.164 normalisation; per-user throttling; delivery through the durable courier (Phase 24), so a relay outage retries instead of losing the code |
| Verification gate | Checkout (`POST /v1/orders`) refuses an unverified address with `EMAIL_NOT_VERIFIED` |
| Rate limits | Per-route, **in-process** — resets on restart and does not span instances |
| Refresh tokens / rotation | **Not started** — one 7-day opaque session. Rotating refresh tokens without short-lived access tokens would be theatre, so this needs a client session strategy first |
| **Account suspension** | **Done** — `POST /v1/admin/users/{id}/suspend` and `/reinstate`, platform administrators only, never against an administrator or yourself |
| Moderation lookup | `GET /v1/admin/users?q=&status=` — an administrator cannot suspend an account they cannot find |
| Security-event audit trail | Suspension and reinstatement write `user.suspended` / `user.reinstated` to `audit_logs` in the same transaction as the change |

Verification is proven by `TestEmailVerificationJourney`, `TestCheckoutRequiresVerifiedEmail`,
`TestVerificationAttemptsAreLimited`, `TestVerificationResendIsThrottled`, `TestPhoneVerification`,
`TestVerificationRejectsBadInput`, `TestUndeliveredCodeIsRetriedThenWithdrawn`,
`TestDeliveryIsRecordedAndVisibleToStaff`, plus the live smoke run and a manual outage drill against a
real SMTP relay.

**What verification does not yet do:** it is not a required onboarding step, so an unverified account
can still practise, take mocks and hold points. Only money is gated. That is a deliberate, documented
scope decision — not an oversight.

**What suspension guarantees**, and what it does not:

- Status, the reason, the actor, every live session and any outstanding verification code change in one
  transaction. A suspension that leaves a working session behind is not a suspension.
- `AuthenticateSession` also refuses any non-active account, so the rule survives a restored backup, a
  replica that has not caught up, or a bug in the revocation path.
- Reinstatement does **not** restore sessions. The person signs in again, which re-establishes that they
  still control the mailbox.
- Proven by `TestSuspensionEndsSessionsAndBlocksSignIn`, `TestSuspensionRevokesOutstandingCodes`,
  `TestSuspensionIsReservedForAdministrators`, `TestSuspensionIsStoredOnTheAccount`,
  `TestReinstateRequiresASuspendedAccount`, `TestStaffCanFindAnAccountToModerate`, and the moderation
  section of the smoke run.
- Not covered: there is no admin web console for moderation yet (the API is what exists), no suspension
  expiry or graduated limits, and no appeal workflow.

### Phase 8 — Authorization and multi-tenancy · Partial

**Correction:** an earlier version of this page claimed there was no `organizations` table. That was
wrong — `organizations`, `organization_memberships` and `invitations` were in the baseline schema all
along, unused by any Go code. What was missing was the model, not the tables.

Implemented now (`internal/orgs`):

| Item | Evidence |
| --- | --- |
| Organisations and membership | `POST/GET /v1/organizations`, owner created with the organisation |
| Approval flow | New organisations start `pending_verification`; only a platform administrator can approve (`/v1/admin/organizations/{id}/verify`) |
| Invitations | Single-use, email-locked, seven-day tokens stored as hashes; withdrawing is separate from expiry; a new invitation supersedes an older open one |
| Role assignment | Membership roles (`owner`, `admin`, `teacher`, `student`); a teacher invitation is the one path that grants the platform teacher role, because an owner deliberately issued it |
| Role revocation | Removing a membership deletes the membership and the teacher role when no other organisation still grants it — access ends in the same transaction |
| Tenant scoping | Every organisation query is scoped by membership; non-members get 404, not 403, so the API cannot be used to enumerate institutions |
| Audit trail | `organization.*` events written to `audit_logs` with actor, resource and metadata, inside the same transaction as the change |
| Institution classes | `POST /v1/classes` accepts `organizationId`; the caller must own or administer that institution. `GET /v1/organizations/{id}/classes` lists them for its owners and administrators |
| Negative tests | `TestOrganizationLifecycle`, `TestInvitationIsEmailLockedAndSingleUse`, `TestInvitationRevocationAndExpiry`, `TestCrossTenantIsolation`, `TestRosterIsForAdministrators`, `TestRemovingMembershipRemovesAccess`, `TestLastOwnerCannotBeRemoved`, `TestInstitutionClassesBelongToTheInstitution`, `TestPersonalClassesAreUnaffected` |

Class ownership is deliberately narrow: a teacher's personal class is theirs and never appears in an
institution's reporting, and a class is only attached to an organisation when an owner or administrator
creates it that way.

Still missing: org-scoped reporting and analytics, membership suspension, ownership transfer, an admin
surface that lists pending organisations, organisation-level billing, and SSO.

### Phase 20 — Institutions and administration · Partial

| Item | Evidence |
| --- | --- |
| Approval queue | `GET /v1/admin/organizations?status=` — pending institutions oldest-first, with the applicant's contact address. Platform administrators only |
| Refusal with a record | `POST /v1/admin/organizations/{id}/reject` `{reason}` → `rejected`, the reason in `audit_logs`, and the record kept. A rejected institution can still be approved later: refusing an application is a decision, not a permanent ban |
| Approval | `POST /v1/admin/organizations/{id}/verify` — the only path from `pending_verification` to `active` |
| Institution classes | `GET /v1/organizations/{id}/classes` with member and assignment counts (Phase 19) |
| Institution dashboard | `GET /v1/organizations/{id}/report` — members by role, classes, published assignments, submissions, active learners and a 30-day average, plus a per-class breakdown. Every query is scoped by organisation id in SQL; teachers get `403`, non-members `404`. Shown on `/institutions` in the web app |

Proven by `TestApprovalQueueIsTheStaffView`, `TestRejectingAnInstitutionKeepsTheRecord`,
`TestInstitutionReportCountsWhatHappened`, `TestInstitutionReportIsForAdministrators`, and the
institution, approval and reporting sections of the smoke run.

Two decisions in the report are worth stating because they are the difference between a dashboard and a
misleading one. The window is a fixed 30 days rather than "this term", because the product has no calendar
concept yet and a window that silently means "everything ever" makes every trend look flat. And an average
with no submissions is `null`, not `0`: zero is a claim about performance, null is the absence of data.

Still missing: organisation-level data export and retention rules,
a platform-wide view of suspended institutions (`status = 'suspended'` is defined but nothing sets it),
and a web console — the API exists, the screen does not.

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

### Phase 24 — Notifications · Partial (delivery is now real)

`internal/notify` owns delivery end to end:

| Item | Evidence |
| --- | --- |
| Durable queue | `notification_deliveries` (migration `0004`), written **before** the first attempt, so a crash between deciding to send and sending still retries |
| Retries and backoff | 30 s → 2 m → 10 m → 30 m, five attempts, swept by a worker that survives restarts — proven live: a stopped relay recovered on its own at ~50 s |
| Real provider | `SMTPSender` (STARTTLS / implicit TLS / plain), verified against a socket in `smtp_test.go` and against a sandbox relay in the smoke run |
| Templates | Code, not database rows: a message body editable at runtime is a phishing vector aimed at our own users |
| Credential hygiene | An abandoned delivery **deletes** the verification code or reset token it carried; delivered bodies are cleared; `GET /v1/admin/deliveries` never returns a body |
| Messages that actually go out | Verification codes, password resets, organisation invitations, purchase receipts |
| Observability | `GET /v1/admin/deliveries` with `{sent, pending, failed}`; `FailedSender` injections are covered by tests |

**Not done, and not claimed:** SMS and push channels (no provider wired; `notify.Channel` already has
the seam), class/assessment reminders (there is no assignment model to remind about yet), digests,
bounce and complaint feedback loops, and per-tenant sender identities.

---

## Recommended next three steps

1. **Phase 19: link classes to organisations.** `classes.organization_id` exists and no code path sets
   it, so an institution cannot yet own a class — which blocks org-scoped reporting, assignment
   ownership and institution billing.
2. **Phase 7 hardening: Argon2id, refresh-token rotation and account suspension.** The lifecycle works,
   but a stolen session lives until it expires and there is no way to suspend an abusive account.
3. **Phase 28's first slice: a threat model, dependency and secret scanning, and a restore drill.**
   Cheap, and they turn "we believe it is recoverable" into evidence.

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
