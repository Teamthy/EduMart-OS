# Edumart — Architecture

Status: implemented baseline. This documents what exists in the repository, the decisions behind it,
and the explicit path from the local SQLite system of record to managed PostgreSQL.

## 1. Shape

```
Browser / installed PWA
   │  HTML (server components) + JSON over /api/v1
   ▼
Next.js App Router  ─ single deployable ─┐
   ├── Presentation (src/app/**)          │
   ├── HTTP contract (src/app/api/v1/**)  │  modular monolith:
   ├── Domain services (src/lib/server)   │  boundaries are modules + services,
   └── Pure rules (src/lib/domain)        │  not network hops
   ▼
Data access layer (src/lib/server/db.ts)
   ▼
SQLite (dev/CI) ── swap adapter ──► managed PostgreSQL (production)
```

The PRD's guidance is followed literally: **no microservices, no Kafka, no Kubernetes, no distributed
cache**. One process, one database, explicit module ownership, and every business-critical write inside
a database transaction.

## 2. Module boundaries

| Module | Files | Owns |
| --- | --- | --- |
| Identity & access | `auth.ts`, `password.ts`, `guards.ts` | users, sessions, role assignments, session revocation |
| Organizations & classes | `classes.ts` | organizations, memberships, classes, invitations |
| Content | `content.ts`, `content/seed-content.ts` | taxonomy, question bank, publication metadata |
| Assessment | `assessment.ts`, `domain/exam.ts`, `domain/scoring.ts` | definitions, items, sessions, responses, scoring, expiry |
| Learning analytics | `insights.ts` | attempts history, topic performance, class rollups (derived, rebuildable) |
| Commerce | `commerce.ts` | products, orders, payment attempts, provider events, entitlements |
| Rewards | `rewards.ts` | points ledger, commission ledger, referrals, reversals |
| Operations | `audit.ts`, `http.ts` | audit trail, learning events, job queue, error envelope, rate limiting |

Rules upheld:

* A module reads another module's tables only through a service function, never by writing into them.
* Cross-module effects that must be atomic (order paid → entitlement granted) share one transaction.
* Side effects that must not block the request (reconciliation, email) are queued in `job_queue_items`.

## 3. Consistency and CAP decisions per workflow

| Workflow | Decision | Implementation |
| --- | --- | --- |
| Answer writes | strong consistency, durable acknowledgement | synchronous upsert + version check, response returned only after commit |
| Final submission | authoritative server transition | single `finalizeSession` inside `BEGIN IMMEDIATE`, status guard prevents double finalization |
| Deadlines | server-authoritative time | `deadline_at` computed at start; writes validated against it (5s grace) |
| Scoring | deterministic, versioned | snapshot + policy version stored per attempt; pure `scoreAttempt` |
| Payments | verified provider state | HMAC over raw body, dedupe on (provider, event_id), amount/currency compared to stored attempt |
| Entitlements | idempotent grant | unique (user, product), `INSERT` guarded by lookup, safe on webhook retry |
| Points & commissions | strong consistency, append-only | unique `idempotency_key` per entry, compensating entries for corrections |
| Catalog reads | caching allowed | static content, safe to cache; assessment data is `no-store` |
| Analytics | eventual consistency | derived tables (`topic_performance`), rebuildable from sessions/responses |

## 4. Assessment engine

```
POST /practice-sessions            POST /assessment-definitions/{id}/sessions
        │                                          │
        └──────────► start (snapshot + policy) ◄───┘
                                │
                       status = in_progress
                     started_at, deadline_at (server)
                                │
             PUT .../responses/{questionVersionId}
             (ownership, state, deadline, membership, option, version)
                                │
                 ┌──────────────┴───────────────┐
        POST .../submit                 expiry sweep / deadline
                 └──────────► finalizeSession ◄─────────┘
                     (score, per-question marks, topic performance,
                      points entry, audit) — exactly once
```

Invariants enforced in code, not in the UI:

* Snapshot immutability: `snapshot_json` holds stems, options, answer keys, topic mapping, points and
  policy as of start. Later content edits cannot rewrite a completed attempt.
* Retry safety: the same mutation (same `client_sequence`, or same `expected_version` + same value)
  replays the previous acknowledgement instead of returning a conflict.
* Stale-write policy: a different value with an outdated `expected_version` returns `409 CONFLICT` with
  the canonical version, rather than silently overwriting a newer answer.
* Expiry uses the same finalization function as submission, so scores cannot diverge.
* Points are awarded with `points:attempt:{session_id}` as the idempotency key.

## 5. Client-side assessment behaviour

* Countdown = `deadline_at − (now − server_offset)`, where the offset is measured from `server_time`
  returned with the session. Device clocks are irrelevant.
* Save state machine: `idle → saving → saved | pending | failed`.
  * `pending` = queued in a bounded `localStorage` queue and retried with backoff and on `online`.
  * `failed` = conflict or terminal state; the reason is always visible.
* Submission posts once; a duplicate is safe server-side. Auto-submit fires when the timer reaches zero.
* Service worker never caches `/api/**`, so assessment state can never be served stale.

## 6. Data access

`src/lib/server/db.ts` exposes `q.all/get/run`, `transaction`, `id`, `nowIso`.

* Timestamps are ISO-8601 UTC strings, ordered lexicographically and rendered in the user's timezone.
* Money is `INTEGER` minor units plus an ISO currency code; there are no floating-point money paths.
* `PRAGMA foreign_keys = ON`, `journal_mode = WAL`, `busy_timeout = 5000`.
* Schema is applied idempotently from `db/schema.sql`; demo content is guarded by a `schema_meta`
  seed version so partial seeding is detected and repaired rather than trusted.

### Path to PostgreSQL

The schema is written to be portable: text ids, ISO timestamps, integer money, standard constraints.
To move the system of record:

1. Translate `db/schema.sql` DDL: `TEXT` → `uuid`/`text`, `INTEGER` booleans → `boolean`,
   ISO strings → `timestamptz`, add `CHECK` constraints and partial indexes.
2. Replace the adapter (`db.ts`) with a `pg` pool implementing the same `q.*` surface and a real
   transaction/`SERIALIZABLE`-where-needed strategy.
3. Add expand/contract migrations (`migrations/` + a runner in CI), never destructive in the same
   release that removes the old code path.
4. Keep the seeded demo dataset as fixtures for integration tests only.

## 7. Deployment topology (target)

```
CDN / edge ──► App runtime (2+ instances, health checks) ──► PostgreSQL primary (+ read replica, PITR)
                     │                                   └──► Object storage (private buckets)
                     └──► Job worker (same image, worker entrypoint) ──► queue table / managed queue
                     └──► Logs, metrics, traces, error tracking, alerts
```

Staging mirrors production with a payment sandbox, test email provider and synthetic data. Production
runs with restricted admin access, audited emergency access, backups, and feature flags for risky
capabilities.

## 8. Deliberate deferrals

* MFA for privileged staff — schema and flow designed, not implemented.
* Real payment provider, email/SMS delivery, object-storage streaming.
* Distributed rate limiting (in-process sliding window today, Redis-backed later with the same
  interface in `http.ts`).
* AI tutor: behind a feature flag, grounded retrieval only; not built.
* Native apps: PWA first, per PRD non-goals.
