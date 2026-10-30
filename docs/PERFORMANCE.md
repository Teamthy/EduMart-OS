# Performance and correctness under load

Phase 29's exit gate is "measured, not assumed". The readiness document had been saying the honest thing for
a while: *no measurement of concurrent attempts against a realistic PostgreSQL instance*. This is that
measurement, plus the half that matters more — proof the invariants survived it.

```bash
# measure and verify, on both engines
python3 scripts/loadtest.py both --dsn 'postgres://…' --concurrency 96 --duration 45s

# the capacity run, with a budget that fails the command
python3 scripts/loadtest.py postgres --dsn 'postgres://…' --concurrency 300 --duration 40s \
    --max-p95 250ms --max-error-rate 0.01
```

## The workload

`apps/api/cmd/loadtest` drives the **whole attempt lifecycle**, not one endpoint, because a benchmark of
`GET /products` measures the cheapest thing in the system and says nothing about the write path where the
server owns deadlines, optimistic versions and duplicate-submission protection. Each journey is:

| Step | Endpoint | Why it is in the mix |
| --- | --- | --- |
| Browse | `GET /v1/products`, `GET /v1/dashboard` | Pure reads; a cohort mostly reads |
| Start | `POST /v1/practice-sessions` | Assembles a snapshot, writes a session, allocates the deadline |
| Answer | `PUT …/responses/{qvId}` ×3 | The hot write: optimistic version check, upsert, sequence |
| Submit | `POST …/submit` | Scoring, entitlement of results, the ledger write |
| Read back | `GET …/assessment-sessions/{id}` | Reads the state it just produced |

Reads are 30% of journeys by default. Virtual users hold keep-alive connections, and each sends its own
`X-Forwarded-For` so the per-client rate limits do not collapse the cohort into one budget.

### On pacing

The API rate-limits assessment traffic to **120 requests a minute per client address**, and a journey is
seven of those requests. A virtual user therefore cannot exceed one journey every 3.5 seconds without being
throttled, so that is the default think time. This is worth stating plainly: **throughput in these numbers is
bounded by the rate limiter, not by the database.** Pushing harder would measure the limiter and then
mislabel its 429s as server errors. When throttling does happen the tool reports the percentage separately
and says the run cannot describe capacity — silently averaging refusals into a latency figure is how a load
test lies.

## Measured numbers

Recorded on the CI-class sandbox used for this work: **2 vCPU**, PostgreSQL 16.2 and SQLite on the same
machine as the load generator, API and database local (no network). Read them as *relative* evidence — "this
is what the design costs, and where the ceiling is" — not as a production capacity plan, which needs the real
hardware, the real database size and the real network.

| Engine | Users | Requests | Throughput | p50 | p95 | p99 | Errors | Invariants |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| SQLite | 32 | 1,242 | 31 req/s | 0.9 ms | 2.2 ms | 3.2 ms | 0% | held |
| PostgreSQL | 96 | 3,956 | 88 req/s | 3.1 ms | 6.6 ms | 8.7 ms | 0% | held |
| PostgreSQL | 300 | 10,550 | 264 req/s | 3.4 ms | 8.5 ms | 16.1 ms | 0% | held |

Two things stand out, and both are the design working rather than a surprise:

- **Latency barely moves between 96 and 300 users** (p95 6.6 ms → 8.5 ms) while throughput triples. The box
  is not saturated at 300 concurrent learners: the per-request work is small, dominated by short indexed
  queries, and the two CPUs have headroom. The honest reading is *we did not find the ceiling*, not *the
  system scales indefinitely*.
- **SQLite is the slower engine and correctly so.** It runs on exactly one connection by design, so writes
  serialise. It is the local and CI engine; PostgreSQL is the system of record.

The most expensive endpoint is `submit` (p95 12.8 ms at 300 users), which is what it should be: it scores the
attempt and writes the ledger.

## What the load found, and what was fixed

**1. The connection pool was unbounded.** Go's default for `sql.DB` is *unlimited* open connections, and
nothing overrode it for PostgreSQL. Under load the process would open one per in-flight query until the
server refused with `too many clients` — a failure that arrives as a wave of 500s at the exact moment the
system is under the most pressure, and that gets worse with every extra API replica sharing the same
`max_connections`. Now bounded by `EDUMART_DB_MAX_OPEN_CONNS` (default 20) and `EDUMART_DB_MAX_IDLE_CONNS`
(default half that), refusing silly values rather than obeying them: `0` means "unlimited" to `database/sql`,
which is the footgun the bound exists to remove. Measured cost of the bound: none (264 vs 263 req/s, p95 8.5
vs 8.2 ms).

**2. A seeded fixture violated the rule the engine enforces.** The demo data shipped a practice attempt whose
`submitted_at` was *ten minutes after* its `deadline_at`. The server never produces that state — it refuses a
late submission — so the seed was quietly teaching anyone who read it that Phase 14's deadline rule does not
hold. The integrity check caught it on the first run.

## The half that matters: invariants after the load

A load test that reports latency proves the server stayed up. These are checked in SQL against the database
the load just wrote to, which is the only place a concurrency bug shows up:

| Check | What breaking looks like |
| --- | --- |
| `sessions_scored_once` | A submitted attempt with no score — a lost write at the worst moment |
| `responses_belong_to_a_session` | An orphaned answer row |
| `no_duplicate_responses` | Two answer rows for one question in one attempt |
| `deadline_respected` | The server accepting a submission after the deadline *it* set |
| `points_ledger_traces_to_something` | A points entry with no owner |
| `no_duplicate_ledger_keys` | The ledger's idempotency keys colliding under concurrency |
| `reconciliation_clean` | Any of the six finance contradictions, including a refund that exceeds its charge |

`submitted_attempts_exist` guards against the vacuous pass: a load test over an empty database satisfies
every check and measures nothing.

All of these held at 96 and 300 concurrent users on PostgreSQL, with 1,812 attempts submitted during the run
and reconciliation reporting zero contradictions across its six checks.

## In CI

The `Load and invariants` job runs both engines for a short window with a **loose** budget (p95 400 ms, error
rate 0.5%). Loose on purpose: a tight p95 on a shared runner measures the runner, and a flaky gate gets
switched off, which is worse than no gate. What it catches is the real regression — an order of magnitude of
latency, any 5xx under concurrency, or a broken invariant.

## Not measured, and said plainly

- **The ceiling.** 300 concurrent learners did not saturate a 2-vCPU box. Where it breaks, and what breaks
  first, is unknown.
- **A production-sized database.** Every run starts from an empty schema with 50 seeded questions. Index
  behaviour and query plans at 10⁵–10⁶ rows are unmeasured, and a plan that is fine at 50 rows can be a
  sequential scan at a million.
- **Network, TLS and the proxy hop.** Everything here is loopback.
- **The web app under load.** This measures the API. Nothing has driven the Next.js app concurrently.
- **Soak behaviour.** The longest run is 45 seconds. Slow leaks, connection drift and index bloat need hours.
- **Accessibility and the device matrix.** Still not started: nothing has been checked with a screen reader
  or on a low-end Android device, and those are Phase 29's other two exit criteria.
