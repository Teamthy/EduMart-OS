# Edumart build blueprint — 30 phases

This is the engineering roadmap: what gets built, in what order, and what must be true before the
next phase starts. It replaces the earlier ten-phase outline, which folded several of these phases
together and hid the dependencies between them.

**This document is a plan, not a status report.** For what actually exists today, and the evidence
for each claim, read [`PHASE_STATUS.md`](./PHASE_STATUS.md). The two files are deliberately separate:
a roadmap that doubles as a scoreboard stops being honest about either.

## How to read a phase

Every phase has the same three parts.

- **Objective** — the question the phase answers.
- **Build** — the work items. Not all of them are equally urgent; the exit gate is what matters.
- **Exit gate** — the evidence required before the phase counts as done. A later phase must not
  bypass a failed gate in an earlier dependency.

## Governing principle

> Build the foundation once, enforce critical rules on the server, make important writes durable, and
> expand features only when their dependencies are reliable.

Three corollaries the codebase is held to:

1. **The server is the authority.** Deadlines, scores, permissions, prices, entitlements. A client
   timer, a client total and a client role claim are display or intent, never truth.
2. **A write is real when the database says so.** "Saved" is a claim about durable state; returning
   it before commit is a lie that shows up as lost work during an exam.
3. **Records are corrected, not rewritten.** Financial and scoring history is append-only plus
   compensating entries, because an auditable history that can be edited is not auditable.

---

# Part I — Engineering foundation

## Phase 1 — Product specification and requirements

**Objective** Fix exactly what Edumart delivers, for whom, and what success means.

**Build** A PRD covering MVP, later releases and explicit exclusions; journeys for student, teacher,
institution, publisher and administrator; must/should/later prioritisation; business rules for exams,
institutional access, payments, points and commissions; functional and non-functional requirements;
acceptance criteria per feature; product metrics and launch criteria.

**Exit gate** Product, design and engineering agree on scope, and the critical business-rule
ambiguities are resolved rather than deferred.

## Phase 2 — System architecture and engineering standards

**Objective** Set the technical boundaries before feature work multiplies them.

**Build** A Go modular monolith with explicit domain packages; Next.js web/PWA and Expo clients;
PostgreSQL as the system of record; Redis for caching, rate limiting and ephemeral coordination;
object storage for media; durable background processing; architecture decision records; dependency
direction, package boundaries and error-handling standards.

**Exit gate** Every domain has an owner, a data responsibility and an interface.

## Phase 3 — Repository and developer environment

**Objective** Make the project reproducible for every engineer.

**Build** `apps/web`, `apps/mobile`, `apps/api`, `packages/contracts`, `packages/shared`, `infra` and
`docs`; standard Go and Node versions; formatting, linting, type checks and tests; `.env.example` with
configuration validation; branch protection, pull-request templates and code ownership; onboarding and
troubleshooting docs.

**Exit gate** A new team member clones, installs, starts local services and runs the tests using only
the documented commands.

## Phase 4 — Infrastructure and CI/CD

**Objective** Automate validation and safe delivery.

**Build** Local Compose; separate development, test, staging and production configuration; CI for
formatting, type checks, compilation, tests and security scans; artifact builds and staging deploy;
secret management and least-privilege deployment credentials; a migration execution policy; rollback
or safe forward-fix; release approval and a deployment audit trail.

**Exit gate** A failed quality gate blocks release, and staging deploy *and* recovery have been
exercised, not merely described.

## Phase 5 — Database and persistence foundation

**Objective** Durable, consistent storage.

**Build** PostgreSQL connection pooling and health checks; versioned migrations; primary, foreign,
unique and check constraints; explicit transaction boundaries and isolation; UTC timestamps with
explicit timezone handling; an index strategy derived from real queries; backup and point-in-time
recovery; migration tests and restore rehearsals.

Logical areas: identity, organizations, authorization, catalog, content, assessment, results,
classroom, publications, commerce, rewards, notifications, audit — domains, not necessarily databases.

**Exit gate** Migrations build an empty database, constraints protect the critical invariants, and a
restore has been demonstrated.

## Phase 6 — API platform and shared contracts

**Objective** One stable interface every client integrates against.

**Build** OpenAPI; consistent URL and resource naming; request validation and a standard error
schema; request/correlation ids; pagination, filtering and sorting conventions; versioning and
compatibility rules; idempotency for retry-sensitive writes; generated TypeScript types or equivalent
contract checks; contract tests for Go endpoints.

```json
{
  "code": "VALIDATION_ERROR",
  "message": "The request could not be validated.",
  "field_errors": { "email": "Enter a valid email address." },
  "request_id": "req_example"
}
```

**Exit gate** Web and mobile consume the same published contract, and a breaking change fails CI.

---

# Part II — Identity, UI and content

## Phase 7 — Identity and account lifecycle

**Objective** Secure account creation and authentication.

**Build** Registration and verification; login and logout; password hashing and recovery; session
issuance, renewal and revocation; email and phone verification adapters; rate limits and abuse
controls; account status and suspension; session and security-event audit trails.

Endpoints (`/v1` in this repository): `POST /auth/register`, `POST /auth/verify-email`,
`POST /auth/login`, `POST /auth/refresh`, `POST /auth/logout`, `POST /auth/password/forgot`,
`POST /auth/password/reset`, `GET /me`, `PATCH /me`.

**Exit gate** The lifecycle works end to end, revoked sessions fail, recovery is safe, and duplicate
requests cannot create duplicate accounts.

## Phase 8 — Authorization and multi-tenancy

**Objective** Enforce access boundaries for individual and institutional users.

**Build** Roles and permissions; organisation membership and invitations; resource-level policies;
tenant scoping in queries; role assignment and revocation; administrative access controls; audit
logging for privileged actions; negative access tests.

**Core rule** Choosing "teacher" or "institution admin" at signup must never grant privileged access.
Privilege comes from an invitation, a verification or an approval — never from a self-selected label.

**Exit gate** Tests prove a user cannot reach another user's records, an unrelated class, or another
organisation's resources by changing an id.

## Phase 9 — Design system and application shells

**Objective** A consistent, accessible interface.

**Build (web)** Next.js layouts and route groups; Tailwind tokens; shadcn/ui integration; navigation,
forms, dialogs, tables, cards, charts; loading, empty, error and permission-denied states; responsive
dashboards. **(mobile)** Expo navigation; shared tokens; safe-area and keyboard handling; platform
loading and error states.

**Exit gate** Representative screens match approved designs, and accessibility checks cover keyboard,
focus, contrast and form labels.

## Phase 10 — Student onboarding and profiles

**Objective** Guide learners into the right learning journey.

**Five steps** account details → verification → basic profile → exam, subjects and goals → review and
completion.

**Build** Stateful step navigation; per-step validation; safe draft persistence that excludes
passwords and verification secrets; server-side final validation; profile and preferences; onboarding
completion state; recovery from an interrupted signup; distinct paths for student, teacher, institution
and publisher.

**Exit gate** Users can complete, resume and retry onboarding without duplicate accounts or accidental
privilege grants.

## Phase 11 — Education taxonomy and syllabus

**Objective** A structured catalogue of exams and topics.

**Build** Examination categories; exam-specific subjects; topic and subtopic hierarchy; syllabus
versions and effective dates; content-to-syllabus mapping; administrative taxonomy management;
validation for missing or duplicated mappings.

```
JAMB / UTME
├── Mathematics
│   ├── Algebra
│   ├── Geometry
│   └── Statistics
├── English Language
└── Physics
    ├── Mechanics
    └── Waves
```

Illustrative only: real mappings must be verified against the applicable syllabus.

**Exit gate** Questions and assessments can be reliably tied to the correct exam, subject, topic and
syllabus version.

## Phase 12 — Question bank and content operations

**Objective** A trusted, reviewable question repository.

**Build** Authoring and editing; multiple-choice options and answer keys; explanations and source
metadata; bulk import with validation; duplicate detection; editorial review and approval; publication
and withdrawal; media handling and access control; immutable published versions; content-rights
tracking.

**Exit gate** Invalid or unreviewed questions cannot be published, imports are repeatable, and version
history is preserved.

---

# Part III — Core learning engine

## Phase 13 — Assessment configuration and snapshots

**Objective** Define how an assessment is assembled and scored before a learner starts it.

**Build** Templates and versions; duration, question count and attempt limits; sections; selection and
randomisation rules; marking schemes including negative marking and partial credit where supported;
immutable snapshots of questions, options, keys and scoring rules; availability windows; administrative
preview and validation.

Data model: `assessment_templates`, `assessment_versions`, `assessment_sections`,
`assessment_question_snapshots`, `scoring_rules`.

**Exit gate** Every attempt is tied to a stable version, and editing a published template cannot
silently change an existing attempt.

## Phase 14 — CBT attempt lifecycle

**Objective** Timed exams that stay dependable through interruptions, retries and concurrency.

**Build** Creation and eligibility; server-issued start and deadline; answer saving with version
checks; autosave and retry-safe mutations; resume after disconnection; final submission and timeout
handling; duplicate-submission protection; an attempt state machine with audit events.

```
CREATED → IN_PROGRESS → SUBMITTED | EXPIRED | CANCELLED (defined rules only)
```

Permitted transitions are explicit and transactional.

**Exit gate** Repeated submissions, network timeouts, multi-device edits and deadline races cannot
corrupt an attempt or produce duplicate results.

## Phase 15 — Scoring and results

**Objective** Accurate, reproducible results.

**Build** A deterministic scoring service; answer-key validation; scoring-rule versioning; result
persistence and status transitions; topic and subject breakdowns; answer review where permitted;
attempt history; a re-scoring policy for verified content or scoring defects; an audit trail for
authorised adjustments.

**Exit gate** The same stored snapshot and rules reproduce the same result, and no client request can
alter a score.

## Phase 16 — Student dashboard and learning experience

**Objective** Turn raw assessments into daily learning workflows.

**Build** Dashboard; exam and subject discovery; topic practice; timed mocks; recent attempts and
review; weak-topic identification; syllabus coverage; streaks and goals; empty, error and first-use
states; responsive charts.

**Exit gate** Dashboard numbers agree with authoritative attempt records, and a learner can go from
discovery to practice to review to progress without a broken flow.

## Phase 17 — PWA and offline-aware learning

**Objective** Browser learning on unstable networks without risking private data.

**Build** Installable manifest and icons; service-worker lifecycle and cache versioning; explicit
caching rules; offline detection and reconnection; a pending-write queue for approved mutations;
mutation ids, retry handling and conflict reconciliation; clear acknowledged-versus-pending states;
cache invalidation and app-update handling.

**Design boundary** Offline browsing and offline exams are different capabilities. Offline exams need
device storage, content expiry, answer encryption, integrity limits and sync rules designed as a
separate feature.

**Exit gate** No false "saved" acknowledgement, no silent overwrite of a newer answer, and no
uncontrolled caching of authenticated data.

## Phase 18 — Native mobile application

**Objective** iOS and Android apps under the same backend rules as web.

**Build** Expo structure and navigation; authentication and secure session storage; onboarding and
profile; dashboard, catalogue and practice; attempt and answer synchronisation; results and progress;
network and app-lifecycle handling; push readiness; accessibility, device testing and release builds;
crash reporting and release procedures.

**Exit gate** Critical journeys work on supported devices, interrupted sessions and network changes
have defined behaviour, and production API configuration and signing are documented.

---

# Part IV — Institutional and commercial capabilities

## Phase 19 — Classrooms and assignments

**Objective** Let teachers organise learners and assign work.

**Build** Class creation and editing; teacher and student membership; invitation links or codes with
expiry and usage limits; assignment creation and scheduling; availability and due dates; completion
tracking; teacher dashboards and permitted reports; membership removal and audit events.

**Exit gate** Teachers manage only authorised classes, invitations follow explicit security rules, and
assignment reports respect learner privacy.

## Phase 20 — Institutions and administration

**Objective** Organisation-level management and governance.

**Build** Organisation onboarding and verification; membership administration; organisation roles and
policies; class and teacher management; access-policy integration where applicable; institution
dashboards; audit trails and controlled administrative actions; suspension and recovery; organisation
data export and retention rules.

**Exit gate** Cross-tenant isolation tests pass, administrative changes are auditable, and removing
membership removes access.

## Phase 21 — Digital library and publications

**Objective** Approved publishers distribute educational material.

**Build** Publisher onboarding and approval; publication metadata and categories; versions and
updates; document/media upload and processing; content review and rights validation; searchable
catalogue; access permissions; reader or download experience; withdrawal and version history.

**Exit gate** Only approved, rights-cleared publications are distributed, and access follows the
applicable entitlement and licensing rules.

## Phase 22 — Commerce and payment processing

**Objective** Accurate purchases and entitlements.

**Build** Products, prices and currency; orders and items; payment initiation; a provider adapter;
signed webhook verification; idempotent event processing; reconciliation; entitlement grants and
revocations; refunds, failures and disputes; transaction history and receipts; financial audit records.

**Exit gate** Success is never inferred from a client redirect, a duplicate webhook cannot duplicate a
purchase, and internal state can be reconciled with the provider.

## Phase 23 — Rewards and referral system

**Objective** Engagement points and eligible monetary commissions as separate systems.

**Build** Activity eligibility rules; a points ledger and derived balances; referral codes and
attribution; commission eligibility and approval; a commission ledger; fraud and abuse checks;
reversals on refund; payout requests and settlement where enabled; administrative adjustments with
reasons and audit events.

**Exit gate** Duplicate events cannot generate duplicate rewards, money is handled safely, and every
balance is explainable from its ledger entries.

---

# Part V — Intelligence and platform operations

## Phase 24 — Notifications and communications

**Objective** Reliable, user-controlled communications.

**Build** Email and SMS adapters; push; templates and localisation; verification and recovery messages;
reminders and result notifications; delivery attempts, retries and failure handling; preferences and
opt-outs; delivery status; durable asynchronous processing.

**Exit gate** Retries do not produce uncontrolled duplicates, and delivery failures are observable and
recoverable.

## Phase 25 — Analytics and reporting

**Objective** Measure outcomes and performance without compromising data access.

**Build** Progress aggregates; subject and topic performance; completion and timing metrics;
institution reports; product and engagement metrics; export controls; metric definitions and
validation; privacy-aware retention and access; reconciliation between aggregates and source records.

**Exit gate** Reports share one definition per metric, sensitive data is access-controlled, and any
discrepancy traces to source records.

## Phase 26 — Search, discovery and recommendations

**Objective** Help learners find the right material.

**Build** Search across exams, subjects, topics and approved publications; filtering and sorting;
pagination and query limits; relevance tuning; access-aware filtering; recommended practice from
performance and goals; indexing and re-indexing; search analytics.

**Exit gate** Search never reveals unauthorised content, and recommendations are explainable enough to
debug and evaluate.

## Phase 27 — AI learning capabilities

**Objective** Add AI only once content, permissions and assessment records are trustworthy.

**Build** Grounded explanations and tutoring; retrieval from approved material; source attribution
where supported; input and output safety checks; prompt-injection-resistant retrieval; privacy and data
minimisation; usage limits and cost monitoring; evaluation datasets for factuality and curriculum
alignment; human escalation and correction; clear disclosure of generated content.

**Non-negotiable boundaries**
- AI never silently changes an official answer or a score.
- AI output is never presented as verified syllabus content without review.
- Retrieved documents and learner content are untrusted input.
- Learner data does not go to an external AI service without an approved handling policy.

**Exit gate** Evaluation shows acceptable accuracy and safety for the intended use, and the system has
defined fallback behaviour when confidence or source coverage is insufficient.

## Phase 28 — Security, privacy and resilience

**Objective** Validate the platform as one security and reliability system.

**Build** Threat modelling and risk assessment; authentication and authorisation testing; tenant
isolation and IDOR testing; rate limiting and abuse controls; secure file handling; dependency and
secret scanning; privacy, retention and deletion workflows; recovery and restore drills; payment and
reward reconciliation drills; incident response; security monitoring and privileged-action audit.

**Exit gate** Critical vulnerabilities are closed, residual risks have named owners, recovery targets
are tested, and incident procedures are operational.

## Phase 29 — Performance, QA and release qualification

**Objective** Prove the release meets agreed quality and capacity targets.

**Build** Unit, integration, contract and end-to-end tests; regression suites; browser and device
matrix; accessibility testing; API load and stress testing; concurrent exam and autosave simulations;
query and connection-pool tuning; cache and worker testing; failure injection and recovery; release
candidate verification; known-defect and risk sign-off.

**Exit gate** Service objectives hold under expected and peak load, no unapproved critical defect
remains, and rollback and monitoring are ready.

## Phase 30 — Pilot, production launch and operations

**Objective** Release in controlled stages and run the service reliably.

**Build** Internal alpha; limited student and teacher pilot; feedback and defect triage; production
deployment and smoke tests; controlled rollout and capacity monitoring; support and escalation;
operational dashboards and alerts; backup monitoring and drills; release notes and user communications;
post-launch review.

**Exit gate** Product, engineering, security and operations owners approve the release on evidence —
not because every screen has been built.

---

# Cross-phase acceptance gates

These apply everywhere. A later phase must not bypass a failed gate in an earlier dependency.

| Gate | Required evidence |
| --- | --- |
| Product | Approved requirements and measurable acceptance criteria |
| Architecture | Reviewed design decisions and documented boundaries |
| Data | Validated migrations, constraints and a recovery strategy |
| API | Contract tests and compatibility checks |
| Security | Authorisation tests, secret controls and risk review |
| Learning | Reproducible scoring and durable answer persistence |
| Commerce | Verified payment events and reconciled entitlements |
| Clients | End-to-end journeys on supported browsers and devices |
| Operations | Monitoring, alerts, runbooks and tested recovery |
| Release | QA approval, known-risk review and deployment sign-off |

# Recommended execution order

1. **Phases 1–6** — requirements, architecture, repository, infrastructure, database, contracts.
2. **Phases 7–10** — identity, authorisation, design system, onboarding.
3. **Phases 11–16** — catalogue, question bank, CBT engine, scoring, learner experience.
4. **Phases 17–20** — offline web/mobile behaviour and institutional workflows.
5. **Phases 21–27** — publications, commerce, rewards, notifications, analytics, discovery, AI, in the
   order the approved priorities set.
6. **Phases 28–30** — security, reliability and performance qualification, then a controlled pilot.

Some work parallelises: mobile navigation, design-system implementation and content preparation can
proceed while endpoints are being written, provided their contracts and dependencies are stable.

# Intended deliverables

- A documented, tested Go backend with modular domain boundaries.
- A Next.js web application and installable PWA.
- React Native apps for iOS and Android.
- A shared, versioned API contract.
- A versioned education catalogue and content workflow.
- A reliable CBT engine with durable answer saving, authoritative deadlines and reproducible scoring.
- Student, teacher, institution, publisher and platform-administration workflows.
- Digital publications, verified payments and auditable entitlements.
- Separate learning-points and monetary-commission ledgers.
- Notifications, analytics, search and governed AI learning tools.
- Automated testing, deployment, observability, backup and recovery procedures.
- Documented operational ownership and a controlled production release process.
