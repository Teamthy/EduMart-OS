# API reference (`/v1`)

All requests and responses are JSON. Authentication is a session cookie (`edumart_session`) on the
web or `Authorization: Bearer <token>` on native. Every response includes `X-Request-Id`.

## Error envelope

Every non-2xx response has the same shape:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Please fix the highlighted fields.",
    "request_id": "9f2c…",
    "details": [{ "field": "email", "message": "Enter a valid email address." }]
  }
}
```

| Code | Status | Meaning |
| --- | --- | --- |
| `VALIDATION_ERROR` | 400 | Field-level problems in `details` |
| `UNAUTHENTICATED` | 401 | No or expired session |
| `FORBIDDEN` | 403 | Wrong role, or not a member of the tenant |
| `NOT_FOUND` | 404 | Missing, or not visible to this caller |
| `CONFLICT` | 409 | Stale version, duplicate, or attempt limit reached |
| `ASSESSMENT_NOT_OPEN` | 409 | Outside the availability window |
| `SESSION_EXPIRED` | 409 | Deadline passed; the attempt has been finalised |
| `SESSION_CLOSED` | 409 | Attempt already submitted/expired/cancelled |
| `RATE_LIMITED` | 429 | `Retry-After` is set |
| `PAYMENT_PENDING` | 409 | Payment not yet confirmed by webhook |
| `INTERNAL_ERROR` | 500 | Quote `request_id` to support |

## Identity

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/auth/register` | Creates account + default role + referral code; sets session cookie |
| `POST` | `/auth/login` | `{email, password, client}` → `{token, expires_at, user, next}` |
| `POST` | `/auth/logout` | Revokes the session server-side |
| `POST` | `/auth/password/forgot` | Always 200; never reveals whether the email exists |
| `POST` | `/auth/password/reset` | Consumes a single-use token, revokes all sessions |
| `GET` | `/me` | Current principal with roles and subject preferences |
| `PATCH` | `/me` | Profile, plan and onboarding progress |

### Signup wizard

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/signup-drafts` | Creates a draft, returns `{token, draft, step, stepIndex}` |
| `GET` | `/signup-drafts/current` | Resumes the draft from the cookie or bearer token |
| `PATCH` | `/signup-drafts/current` | Merges fields, advances `currentStep` |

Draft steps: `pathway → subjects → schedule → account → confirm`.

## Taxonomy (public)

| Method | Path |
| --- | --- |
| `GET` | `/exam-pathways` |
| `GET` | `/subjects?pathwayKey=jamb` |
| `GET` | `/subjects/{subjectId}/topics` |
| `GET` | `/referrals/validate/{code}` |

## Learner

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/dashboard` | Home screen payload, one round trip |
| `GET` | `/learning-summary` | Topic mastery, subject accuracy, history |
| `GET`/`PUT` | `/study-plan` | Target date, daily minutes, focus topics |
| `GET` | `/rewards` | Points balance (derived), referral code, ledger entries |
| `POST` | `/rewards/redeem` | Spends points via a negative ledger row |

## Assessment engine

| Method | Path | Notes |
| --- | --- | --- |
| `POST` | `/practice-sessions` | `{subjectId, topicIds[], count, difficulty}` |
| `POST` | `/assessment-definitions/{id}/sessions` | Starts a timed attempt; enforces window, class membership, attempt limit |
| `GET` | `/assessment-sessions/{id}` | Owner only (or a teacher of the owning class) |
| `GET` | `/assessment-sessions?limit=20` | History |
| `PUT` | `/assessment-sessions/{id}/responses/{qvId}` | Answer write (below) |
| `POST` | `/assessment-sessions/{id}/submit` | Idempotent finalisation |
| `POST` | `/assessment-sessions/{id}/cancel` | Only while `in_progress` |
| `PUT` | `/assessment-sessions/{id}/corrections/{qvId}` | Post-submission correction (practice only) |
| `GET` | `/me/sessions` | Active devices for the caller |
| `DELETE` | `/me/sessions/{id}` | Revoke one device (scoped to the caller) |

### Answer write

```http
PUT /v1/assessment-sessions/{sessionId}/responses/{qvId}
Content-Type: application/json

{ "selectedOptionId": "opt_2", "expectedVersion": 3, "clientSequence": 7 }
```

```json
{ "saved": true, "server_saved_at": "2026-10-28T18:43:40Z", "version": 4,
  "session_status": "in_progress", "remaining_seconds": 14398 }
```

- **Replay** (same value, same `clientSequence`, or `expectedVersion = current - 1`): 200 with the
  existing version. Response loss never corrupts state.
- **Stale value**: `409 CONFLICT` with `details: { current_version }`.
- **Deadline passed**: `409 SESSION_EXPIRED`; the attempt is finalised in the same request.
- **Closed attempt**: `409 SESSION_CLOSED`.

### Session projection

### Corrections

```
PUT /v1/assessment-sessions/{sessionId}/corrections/{qvId}
{ "selectedOptionId": "opt_2", "expectedVersion": 1, "clientSequence": 2 }
→ 200 { "saved": true, "version": 2, "session_status": "submitted", "server_saved_at": "…" }
```

Only `practice` attempts accept corrections, and only once they are closed — a graded assessment is
evidence, so the released answer key can never be used to rewrite its score (`409 CONFLICT`). The
response carries the new answer version and nothing else: no correctness, no score, so the endpoint
cannot be used as an answer oracle. Everything the learner scored is then recomputed from the stored
responses with the same `domain.ScoreAttempt` used at submission.

`GET /assessment-sessions/{id}` returns questions **without** `answerOptionId` or explanations while
the attempt is in progress. After finalisation, `result_released` follows the assessment's policy
(`immediate`, `scheduled`, `on_close`); only then does `review[]` include keys and explanations.

## Classes

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/classes` | Classes the caller belongs to, with their role |
| `POST` | `/classes` | Teacher/org admin; generates the join code |
| `POST` | `/classes/join` | Case-insensitive code |
| `GET` | `/classes/{id}/roster` | Teacher-only, includes attempts and averages |
| `GET` | `/classes/{id}/assignments` | Published assessments with submission counts |

## Commerce

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/products` | Catalogue with `owned` flag |
| `POST` | `/orders` | Server-computed totals from catalogue prices |
| `GET` | `/orders`, `/orders/{id}` | Owner only |
| `GET` | `/library` | Entitlements, not a client-side purchase flag |
| `POST` | `/orders/{id}/simulate-payment` | Development only; signs a real webhook |
| `POST` | `/webhooks/payments/{provider}` | `x-edumart-signature` = HMAC-SHA256 hex of the raw body |

Webhook behaviour: verify signature → record event (unique per `provider`+`event_id`) → match order by
reference → verify amount and currency → ignore non-`succeeded` statuses → mark paid, grant
entitlements, audit. Replays return `{received: true, duplicate: true}`.

## Staff

| Method | Path | Roles |
| --- | --- | --- |
| `GET` | `/admin/review-queue` | content_editor, platform_admin |
| `POST` | `/admin/question-versions/{id}/review` | content_editor, platform_admin |
| `GET` | `/admin/metrics` | platform_admin, finance |
| `GET` | `/admin/cases` | support, platform_admin |
| `POST` | `/admin/cases/{id}/resolve` | support, platform_admin |
| `POST` | `/support-cases` | any signed-in user |

## Outside /v1

| Endpoint | Contract |
| --- | --- |
| `GET /healthz` | `200 {status:"ok",time,driver}`. Liveness only: never checks a dependency, so a database blip cannot restart a healthy process. |
| `GET /readyz` | `200` when the database answers and every migration is applied; `503` otherwise, with `checks[]` naming the failing dependency. Used by load balancers and deploy gates. |

`/readyz` is deliberately stricter than `/healthz`. `healthz` green while `readyz` is red is the
intended signal: the process is alive, it just cannot serve traffic yet.

## Sessions

```
GET /v1/me/sessions
→ 200 { "sessions": [
    { "id": "9f2c…", "client": "web", "userAgent": "Mozilla/5.0…",
      "createdAt": "…", "expiresAt": "…", "current": true } ] }

DELETE /v1/me/sessions/{id}
→ 200 { "revoked": true, "wasCurrent": false }
→ 404 when the id is unknown or belongs to someone else
```

`id` is the stored session key — the sha256 of the token — so it can revoke a session but cannot be
replayed as a credential. Revocation is scoped by `user_id` in the `WHERE` clause, not by a check in
the handler, so a session id copied from another account cannot sign that learner out. Expired and
revoked sessions never appear in the list.
