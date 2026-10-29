# Architecture baseline

## Deployment units

- Web/PWA: Next.js deployed to a platform supporting the chosen Next.js runtime and security headers.
- Mobile: Expo/EAS build pipeline for iOS and Android.
- API: stateless Go service behind TLS-terminating load balancer/reverse proxy.
- Data: managed PostgreSQL as system of record; private object storage for uploaded files; Redis only for cache/rate-limit/ephemeral coordination where appropriate.
- Async work: transactional outbox plus a durable worker/queue when notifications, scoring jobs, imports, or reconciliation require asynchronous execution.

## Suggested Go module boundaries

`identity`, `authorization`, `catalog`, `content`, `assessment`, `results`, `classroom`, `commerce`, `rewards`, `notifications`, `audit`, `platform`.

Begin as a modular monolith. Extract a service only when independent deployment, scaling, fault isolation, or team ownership has a measured benefit.

## Shared API contract

- REST under `/api/v1`.
- OpenAPI is the canonical contract; generate clients where possible.
- Standard error shape: `code`, `message`, optional `field_errors`, `request_id`.
- Paginated collections use a documented cursor format.
- Dates are ISO-8601 UTC timestamps; exam policy stores explicit timezone semantics.
- Mutating endpoints accept `Idempotency-Key` where retries could duplicate effects.
