# Security and data durability baseline

## CAP decisions

CAP applies to distributed data systems during a network partition. It is not a backup or recovery plan.

- Exam answer writes, final submission, payment state, entitlements, permissions, points and commissions: prefer authoritative consistency; if the source of truth cannot safely commit, do not pretend the mutation succeeded.
- Catalog browsing and rebuildable analytics: cached/eventually consistent reads are acceptable when clearly safe.
- Exam deadlines: server-authoritative clock and policy.

## Data-loss controls

- PostgreSQL constraints and transactions protect invariants.
- Idempotency keys protect retryable requests.
- Answer mutations carry client mutation IDs and expected versions.
- Acknowledge “saved” only after a durable server commit.
- Backups and point-in-time recovery must be enabled and independently tested.
- Replication is not a backup; deletion/corruption can replicate.
- Set RPO/RTO per data class and validate with restore/failover drills.
- Use append-only financial ledgers and compensating entries for corrections.
- Verify payment webhooks and deduplicate by provider event ID.
- Use an outbox for events that must follow a database commit.

## Security baseline

- TLS everywhere; encryption at rest for databases, backups and object storage.
- Least privilege, MFA for privileged users, protected production credentials.
- Server-side authorization and tenant-scope enforcement on every request.
- Rate limits, secure password hashing, session revocation and account-recovery controls.
- Secret scanning, dependency scanning, SAST, code review and protected branches.
- Audit privileged actions, exports, exam overrides, financial changes and access-policy changes.
- Minimize learner data; establish retention/deletion rules and legal review.
