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
| Accessibility | **Contrast and structure are measured** on every build: seven shipped contrast failures were found and fixed (the worst at 1.55:1) and axe caught two landmark/role defects. **Not verified with assistive technology**: no screen-reader pass, no keyboard-only pass, no zoom/reflow check, no testing with AT users. The device matrix is not started — see `docs/ACCESSIBILITY.md` |
| Mobile builds | Bundles verified for all three platforms; signed `.apk`/`.aab`/`.ipa` still come from EAS (no SDK or signing identity here) |

## Not ready

| Area | Why it matters |
| --- | --- |
| Fleet startup | **Six replicas start together in CI** against one database; migrations are serialised by an advisory lock and recorded exactly once. Before this, three of four replicas exited on a fresh database. No orchestrator yet — the check starts processes, not containers |
| Rolling rollout | **Replaced, aborted and recovered under live traffic in CI**: a four-replica fleet behind a load balancer swaps one replica at a time, then the rollout is aborted half way and every replaced replica is put back on the old binary — with a client requesting throughout and **zero failed requests**. Two deliberately broken rollouts prove the gate still fails. Not proven: a real orchestrator, ingress health checks, DNS, TLS |
| Container images | **Both** (`apps/api/Dockerfile`, `apps/web/Dockerfile`): non-root, read-only root filesystem assumptions, CA bundle, healthchecks, version stamped at build time. CI builds both and runs them as one stack against PostgreSQL — page renders, `/v1` proxied, session cookie survives. Missing: a registry, image signing, an SBOM, image scanning |
| Deployment manifests | `infra/k8s` describes the whole deployment: restricted Pod Security, migration Job, rolling Deployments with zero-unavailable strategy, probes that distinguish readiness from liveness, PDBs, TLS Ingress, default-deny network policy. Ten checks in `scripts/check-manifests.py` (fourteen injected faults prove they fail) plus a `kubectl` dry run in CI. **Never applied to a real cluster** |
| Migration strategy | **Versioned runner** (`schema_migrations` + checksums, applied once, refuses edited-after-apply files) and **a rollback rehearsed on every pull request**: two real releases, migrate-then-serve, deploy forward, roll back, and the old code reads the new code's data. Destructive DDL is refused unless declared. Still missing: any of this against a production-shaped database or a hosting target — see `docs/DEPLOYMENT.md` |
| Observability | Structured logs, request ids, and now a **Prometheus `/metrics` endpoint with fourteen alert rules** over it (errors, refusals, webhooks, refunds, reconciliation, backup freshness, latency), plus `GET /readyz` for load balancers. What is missing is the surrounding stack — Prometheus, Grafana and Alertmanager are deployment choices, and **nothing routes an alert to a person**, so today an alert that fires is a rule that fires |
| Backups | A restore **is** demonstrated, and so is the operational half: `scripts/backup.py` takes consistent snapshots, verifies each one by restoring it into a scratch database, applies two-window retention, and mirrors off-site with a checksum re-read. `scripts/pitr-drill.py` proves recovery to a chosen second by deleting the cluster and replaying archived WAL to a target time. What is still missing is encryption at rest by this tooling, cross-region replication, and a drill against production-sized data |
| Notification delivery | Email is real: a durable outbox with exponential backoff, SMTP or log adapters, per-message delivery records, and scheduled reminders that are planned once, claimed before sending and withdrawn when their reason disappears. No SMS or push channel, no unsubscribe or quiet hours |
| Live payment provider | **Refunds have an adapter** (`commerce.PaystackRefunds`: bounded HTTP client, constant-time signature check on Paystack's callback envelope, and a refusal-versus-silence distinction that stops an ambiguous timeout from being retried into a double refund). What is missing: taking *payments* through the provider rather than the sandbox, key rotation, and a webhook replay window. The adapter has never been run against the live API from this repository |
| Content operations | 50 demo questions; no editorial pipeline or licensing workflow in practice |
| Load testing | **Measured.** `scripts/loadtest.py` drives the whole attempt lifecycle and reports percentiles: 300 concurrent learners on PostgreSQL gave 264 req/s at p95 8.5 ms with zero errors, and seven integrity invariants (scored-once, no orphaned responses, deadline respected, ledger keys unique, reconciliation clean) held afterwards. The ceiling was **not** reached on a 2-vCPU box, the data is 50 seeded questions rather than production-sized, and nothing has been soaked beyond 45 seconds — see `docs/PERFORMANCE.md` |
| Security operations | Sign-in failures, webhook rejections and API error codes are now measurable and alertable, but audit rows (refunds, suspensions) are not, so an insider refunding repeatedly is still visible only to someone reading the table. No WAF or bot protection, no incident-response runbook, no dependency-update automation. No 2FA |
| Staff 2FA | No device-level second factor, so one phished finance password is refund and reconciliation authority |
| Backup alerting | **Closed in code**: the backup job writes a heartbeat, `/metrics` publishes backup age and verification status, and three alert rules fire on missing, stale or unverified backups (see `docs/OBSERVABILITY.md`). Delivery is still configuration: wiring Alertmanager to a phone is not something this repository can do |

## Pre-launch checklist

1. Move the production database to PostgreSQL; run the migrations; run `go test` against it.
2. Install the backup schedule (`infra/backup/`), enable WAL archiving per `docs/BACKUP_AND_RECOVERY.md`, run `scripts/backup.py --verify` by hand once, and then run `scripts/pitr-drill.py` on the production host before trusting any of it. Point `EDUMART_BACKUP_HEARTBEAT` at the heartbeat file and confirm `edumart_backup_age_seconds` appears on `/metrics`.
3. Add the live payment adapter; test a real webhook in staging, including a replayed event.
4. Move rate limiting to Redis; set `EDUMART_ALLOW_SANDBOX_PAYMENTS=false`.
5. Deploy Prometheus with `infra/observability/prometheus.yml`, and route the `severity: page` rules to a phone. Add tracing when a request first crosses a service boundary; today it crosses none.
6. Load test: 500 concurrent attempts, 50 answer writes/second.
7. Screen-reader pass on signup, attempt and results; fix whatever it finds.
8. Rotate the demo users out (`EDUMART_SEED_DEMO_USERS=false`) and delete the seeded accounts.
9. Run the migration on a copy of production data before the real one.
10. Rehearse the runbook: what happens when the API is down mid-mock (attempts continue to save
    nothing; the deadline still closes them) and what the learner is told.
