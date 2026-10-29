# Production readiness checklist

- [ ] Threat model and data classification approved.
- [ ] API auth, authorization, tenant isolation and rate limiting tested.
- [ ] Signup verification and account recovery abuse-tested.
- [ ] Exam answer writes are idempotent and server-acknowledged.
- [ ] Submission and scoring are reproducible using immutable snapshots.
- [ ] Payment provider events are verified, deduplicated and reconciled (if payments enabled).
- [ ] Migrations are backward-compatible and have rollback/forward-fix plans.
- [ ] CI runs formatting, lint, unit/integration tests, dependency and secret scans.
- [ ] Production secrets are in a secrets manager, not environment files in source control.
- [ ] Backups/PITR are configured; isolated restore drill has passed.
- [ ] RPO/RTO are documented and tested.
- [ ] Logs, metrics, traces, alerts and dashboards are operational.
- [ ] Load tests cover concurrent exam starts, autosave bursts and deadline spikes.
- [ ] Privacy notices, terms, content rights and relevant Nigerian legal obligations reviewed.
- [ ] Incident response, restore, payment reconciliation and support runbooks exercised.
