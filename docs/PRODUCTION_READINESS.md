# Edumart — Production readiness plan

This document is the gap analysis between the implemented baseline and a production launch. It follows
the PRD's launch gates; nothing here should be treated as already satisfied unless marked.

## 1. Security

Implemented| Control | Evidence in repo |
| --- | --- |
| Salted scrypt hashing (N=16384, r=8, p=1) | `src/lib/server/password.ts` |
| HttpOnly, SameSite=Lax, Secure-in-production session cookie | `src/lib/server/auth.ts` |
| Server-side session revocation on sign-out and password reset | `auth.ts`, `auth/password/reset` |
| Same-origin check on every state-changing handler | `assertSameOrigin` in `http.ts` |
| Rate limits: register, login (per IP and per account), reset, reports, class creation | `rateLimit` |
| Uniform auth failures (no account enumeration) | login + forgot handlers |
| Server-side authorization and tenant scoping on every protected read/write | `classes.ts`, route handlers |
| No stack traces or secrets in responses; request id returned | `handle()` |
| Security headers (nosniff, frame options, referrer policy, permissions policy) | `next.config.ts` |
| Parameterised SQL only | `db.ts` helpers |
| Append-only audit trail for privileged actions | `audit.ts`, admin routes |

Outstanding before launch:

1. MFA (TOTP) for platform admins, finance and institution admins.
2. Formal threat model workshop and data classification register.
3. Dependency/container/secret scanning wired into CI, plus protected branches and peer review rules.
4. CSRF token strategy if cookie auth is extended cross-site; today same-site only.
5. Penetration test of authentication, authorization and payment flows.
6. Redis-backed distributed rate limiting (the interface is already isolated in `http.ts`).

## 2. Privacy and data protection

* Data minimisation is visible in the product: name, email, pathway, target date only; no date of
  birth, location or public rankings.
* Retention and deletion: settings exposes export/deletion requests which become support cases today;
  automated export/deletion jobs and a documented retention schedule are outstanding.
* Learner records are readable only by the learner, their class teachers and privileged operations
  roles; class-level teacher views exclude other learners' individual scores for students.
* Minors: additional safeguards and guardian consent policy need legal review before institution
  onboarding.
* Analytics events carry ids and coarse metadata, never answer text or payment payloads.
* Nigerian data-protection, consumer-protection, tax and copyright review must precede real-money launch.

## 3. Observability

Present: request ids on every API response, structured audit and learning-event tables, a durable job
table with attempt counters, reconciliation mismatch counter in the admin console.

Required before launch:

* Metrics: request rate/errors/latency per route, DB pool saturation, job lag, **answer-save success
  rate**, **submission success rate**, payment state age, webhook failures, reconciliation differences.
* Traces across API → database → queue for exam start, save, submit and payment confirmation.
* Alerts with named owners: submission failures, exam save failures, webhook signature failures,
  reconciliation mismatches, backup failures, auth anomaly spikes.
* Business dashboards: activation, attempt completion, content reports, purchases, refunds, support backlog.

## 4. Backup, recovery and durability

| Item | Status |
| --- | --- |
| Automated database backups + PITR | Not applicable to SQLite dev; required on PostgreSQL |
| Independent backup boundary (separate account/region, restricted access) | To do |
| Restore drill verifying real workflows (login, attempt, submit, entitlement) | To do — runbook drafted below |
| RPO/RTO defined per data class | To do (proposal: sessions/responses RPO ≤ 1 min via WAL/PITR, RTO ≤ 4 h) |
| Idempotent retries for critical writes | Implemented |
| Durable submission receipts | Implemented (`assessment_sessions` + idempotency records) |

Restore drill outline: restore snapshot into an isolated environment → run migrations → verify counts
and checksums → log in as a seeded learner → start and submit an attempt → confirm score and points
entry → open a paid entitlement → record evidence in the release log.

## 5. Quality gates

| Gate | Current evidence |
| --- | --- |
| Unit + integration tests | `npm test` — 23 tests (scoring, state machine, engine invariants) |
| Type checking | `npm run typecheck` clean |
| Production build | `npm run build` clean |
| Authorization tests | cross-user and cross-tenant cases in `tests/engine.test.ts` and manual smoke tests |
| Load test at expected pilot peak | To do |
| Accessibility review (WCAG 2.2 AA target) | Design supports it (focus states, contrast, labels, aria-live); formal audit to do |
| PWA install/offline test on real devices | To do (service worker and offline page implemented) |
| End-to-end browser suite | To do — add Playwright against staging |

## 6. Operations runbooks to write before launch

1. Assessment submission failure or widespread save latency.
2. Payment provider outage, delayed webhooks, reconciliation mismatch.
3. Defective question or scoring incident, including affected-learner review.
4. Suspected data exposure or account takeover.
5. Database availability/capacity incident and restore.
6. Bad deployment: rollback and feature-flag disablement.
7. Content takedown and publisher rights dispute.
8. Referral/commission dispute.

## 7. Launch gates (go / no-go)

| Gate | Owner | Status |
| --- | --- | --- |
| P0 acceptance criteria met, limitations recorded | Product | Baseline built; formal sign-off pending |
| 100% of published questions have provenance and review | Content ops | True by construction (API refuses otherwise) |
| No known critical timer/scoring/submission defect | Backend + QA | Covered by tests; exploratory pass pending |
| No unresolved high/critical security findings | Security | Pending pen test and scans |
| Verified payments, refunds, reconciliation rehearsed | Backend + finance | Sandbox path implemented; real provider pending |
| Separate ledgers, reversals and fraud controls tested | Backend + finance | Implemented; payouts intentionally disabled |
| Load test meets agreed capacity | Platform | Pending |
| Backup restore demonstrated with RTO/RPO recorded | Platform | Pending (needs PostgreSQL) |
| Monitoring and alert ownership assigned | Platform | Pending |
| Support playbooks and severity model published | Operations | Pending |
| Staged rollout with rollback trigger and on-call roster | Product + engineering | Pending |

**A GO decision requires every mandatory gate to have evidence and a named owner. Unresolved critical
security findings, scoring corruption, unexplained ledger discrepancies, or an untested restore are
automatic NO-GO conditions.**
