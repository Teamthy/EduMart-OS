# API contract

`openapi.yaml` is the contract the Go API implements and both clients consume. It is not
documentation written after the fact: `scripts/check-contract.py` compares it with
`apps/api/internal/api/router.go` on every CI run, in both directions.

```bash
python3 scripts/check-contract.py     # or: npm run contract
```

- **Undocumented route** — a handler exists that the spec does not describe.
- **Contract only** — the spec promises an endpoint that no handler serves.

Rules worth stating outside the YAML:

- One error envelope: `{ "error": { code, message, request_id, details } }`.
- Money is integer minor units plus a currency code.
- Deadlines come from the server; `remaining_seconds` is authoritative and a device clock is not.
- Answer keys are absent (not null) until `result_released`.
- Answer writes carry `expectedVersion`; a replay is idempotent and a stale write is `409` with
  `details.current_version`.
- Payment state changes only through a signed provider webhook.
- Identity on authenticated routes comes from the session, never from the request body.

Editing the spec is part of changing the API. If a route changes, the contract changes in the same
commit — the check will fail otherwise.
