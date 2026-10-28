# Edumart — API contract (v1)

Base path `/api/v1`. JSON in, JSON out. Authentication is an HttpOnly, SameSite=Lax session cookie
(`edumart_session`) issued by `/auth/login` and `/auth/register`. Every state-changing request is
same-origin checked; browser clients send `Origin`, non-browser clients may omit it.

## Conventions

* **Errors**

  ```json
  { "error": { "code": "VALIDATION_ERROR", "message": "…", "request_id": "…", "details": [] } }
  ```

  | Code | HTTP | Meaning |
  | --- | --- | --- |
  | `VALIDATION_ERROR` | 400 | Invalid input; `details` may be `[{field,message}]` |
  | `UNAUTHENTICATED` | 401 | No or expired session |
  | `FORBIDDEN` | 403 | Authenticated but not permitted |
  | `NOT_FOUND` | 404 | Missing, or intentionally hidden from this identity |
  | `CONFLICT` | 409 | State, version or idempotency conflict |
  | `ASSESSMENT_NOT_OPEN` | 409 | Outside the availability window |
  | `SESSION_EXPIRED` / `SESSION_CLOSED` | 409 | Deadline passed, or attempt finalized/cancelled |
  | `RATE_LIMITED` | 429 | Back off |
  | `INTERNAL_ERROR` | 500 | Generic message plus request id — never a stack trace |

* **Idempotency** — `Idempotency-Key` is honoured on practice/assessment start, submission, order
  creation and payment initiation. A replay returns the stored response with `idempotent-replay: true`
  where applicable.
* **Concurrency** — answer writes carry `expected_version`; the canonical `version` is returned.
* **Time** — ISO-8601 UTC everywhere; every session response includes `server_time`.
* **Money** — `{ "amount_minor": 200000, "currency": "NGN" }`; no floats.

## Identity

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/auth/register` | rate-limited; returns user + `next` route, sets cookie |
| POST | `/auth/login` | rate-limited per IP and per account; uniform failure message |
| POST | `/auth/logout` | revokes the current session |
| POST | `/auth/password/forgot` | always 200 (no account enumeration); queues email |
| POST | `/auth/password/reset` | single-use token; revokes all sessions |
| GET | `/me` | current user, roles, referral code |
| PATCH | `/me` | display name, pathway, target date, timezone |

## Content & taxonomy

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/exam-pathways` | published pathways with subject counts |
| GET | `/subjects?pathwayId=` | subjects with approved-question counts |
| GET | `/subjects/{subjectId}/topics` | topics with counts |

## Practice

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/practice-sessions` | body `{subjectId, topicIds[], count, difficulty}`; returns session with questions only |
| POST | `/practice-sessions/{sessionId}/responses` | legacy alias for the answer write |

## Assessments

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/assessment-definitions` | published public mocks |
| POST | `/assessment-definitions` | teacher/admin authoring; builds a published assessment from approved items |
| POST | `/assessment-definitions/{definitionId}/sessions` | start attempt; enforces window, membership, attempt limit |
| GET | `/assessment-sessions/{sessionId}` | session state and permitted question content |
| PUT | `/assessment-sessions/{sessionId}/responses/{questionVersionId}` | idempotent, versioned answer write |
| POST | `/assessment-sessions/{sessionId}/submit` | idempotent finalization; returns scored session |
| GET | `/assessment-sessions/{sessionId}/result` | respects result-release policy |

### Start an attempt

```http
POST /api/v1/assessment-definitions/{definitionId}/sessions
Idempotency-Key: 6dd8f0d1-…
Content-Type: application/json

{ "client_context": { "app_version": "web-1.0.0", "timezone": "Africa/Lagos" } }
```

```json
{
  "session": {
    "session_id": "ucm…",
    "status": "in_progress",
    "started_at": "2026-10-26T10:00:00.000Z",
    "deadline_at": "2026-10-26T10:30:00.000Z",
    "server_time": "2026-10-26T10:00:00.004Z",
    "question_count": 40,
    "questions": [
      { "qvId": "…", "stem": "…", "options": [{ "id": "option-a", "label": "…" }], "position": 1, "selectedOptionId": null, "answerVersion": 0 }
    ]
  }
}
```

The response never contains `answerOptionId` or explanations while the attempt is in progress.

### Save an answer

```http
PUT /api/v1/assessment-sessions/{sessionId}/responses/{questionVersionId}
Content-Type: application/json

{ "answer": { "selected_option_id": "option-b" }, "expected_version": 3, "client_sequence": 12 }
```

```json
{ "saved": true, "server_saved_at": "2026-10-26T10:08:12.114Z", "version": 4, "session_status": "in_progress", "remaining_seconds": 1308 }
```

* `409 CONFLICT` + `details.current_version` when a stale client tries to overwrite a newer value.
* A retry of an already-committed mutation (same `client_sequence`, or same `expected_version` and value)
  returns the original acknowledgement instead of a conflict.
* `409 SESSION_EXPIRED` finalizes the attempt and tells the client to fetch the result.

## Classroom

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/classes` | classes the caller belongs to |
| POST | `/classes` | teacher/admin; tenant membership checked |
| GET | `/classes/{classId}` | class, members; teacher sees rollup report |
| POST | `/classes/{classId}/invitations` | expiring, limited-use, hashed token |
| POST | `/classes/join` | join with class code |
| POST | `/invitations/{token}/accept` | validates expiry, usage and email binding |

## Commerce

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/products?q=&subject=&exam=&type=` | published catalogue |
| GET | `/products/{slug}` | detail plus `owned` flag |
| POST | `/orders` | body `{items: [slug]}`; totals computed server-side |
| GET | `/orders`, `/orders/{orderId}` | own orders only |
| POST | `/orders/{orderId}/payment-attempts` | creates provider payment; returns `checkout_url` |
| POST | `/webhooks/{provider}` | HMAC-verified, deduplicated, transactional |
| GET | `/me/entitlements` | active access |
| GET | `/entitlements/{id}/content` | signed link valid 5 minutes |
| GET | `/entitlements/{id}/content/file?token=&expires=` | validates signature, then serves the protected asset |
| POST | `/sandbox/pay` | **sandbox only**: signs and delivers a provider event; disabled in production |

Webhook payload (sandbox reference implementation):

```json
{ "event_id": "evt_…", "type": "payment.updated", "provider_reference": "sbx_…",
  "amount_minor": 200000, "currency": "NGN", "status": "succeeded" }
```

Header `x-edumart-signature: hex(HMAC_SHA256(secret, raw_body))`. Processing order: verify → dedupe →
match attempt → compare amount/currency → transactional state change → idempotent entitlement grant →
queue reconciliation.

## Rewards

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/me/rewards/summary` | points balance + ledger, commission summary, referral code |
| GET | `/me/rewards/referral` | code, attributions |

## Operations

| Method | Path | Required role |
| --- | --- | --- |
| GET | `/admin/overview` | content_editor, finance, support, platform_admin |
| POST | `/admin/questions/{questionVersionId}/review-decision` | content_editor, platform_admin |
| POST | `/admin/orders/{orderId}/refund` | finance, platform_admin |
| POST | `/jobs/sweep` | operator/scheduler |
| GET/POST | `/support/cases` | any authenticated user |
| POST | `/reports` | any authenticated user (content report) |
