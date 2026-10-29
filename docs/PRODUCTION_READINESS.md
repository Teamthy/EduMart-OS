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
| CI | Go format/vet/test/build, OpenAPI-vs-router contract check, web typecheck/test/build, mobile typecheck + Metro bundle for Android/iOS/web, HTTP smoke of the learner journey, restore drill on both engines |
| Security scanning | A `Security` job fails the build on a committed secret (`scripts/scan-secrets.py`, with a self-test asserting the detector), a reachable Go advisory (`govulncheck`) or a high-severity production npm advisory (`npm audit --omit=dev`). Zero production advisories at the time of writing |
| Threat model | `docs/THREAT_MODEL.md`: assets, adversaries, the control behind each mitigation, and eleven residual risks with severity, consequence and owner |

## Partially ready

| Area | Gap |
| --- | --- |
| Database | Both engines are tested: the suite, the HTTP smoke journey and the restore drill run on SQLite and on PostgreSQL 17 in CI. What is still untested is a *production-like* PostgreSQL: managed instance, TLS, connection limits and real data volume |
| Migrations | Versioned runner with checksums, proven on both engines. No rehearsal of a forward-fix on a database carrying production data |
| Driver | `lib/pq` rather than `pgx`, because pgx's module graph is unfetchable in the build environment (`gopkg.in`). Swapping is a one-file change |
| Sessions | Argon2id hashing, rotating single-use refresh tokens with reuse detection and family revocation, server-side revocation and a device list; no idle timeout and no device-level 2FA |
| Rate limiting | In-process only — resets on deploy and does not coordinate across instances (Redis is scaffolded in `infra/docker` but unused). Keyed on the socket address unless `EDUMART_TRUST_PROXY=true`, so a forwarded header cannot buy extra attempts |
| Payments | Only the sandbox provider is wired. Refunds and reconciliation exist and are auditable, but a refund **records** money rather than moving it — there is no provider refund adapter |
| Offline | Shell cached; attempts require connectivity by design |
| Accessibility | Designed to WCAG 2.2 AA; not verified with assistive technology on real devices |
| Mobile builds | Bundles verified for all three platforms; signed `.apk`/`.aab`/`.ipa` still come from EAS (no SDK or signing identity here) |

## Not ready

| Area | Why it matters |
| --- | --- |
| Migration strategy | **Versioned runner exists** (`schema_migrations` + checksums, applied once, refuses edited-after-apply files). Migrations now run on SQLite and PostgreSQL 17 in CI. What is still missing is a forward-fix/rollback rehearsal against a production-shaped database |
| Observability | Structured logs and request ids only: no metrics, tracing, error aggregation or alerting. `GET /readyz` gives a load balancer something real to probe |
| Backups | A restore **is** demonstrated, and so is the operational half: `scripts/backup.py` takes consistent snapshots, verifies each one by restoring it into a scratch database, applies two-window retention, and mirrors off-site with a checksum re-read. `scripts/pitr-drill.py` proves recovery to a chosen second by deleting the cluster and replaying archived WAL to a target time. What is still missing is encryption at rest by this tooling, cross-region replication, and a drill against production-sized data |
| Notification delivery | Email is real: a durable outbox with exponential backoff, SMTP or log adapters, per-message delivery records, and scheduled reminders that are planned once, claimed before sending and withdrawn when their reason disappears. No SMS or push channel, no unsubscribe or quiet hours |
| Live payment provider | No adapter, no key management, no webhook replay window |
| Content operations | 50 demo questions; no editorial pipeline or licensing workflow in practice |
| Load testing | No measurement of concurrent attempts against a realistic PostgreSQL instance |
| Security operations | No monitoring or alerting (audit rows are written and nothing watches them), no WAF or bot protection, no incident-response runbook, no dependency-update automation |
| Staff 2FA | No device-level second factor, so one phished finance password is refund and reconciliation authority |
| Backup alerting | The schedule, retention, off-site mirror and PITR are implemented and drilled (see `docs/BACKUP_AND_RECOVERY.md`), but **nothing alerts when a backup fails**: the systemd unit failing is only visible if someone reads it |

## Pre-launch checklist

1. Move the production database to PostgreSQL; run the migrations; run `go test` against it.
2. Install the backup schedule (`infra/backup/`), enable WAL archiving per `docs/BACKUP_AND_RECOVERY.md`, run `scripts/backup.py --verify` by hand once, and then run `scripts/pitr-drill.py` on the production host before trusting any of it. Put an alert on the unit's failure status.
3. Add the live payment adapter; test a real webhook in staging, including a replayed event.
4. Move rate limiting to Redis; set `EDUMART_ALLOW_SANDBOX_PAYMENTS=false`.
5. Add metrics/tracing and an on-call alert on `INTERNAL_ERROR` rate and webhook failures.
6. Load test: 500 concurrent attempts, 50 answer writes/second.
7. Screen-reader pass on signup, attempt and results; fix whatever it finds.
8. Rotate the demo users out (`EDUMART_SEED_DEMO_USERS=false`) and delete the seeded accounts.
9. Run the migration on a copy of production data before the real one.
10. Rehearse the runbook: what happens when the API is down mid-mock (attempts continue to save
    nothing; the deadline still closes them) and what the learner is told.
