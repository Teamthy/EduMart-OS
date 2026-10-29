# Production readiness

An honest assessment of this repository. "Ready" means it has been exercised, not merely designed.

## Ready

| Area | Evidence |
| --- | --- |
| Server-authoritative assessment | Deadlines, snapshots, versioned writes and finalisation are covered by Go tests, including the expiry path and duplicate-submit case |
| Deterministic scoring | Pure function, policy version recorded per attempt; property-style repetition test |
| Answer-key secrecy | The attempt projection omits keys until the release policy allows; asserted in Go and in the React test |
| Idempotent writes | Replay returns the existing version; `409` with `current_version` on stale writes |
| Payment integrity | HMAC verification, amount/currency checks, `provider_events` uniqueness, entitlement granted only by webhook |
| Separate ledgers | Points and commission in distinct append-only tables; balances derived |
| Institution tenancy | Organisation creation, approval, membership and invitations are enforced and tested, including cross-tenant IDOR cases |
| Contact verification | Email and phone codes are HMAC-stored, single-use, attempt-limited and throttled; purchase requires a verified address; verified against a live PostgreSQL server |
| Dual database engines | The full suite and the HTTP smoke journey run on SQLite and PostgreSQL 17 in CI |
| Tenant/role authorisation | Role checks live in handlers; teacher access is class-scoped; covered by tests |
| Money handling | Integer minor units end to end; formatting tested |
| Error contract | One envelope, request ids on every response, mapped statuses |
| Seedable, reproducible environments | Idempotent deterministic seed; a fresh DB boots to a working product |
| CI | Go format/vet/test/build, OpenAPI-vs-router contract check, web typecheck/test/build, mobile typecheck + Metro bundle for Android/iOS/web, HTTP smoke of the learner journey |

## Partially ready

| Area | Gap |
| --- | --- |
| Database | Both engines are tested: the suite, the HTTP smoke journey and the restore drill run on SQLite and on PostgreSQL 17 in CI. What is still untested is a *production-like* PostgreSQL: managed instance, TLS, connection limits and real data volume |
| Migrations | Versioned runner with checksums, proven on both engines. No rehearsal of a forward-fix on a database carrying production data |
| Driver | `lib/pq` rather than `pgx`, because pgx's module graph is unfetchable in the build environment (`gopkg.in`). Swapping is a one-file change |
| Sessions | Opaque tokens with revocation; no rotation, no device list, no idle timeout |
| Rate limiting | In-process only — resets on deploy and does not coordinate across instances (Redis is scaffolded in `infra/docker` but unused) |
| Payments | Only the sandbox provider is wired; no live adapter, refunds or reconciliation |
| Offline | Shell cached; attempts require connectivity by design |
| Accessibility | Designed to WCAG 2.2 AA; not verified with assistive technology on real devices |
| Mobile builds | Bundles verified for all three platforms; signed `.apk`/`.aab`/`.ipa` still come from EAS (no SDK or signing identity here) |

## Not ready

| Area | Why it matters |
| --- | --- |
| Migration strategy | **Versioned runner exists** (`schema_migrations` + checksums, applied once, refuses edited-after-apply files). Migrations now run on SQLite and PostgreSQL 17 in CI. What is still missing is a forward-fix/rollback rehearsal against a production-shaped database |
| Observability | Structured logs and request ids only: no metrics, tracing, error aggregation or alerting. `GET /readyz` gives a load balancer something real to probe |
| Backups | A restore **is** demonstrated: `scripts/restore-drill.py` (CI job *Restore drill*) destroys both databases and recovers them, verifying every table's row count and a login. What is missing is the operational half — a schedule, off-site copies, retention, encryption at rest, and a measured recovery objective |
| Notification delivery | Email is real: a durable outbox with exponential backoff, SMTP or log adapters, and per-message delivery records in the admin console. Codes are written atomically with their message and withdrawn if the send fails. No SMS or push channel, and no scheduled reminders |
| Live payment provider | No adapter, no key management, no webhook replay window |
| Content operations | 50 demo questions; no editorial pipeline or licensing workflow in practice |
| Load testing | No measurement of concurrent attempts against a realistic PostgreSQL instance |

## Pre-launch checklist

1. Move the production database to PostgreSQL; run the migrations; run `go test` against it.
2. Schedule backups in the target environment (nightly base backup plus WAL for PITR), copy them off-site, and run `scripts/restore-drill.py` against a restored copy of production data before trusting the schedule.
3. Add the live payment adapter; test a real webhook in staging, including a replayed event.
4. Move rate limiting to Redis; set `EDUMART_ALLOW_SANDBOX_PAYMENTS=false`.
5. Add metrics/tracing and an on-call alert on `INTERNAL_ERROR` rate and webhook failures.
6. Load test: 500 concurrent attempts, 50 answer writes/second.
7. Screen-reader pass on signup, attempt and results; fix whatever it finds.
8. Rotate the demo users out (`EDUMART_SEED_DEMO_USERS=false`) and delete the seeded accounts.
9. Run the migration on a copy of production data before the real one.
10. Rehearse the runbook: what happens when the API is down mid-mock (attempts continue to save
    nothing; the deadline still closes them) and what the learner is told.
