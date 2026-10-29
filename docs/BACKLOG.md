# Backlog

Ordered by what would break a real launch first. Each item names the seam that already exists, so it
is a change, not a rewrite.

## P0 — before any real learner data exists

1. **PostgreSQL as the only production dialect.** The schema mirrors SQLite today. Move timestamps to
   `timestamptz`, money to `numeric(14,2)` or keep integer minor units with a `CHECK`, and add a real
   migration runner (the embedded `IF NOT EXISTS` bootstrap is not a migration strategy).
2. **Webhook provider integration.** `commerce.HandleWebhook` is provider-agnostic. Add a Paystack
   adapter (signature header + payload mapping), and reject events older than a replay window.
3. **Session hardening.** Session rotation on privilege change, absolute + idle expiry, a
   "signed-in devices" list in Settings, and rate limiting moved to Redis (`httpx.Limiter` is the seam).
4. **Backups and restore drills.** Daily PostgreSQL snapshots, PITR, and a documented restore test.
5. ~~**Observability.**~~ **Metrics and alert rules are done** — `GET /metrics` publishes request counts
   and latency by route pattern, error codes, webhook outcomes, refunds, reconciliation state, backup
   freshness and sign-in failures, with fourteen alert rules checked against the exposition in CI. What
   remains is not code: run Prometheus and route the `severity: page` rules to a person, and add tracing
   when a request first crosses a service boundary. See `docs/OBSERVABILITY.md`.

## P1 — launch completeness

6. **Teacher authoring UI.** The API accepts assessment definitions with items; there is no screen to
   compose a mock. This is the largest remaining product gap.
7. **Notifications.** Exam countdown and "your mock is due" reminders. `job_queue_items` exists;
   nothing drains it. Email provider + native push tokens are missing.
8. **Payments beyond the sandbox.** *Partly done.* Refunds and partial refunds now move money through a
   provider adapter with a pending/settled lifecycle, Paystack's signed callbacks are normalised, and the
   reconciliation report compares `provider_events` with `orders`. What is left: **run the adapter against
   the live API once** (it has only ever spoken to a fake server), take payments themselves through
   Paystack rather than the sandbox, and handle disputes and chargebacks.
9. **Content volume.** 50 seeded questions is a demo. The review queue, provenance and licence fields
   are ready; the editorial pipeline is not.
10. **Full offline mode.** The PWA caches the shell and the runner retries writes, but questions are
    deliberately not cached. A proper offline attempt (pre-fetched snapshot, signed deadline) needs a
    product decision about the clock.
11. **Accessibility audit.** WCAG 2.2 AA is designed in (focus rings, aria-live save states, 44px
    targets, reduced motion). It has not been verified with a screen reader on a real device.

## P2 — growth and polish

12. **Study plan generation.** `study_plans` stores intent; nothing derives a week-by-week plan from
    topic performance.
13. **Leaderboards and class analytics.** `topic_performance` and roster averages exist; the
    comparison views do not.
14. **Publisher portal.** Product/payout management for third parties.
15. **Localisation.** Copy is en-NG only; the design tokens and formatting helpers are centralised,
    so adding a second locale is contained.
16. **PDF/print result sheets** for parents and schools.

## Explicit non-goals for now

- Native web push for the PWA (browser support is uneven in the target market).
- A separate admin SPA: the staff surfaces live in the same Next.js app behind role checks.
- Multi-region deployments: one region, one primary database.
