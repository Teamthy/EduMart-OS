# Local infrastructure

```bash
docker compose -f infra/docker/compose.yml up -d
docker compose -f infra/docker/compose.yml down          # keep data
docker compose -f infra/docker/compose.yml down -v       # drop volumes
```

| Service | Port | Used by the app today? |
| --- | --- | --- |
| `postgres` | 5432 | No — the API defaults to SQLite. The PostgreSQL schema is ported and the driver is not linked yet. |
| `redis` | 6379 | No — reserved for cross-instance rate limits and workers. Rate limiting is in-process. |

Both are here so the production-shaped path can be developed against something real, and so the
gap is visible rather than implied. Nothing in `docker compose up` is required for
`npm run dev:api` / `npm run dev` / `npm run dev:mobile`.
