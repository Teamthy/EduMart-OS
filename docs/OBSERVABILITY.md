# Observability and alerting

Phase 28's gap was not "we have no metrics". It was that every control built over the previous phases —
backups, reconciliation, webhook verification, rate limits — was only visible to someone who remembered to
look. A backup job failing for three weeks looks exactly like a backup job that works, and a reconciliation
discrepancy sits in a report nobody opens. This is the part that turns controls into a control system.

## What the API exposes

`GET /metrics`, Prometheus text format, no external dependency:

| Metric | Type | Answers |
| --- | --- | --- |
| `edumart_http_requests_total{method,route,status}` | counter | What is being called, and what it returned |
| `edumart_http_request_duration_seconds{route,method}` | histogram | How long it took, by route |
| `edumart_api_errors_total{code}` | counter | Which refusals and failures, by API error code |
| `edumart_webhook_events_total{outcome}` | counter | `accepted`, `duplicate`, `rejected`, `ignored` — four different problems |
| `edumart_refunds_total{currency}` / `edumart_refunded_minor_total{currency}` | counter | Money going back out |
| `edumart_reconciliation_discrepancies{kind}` | gauge | What the books said the last time anyone asked |
| `edumart_reconciliation_last_run_timestamp_seconds` | gauge | Whether anybody has asked recently |
| `edumart_backup_age_seconds`, `edumart_backup_rows`, `edumart_backup_last_run_succeeded` | gauge | Whether a usable backup exists |
| `edumart_login_failures_total` | counter | Credential stuffing, or a broken client |
| `edumart_process_uptime_seconds` | gauge | Restart detection |

Three rules hold this together, and all three are tested:

**Labels are chosen by the code, never by the caller.** The route label is chi's pattern — `/v1/orders/{id}`
— so a million orders produce one series. An unbounded label is how a monitoring system dies during the
incident it exists to describe.

**No identifier is ever a label.** There is no metric containing a user id, an email, an order id or a token.
`TestMetricsNeverCarryIdentifiers` asserts it, because metrics are read by a system that is not covered by
the API's access controls.

**Nothing is emitted that nothing watches, and nothing watched is unemitted.** Every metric in
`infra/observability/alerts.yml` is checked against a live scrape in CI.

## Access

`/metrics` is infrastructure, not product, and it is not public:

- **A token is configured** (`EDUMART_METRICS_TOKEN`, the production case): the scrape must send
  `Authorization: Bearer …`, compared in constant time.
- **No token** (local development): only a loopback caller may scrape, so `curl localhost:8080/metrics`
  works and the endpoint is not on the public internet by default.

Either way the refusal is a `404`, not a `403`: an unauthenticated prober should not learn that a metrics
endpoint exists at all.

```bash
# local
curl -s localhost:8080/metrics | head

# production
curl -s -H "Authorization: Bearer $EDUMART_METRICS_TOKEN" https://api.edumart.ng/metrics | head
```

## Wiring it up

`infra/observability/` holds a scrape configuration and the alert rules:

```yaml
scrape_configs:
  - job_name: edumart-api
    metrics_path: /metrics
    authorization: { credentials_file: /etc/edumart/metrics-token }
    static_configs: [{ targets: ["api-1:8080"] }]
```

The backup job writes a heartbeat the API reads at scrape time, so backup freshness is part of the same
scrape rather than a second system to keep alive:

```bash
EDUMART_BACKUP_HEARTBEAT=/var/lib/edumart/backup-heartbeat.json   # API
python3 scripts/backup.py … --heartbeat /var/lib/edumart/backup-heartbeat.json   # backup unit
python3 scripts/backup.py --verify … --heartbeat /var/lib/edumart/backup-heartbeat.json
```

`backup.py` writes the heartbeat with `verified: false` and flips it to `true` only after `--verify` has
restored the backup and compared row counts. A job that dumps and never verifies therefore alerts.

## Alerts, and why each threshold is the number it is

Fourteen rules in `infra/observability/alerts.yml`. `severity: page` means a person is woken;
`severity: ticket` means it is dealt with the next working day. `scripts/check-alerts.py` verifies that
every rule's runbook anchor exists in this document and that every metric it names is really exposed —
because the failure mode of an alerting setup is silence, not noise.

### Api down

`up{job="edumart-api"} == 0` for 2 minutes. Prometheus cannot reach the API at all. Check the process, then
`/healthz` and `/readyz`: a green healthz with a red readyz means the process is alive but cannot serve —
usually the database, and restarting it fixes nothing.

### Readiness failing

`/readyz` failing for 5 minutes means the API cannot reach the database or a migration is missing. A load
balancer is (correctly) refusing to send it traffic. Look at the `checks[]` array in the response: it names
the failing dependency rather than saying "unhealthy".

### Internal errors

More than 0.2 `INTERNAL_ERROR`/s for 5 minutes — roughly one failure every five seconds, sustained. Each
one is a request that should have worked. **Do not restart first**: the error rate is the evidence, and the
log line for each carries the same request id the user was shown. Find them with
`request_id` in the API log, then read the stack-free message.

### Refusal spike

401s and 403s at ten times their level an hour ago, for 10 minutes. The split tells you which: a wall of
`UNAUTHENTICATED` is credential stuffing (the per-address limiter caps one source, but a distributed attempt
stays under it by design — mitigation is a WAF, which is an open risk); a wall of `FORBIDDEN` after a deploy
is almost always a permission regression.

### Login failures

More than one failed sign-in per second for ten minutes. Same family as the refusal spike, narrower signal.
If it correlates with a single address, the limiter is already handling it; if it is spread across
addresses, it is an attack in progress.

### Webhook rejections

More than five rejected callbacks in fifteen minutes. Two causes and both stop money being recorded: the
shared secret was rotated on one side only, or someone is posting forged callbacks. Check
`edumart_webhook_events_total{outcome="rejected"}` against `paid_without_attempt` in the reconciliation
report — if real payments are landing without a webhook, orders will start appearing as unpaid.

### No webhooks

Zero webhook events for two hours during the day. **Silence is ambiguous**: it can mean nobody bought
anything. Compare with the order count before assuming the path is healthy — a provider outage, a wrong
callback URL and an empty afternoon all look identical here, and only one of them is fine.

### Reconciliation discrepancy

Any non-zero `edumart_reconciliation_discrepancies` is a page, because it is money. The kind tells you what
to do:

| Kind | Meaning | Action |
| --- | --- | --- |
| `paid_without_entitlement` | Money taken, nothing granted | Grant the entitlement or refund the order |
| `entitlement_without_payment` | Access without payment | Revoke, then find out how it was granted |
| `refund_exceeds_total` | More returned than charged | Freeze refunds for that order and reconcile by hand |
| `succeeded_attempt_unpaid` | The provider says it succeeded; the order is not paid | Replay the webhook, then check the amount |
| `paid_without_attempt` | Paid, but no attempt recorded the money | Investigate before touching the order |

The report itself is at `GET /v1/admin/finance/reconciliation` and names the orders and amounts.

### Reconciliation missing

The reconciliation gauges are only published when a report is produced, so a run that has never happened
creates no series at all — and every rule that compares a gauge to a number is then quietly satisfied. That
is the hole this rule plugs: absence is a signal and needs its own alert. Fires after 24 hours with no
report. Pull `GET /v1/admin/finance/reconciliation` once to publish the gauges, then schedule the run.

### Reconciliation stale

No report in 26 hours means the gauges are as old as the last run: a clean
`edumart_reconciliation_discrepancies` then means "nothing has looked", not "nothing is wrong". **There is
no scheduled reconciliation job yet** — this alert is what makes that visible rather than assumed.

### Backup missing

`absent(edumart_backup_age_seconds)` — no heartbeat at all. Either `EDUMART_BACKUP_HEARTBEAT` is unset, the
job has never completed, or the two are looking at different files. Until it is resolved there is **no
restore point**, and that is a page rather than a ticket.

### Backup stale

The newest verified backup is older than 36 hours: a nightly job plus half a day of slack for a late run or
a long restore. Anything tighter pages on a schedule rather than on a problem. Check the unit:
`systemctl status edumart-backup`, `journalctl -u edumart-backup -n 50`. See
[`BACKUP_AND_RECOVERY.md`](./BACKUP_AND_RECOVERY.md).

### Backup unverified

`edumart_backup_last_run_succeeded == 0`: a backup exists but nobody restored it. The verification step
restores into a scratch database and compares row counts. Without it, "we have a backup" is a belief — and
a backup that has never been read back is exactly the hypothesis this project has spent two phases trying
to stop relying on. Check that the schedule still runs `--verify`.

### Slow requests

p95 above one second for ten minutes. This API answers most reads in a few milliseconds, so this is a
database problem (a lock, a missing index, a table scan that grew), not "the server is a bit busy". Look at
`edumart_http_request_duration_seconds` by route to find which one.

## How this is proven

Claims about monitoring are cheap, so each one has something that fails when it stops being true:

| Claim | What proves it |
| --- | --- |
| Route labels are patterns, not ids | `TestMetricsRecordRequestsByRoutePattern` — three calls to one book and two different order ids produce one series each |
| No metric carries an identifier | `TestMetricsNeverCarryIdentifiers` — places an order, then asserts its id and the caller's email appear nowhere in the exposition |
| The scrape endpoint fails closed | `TestMetricsAreNotPublic` (loopback and token policy) plus a CI step that starts a token-guarded instance and asserts `404` without a token, `404` with the wrong one, `200` with the right one |
| Every alert rule can fire | `TestEveryMetricAnAlertRuleNamesExists` in `go test`, and `scripts/check-alerts.py --metrics` in CI against a live scrape |
| The checker catches a rename | `check-alerts.py --self-test`, run in CI *before* the real check — a gate that has only ever passed is not known to work |
| The backup heartbeat reaches the alert | the CI job takes a real backup, verifies it, and asserts `edumart_backup_age_seconds` and `edumart_backup_last_run_succeeded 1` appear; `backup-drill.py` claim 5 asserts the heartbeat says `verified: false` until `--verify` has restored the archive |

## Not built, and said plainly

- **No metrics storage in this repository.** Prometheus, Grafana and Alertmanager are deployment choices;
  `infra/observability/` provides the scrape config and rules, not the stack.
- **No alert delivery.** Alertmanager routing to email, Slack or PagerDuty is configuration in the target
  environment. An alert rule that fires and reaches nobody is a log line.
- **No tracing.** Correlation ids tie one request's log lines together; a request that crosses no service
  boundary needs no trace yet, and this will get one when it does.
- **No synthetic monitoring from outside the network.** `probe_success` and `EdumartReadinessFailing` assume
  a blackbox exporter pointed at `/readyz`; without one, the rule matches nothing and
  `check-alerts.py --metrics` will say so.
- **No scheduled reconciliation job.** The report is pulled, so its gauges are only as fresh as the last
  admin call. `EdumartReconciliationStale` exists to make that a visible fact rather than a silent one.
