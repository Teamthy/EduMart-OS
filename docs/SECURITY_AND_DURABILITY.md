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
  the missing driver rather than falling back to SQLite.

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

Not there yet — see `docs/PRODUCTION_READINESS.md` for the full list:

- MFA, device/session listing, idle timeout, and login anomaly detection.
- Email or phone verification: accounts are created and usable immediately. This is the largest
  remaining identity gap and it gates public launch.
- Secret management, key rotation, SAST/dependency scanning in CI.
- Audit trail for privileged actions: admin review and case resolution are recorded, but there is no
  general audit stream yet.

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
