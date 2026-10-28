# Edumart OS

A working, production-shaped implementation of the Edumart platform described in [`edumart-prd`](./edumart-prd):
exam preparation and timed CBT, classroom assessments, a digital publications library with verified
payments, learning analytics, rewards with separate ledgers, and an operations console.

The product is built as a **modular monolith**: one deployable, with explicit domain modules, a versioned
REST API (`/v1`), and a server-authoritative assessment engine. Progressive Web App shell with offline
handling, mobile-first design system, and 23 automated tests covering the invariants the PRD calls out.

---

## Quick start

```bash
npm install
cp .env.example .env       # optional; sensible defaults are built in
npm run dev                # http://localhost:3000
```

The database schema and demo content load automatically on first request. To start clean:

```bash
npm run db:reset           # deletes the local SQLite file; it is recreated and reseeded automatically
```

### Demo accounts

| Role | Email | Password |
| --- | --- | --- |
| Student (with history) | `student@edumart.ng` | `Student123!` |
| Teacher / institution admin | `teacher@edumart.ng` | `Teacher123!` |
| Platform admin | `admin@edumart.ng` | `Admin123!` |
| Content editor | `editor@edumart.ng` | `Editor123!` |

The login screen has a "Use a demo account" shortcut that fills these in.

### Useful commands

```bash
npm run dev         # development server on 0.0.0.0:3000
npm run build       # production build
npm start           # serve the production build
npm run typecheck   # TypeScript, no emit
npm test            # vitest: domain + assessment-engine integration tests
npm run sweep       # POST /api/v1/jobs/sweep — expire overdue attempts and drain queued jobs
```

---

## What is implemented

| Area | Status | Notes |
| --- | --- | --- |
| Identity & sessions | ✅ | scrypt hashes, HttpOnly cookie sessions, revocation, single-use reset tokens, login/registration rate limits, account-enumeration-safe responses |
| Roles & tenant isolation | ✅ | global roles + organization/class scopes; server-side checks on every protected handler; cross-tenant attempts return 404 |
| Exam taxonomy & content | ✅ | 4 pathways, 17 subjects, 68 topics, 104 reviewed questions with source/licence metadata and explanations |
| Question lifecycle | ✅ | draft → in review → approved/published, immutability of published versions, publish blocked without provenance, audited decisions |
| Practice | ✅ | subject/topic/difficulty selection, immutable snapshot per attempt, autosave, scoring, explanations after submission, history |
| Timed CBT | ✅ | server-owned deadline with grace window, versioned retry-safe answer writes, offline queue with visible sync state, duplicate-submit-safe finalization, auto-expiry |
| Results & analytics | ✅ | score breakdown, per-topic accuracy, trend, weak-topic recommendations, rebuildable derived tables |
| Classroom | ✅ | class creation, join codes, expiring/limited-use hashed invitations, scheduled assessments, class rollup reports |
| Publications & commerce | ✅ | catalogue, private file keys, server-calculated totals, signed short-lived access links, refunds that revoke entitlements |
| Payments | ✅ (sandbox) | HMAC-verified webhooks, event dedupe, amount/currency validation, idempotent entitlement grant, reconciliation counter |
| Rewards | ✅ (payouts disabled) | separate points and commission ledgers, versioned rules, referral attribution with self-referral and cap controls, compensating reversals |
| Operations console | ✅ | moderation queue, content reports, order/refund review, support queue, provider events, audit trail |
| PWA | ✅ | manifest, app icons, service worker (network-first navigations, no caching of assessment data), offline page |
| Observability | ◻︎ partial | structured audit + learning events and a job table exist; external metrics/tracing/paging are documented, not wired |

Legend: ✅ implemented and exercised in this repository · ◻︎ foundation present, production integration pending.

---

## Architecture at a glance

```
Next.js App Router (PWA)             — presentation, interaction state, offline queue
        │  fetch /v1 (JSON, cookie session)
        ▼
Versioned REST API route handlers    — validation, authorization, error envelope, rate limits
        ▼
Domain services (src/lib/server)     — assessment engine, commerce, rewards, classes, insights
        ▼
SQLite (node:sqlite) → PostgreSQL    — transactions, constraints, append-only ledgers
```

* `src/lib/domain/` — pure, testable rules: scoring, timed-exam state machine, timer maths.
* `src/lib/server/` — use cases and data access. Modules never reach across boundaries implicitly;
  cross-module effects go through service functions inside one transaction.
* `src/app/api/v1/` — the documented HTTP contract (see [`docs/API.md`](./docs/API.md)).
* `src/app/` — server-rendered screens with client islands for interactive flows.

Read [`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) for component boundaries, consistency
decisions and the path to managed PostgreSQL.

---

## The invariants this build protects

1. **Server owns assessment time.** The deadline is computed from `started_at + duration` and stored;
   the client countdown is derived from a measured clock offset. Changing a device clock changes nothing.
2. **An answer is not "saved" until the server says so.** Writes are versioned, acknowledged, and
   retried; a retried write replays the acknowledgement instead of duplicating or conflicting.
3. **Submission finalizes once.** Explicit submission and server expiry share one finalization
   function, guarded by a status check inside a transaction and a unique points idempotency key.
4. **Answer keys never leak early.** Session projections omit `answerOptionId` and explanations until
   the result-release policy allows them.
5. **A browser redirect is not payment.** Only an HMAC-verified, deduplicated provider event with a
   matching amount and currency can mark an order paid.
6. **Money and points are different assets.** Separate append-only ledgers, integer minor units,
   compensating reversals, idempotency keys on every entry.
7. **Tenant boundaries are enforced in queries.** Class and organization reads are scoped by
   membership; unauthorized access is a 404, not a filtered list.

---

## Testing

```bash
npm test
```

* `tests/domain.test.ts` — scoring determinism, negative marking, policy parsing, deadline grace
  window, terminal-state immutability, result release, seeded shuffle.
* `tests/engine.test.ts` — real database integration: snapshot immutability and answer-key
  withholding, acknowledged writes, retry replay without duplication, stale-write rejection,
  cross-user denial, single finalization with single points award, expiry sweeping, attempt limits.

---

## Documentation

| Document | Contents |
| --- | --- |
| [`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) | component boundaries, data flows, CAP decisions, deployment topology, roadmap to PostgreSQL |
| [`docs/DATA_MODEL.md`](./docs/DATA_MODEL.md) | entity reference, state machines, constraints, ledger rules |
| [`docs/API.md`](./docs/API.md) | endpoint catalogue, request/response contracts, error codes, idempotency |
| [`docs/BACKLOG.md`](./docs/BACKLOG.md) | epics mapped to PRD IDs with built / next / deferred status |
| [`docs/PRODUCTION_READINESS.md`](./docs/PRODUCTION_READINESS.md) | security, privacy, observability, backup, DR and launch gates |

---

## Scope boundaries in this build

Deliberately **not** production-complete, and clearly marked as such in the code:

* Payment provider is a signed **sandbox** stand-in; wire a real provider behind the same webhook contract.
* Payouts, cash-out and stored value are disabled pending legal, accounting and fraud sign-off.
* Notifications run through a durable job table but no email/SMS provider is connected.
* Object storage returns a reader manifest instead of streaming a protected file.
* SQLite is a local/CI system of record; PostgreSQL is the production target (see architecture doc).
* Multi-factor authentication and full analytics pipeline are designed and specified, not implemented.

Content in the demo bank is original practice material written for this repository, not reproduced
past-examination papers.
