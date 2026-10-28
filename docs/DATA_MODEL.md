# Edumart — Data model

Physical schema: [`db/schema.sql`](../db/schema.sql). All ids are opaque text (UUID v4 in practice),
all timestamps ISO-8601 UTC, all money integer minor units with an explicit currency.

## Identity & access

| Table | Purpose | Key constraints |
| --- | --- | --- |
| `users` | account, credential pointer, pathway preference | unique `email_normalized`; status in (active, suspended, deactivated) |
| `sessions` | server-side session records | primary key is SHA-256 of the cookie token; expiry + revocation checked per request |
| `password_reset_tokens` | single-use recovery | token stored hashed; `used_at` + `expires_at` enforced |
| `role_assignments` | role grants with scope | unique (user, role, scope) |
| `organizations`, `organization_memberships` | tenant | unique (organization, user); role in (org_admin, teacher, student) |
| `classes`, `class_memberships` | classroom | unique `join_code`; unique (class, user) |
| `invitations` | controlled enrolment | `token_hash` unique; `expires_at`, `max_uses`, `use_count` enforced on accept |

A role grant never substitutes for resource-level authorization: every class/order/session read checks
membership or ownership.

## Content

| Table | Purpose | Key constraints |
| --- | --- | --- |
| `exam_pathways`, `subjects`, `topics` | versioned taxonomy | unique keys; `syllabus_version` on topics |
| `questions` | stable question identity | status lifecycle (draft → in_review → approved → published → withdrawn) |
| `question_versions` | immutable content versions | unique (question, version); `review_state`, `answer_option_id`, provenance fields |
| `content_reports` | learner-reported issues | reason enum, status (open/resolved/dismissed) |

Publication requires `source_name`, `license_basis` and `explanation` — the API refuses otherwise.
Published versions are never edited; a correction is a new version, and attempt snapshots keep the old one.

## Assessment

| Table | Purpose | Key constraints |
| --- | --- | --- |
| `assessment_definitions` | reusable template | `duration_seconds > 0`, status, availability window, attempt limit, scoring policy json |
| `assessment_items` | question membership | unique (definition, position) |
| `assessment_sessions` | one attempt | status in (in_progress, submitted, expired, cancelled); unique (user, definition, attempt_no); `idempotency_key` unique; `snapshot_json` immutable |
| `assessment_responses` | answers | unique (session, question_version); `answer_version` for optimistic concurrency; `client_sequence` for retry replay |
| `idempotency_records` | replay receipts | unique `key`, stored response + status |

### Session state machine

```
created → in_progress ──submit──► submitted   (terminal)
                       ├─deadline─► expired    (terminal)
                       └─policy───► cancelled  (terminal, not learner-initiated)
```

No transition out of a terminal state exists in any code path. `submitted_at`, `score_raw`,
`score_max`, `score_percent` are written once, by the single finalization function.

## Commerce

`products` (private `file_key`, price history by row), `orders` (server-calculated totals),
`order_items` (title and unit price snapshot), `payment_attempts` (unique provider reference),
`provider_events` (unique provider+event id, signature validity, processing status),
`entitlements` (unique user+product, status/expiry rechecked on every access).

Order states: `pending_payment → paid → fulfilled`, with `payment_failed`, `cancelled`, `refunded`.
Refunding revokes entitlements and reverses commissions with compensating entries.

## Rewards

* `points_ledger_entries` — signed integer points, `entry_type` (earn/redeem/adjust), source event,
  rule version, unique idempotency key.
* `commission_ledger_entries` — integer minor units, status (pending/eligible/reversed/settled),
  unique idempotency key, reversal rows are negative and additive.
* `referral_codes`, `referral_attributions` — one attribution per referred user, qualifying event required.

Points and money are never merged, converted, or stored in the same column.

## Operations

`audit_logs` (append-only actor/action/resource/reason/request id/metadata), `support_cases`,
`job_queue_items` (durable retryable work), `learning_events` (minimised analytics), `topic_performance`
(derived, rebuildable), `study_plans`.

## Integrity rules applied

1. Foreign keys on everywhere; cascade only for genuinely owned rows.
2. Unique constraints double as idempotency guarantees (attempts, responses, ledgers, provider events).
3. Check constraints on every state column and on non-negative money.
4. Multi-record business operations run in one transaction (`createOrder`, `finalizeSession`,
   `processProviderEvent`, `acceptInvitation`).
5. Derived analytics can be dropped and rebuilt from `assessment_sessions` / `assessment_responses`.

## Indexing

Chosen for the access paths that exist today: sessions by (user, started), (status, deadline) for the
expiry sweep, responses by session, orders by (user, created), ledger entries by (user, created),
audit by (action, occurred). Partitioning is deferred until growth and query evidence justify it.
