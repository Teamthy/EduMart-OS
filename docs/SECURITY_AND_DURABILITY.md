# Security, consistency and data durability

What the code actually guarantees today, what it assumes, and what is still missing. Claims here
should be traceable to a test or a line of code; if one drifts, it is a bug.

## Consistency decisions

There is no distributed system to trade availability against yet: the API is one Go binary in
front of one database. The decisions that matter are therefore about which paths may be eventually
consistent and which may never be.

| Path | Model | Why |
| --- | --- | --- |
| Answer writes (`PUT …/responses/{qvId}`) | Authoritative; acknowledged after commit | "Saved" must never be optimistic. The client shows `pending` until the server confirms |
| Submission and scoring | Authoritative, transactional | Re-checked inside the transaction, so two concurrent submits cannot both score |
| Deadlines | Server clock only | A client clock is display-only; `CanAcceptAnswer` decides |
| Permissions, tenancy | Authoritative on every request | No cached role on the client is trusted |
| Payments and entitlements | Authoritative, webhook-driven | An order is paid only after a verified signature and amount check |
| Points and commission | Authoritative, append-only ledgers | Balances are derived from entries, never mutated in place |
| Dashboard, analytics, learning summary | Rebuildable read models | Losing them costs a recompute, not data. They are never the source of a score |

A practice answer is the one case where a correction after submission is allowed. It is rescored
from the stored responses with the same `domain.ScoreAttempt` used at submit, and the endpoint
returns no correctness signal, so it cannot be used to discover the key. Graded assessments are
frozen on submit.

## Durability controls in place

- **Transactions** around anything that must land together: finalisation (status, score, per-question
  results, points award), order and entitlement creation, login and session writes.
- **Optimistic concurrency** on answers: `answer_version` increments on write; a stale
  `expectedVersion` is refused with `409` and the current version, rather than overwriting a newer
  answer from another device.
- **Idempotency by construction**: replayed answer writes return the stored version; webhook event
  ids are unique in `provider_events`, so a retried callback grants nothing twice.
- **Immutable attempt snapshots**: `assessment_sessions.snapshot_json` holds the questions, options
  and scoring policy version used for that attempt, so re-scoring an old attempt stays reproducible
  after content edits.
- **Append-only ledgers**: `points_ledger_entries` and `commission_ledger_entries` are separate and
  insert-only; corrections are compensating entries.
- **Constraints in the schema**, not only in Go: foreign keys, uniqueness on session tokens, class
  memberships, provider event ids and referral codes.
- **Silent-failure avoidance**: the API refuses to boot when the database is unreachable, and names
  the missing driver rather than falling back to SQLite. Delivery failures are recorded on the
  message row rather than swallowed, and an unknown SMTP configuration exits non-zero at startup.
- **A durable outbox for credentials**: verification codes, reset tokens, invitations and receipts are
  written to `notification_deliveries` before the first send attempt, then retried on a backoff
  (30 s → 2 m → 10 m → 30 m, five attempts). A relay outage delays a code; it does not lose it.
- **Secrets do not live in the queue**: a delivered or abandoned message has its body cleared, an
  abandoned verification code or reset token is deleted, and the operator view cannot return a body.

### Delivery-specific controls

| Control | Where |
| --- | --- |
| The queue is a database table, not an in-memory list | `notification_deliveries`; a restart resumes retries |
| Bodies are cleared on send or abandon, and never returned by the API | `notify.Service.attempt`, `/v1/admin/deliveries` |
| An undelivered verification code or reset token is deleted, not left live | `notify.Service.abandon` |
| Header injection is refused, and CRLF is enforced on every line | `SMTPSender.composeMessage` |
| Cleartext SMTP is refused when `EDUMART_ENV=production` unless explicitly opted in | `SMTPConfig.Validate` |
| A bad SMTP configuration stops the process at startup | `cmd/api` `buildCourier` |

Delivery is at-least-once: a message that times out after the relay accepted it can be attempted again.
Every message is therefore written to be idempotent — a second receipt for the same order references the
same order and grants nothing, and a second verification code supersedes the first.

### Account status and moderation

| Rule | Enforced by |
| --- | --- |
| A suspended account stops working immediately | `Suspend` sets the status **and** revokes every live session in one transaction |
| A surviving token still cannot be used | `AuthenticateSession` refuses any account whose status is not `active` — the check is what makes the rule survive a restore or a stale replica |
| Codes cannot be redeemed after suspension | outstanding `verification_codes` rows are deleted with the suspension |
| Moderation cannot lock everyone out | only platform administrators may suspend, never themselves, never another administrator |
| Every decision is attributable | `user.suspended` / `user.reinstated` rows in `audit_logs`, with actor, reason and request id, written in the same transaction |
| Lookup does not become browsing | `/v1/admin/users` returns identity and status only, caps at 100 rows, and is closed to everyone but platform administrators |

Reinstatement deliberately does not restore sessions: the person signs in again, which also proves they
still control the mailbox on the account.

### Tenancy boundaries

| Rule | Enforced by |
| --- | --- |
| A resource is reachable only through a membership | every organisation query joins `organization_memberships` |
| A non-member is told "not found", not "forbidden" | 404 on organisation, roster, invitations and classes — the API cannot be used to enumerate institutions |
| Roles never come from a self-selected label | signup roles grant nothing; a teacher's platform role comes from an owner's invitation, and admin/student invitations grant nothing globally |
| A membership ends an access in the same transaction | `RemoveMember` deletes the membership and the teacher role it uniquely carried |
| Class ownership is explicit | `classes.organization_id` is set only when an owner or administrator creates the class for the institution |
| Privileged actions are auditable | `organization.*` events in `audit_logs`, written inside the same transaction |

The negative tests that hold these rules are `TestCrossTenantIsolation`, `TestRosterIsForAdministrators`,
`TestRemovingMembershipRemovesAccess`, `TestLastOwnerCannotBeRemoved`, `TestInstitutionClassesBelongToTheInstitution`
and `TestStudentCannotAdministerInstitutionClasses`, plus the institution section of the smoke run.

## Security baseline

Implemented:

- Password hashing with PBKDF2 (per-password salt, constant-time comparison) — `internal/auth`.
- Opaque session tokens, server-side revocation on logout and on password reset.
- httpOnly, SameSite=Lax cookies for the web app; bearer tokens in the OS keystore on mobile. Both
  resolve to the same server-side session, so revoking in one place signs out the other.
- Authorisation in handlers, not the UI: role checks plus class-scoped teacher access
  (`assessment.owned`, `classroom.Roster`), with a test that a teacher cannot read another class.
- Rate limits on auth and assessment routes, per client address.
- CORS restricted to configured origins with credentials allowed only for them.
- Webhook authentication by HMAC-SHA256 over the raw body, with the secret per environment.
- Security headers on the web app (no framing outside development, no sniffing, no referrer leak).
- One error envelope with a request id; no stack traces or SQL errors reach the client.
- Institution membership: org-scoped authority from membership, invitations that are single-use,
  email-locked, hash-stored and revocable, `404` (not `403`) for non-members to prevent organisation
  enumeration, and every privileged change written to `audit_logs` in the same transaction.
- Email and phone verification: six-digit codes stored only as an HMAC keyed by a server secret,
  single-use (claimed by an affected-row count), valid for 15 minutes, spent after five wrong
  attempts, throttled to one send per 60 seconds and five per hour per channel. An undelivered code is
  deleted rather than left valid. Checkout requires a verified address.

Not there yet — see `docs/PRODUCTION_READINESS.md` for the full list:

- MFA, device/session listing, idle timeout, and login anomaly detection.
- Verification is not a mandatory onboarding step: an unverified account can still practise and take
  mocks. Money is gated; learning is not.
- No real delivery provider yet — codes go to the structured log in development, which is why the log
  sender redacts in production and refuses to pretend it delivered anything.
- Secret management, key rotation, SAST/dependency scanning in CI.
- Audit trail: admin review, case resolution and organisation membership changes are recorded in
  `audit_logs`, but there is no general audit stream and no retention policy for it.

## Backup, restore and the honest gaps

There is no backup configuration in this repository, and claiming otherwise would be worse than
saying nothing. Before launch:

1. Choose managed PostgreSQL with point-in-time recovery; replication is not a backup.
2. Define RPO/RTO per data class — attempts and payments are the strictest, read models the loosest.
3. Run a restore drill into a scratch environment and prove the app boots against it.
4. Keep the append-only ledgers exportable independently of the primary database.

## Data minimisation

Learners are the sensitive population here: many will be minors. The schema stores what assessment
requires — name, email, exam pathway, target date, timezone, study minutes, responses, points.
It does not store payment card data (the provider holds it), device identifiers, location or
biometrics. A retention and deletion policy still needs product and legal sign-off, and Nigerian
NDPA obligations (parental consent, data subject requests) must be reviewed before launch.
