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
| `POST` | `/auth/refresh` | `{refreshToken}` → new `{token, refresh_token}`, both new. The presented token is retired by the call; replaying it past a 20s grace window revokes the whole session family with `401 SESSION_REUSED` |
| `POST` | `/auth/logout` | Revokes the session server-side |
| `POST` | `/auth/password/forgot` | Always 200; never reveals whether the email exists |
| `POST` | `/auth/password/reset` | Consumes a single-use token, revokes all sessions |
| `POST` | `/auth/verification/request` | `{channel: "email"\|"phone", phone?}` → `202 {sent, channel, destination (masked), expires_at, resend_after, attempts_allowed}` |
| `POST` | `/auth/verification/confirm` | `{channel, code}` → `{verified, channel, user}`; single-use, 15-minute, 5 attempts |
| `GET` | `/me` | Current principal with roles, subject preferences, `emailVerified`, `phoneVerified`, `phone`, `suspended`, `suspendedReason` |
| `PATCH` | `/me` | Profile, plan and onboarding progress |

### Institutions

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/organizations` | Organisations the caller belongs to, each with their own role |
| `POST` | `/organizations` | Create one; caller becomes owner. Requires a verified email. Starts `pending_verification` |
| `GET` | `/organizations/{id}` | Members only; non-members get `404`, not `403` |
| `GET` | `/organizations/{id}/members` | Owner/admin only (roster holds other people's contact details) |
| `GET` | `/organizations/{id}/classes` | Owner/admin only: classes belonging to the institution |
| `GET` | `/organizations/{id}/report` | Institution dashboard: member counts by role, classes, published assignments, and the last 30 days of submissions, active learners and average score (per class too). Averages are `null` when nothing was submitted |
| `POST` | `/organizations/{id}/invitations` | `{email, role}` → `201 {invitation, token}`; the token is returned to the inviter **once** and the join link is also emailed to the invitee |
| `GET` | `/organizations/{id}/invitations` | Open and recent invitations, never their tokens |
| `DELETE` | `/organizations/{id}/invitations/{invitationId}` | Withdraw; the link stops working immediately |
| `POST` | `/organizations/invitations/accept` | `{token}`; must match the signed-in account's email |
| `DELETE` | `/organizations/{id}/members/{userId}` | Remove a member, or leave. Withdraws the teacher role with the membership |
| `POST` | `/admin/organizations/{id}/verify` | Platform administrator approves the institution |
| `GET` | `/admin/reminders?limit=` | Scheduled reminders with `{pending, claimed, sent, skipped, cancelled}`. Bodies are shown: they are generated from public facts, not credentials. Platform administrators and support |
| `POST` | `/admin/reminders/sweep` | Plan and send due reminders now. Platform administrators only, audited. Planning is idempotent; each reminder is claimed before it is sent and revalidated before it goes out |
| `GET` | `/admin/deliveries?limit=` | Outbound messages with status, attempts and last error, plus `{sent, pending, failed}`. Bodies are never returned. Platform administrators and support only |

Invitation rules: single use, addressed to one email, valid seven days, single-use claimed by
affected-row count. The invitation email carries the join link; if the relay is down the invitation row
still exists, the link is retried through the delivery queue, and the inviter still holds the token. A teacher invitation grants the platform teacher role — an owner issued it
deliberately — while admin and student invitations grant nothing outside the organisation.

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
| `POST` | `/practice-sessions` | `{mode, subjectId \| topicIds \| classId \| examBody \| mixed, count, difficulty, durationMinutes}` — see *Practice and simulation* below |
| `POST` | `/assessment-definitions/{id}/sessions` | Starts a timed attempt; enforces window, class membership, attempt limit |
| `GET` | `/assessment-sessions/{id}` | Owner only (or a teacher of the owning class) |
| `GET` | `/assessment-sessions?limit=20` | History |
| `PUT` | `/assessment-sessions/{id}/responses/{qvId}` | Answer write (below) |
| `POST` | `/assessment-sessions/{id}/submit` | Idempotent finalisation |
| `POST` | `/assessment-sessions/{id}/cancel` | Only while `in_progress` |
| `POST` | `/assessment-sessions/{id}/integrity` | The candidate's own browser reporting on itself: `focus_lost`, `focus_back`, `fullscreen_exit`, `paste`, `copy`, `offline`, `back_online`. Refused for somebody else's attempt (403), a closed attempt (409), or an unknown kind (400). The host's exam report shows the tally beside the score |
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

### Practice and simulation

`POST /v1/practice-sessions` starts one of two products, chosen by `mode`:

| | `practice` (default) | `simulation` |
| --- | --- | --- |
| Clock | four-hour server window nobody is meant to notice | `durationMinutes` (default 45, 5–240), set by the server |
| Paper | fresh draw every start | fixed: same selection + same caller ⇒ same paper, options shuffled per candidate |
| Corrections after submit | allowed | refused (`409`) — an exam is evidence |
| Answers and explanations | after submit | only after submit, exactly as in the graded path |

Both modes are scored by the same engine (`domain.ScoreAttempt`), so a simulated paper and a real one cannot drift
apart in marking.

Selection is **one** dimension, and `mixed` means "no dimension":

- `subjectId` (+ optional `topicIds` to narrow it);
- `classId` — the questions that class's published assessments already use; membership is checked server-side;
- `examBody` — the pathway key or body (`jamb`, `WAEC`, `Post-UTME`, …), matched case-insensitively, so a JAMB
  paper can only hold questions from JAMB subjects;
- `mixed: true` — the whole bank.

Combining two sources, or `mixed` with a source, is a `400 VALIDATION_ERROR` naming `selection`: "this subject but
that class" is ambiguous, and guessing would show a learner the wrong questions.

### Session projection

`GET /assessment-sessions/{id}` returns questions **without** `answerOptionId` or explanations while
the attempt is in progress. After finalisation, `result_released` follows the assessment's policy
(`immediate`, `scheduled`, `on_close`); only then does `review[]` include keys and explanations.

## Classes

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/classes` | Classes the caller belongs to, with their role |
| `POST` | `/classes` | Teacher/org admin; issues a six-character join code valid for 30 days |
| `POST` | `/classes/join` | Case-insensitive code. An expired code returns `410 JOIN_CODE_EXPIRED`, a wrong one `404` |
| `POST` | `/classes/{id}/join-code` | Reissue the code: the teacher who runs the class, a teacher in it, or an institution administrator. Replaces the old code immediately |
| `GET` | `/classes/{id}/roster` | Teacher-only, includes attempts and averages |
| `GET` | `/classes/{id}/assignments` | Published assessments with submission counts |

## Live classes

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/virtual-classes?scope=mine\|past\|all` | Classes the caller hosts or attends, each with the caller's `role` (`host`, `attendee`, or empty for a public listing) |
| `POST` | `/virtual-classes` | `{title, startsAt, creditsCost, durationMinutes, capacity, admission}`. `capacity` defaults to 50 and is refused outside 2–50, `creditsCost: 0` is capped at 45 minutes, `admission` is `open` (default) or `approval` and is fixed at creation |
| `GET` | `/virtual-classes/{id}` | One class, its attendees and the caller's role in it |
| `GET` | `/join/{slug}` | **Anonymous.** What an invite link says before it asks for an account: title, host, time, price, capacity, status |
| `POST` | `/virtual-classes/{id}/join` | Seat, then attendance row. Paid classes charge credits in the same transaction. On an `approval` class this answers **`202`** with `pending: true` and charges nothing; once the host declines, the same call is `403` |
| `POST` | `/virtual-classes/{id}/leave` | Closes the attendance row; presence is a heartbeat, not a flag |
| `POST` | `/virtual-classes/{id}/start` | Host only. `scheduled → live` |
| `POST` | `/virtual-classes/{id}/end` | Host only. `live → ended` |
| `GET` | `/virtual-classes/{id}/roster` | Attendees with `avatar`, `stageRole`, `stageSlot`, `present`, and join/leave timestamps |
| `GET` | `/virtual-classes/{id}/attendance` | The recorded session timeline, not a client-side counter |
| `GET` | `/virtual-classes/{id}/requests` | **Host only.** The admission queue: everyone whose request is undecided, oldest first |
| `POST` | `/virtual-classes/{id}/requests/{userId}` | **Host only.** `{decision: "accept" \| "reject"}`. Accepting takes a seat under the class row lock and charges then; `409` when the room is full or the learner is short of credits, and the request stays queued |
| `GET` | `/virtual-classes/{id}/ice` | iceServers for this caller: the deployment's STUN entries plus, when the relay is enabled, a `turn:` entry carrying a credential minted for that participant and expiring within the hour |
| `GET`/`POST` | `/virtual-classes/{id}/board` | The whiteboard payload, versioned so concurrent strokes do not overwrite each other |
| `GET`/`POST` | `/virtual-classes/{id}/signal` | SDP/ICE exchange for the board's end-to-end-encrypted channel |
| `GET`/`POST` | `/virtual-classes/{id}/discount-codes` | Host-issued codes that reduce the credit price |
| `POST` | `/virtual-classes/{id}/media` | One negotiation per participant: the browser posts an SDP offer, the server's forwarder answers |
| `POST` | `/virtual-classes/{id}/stage` | Host only. `{userId, role}` where `role` is `speaker` or `audience`; slots are assigned by the server from the lowest free one, and a host cannot be demoted |

A room holds **50 people**; the **six-seat stage** (host slot 0 plus five speaker slots) is what the media server
forwards, so fifty people in a room never means fifty simultaneous video streams from one laptop. Audio and video
travel hop by hop — browser → server → browser — so the honest claim is *encrypted in transit and forwarded by an
SFU we run*, not end to end. Only the whiteboard is end-to-end encrypted.

A class with `admission: "approval"` is a **waiting room**: a learner's join creates a `pending` attendance row — no
seat, no charge — and the roster, board, stage and signalling all answer `403` (naming whether the request is
pending or declined) until the host decides. Charging at acceptance rather than at knocks means a decline never
costs a refund. See [`VIRTUAL_CLASSES.md`](./VIRTUAL_CLASSES.md#admission-and-the-waiting-room).

Free classes are capped at **45 minutes server-side**, enforced twice: at creation, and again at the door by
`join`, `start` and `/media`, so a clock cannot be talked past. A paid class keeps its own `durationMinutes`.
See [`VIRTUAL_CLASSES.md`](./VIRTUAL_CLASSES.md).

## Synchronised exams

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/exams` | Exams the caller hosts, has sat, or that are open now |
| `POST` | `/exams` | `{title, startsAt, endsAt, durationMinutes, questionVersionIds \| questionCount, shuffle}`. Any signed-in account may host a sitting; the paper is drawn from approved questions, which is what makes two candidates comparable |
| `GET` | `/exams/link/{slug}` | **Anonymous.** Invite payload for a shared URL; never includes questions or answers |
| `GET` | `/exams/{id}` | The sitting as the caller may see it: the paper only while open, and the caller's own attempt |
| `POST` | `/exams/{id}/start` | Opens the caller's attempt inside the window and returns it as an assessment session — questions, deadline, no answer keys. Hosts cannot sit their own sitting |
| `GET` | `/exams/{id}/results` | Host-only, end to end: per candidate status, start, submit, deadline, score, and seconds still accepted |

`{id}` accepts either the exam id or its slug, so a shared link resolves without a lookup first. Answering and
submitting deliberately reuse the assessment engine rather than growing a second one: candidates write responses
with `PUT /assessment-sessions/{id}/responses/{qvId}` and finish with
`POST /assessment-sessions/{id}/submit`, which is where the deadline, the one-attempt rule and the answer-key
withholding are already enforced. `/exams/{id}/start` is the door; the engine is the room.

Everyone sits the **same paper in the same window for the same duration**; the order of questions and options is
permuted per candidate from a seed derived from `(examId, userId)`, so two neighbours cannot read answers off each
other's screen while the content stays identical. The deadline is `min(startedAt + duration, endsAt)`, set by the
server when the attempt opens — a late join gets the time that is left, not a fresh window. Answers stay hidden
until submit, one attempt per candidate, and the paper is auto-submitted at its deadline. See
[`EXAMS.md`](./EXAMS.md).

## Consistency

```
GET /v1/consistency?days=30
→ 200 { "consistency": {
    "currentStreak": 4, "longestStreak": 11, "activeDays": 9, "windowDays": 30,
    "totalActiveDays": 43, "todayActive": false, "streakAtRisk": true, "timeZone": "UTC",
    "kindLabels": { "practice": "Practice", "class": "Live class", "credits": "Credits earned" },
    "days": [{ "date": "2026-10-31", "active": true, "kinds": ["class", "practice"],
               "isToday": false, "inStreak": true }, …] } }
```

The streak is **derived** from activity the system already records — an assessment session, a live class
attended, credits earned — not from a counter a client increments, so a client that never opens the page still
accrues one and a client that forges a day is ignored. `days` is the strip a UI draws; `kinds` means it can say
"class and practice" instead of a bare dot; `streakAtRisk` is the one field a reminder needs (today not done,
yesterday done). A day is a UTC day and the response says so rather than leaving a client to guess. The `days`
parameter is clamped to 1–180 and defaults to 30; an out-of-range value is clamped rather than refused, because a
strip size is a display choice and not a rule. See [`PHASE_STATUS.md`](./PHASE_STATUS.md).

## Discovery

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/search?q=&type=&limit=` | Public. Prefix matching over a token index; every term must match |
| `POST` | `/admin/search/reindex` | platform_admin. Full rebuild of the projection, idempotent |

`terms` in the response is what the server actually searched for after tokenising and dropping stopwords, so
a puzzling result can be explained instead of guessed at. An empty or stopword-only query returns
`200 {results: [], terms: []}` — an empty answer is not an error, and a client that treats it as one shows a
failure where it should show "no results".

`type` accepts `all | product | subject | topic`; `limit` is clamped to 50 rather than refused. Ranking is
title 3 / subtitle 2 / body 1, plus 5 for an exact title, with a stable total order.

Results are filtered against the **source tables** on every query, so an unpublished product or a deactivated
subject cannot appear even if the index is stale. The index only decides matching; it never decides
visibility. See [`SEARCH.md`](./SEARCH.md).

## Commerce

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/products` | Catalogue with `owned` flag |
| `POST` | `/orders` | Server-computed totals from catalogue prices |
| `GET` | `/orders`, `/orders/{id}` | Owner only |
| `GET` | `/library` | Entitlements, not a client-side purchase flag |
| `POST` | `/orders/{id}/simulate-payment` | Development only; signs a real webhook |
| `POST` | `/webhooks/payments/{provider}` | `x-edumart-signature` = HMAC-SHA256 hex of the raw body |
| `POST` | `/webhooks/wallet/{provider}` | The provider's own signature (`x-paystack-signature` = HMAC-SHA512 hex of the raw body). The only path that can mark a top-up paid when a live provider is configured: signature → event type → reference → amount, and each failure moves nothing |

Webhook behaviour: verify signature → record event (unique per `provider`+`event_id`) → match order by
reference → verify amount and currency → ignore non-`succeeded` statuses → mark paid, grant
entitlements, audit. Replays return `{received: true, duplicate: true}`.

### Wallet (recharge → credits)

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/wallet` | Balance (summed from the ledger, never stored), `earned`/`spent`, the terms (`minTopupMinor`, `nairaPerPoint`), the last ten top-ups and the last ten ledger rows |
| `POST` | `/wallet/topups` | `{amountMinor}` in **kobo**. Minimum **₦200 (20000)**; at most ₦1,000,000. Creates a `pending` top-up and credits nothing. With a live provider the response also carries `checkoutUrl` — send the browser there; credits appear when the signed webhook settles |
| `POST` | `/wallet/topups/{id}/confirm` | **Sandbox only** (registered only when `EDUMART_ALLOW_SANDBOX_PAYMENTS` is on). Marks the top-up paid and credits the points once |

A top-up is **not** an order: an order buys a product and grants an entitlement, a top-up changes a balance at a
fixed rate. Three consequences are contractual:

- **The floor is ₦200 and it is the server's.** A smaller amount is `400 VALIDATION_ERROR` on the field
  `amountMinor`, with the floor in the message; nothing is written for a refused attempt.
- **The rate is stored on the row.** `naira_per_point` is copied into each top-up, so changing the price tomorrow
  cannot rewrite today's receipt.
- **Crediting is idempotent.** The ledger row carries the idempotency key `topup:<topup id>` and the status update
  is conditional on the row still being `pending`, so a retried or duplicated confirmation pays once. A confirmation
  for somebody else's top-up is `404`, never `403` — its existence is not the caller's business.

Points land in the same append-only ledger that referrals and rewards write to (`entry_type = 'topup'`,
`source_type = 'wallet_topup'`), so a live class charges topped-up and earned credits identically.

**With a live provider** (`EDUMART_PAYMENT_PROVIDER=paystack` plus `EDUMART_PAYSTACK_SECRET_KEY`) the top-up is
started by writing the row **first**, so a webhook that arrives before the checkout response still matches a
reference the ledger knows, and then the host is sent to Paystack's page. Only `POST /webhooks/wallet/paystack`
marks it paid:

| Check, in order | Failure |
| --- | --- |
| HMAC-SHA512 of the raw body in `x-paystack-signature` | `403`, nothing recorded |
| the event is `charge.success` or `charge.failed` | `200 {ignored: true}` — a transfer is not a top-up |
| the reference names a top-up | `404`, and an error log: money arrived for a reference we never issued |
| amount **and** currency equal the row's | `409`, nothing credited, logged for a person to resolve |

A retry of the same event hits the ledger's `topup:<id>` idempotency key and answers `credited: false`. The
browser returning from the checkout is not evidence of payment: the wallet re-reads the balance, and the
callback URL can be typed by anybody.

### Refunds

| Method | Path | Roles | Notes |
| --- | --- | --- | --- |
| `POST` | `/admin/orders/{id}/refund` | finance, platform_admin | `{amountMinor?, reason, idempotencyKey?}` — omit the amount to refund the remainder |
| `GET` | `/admin/orders/{id}/refunds` | finance, platform_admin, support | Refund history plus `{totalMinor, refundedMinor}` |
| `POST` | `/admin/refunds/{id}/resolve` | finance, platform_admin | `{outcome: succeeded\|failed, note}` — close a refund the provider left ambiguous |
| `GET` | `/admin/finance/reconciliation?from=&to=` | finance, platform_admin | Six contradiction checks; reports how many ran |

A refund is an **append-only compensating entry**: `orders.totalMinor` keeps saying what was charged,
the refund rows say what came back, and `status` becomes `partially_refunded` or `refunded` as a derived
value recomputed when a refund settles. An amount beyond the remainder is a `409 CONFLICT` whose `details`
carry `{totalMinor, reservedMinor, remainingMinor, attemptedMinor}` — a refusal has to say what is left.
Repeating a request with the same `idempotencyKey` returns the original refund and does not call the
provider again; providers retry.

**A refund is a request until the provider confirms it**, and `refund.status` says which:

| Status | Meaning | Money effects |
| --- | --- | --- |
| `pending` | Accepted, confirmation still to come | Amount reserved against the order; order, entitlements and commission untouched |
| `succeeded` | Confirmed | Order recomputed, entitlements revoked on a full refund, commission reversed proportionally, receipt queued |
| `failed` | Provider refused | Nothing moved; the reservation is released |
| `needs_review` | Ambiguous answer (timeout, 5xx) | Nothing moved and **nothing retried** — a person checks the provider and resolves it |

`POST /admin/refunds/{id}/resolve` is how `needs_review` is closed: the operator reports what they found,
the same settlement path runs, and `payment.refund_resolved` records who decided and why. Every attempt is
audited — `payment.refund_requested`, then `payment.refunded` or `payment.refund_failed` /
`payment.refund_needs_review` — with the amount, refund id and provider reference, never customer details.

The sandbox provider settles in-process, so a local refund is `succeeded` immediately. With
`EDUMART_PAYMENT_PROVIDER=paystack` the refund is posted to the provider's API, stays `pending` until its
`refund.processed` callback arrives, and callbacks are verified with the provider's HMAC-SHA512 header
rather than the API's own signature scheme.

## Staff

| Method | Path | Roles |
| --- | --- | --- |
| `GET` | `/admin/review-queue` | content_editor, platform_admin |
| `POST` | `/admin/question-versions/{id}/review` | content_editor, platform_admin |
| `GET` | `/admin/metrics` | platform_admin, finance |
| `GET` | `/admin/cases` | support, platform_admin |
| `POST` | `/admin/cases/{id}/resolve` | support, platform_admin |
| `POST` | `/support-cases` | any signed-in user |
| `GET` | `/admin/users?q=&status=` | platform_admin — find an account by email or name; identity and status only, never credentials |
| `POST` | `/admin/users/{id}/suspend` | platform_admin — `{reason}`; suspends the account **and** revokes every session it holds |
| `POST` | `/admin/users/{id}/reinstate` | platform_admin — lifts the suspension; the person signs in again |
| `GET` | `/admin/organizations?status=` | platform_admin — institutions awaiting a decision, oldest first, with the applicant's contact address |
| `POST` | `/admin/organizations/{id}/reject` | platform_admin — `{reason}`; refuses the application and keeps the record |
| `GET` | `/admin/deliveries?limit=` | support, platform_admin |

`/admin/deliveries` answers "the emails are not arriving" without a provider dashboard: it returns the
most recent messages with `status` (`pending`, `sent`, `failed`), `attempts`, `lastError` and the
credential they relate to, plus `stats {sent, pending, failed}`. Bodies are never returned — an operator
needs to know a message failed, not to read the code inside it.

## Response headers

Every response — success, refusal or 404 — carries:

| Header | Value | Why |
| --- | --- | --- |
| `X-Content-Type-Options` | `nosniff` | A JSON error body that a browser renders as HTML is a reflected-XSS primitive |
| `X-Frame-Options` | `DENY` | An API response must never be framed |
| `Referrer-Policy` | `no-referrer` | Ids in a path must not leak to third parties via `Referer` |
| `Cache-Control` | `no-store` | Every response here can carry one learner's orders, answers or scores |
| `X-Request-Id` | a per-request id | Quoted in the error envelope, and in the log line for the same request |

Behind a load balancer, set `EDUMART_TRUST_PROXY=true` so rate limits key on the real client address.
Left `false`, `X-Forwarded-For` is ignored — a caller cannot choose its own rate-limit bucket.

## Outside /v1

| Endpoint | Contract |
| --- | --- |
| `GET /healthz` | `200 {status:"ok",time,driver}`. Liveness only: never checks a dependency, so a database blip cannot restart a healthy process. |
| `GET /readyz` | `200` when the database answers and every migration is applied; `503` otherwise, with `checks[]` naming the failing dependency. Used by load balancers and deploy gates. |
| `GET /metrics` | Prometheus exposition for the monitoring system. **Not part of the product contract** and deliberately outside `/v1`: it is infrastructure, its labels are chosen by the code rather than the caller, and it is not versioned with the API. Requires `Authorization: Bearer $EDUMART_METRICS_TOKEN` when that is set, and answers loopback callers only when it is not. A refusal is a `404`, not a `403`. See `docs/OBSERVABILITY.md`. |

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
