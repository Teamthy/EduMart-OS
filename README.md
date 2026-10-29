# Edumart Monorepo Scaffold

Production-oriented starting point for Edumart: Go API, Next.js web/PWA, and React Native (Expo) mobile.

## Architecture

- `apps/web`: Next.js App Router, TypeScript, Tailwind CSS, shadcn/ui-ready.
- `apps/mobile`: Expo React Native app with shared API contract conventions.
- `services/api`: Go REST API, PostgreSQL, health/readiness endpoints, migration-ready layout.
- `packages/contracts`: API conventions and shared schema documentation. TypeScript types are not runtime-shared with Go; OpenAPI is the contract source of truth.
- `infra/docker`: local PostgreSQL and Redis services.
- `docs`: architecture, onboarding, security, CAP/data durability, release gates.

## Prerequisites

- Go 1.24+ (or current supported Go release approved by the team)
- Node.js 22 LTS and npm
- Docker Compose
- Expo-compatible development environment for mobile

## Start local dependencies

```bash
docker compose -f infra/docker/compose.yml up -d
```

## Start Go API

```bash
cd services/api
cp .env.example .env
 go mod tidy
go run ./cmd/api
```

API defaults to `http://localhost:8080`; health: `/healthz`, readiness: `/readyz`, versioned API: `/api/v1`.

## Start web app

```bash
cd apps/web
cp .env.example .env.local
npm install
npm run dev
```

Open `http://localhost:3000`. The five-step signup wizard is available at `/signup`. Tailwind CSS is configured; initialize/add shadcn/ui components with `npx shadcn@latest add button input card form progress checkbox select` as the component set is implemented. The initial service worker only caches same-origin static assets and intentionally excludes API/auth/signup routes; review cache policy before production.

## Start mobile app

```bash
cd apps/mobile
cp .env.example .env
npm install
npx expo start
```

Set `EXPO_PUBLIC_API_URL` to an address reachable from the device. On a physical phone, `localhost` refers to the phone, not your development machine.

## Engineering rules

1. PostgreSQL is the source of truth for critical writes.
2. Never report an exam answer as server-saved until the API acknowledges the durable write.
3. Use idempotency keys for retryable mutations and unique business-event IDs for financial events.
4. Never trust client-supplied role, score, deadline, payment status, or commission eligibility.
5. Add database migrations, request validation, auth middleware, structured logs, metrics, tracing, rate limits, and tests before production.
6. This is a scaffold, not a production-ready security-reviewed system. See `docs/production-readiness.md`.
