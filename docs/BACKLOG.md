# Edumart — Backlog and delivery status

Mapped to the PRD's engineering backlog. "Built" means implemented and exercised in this repository;
"Next" means the boundary exists but production work remains; "Deferred" follows the PRD's staged scope.

## P0 — launchable core

| ID | Deliverable | Status | Implementation notes |
| --- | --- | --- | --- |
| FOUND-01 | Personas, research, launch metrics | Deferred (product work) | metric definitions documented in PRD only |
| FOUND-02 | Architecture decisions & domain boundaries | **Built** | `docs/ARCHITECTURE.md`, module layout |
| FOUND-03 | Design system and responsive shell | **Built** | tokens + primitives in `globals.css`, role-aware shell, mobile tab bar |
| SEC-01 | Threat model & data classification | Partial | controls in `docs/PRODUCTION_READINESS.md`; formal threat model outstanding |
| AUTH-01 | Registration, verification, sessions, recovery | **Built** | scrypt, cookie sessions, revocation, reset tokens, rate limits. Email verification flow queued, not delivered |
| AUTH-02 | Role and tenant authorization | **Built** | role + org/class scope checks; 404 for cross-tenant probes |
| CONTENT-01 | Taxonomy & publishing workflow | **Built** | pathways/subjects/topics/syllabus version; publish requires provenance |
| CONTENT-02 | Question authoring & import | Partial | review queue and decisions built; bulk CSV import with row-level errors is next |
| LEARN-01 | Practice sessions and scoring | **Built** | snapshot per attempt, deterministic scoring, explanations after submit |
| EXAM-01 | Timed exam state machine and snapshots | **Built** | server deadlines, immutable snapshots, terminal states |
| EXAM-02 | Idempotent autosave and recovery UX | **Built** | versioned writes, retry replay, offline queue with visible state |
| RESULT-01 | Results and topic analytics | **Built** | breakdown, topic accuracy, trend, weak-topic recommendations |
| OPS-01 | Observability and alerting | Partial | audit + event tables, request ids; metrics/traces/alert routing next |
| OPS-02 | Backup, PITR, restore drill | Next | blocked on PostgreSQL; runbook written |
| REL-01 | CI/CD, migration, rollback | Partial | build/typecheck/tests; pipeline definition and migrations next |
| TEACH-01 | Classes, invitations, assignments | **Built** | codes, expiring/limited links, scheduled assessment, class rollup |
| PAY-01 | Verified payment and webhook processing | **Built (sandbox)** | signature verification, dedupe, amount checks, idempotent fulfilment |
| MKT-01 | Digital catalogue and entitlements | **Built** | private keys, signed links, refund revocation |
| LEDGER-01 | Separate points and commission ledgers | **Built** | append-only, idempotency keys, compensating reversals, payouts disabled |
| AI-01 | Approved-source AI tutor | Deferred | feature-flagged, not built per PRD staging |

## Next sprint candidates (dependency ordered)

1. **PostgreSQL adapter + migrations** — unblocks backups, PITR, real load testing.
2. **Email/notification worker** — verification, reset, deadline reminders; retries and dead-letter view.
3. **Provider integration** — replace the sandbox with a Nigerian provider; add refund execution and
   settlement reconciliation report.
4. **Authoring depth** — CSV import with row-level validation, duplicate detection, version compare.
5. **Observability** — structured log shipping, request metrics, exam save/submit success SLOs, alerts.
6. **MFA + access review** — TOTP for privileged roles, quarterly permission review export.
7. **Teacher exports** — authorized CSV of class results with audit entry.
8. **Study planner** — daily plan from weak topics and target date; reminders.

## Deferred by PRD policy

* Institution billing and seat plans (validate willingness to pay with pilot institutions).
* Publisher self-service portal, royalties and settlement statements.
* Monetary payouts, transfers or stored value — requires legal, accounting, provider and fraud sign-off.
* Native applications — PWA evidence first.
* AI tutor — grounded retrieval, evaluation harness and privacy review before any release.
