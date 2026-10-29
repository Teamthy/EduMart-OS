# Threat model

Phase 28 asks for the platform to be validated as one security and reliability system. This document is
that assessment: what we are protecting, who would take it, which controls actually exist in this
repository, and — the part that makes a threat model worth writing — **what is still open, and who owns
it**.

It is deliberately not a compliance artefact. Every mitigation below points at a file, an endpoint or a
test that can be read; every residual risk is stated in plain language with a name against it. The
blueprint's exit gate ("critical vulnerabilities closed, residual risks have named owners") cannot be met
by a document that says "we follow best practice".

**Last reviewed:** the refund-provider slice, with refunds moving money through an adapter and a lifecycle.

---

## 1. What we are protecting

| Asset | Why it matters | Where it lives |
| --- | --- | --- |
| Learner accounts and sessions | Many users are minors; account takeover exposes study history, contact details and purchases | `users`, `sessions`, `refresh_tokens` |
| Assessment integrity | A leaked answer key or a forged submission makes every score worthless and the product unsellable | `assessment_sessions.snapshot_json`, `question_versions` |
| Money | Refunds and commission are real money movements, and a wrong ledger is unauditable | `orders`, `refunds`, `commission_ledger_entries` |
| Institution data | Teacher rosters, class codes and reports identify children by name and school | `organizations`, `class_memberships` |
| Outbound mail | A stolen SMTP credential or a spoofed sender is a phishing engine aimed at our own users | `notification_deliveries`, `internal/notify` |
| The service itself | Exam season is a deadline; an outage during a mock exam is a trust event, not an inconvenience | whole system |

**Not in scope, and stated so nobody assumes otherwise:** card data (the provider holds it; we store
none), national identity data, health data, and the content of a learner's free-text notes if any are
added later.

---

## 2. Who we are defending against

| Adversary | Capability | Most attractive target |
| --- | --- | --- |
| Opportunistic scraper | Unauthenticated HTTP, rotating proxies, commodity scanners | Signup drafts, referral-code lookup, login endpoint |
| Credential-stuffing operator | Large lists of leaked email/password pairs, many IPs | `/v1/auth/login` |
| A learner who wants a better score | Own account, browser devtools, network tab | Answer keys before release, submission timing, correction endpoints |
| A learner who wants free content | Own account | Entitlement checks, `simulate-payment` |
| A teacher or institution admin who is curious | A real but narrow role | Other institutions, other rosters, platform-wide admin routes |
| A support or finance insider | An authenticated staff session | Refunds, moderation, delivery bodies |
| A payment provider callback forger | Can reach the public internet | `/v1/webhooks/payments/{provider}` |
| Someone with repository access | Read access to source and CI | Committed secrets, CI workflow injection |

Insider risk is included because the highest-value actions here (a refund, an account suspension, an
institution approval) are all legitimate staff operations, not bugs.

---

## 3. Controls that exist, and where to check them

### 3.1 Authentication and sessions

| Control | Evidence | Notes |
| --- | --- | --- |
| Argon2id password hashing (m=19456 KiB, t=2, p=1) | `apps/api/internal/auth/password.go` | Legacy PBKDF2 verifies and is upgraded on next sign-in |
| Single-use refresh tokens with reuse detection | `apps/api/internal/auth/refresh.go`, `router_test.go` | Reuse revokes the whole family and returns `401 SESSION_REUSED` |
| Server-side session revocation | `sessions`, `/v1/me/sessions`, `/v1/admin/users/{id}/suspend` | Suspension ends sessions the account already holds |
| Generic failure messages | `TestLoginFailuresDoNotRevealWhichPartWasWrong` | Wrong password and unknown address are indistinguishable |
| Login budget per address **and** address+email | `auth/handlers.go`, `TestAForwardingHeaderCannotBuyMorePasswordAttempts` | Keyed on the socket address unless an operator declares a proxy |
| Verification codes hashed, single-use, expiring | `auth/verification.go`, migration `0004` | The code itself is never stored |

**The interesting fix in this slice:** `X-Forwarded-For` used to be believed unconditionally. That meant
every rate limit in the system was keyed on a value the caller chose — rotating one header gave unlimited
password attempts against a known account. Forwarding headers are now honoured only when
`EDUMART_TRUST_PROXY=true` says a proxy is genuinely in front of the process, and the regression test
proves the difference: with the switch off, 12 attempts from 12 claimed addresses are still limited; with
it on, two real addresses get two honest budgets.

### 3.2 Authorization and tenancy

| Control | Evidence |
| --- | --- |
| Roles are granted, never self-selected | `TestSelfSelectedRoleGrantsNothing`, `signup` service |
| Organisation authority comes from membership | `orgs` handlers; a non-member gets `404`, not `403`, so the API is not an existence oracle |
| Institution classes belong to the institution | `/v1/organizations/{id}/classes`, `TestOrganizationClassesAreInstitutionScoped` |
| Class codes are unguessable, expiring and revocable | migration `0006`, `classroom.RotateJoinCode` |
| IDOR coverage on orders, sessions, answers, orgs | `router_test.go` negative cases, smoke run |
| Money and moderation are role-separated | refund/reconciliation = finance or platform admin; refund *history* also allows support |

### 3.3 Assessment integrity

- The server owns the clock, the deadline and the scoring policy; the client is a view.
- Answer keys are withheld until results are released (`TestAnswerKeyIsHiddenBeforeRelease`).
- Attempts snapshot their questions, options and scoring version, so a content edit cannot retroactively
  change a past score.
- Answer writes are idempotent and versioned; a stale write is refused with the current version rather
  than silently overwriting another device's answer.

### 3.4 Money

Payment truth is a verified webhook, never a browser redirect. Webhooks are HMAC-verified over the raw
body, deduplicated on `provider`+`event_id`, and checked for amount and currency before anything is
granted. Refunds are append-only compensating entries; the charge is never rewritten, over-refund is
impossible, and every refund is audited with an actor and a mandatory reason — plus an adapter that
distinguishes a provider's refusal from its silence, so an ambiguous timeout is never retried into a second
refund. Reconciliation reports six classes of contradiction rather than a revenue total. See
`SECURITY_AND_DURABILITY.md` and `ARCHITECTURE.md`.

### 3.5 Outbound messages

Deliveries are recorded before the first attempt and retried on a backoff. Bodies are cleared once
delivered and an abandoned delivery deletes the credential it carried — so the queue is not a store of
live reset tokens. `GET /v1/admin/deliveries` returns status, attempts and the credential reference, never
a body. Templates are code, not database rows, because a runtime-editable body is a phishing vector aimed
at our own users.

### 3.6 API surface hardening

| Control | Where |
| --- | --- |
| `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store` on every response | `httpx.SecurityHeaders`, asserted by `TestEveryResponseCarriesTheHardeningHeaders` |
| Request body capped at 1 MiB | `httpx.Decode` |
| One error envelope; no stack traces or driver errors reach a client | `httpx.Wrap`, `Recover` |
| Panics become a `500` with a request id, logged with the same id | `httpx.Recover` |
| Correlation id on every response | `httpx.RequestIDMiddleware` |
| Rate limits on auth, assessment, institutions, signup drafts, referral lookup and webhooks | `router.go`, `TestPublicEndpointsHaveABudget` |
| CORS locked to configured origins with credentials | `router.go`, `EDUMART_ALLOWED_ORIGINS` |
| Security headers on the web shell, framing blocked outside development | `apps/web/next.config.ts` |

### 3.7 Supply chain and secrets

| Control | Evidence |
| --- | --- |
| Reachable Go vulnerabilities | `govulncheck ./...` in the **Security** CI job, summarised as annotations by `scripts/summarise-vulns.py` |
| A current Go toolchain | `go 1.27.3` in `apps/api/go.mod`. govulncheck decides which standard-library fixes apply from that line, so a stale toolchain silently widens the blast radius of every advisory filed against `net/http` |
| Reachable production npm advisories | `npm audit --omit=dev --audit-level=high` in the same job |
| Committed secrets | `scripts/scan-secrets.py`, with a self-test that asserts the detector catches what it claims |
| Forbidden file shapes | The same script refuses to track `.env`, key material, database files and netrc |

`npm audit` found a real high-severity advisory in `postcss@8.4.31`, nested inside Next 15. The fix is an
`overrides` entry pinning `postcss@8.5.32` rather than a major framework upgrade, and the web build,
typecheck and tests were re-run against the patched tree. This is what the gate is for: the advisory was
sitting in the dependency tree for as long as nobody looked.

### 3.8 Durability and recovery

Migration discipline from an empty database on both engines, an automated destroy→restore→verify drill in
CI for SQLite and PostgreSQL, and transactional writes for anything that must land together. Backups add
the operational half: consistent snapshots taken while the server serves, a checksum and per-table counts
per backup, two-window retention that cannot delete the newest copy, a re-read off-site mirror, and
point-in-time recovery demonstrated by deleting the cluster and replaying archived WAL to a chosen second
(`docs/BACKUP_AND_RECOVERY.md`). The
compensating-entry design means a bad money record is corrected, never erased.

---

## 4. Residual risks

Each of these is real, known, and owned. None is closed by this document.

| # | Risk | Severity | Consequence | Owner | The honest mitigation today |
| --- | --- | --- | --- | --- | --- |
| R1 | **Payments still run through the sandbox; refunds do not.** A refund adapter exists (Paystack, with its callbacks verified), but taking a payment through the live provider is not wired, and the adapter has never run against the live API from this repository | High | A real launch could take money through a path nobody has exercised, and a refund could fail in a way only a fake server has ever tested | Backend lead | Sandbox payments are off by default in production config; refunds carry a lifecycle that refuses to guess at an ambiguous outcome, and every unresolved refund is reported and pages |
| R2 | **Rate limiting is in-process.** A second instance has its own windows; a restart clears them | Medium | Limits are per-instance, so horizontal scaling weakens them | Platform lead | Single-instance deployment; the limiter is behind one method so a shared store can replace it |
| R3 | **No device-level 2FA or risk signals** on staff accounts | High | One phished finance password is a refund and reconciliation authority | Security lead | Role separation, audit trail on every money movement, session revocation, short-lived access tokens |
| R4 | **No staging environment**, so deploy and rollback are unproven | High | The first real deploy is also the first rehearsal | Release lead | Restore drill and CI cover data recovery; a rollback rehearsal is Phase 4's remaining gate |
| R5 | **Backups exist and are drilled, but nothing watches them** | Medium | A backup job that has been failing for three weeks looks exactly like one that works; encryption at rest is delegated to the volume, and there is no cross-region copy | Platform lead | Nightly schedule with `--verify` and off-site mirroring, retention that cannot delete the newest copy, and a PITR drill that deletes the cluster first (`docs/BACKUP_AND_RECOVERY.md`) |
| R6 | **No data export or deletion workflow**; retention rules unsigned | High (NDPA) | A data-subject request cannot be honoured systematically | Product + legal | Data minimisation: no card data, no location, no device identifiers |
| R7 | **Legacy demo shell** (`legacy/`) ships seeded demo passwords in source | Low | Confusing, and a bad example if copied | Backend lead | Not built in CI; demo data only exists when seeding is explicitly enabled |
| R8 | **No WAF or bot protection** in front of the public endpoints | Medium | Credential stuffing is slowed, not stopped | Platform lead | Per-endpoint budgets and generic auth failures |
| R9 | **Partial security monitoring**: `/metrics` now carries request, error, webhook, refund, reconciliation and backup signals with alerts on them, but audit rows (`refunds`, suspensions) are not among them | Medium | A refund spike is still visible only if someone looks; login failures, webhook rejections and reconciliation discrepancies now page | Platform lead | `edumart_api_errors_total`, `edumart_login_failures_total`, `edumart_webhook_events_total` and the reconciliation gauges are alertable; `audit_logs` is queryable |
| R10 | **No incident response runbook** | Medium | The first incident will be improvised | Engineering lead | This document names the assets and the owners |
| R11 | **No dependency update automation** | Medium | Advisories are found only when CI runs after someone changes a pin | Backend lead | The Security job fails loudly on a reachable advisory |

### Adversary-by-adversary answer

- *Scraper*: budgets on the routes it wants, unguessable class codes, generic errors.
- *Credential stuffer*: Argon2id (expensive per guess), per-address and per-account budgets that cannot be
  evaded with a header, generic failures that give no signal on whether the account exists.
- *Learner seeking a better score*: keys withheld until release, server-owned deadlines, snapshot-based
  scoring, corrections that never leak a score.
- *Learner seeking free content*: entitlements are server-side; `simulate-payment` exists only when
  sandbox payments are enabled, and is off in production config.
- *Curious teacher*: membership-scoped routes returning `404` to outsiders.
- *Insider*: least-privilege roles, mandatory refund reasons, audit rows on every money movement, and
  refund history readable by support while the *ability* to refund is not.
- *Callback forger*: HMAC verification over the raw body, plus dedupe and amount checks before any grant.
- *Someone with repo access*: secret scanning in CI with a tested detector; no secrets are stored in the
  tree, only placeholders in `.env.example`.

---

## 5. What would make this document wrong

A threat model ages. These are the events that invalidate it, each of which requires a re-review:

- A live payment provider adapter (changes R1 completely, and adds a new callback surface).
- Any multi-instance deployment (invalidates R2's mitigation).
- Staff accounts gaining new privileges, or 2FA arriving (R3).
- A staging environment and a rehearsed rollback (R4).
- Scheduled backups and PITR — now in place, drilled, and alerted on via the backup heartbeat; what would change this is encryption at rest or a cross-region copy,
  encryption at rest with a key we manage, or cross-region replication.
- Any change to what personal data is stored (R6).
- A public launch (R8, R9, R10 all become urgent rather than noted).

## 6. How to check the claims in this document

```bash
python3 scripts/scan-secrets.py --self-test   # the detector catches what it says it catches
python3 scripts/scan-secrets.py               # nothing committed looks like a credential
cd apps/api && go test ./... -race            # the auth, tenancy and money invariants
cd apps/api && govulncheck ./...              # reachable Go advisories
npm audit --omit=dev --audit-level=high       # reachable production JS advisories
python3 scripts/restore-drill.py both --dsn … # the database survives its own destruction
```

CI runs all of these except the restore drill's local form; see `.github/workflows/ci.yml`.
