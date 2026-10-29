# Architecture

## Shape

```
Browser (PWA) ─┐
               ├── HTTPS ─→ Next.js (apps/web) ──/v1 proxy──→ Go API (apps/api) ──→ PostgreSQL
Native app ────┘                    │                              │
   (Expo)                           └── server components          └── SQLite (dev/CI)
```

- **Go API** is the only writer of domain state. It owns identity, sessions, attempts, deadlines,
  scores, ledgers, entitlements and every authorisation decision.
- **Next.js** renders screens and server-actions form submissions. It reads through the API with the
  user's cookies forwarded, and proxies `/v1/*` so the browser stays same-origin.
- **Expo app** talks to the API directly with a bearer token from the platform secure store (there
  is no httpOnly cookie jar in React Native). Same endpoints, same responses.
- **`packages/shared`** holds the wire types, the API client, the signup step definition and the
  design tokens. It is the contract that keeps the two shells honest.

## Why a modular monolith

One deployable, explicit packages, no network hops between domains. A single transaction can score an
attempt, update topic analytics and award points atomically — which is exactly what
`assessment.Finalize` does. Splitting that into services later is a package-to-process move, not a
rewrite.

## Backend modules

| Package | Responsibility | Notable rule |
| --- | --- | --- |
| `domain` | Pure functions: scoring policies, deadline arithmetic, release policy, seeded shuffle | No I/O, exhaustively unit-tested |
| `assessment` | Practice sets, timed attempts, answer writes, finalisation, projections | Snapshots + versioned writes + one finalisation path |
| `auth` | Registration, login, sessions, password reset, roles, profile | PBKDF2-SHA256 (stdlib), opaque tokens, hash-at-rest |
| `signup` | Pre-account wizard drafts | Progress survives refresh and crosses devices |
| `content` | Pathways, subjects, topics, approved-question counts | Only approved + published versions are countable |
| `classroom` | Classes, join codes, rosters, assignments | Roster is teacher-only; assignments are class-scoped |
| `commerce` | Catalogue, orders, webhooks, entitlements | Money truth only from a verified webhook |
| `rewards` | Points and commission ledgers | Two ledgers, never one balance that mixes assets |
| `insights` | Dashboard, analytics, study plan | Read models derived from attempts |
| `admin` | Content review, support cases, metrics | Every mutation writes an `audit_logs` row |
| `httpx` | Envelope, request ids, recovery, rate limiting, JSON helpers | One error shape for every failure |

## Assessment lifecycle

```
POST /v1/practice-sessions
  └─ select approved questions → freeze snapshot {policy, questions[]}
     └─ started_at = now (server), deadline_at = started_at + duration
        status = in_progress
            │
            ├─ PUT …/responses/{qvId}   version++, client_sequence recorded, replay-safe
            │
            ├─ POST …/submit  ─┐
            └─ deadline passed ┴─→ Finalize(reason)
                                     ├─ score with the snapshot's policy version
                                     ├─ persist per-question is_correct / awarded
                                     ├─ update topic_performance
                                     ├─ append points ledger (idempotency key)
                                     └─ status = submitted | expired   (terminal)
```

`Finalize` re-checks the status inside its transaction, so two concurrent submissions cannot both
score, and a duplicate submission cannot double-award points.

## The attempt snapshot

`assessment_sessions.snapshot_json` holds the exact questions, option order, points and scoring
policy served for that attempt. Consequences:

- Publishing a correction to a question never rewrites a finished attempt.
- The score can be recomputed years later with the same inputs.
- Answer keys live in the snapshot, which is never sent to the client before release.

## Client save model

The runner keeps three pieces of local state per question: the selected option (optimistic), the last
server-confirmed `version`, and a save status (`pending | saved | failed`).

- A write sends `expectedVersion` and a monotonic `clientSequence`.
- A replayed write with the same value and sequence is a no-op that returns the current version.
- A different value against a stale version gets `409 CONFLICT` with `current_version`, and the UI
  reloads rather than overwriting another device's answer.
- A transport failure leaves the answer `failed` and retries when connectivity returns. The UI says
  "N saved answers are on the server" only for confirmed writes.

## Security posture

- Session tokens are 256-bit random, stored as SHA-256 hashes; the raw token exists only in the
  httpOnly cookie (web) or the OS secure store (native).
- Passwords use PBKDF2-HMAC-SHA256 at 210k iterations with a per-password salt; the cost is stored
  with the digest so it can be raised without invalidating accounts.
- Login failures are deliberately indistinguishable ("Email or password is incorrect.").
- Authorisation is checked in the handler that mutates or reads the resource, not in the UI. Staff
  access to a learner's attempt requires membership of that attempt's class; platform admins are the
  only global bypass, and it is explicit.
- Every response carries `X-Request-Id`, and errors echo it so a screenshot maps to a log line.
- Rate limits: 30 requests/minute per IP on `/v1/auth/*` groups and 8 login attempts per minute per
  IP+email.
- Cache headers are `no-store` on all API JSON.

## Failure behaviour

| Failure | Behaviour |
| --- | --- |
| API unreachable from the Next server | The page renders an error state with the message from `ServerApiError`; `npm run dev:api` is named in the copy |
| Device offline | `OfflineBanner` states it; failed writes queue and retry; the PWA serves `/offline` for navigations |
| Deadline passes mid-answer | The write is refused with `SESSION_EXPIRED`; the attempt is finalised immediately and answers already saved are scored |
| Webhook replay | `provider_events` is unique per `(provider, event_id)`; a replay is acknowledged as a duplicate |
| Webhook amount mismatch | Recorded as `amount_mismatch`, order stays pending, an audit row is written |
| Panic in a handler | Recovered, logged with the request id, returned as `INTERNAL_ERROR` |

## Deliberate trade-offs

- **SQLite in development, PostgreSQL in production.** One portable query layer, no ORM. The trade-off
  is that PostgreSQL-specific features (row-level security, advisory locks) are not used yet.
- **Rate limiting is in-process.** Fine for one instance; the seam (`httpx.Limiter`) is ready for a
  Redis-backed implementation before horizontal scaling.
- **No background job runner yet.** `job_queue_items` exists as a table; the expiry sweep is an
  in-process ticker because it is cheap and must run even when nobody is polling.
- **The wizard draft is an anonymous authenticated resource.** It is a bearer token in an httpOnly
  cookie, expires in 30 days, and is deleted when the account is created.

## Contract and local infrastructure

`packages/contracts/openapi.yaml` is the single description of the HTTP surface, enforced by
`scripts/check-contract.py` against the router in both directions. `infra/docker/compose.yml`
provides PostgreSQL and Redis for the production-shaped path; the default local and CI path is
SQLite with in-process rate limiting. `docs/SECURITY_AND_DURABILITY.md` records the consistency
decisions behind both choices.

## Database engines

One query layer serves two engines. SQLite (a file) is the default for local development, CI and
demos; PostgreSQL is the production system of record. The SQL is written in portable form with `?`
placeholders, and `apps/api/internal/store/pg.go` translates them to PostgreSQL's `$n` inside a
`database/sql` connector — skipping literals, quoted identifiers, dollar-quoted bodies and
comments, so a `?` in data is never mistaken for a bind parameter.

The consequence worth stating: the production engine is exercised by the whole test suite and the
HTTP smoke journey in CI, not only by the first request that reaches production. Schema changes go
through the versioned runner (`store/migrate.go`), which checksums each migration and refuses to
start if an applied one was edited afterwards.

Redis is provisioned in `infra/docker` but unused: rate limiting is in-process, and there is no
background worker yet. Both are Phase 9 items.

## Proving an address is real

An email address typed into a signup form is a claim. `internal/auth/verification.go` turns it into a
fact, and everything about how it does that follows from the fact that a six-digit code is a very
small secret.

- **Stored as a keyed hash.** `HMAC-SHA256(secret, userID ‖ channel ‖ code)`. Plain `sha256` would be
  pointless — a million candidates fall instantly — so the key lives in the environment rather than
  the database. A SQL dump alone reveals nothing, and a row cannot be replayed against another account
  or channel.
- **Single use, claimed by the database.** Confirmation is an `UPDATE … WHERE consumed_at IS NULL`
  whose affected-row count decides the winner, so two simultaneous confirmations cannot both succeed.
- **Finite guessing.** Attempts are counted on the row and the code is *spent* on the last try, rather
  than remaining valid forever at five attempts.
- **Throttled sending.** 60-second cooldown and five codes per hour per user and channel: an endpoint
  that sends a message is an endpoint that can be used to bill you and flood someone's inbox.
- **Delivery is an adapter around a durable queue.** `internal/notify` exposes `Sender` (transport) and
  `Courier` (what identity actually depends on). Every message is written to `notification_deliveries`
  *before* the first attempt, then retried on a backoff by a worker, so a relay outage delays a code
  instead of losing it. `LogSender` is the development default, `SMTPSender` is the real one, and
  `Recorder` is what tests read. A new channel is one type, not a change to identity or commerce.
- **Failure is not success.** If the sender errors, the row is deleted rather than left live: a code
  nobody received must not remain a valid credential, and it must not consume the user's hourly quota.

Verification is enforced where it protects something concrete: `POST /v1/orders` refuses an unverified
address (`EMAIL_NOT_VERIFIED`), because receipts and refunds need a mailbox that exists. It is
deliberately *not* a blanket gate on practice or mocks — an unverified learner loses nothing by
practising, and blocking them would only teach people to type fake addresses.

## Institutions and the authorisation model

Authority inside an institution comes from a membership or an invitation. Never from a role chosen at
signup, and never from an identifier a client sends.

- `organization_memberships` is the source of truth: `owner`, `admin`, `teacher`, `student`.
- Creating an organisation makes you its owner *and grants nothing platform-wide*. The owner can act
  on that institution and nowhere else.
- A teacher invitation is the one invitation that confers the global teacher role, because an owner
  deliberately issued it. Removing the membership withdraws that role when no other organisation still
  grants it — otherwise "removed" would be a label rather than a boundary.
- Every query is scoped by the caller's membership. A non-member receives `404`, not `403`: whether a
  given institution exists is itself information, and a distinguishable `403` turns id guessing into a
  map of other schools.
- Privileged actions write to `audit_logs` in the same transaction as the change they describe, so an
  audit trail cannot disagree with the state it is meant to explain.

Approval is deliberately outside the institution's control: a new organisation is
`pending_verification` until a platform administrator approves it. An institution vouching for itself
is not verification.
