# Data model

Dialects: `apps/api/internal/store/migrations/sqlite/0001_init.sql` (development, CI, demos) and the
mirrored `postgres/0001_init.sql` (production). Both are applied automatically at boot.

Conventions:

- Opaque text ids (UUIDv4 for runtime rows, deterministic hashes for seeded content so re-seeding is
  idempotent).
- Timestamps are ISO-8601 UTC text — sortable, timezone-explicit, and identical across dialects.
- Money is `INTEGER` **minor units** with an explicit `currency`; never a float.
- Ledgers are append-only. Balances are `SUM(...)` over the ledger, never a mutable column.

## Identity and tenancy

| Table | Purpose |
| --- | --- |
| `users` | Account, plan (pathway, target date, daily minutes), onboarding progress |
| `sessions` | Opaque token **hashes**, client (`web`/`mobile`), expiry, revocation |
| `password_reset_tokens` | Single-use, 30-minute expiry, hash-at-rest |
| `role_assignments` | `(user, role, scope)`; roles are additive, `platform_admin` is global |
| `signup_drafts` | Pre-account wizard progress; token hash, draft JSON, current step |
| `organizations`, `organization_memberships` | School/tenant boundary |
| `classes`, `class_memberships` | Teaching groups, join codes, per-class roles |
| `invitations` | Tokenised invites with use limits |

## Content

| Table | Purpose |
| --- | --- |
| `exam_pathways` | JAMB, WAEC, NECO, Post-UTME |
| `subjects`, `topics` | Taxonomy; topics carry `syllabus_version` |
| `questions` | The stable question identity (subject, topic, difficulty, status) |
| `question_versions` | **Immutable** content per version: stem, options JSON, answer key, explanation, provenance, `review_state` |
| `content_reports` | Learner-reported problems with a question version |

Only `question_versions` with `review_state = 'approved'` and a `published_at` can be drawn into an
attempt. Approval is one-way through the API and is audited.

## Assessment

| Table | Purpose |
| --- | --- |
| `assessment_definitions` | Timed assessment: owner, class, duration, window, attempt limit, **scoring policy**, release mode |
| `assessment_items` | Ordered question versions with points |
| `assessment_sessions` | One attempt: **immutable snapshot**, server `started_at`/`deadline_at`, status, score, idempotency key |
| `assessment_responses` | Current answer per question version, `answer_version`, `client_sequence`, awarded points |
| `idempotency_records` | Replay protection for mutating endpoints |
| `topic_performance` | Derived mastery counters (rebuildable) |

Statuses: `in_progress → submitted | expired | cancelled`. Terminal states never reopen.

## Commerce

| Table | Purpose |
| --- | --- |
| `publishers`, `products` | Catalogue with `price_minor` + `currency` |
| `orders`, `order_items` | Totals computed server-side; item titles and prices snapshotted |
| `payment_attempts` | Provider reference per order |
| `provider_events` | Raw webhook events, unique per `(provider, event_id)`, with a processing status |
| `entitlements` | Granted only by a verified webhook; the source of truth for `/library` |

## Ledgers (never mixed)

| Table | Asset | Notes |
| --- | --- | --- |
| `points_ledger_entries` | Engagement points | `idempotency_key` unique; negative rows for redemptions |
| `commission_ledger_entries` | Money owed (minor units) | Separate table, separate status lifecycle |

## Growth and operations

| Table | Purpose |
| --- | --- |
| `referral_codes`, `referral_attributions` | One code per learner; attribution pending → qualified on first paid order |
| `study_plans` | Daily minutes, focus topics |
| `learning_events` | Append-only behavioural log |
| `support_cases`, `audit_logs` | Support queue and an audit trail that names the actor, action and reason |
| `job_queue_items` | Durable queue for background work |

## Indexing notes

Session lookups are covered by `idx_sessions_user`; the expiry sweep uses
`idx_sessions_status(status, deadline_at)`; attempt history uses
`idx_sessions_user(user_id, started_at)`; content selection uses `idx_qv_review` and
`idx_questions_subject`. `idx_sessions_attempt(user_id, definition_id, attempt_no)` enforces one row
per attempt number.

## Migrations

The schema is embedded in the binary (`//go:embed migrations/*/*.sql`) and applied on start. It is
written with `IF NOT EXISTS` so it is safe to run repeatedly. Before production, replace this with a
versioned migration tool and move the PostgreSQL types (`timestamptz`, `numeric` for money) into
place — see PRODUCTION_READINESS.md.
