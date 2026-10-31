# Local infrastructure

```bash
docker compose -f infra/docker/compose.yml up -d
docker compose -f infra/docker/compose.yml down          # keep data
docker compose -f infra/docker/compose.yml down -v       # drop volumes
```

| Service | Port | Used by the app today? |
| --- | --- | --- |
| `postgres` | 5432 | **Yes** — `EDUMART_DRIVER=postgres` runs the app against it; the suite and smoke journey run on it in CI |
| `redis` | 6379 | No — reserved for cross-instance rate limits and workers. Rate limiting is in-process. |

The API itself also builds as an image — `apps/api/Dockerfile`; `make image` builds it and
`docker run --rm -p 8080:8080 -e EDUMART_DRIVER=postgres -e EDUMART_DSN='postgres://…' edumart-api:local`
runs it. It is built and started against a real database in CI, not here.

PostgreSQL is the production engine and is fully supported:

```bash
docker compose -f infra/docker/compose.yml up -d postgres
EDUMART_DRIVER=postgres npm run dev:api   # uses EDUMART_DSN from .env.example
```

Redis is reserved for cross-instance rate limiting and background jobs; the API does not talk to it
yet, and rate limiting is still in-process. Nothing here is required for the default
`npm run dev:api`, which uses a SQLite file.
