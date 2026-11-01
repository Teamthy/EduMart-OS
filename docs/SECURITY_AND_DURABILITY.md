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
  insert-only; corrections are compensating entries. Refunds follow the same rule: `refunds` rows are
  appended, `orders.total_minor` is never rewritten, and a full refund books a negative commission entry
  keyed `commission:reversal:<refundId>` so a reversal cannot happen twice.
- **Money movements are attributable**: every refund writes `payment.refunded` to `audit_logs` with the
  amount, the full flag and the order reference (no customer PII), and a reason is mandatory at the API
  boundary — an unexplained refund is a loss nobody can account for later.
- **A refund is a request until a provider confirms it**: `refunds.status` is
  `pending`/`succeeded`/`failed`/`needs_review`, and the money effects (order status, entitlement
  revocation, commission reversal) hang off the transition to `succeeded`. An ambiguous provider answer is
  never retried automatically, because a timeout may mean the refund already happened; it is recorded as
  `needs_review`, alerted on, and closed by a person whose note is audited.
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

- Password hashing with Argon2id (m=19456 KiB, t=2, p=1, per-password salt, constant-time comparison) —
  `internal/auth/password.go`. Legacy PBKDF2 digests still verify and are upgraded on the next sign-in.
- Opaque session tokens, server-side revocation on logout and on password reset.
- httpOnly, SameSite=Lax cookies for the web app; bearer tokens in the OS keystore on mobile. Both
  resolve to the same server-side session, so revoking in one place signs out the other.
- Authorisation in handlers, not the UI: role checks plus class-scoped teacher access
  (`assessment.owned`, `classroom.Roster`), with a test that a teacher cannot read another class.
- Rate limits on auth, assessment, institution, signup-draft, referral-lookup and webhook routes, per
  client address — and **forwarding headers are only believed when `EDUMART_TRUST_PROXY=true`**, because
  a rate limit keyed on a caller-chosen header is not a rate limit.
- CORS restricted to configured origins with credentials allowed only for them.
- Webhook authentication by HMAC-SHA256 over the raw body, with the secret per environment.
- Security headers on the web app (no framing outside development, no sniffing, no referrer leak) and on
  every API response (`nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`,
  `Cache-Control: no-store`) — asserted by `TestEveryResponseCarriesTheHardeningHeaders`.
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
- Secret management and key rotation for the API's own secrets (the SMTP and webhook keys are environment
  variables; nothing rotates them automatically).
- Dependency and secret scanning now run in CI (see `docs/THREAT_MODEL.md`); there is still no SAST pass
  over first-party code, and no dependency-update automation.
- Audit trail: admin review, case resolution and organisation membership changes are recorded in
  `audit_logs`, but there is no general audit stream and no retention policy for it.

## Threat model

`docs/THREAT_MODEL.md` is the assessment this section belongs to: assets, adversaries, the controls that
exist with the test or file behind each one, and eleven residual risks with owners. Read it before
changing an authorization rule, a money path or the rate limiter.

## Sessions and password hashing

| Concern | What the code does | Why that shape |
| --- | --- | --- |
| Password storage | Argon2id, m=19 MiB, t=2, p=1 | Memory-hard, not just CPU-hard: a GPU farm has to buy RAM for every parallel guess. Parameters live in the digest, so they can be raised without invalidating anyone |
| Legacy digests | PBKDF2 still verifies, and is rewritten as Argon2id on next sign-in | Changing the algorithm must never lock a learner out. Sign-in is the only moment the plaintext exists |
| Tampered digests | Parameters bounded when parsing (≤1 GiB, ≤20 passes, ≤32 lanes) | A compromised row must not be able to make the process allocate a gigabyte per attempt |
| Access token | Opaque, **1 hour** (was 7 days), stored as `sha256` | Only a hash is stored, so a database read cannot be replayed as credentials. Seven days made rotation decorative: the access cookie outlived every session it could have refreshed, so no browser ever called the refresh endpoint |
| Refresh token | Opaque, single-use, 30 days, rotated on every exchange | A token used twice is proof that two parties hold it |
| Refresh cookie | `HttpOnly`, `SameSite=Lax`, `Secure` behind TLS, **`Path=/v1/auth`** | Scoped to the three endpoints that need it: at `Path=/` a 30-day credential rides along on every page, asset and API call. The token is also still returned in the body, because a native client has no cookie jar |
| Browsers that rotate | The web middleware renews an expired session before the page renders, and stores both rotated cookies | A rotation a browser never performs is a property of the test suite, not the product |
| Native clients that rotate | The Expo app keeps the pair in secure storage and refreshes **single-flight** on a 401 | A phone waking from sleep fires several requests at once; independent refreshes would replay a retired token and the server would rightly end the session |
| Reuse detection | Whole *family* revoked (`SESSION_REUSED`) past a 20s grace window | Revoking only the replayed row would leave the thief with a live refresh token while the victim is quietly signed out |
| Sign-out | Revokes the family, by access token **or** by refresh cookie, and clears both cookies at their own paths | "Sign out this device" has to end the chain, or it is advice rather than a control. Clearing at the wrong path is a no-op, and an expired access cookie must not make sign-out a no-op too |
| Suspension | Revokes sessions in the same transaction; the refresh path re-checks account status | A chain must not outlive the account's standing |

Two things this does not do, and they are the honest gaps: there is no device-level second factor, and no
risk signal (new country, impossible travel) that would prompt for re-authentication. The rotation model
makes a *leaked token* detectable; it does nothing about a *guessed password* on a fresh device.

**The gap that closed here was not in the server.** Rotation, single-use tokens and reuse detection were
implemented and tested, and no client used them: the web kept a seven-day access cookie and the Expo app kept
only the access token, so `POST /v1/auth/refresh` was dead code in production and every session quietly lasted
a week. Both clients now rotate. `scripts/session-lifecycle.py` proves the sequence a learner actually
experiences — sign in, token expires, session renews, sign out, replay refused — against a real server and a
real cookie jar, and CI runs it both against the binary and through the web container.

Two defects surfaced only by running it, which is the argument for running it:

- **The middleware refreshed and the page still redirected.** `NextResponse.next({ request: { headers } })`
  copies the headers when it is constructed, so mutating them afterwards renewed the session for the *next*
  request and left the current one signed out — a sign-in loop that looks like a session bug.
- **Sign-out with an expired access cookie revoked nothing.** The handler looked only for the access token;
  with only the refresh cookie present it cleared a cookie and left the chain alive.

## Live classes: what the server can and cannot see

| Asset | Where it lives | Who can read it | Residual risk |
| --- | --- | --- | --- |
| Audio and video | Never reaches the API; peer-to-peer under DTLS-SRTP | The participants only | A peer with a compromised browser can record what it displays. That is true of every video call; the control is that the *server* never holds a copy and there is no recording feature to misfile |
| Whiteboard strokes | `virtual_class_board_events`, opaque ciphertext under a room key held by participants | Anyone seated in the class, who still needs the key | Key distribution is out of band (WebRTC data channel). A participant who shares the key shares the board; owner: API/web |
| SDP and ICE candidates | `virtual_class_signals`, pruned after 10 minutes | Members of that class, or the sender's target | They disclose participants' IP addresses to each other. Unavoidable for a direct connection without a relay; a TURN server hides them from peers but not from the relay operator |
| Who attended and for how long | `virtual_class_attendees`, `virtual_class_sessions` | The host, via the attendance report | Attendance is personal data; it is retained with the class, not indefinitely. Owner: API |
| Signup account type | `users.account_type` | The account itself and administrators | It is *intent*, not authority: an `institution` account holds no organisation until it is verified and approved |

Two rules are enforced rather than documented, and both have negative tests: **a learner cannot start, end, price
or read the attendance of somebody else's class** (`TestOnlyTheHostRunsTheRoom`), and **an outsider can neither
read nor inject into a room's signalling channel** (`TestSignallingRelayIsTargetedAndMemberOnly`). The board
endpoint refuses anything that is not base64 ciphertext, because a server that can store plaintext can also be
compelled to hand it over.

The remaining honest gap: there is **no TURN relay deployed**, so two peers behind symmetric NAT cannot connect,
and **no SFU**, so a room is 6–8 people. Neither is a security hole; both are limits that a client is told about
rather than left to discover.

### Referral commission across levels

Commission now walks the chain: one ledger row per level with its own rate and idempotency key, at most
`EDUMART_REFERRAL_RATES` deep. Three controls keep it from becoming a payout bug:

- **Idempotency per level, per order** — a retried provider callback cannot pay a level twice;
- **A cycle guard** — the walk refuses to revisit a user, so a data fix-up that closes a loop cannot pay around it
  forever (`TestReferralCyclePaysNothingTwice`);
- **A level-aware reversal** — a refund reverses every level proportionally, keyed per refund and level, so a
  half refund cannot leave the upper levels whole (`TestRefundReversesEveryLevel`).

The tree endpoint shows first names and initials only: a referral tree is progress reporting, not a contact
export.

## Backup, restore and the honest gaps

See [`BACKUP_AND_RECOVERY.md`](./BACKUP_AND_RECOVERY.md) for the full picture. In short: consistent
snapshots with per-backup checksums and row counts, two-window retention that cannot delete the newest
copy, an off-site mirror that is re-read, and point-in-time recovery proven by a drill that deletes the
cluster and replays WAL to a chosen second. Each backup is verified by restoring it into a scratch
database — a backup that has never been read back is a hypothesis.

**Restoration is demonstrated; scheduling is not.** `scripts/restore-drill.py` starts the API against an
empty database, fingerprints it (row count of every table plus a real login), snapshots it consistently
(`VACUUM INTO` on SQLite, `pg_dump --format=custom` on PostgreSQL), *destroys* the database and asserts
it is gone, restores, boots the API again, and compares fingerprints table by table. It runs for both
engines in the *Restore drill* CI job.

Two details make the drill worth having rather than decorative:

- It refuses to pass if the destruction step left anything behind, because "the data came back" is
  meaningless if the data never left.
- It compares rows per table, not a single total. A restore that drops one table and duplicates another
  keeps the total intact.

It found a genuine bug on its first run: `GET /v1/dashboard` used SQLite's `julianday()` and returned
`500` on PostgreSQL. That is the argument for drills over unit tests — every unit test passed, because no
fixture put an attempt inside the seven-day window the query needed.

Still missing before launch:

1. Managed PostgreSQL with point-in-time recovery; replication is not a backup.
2. RPO/RTO per data class — attempts and payments are the strictest, read models the loosest.
3. A schedule, off-site copies, retention and encryption at rest for those snapshots.
4. A drill into a *scratch environment* from production data, which is the only version that proves the
   real recovery path.
5. Independent export of the append-only ledgers.

## Data minimisation

Learners are the sensitive population here: many will be minors. The schema stores what assessment
requires — name, email, exam pathway, target date, timezone, study minutes, responses, points.
It does not store payment card data (the provider holds it), device identifiers, location or
biometrics. A retention and deletion policy still needs product and legal sign-off, and Nigerian
NDPA obligations (parental consent, data subject requests) must be reviewed before launch.
