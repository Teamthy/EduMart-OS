# Phase status

Where Edumart actually stands against the thirty phases in
[`BUILD_BLUEPRINT.md`](./BUILD_BLUEPRINT.md). Every claim points at code, a test or a document.
Nothing here is a projection, and a phase is never "done" because its screens exist.

**Legend** — **Done**: implemented and covered by a test or CI check. **Partial**: real code works but
the phase's exit gate is not met. **Not started**: nothing beyond scaffolding or design.

Last updated with fifty-seat rooms, synchronised exams and consistency: media is now forwarded by an in-process
media server instead of meshed, so a room holds 50 people (six on stage) and a free class is capped at 45
minutes on the server; exams are sittings with one paper, one window, a per-candidate permutation and a
server-recorded start, submit and score; and a learner's streak is derived from activity rather than counted.
Before that, live classes with a shareable link, an encrypted whiteboard and attendance by measurement; and
commission that walks the referral chain for students, tutors and institutions alike. Before that, session rotation: the browser and the native app now rotate refresh tokens, so an hour-long
access token is a real bound rather than a number nothing enforced. Before that, the rolling rollout: the API is a tested container image, and the swap that an orchestrator
performs — surge, drain, then abort halfway — is executed under continuous traffic on every pull request, with
two deliberately broken rollouts proving the gate still fails. Before that, search closed the last "lists only" gap,
accessibility was measured rather than claimed, and the system was load-tested with its invariants proved to
survive.

## The whole board

| # | Phase | Status | The gap that matters |
| --- | --- | --- | --- |
| 1 | Product specification | Partial | PRD exists; no recorded sign-off, no owners |
| 2 | Architecture and standards | Partial | Boundaries and ADR-style notes exist; no formal ADR index |
| 3 | Repository and developer environment | Done | — |
| 4 | Infrastructure and CI/CD | Partial | CI is real; **deploy and rollback are rehearsed on every PR**; **six replicas start together in CI** with migrations applied exactly once; **a rolling rollout, its abort, and its failure modes are rehearsed under live traffic**; and **the API ships as a tested, non-root container image**. **No hosting target, no staging environment, no orchestrator, no web-app image** |
| 5 | Database and persistence | Partial | Restore, backup strategy and **point-in-time recovery all demonstrated in CI**; scheduled dumps, retention, off-site mirroring and **alerting on a failed or unverified backup** are implemented and templated. No cross-region copy, no drill against production-sized data |
| 6 | API platform and contracts | Done | 100 routes, contract checked in CI (every router path must be documented or the gate fails) |
| 7 | Identity and account lifecycle | Partial | Verification, suspension, Argon2id, and **rotating refresh tokens that both clients actually use**: a scoped `Path=/v1/auth` refresh cookie, a web middleware that renews an expired session before the page renders, single-flight native rotation, and sign-out that kills the chain from either credential. **No device-level 2FA, no IP/geo risk signals** |
| 8 | Authorization and multi-tenancy | Partial | Organisations, memberships, invitations, approval and audit events exist, and classes can belong to an institution; **no org-scoped reporting, suspension or ownership transfer** |
| 9 | Design system and shells | Partial | Tokens and components shared; contrast and structure now **measured in CI** (seven shipped contrast failures and two landmark defects found and fixed); **no screen-reader or keyboard-only verification** |
| 10 | Student onboarding and profiles | Partial | Five-step wizard works; **verification is not a mandatory step** |
| 11 | Taxonomy and syllabus | Partial | Exam → subject → topic; **no syllabus versioning** |
| 12 | Question bank and content ops | Partial | Authoring, review, publishing; **no import pipeline, no withdrawal** |
| 13 | Assessment configuration and snapshots | Partial | Snapshots and versioned scoring; **no assessment templates/sections** |
| 14 | CBT attempt lifecycle | Partial | Durable, idempotent, deadline-authoritative; **no load test** |
| 15 | Scoring and results | Done for practice and mocks | Re-scoring policy and adjustment audit trail missing |
| 16 | Student dashboard | Partial | Dashboard, weak topics, streaks; no syllabus-coverage view |
| 17 | PWA and offline | Partial | Installable and cache-aware; **no pending-write queue, no offline exams** |
| 18 | Native mobile | Partial | Journeys work; **no crash reporting, no signed release builds** |
| 19 | Classrooms and assignments | Partial | Classes, rosters, assignments, and now unguessable codes that expire in 30 days and can be reissued in one click (revoking the old one); no usage limits on institution invitations |
| 20 | Institutions and administration | Partial | Creation, membership, invitations, an approval queue with refusal and audit all work; **no institution dashboards, data export, retention rules or console** |
| 21 | Digital library and publications | Partial | Catalogue + entitlements; **no publisher onboarding, no versions** |
| 22 | Commerce and payments | Partial | Refunds and reconciliation are append-only **and now move money**: a provider adapter with a pending/succeeded/failed/needs-review lifecycle, Paystack's signed webhook normalised into the same path; **no disputes or chargebacks, no scheduled reconciliation job** |
| 23 | Rewards and referrals | Partial | Two ledgers, dedupe, attribution, and **commission that walks the whole referral chain**: `referral_level` on every ledger row, 10/3/1% by default (`EDUMART_REFERRAL_RATES`), a per-level idempotency key, a cycle guard, a level-aware refund reversal, and `GET /v1/referrals/tree`; refunds now reverse commission proportionally; **no payouts** |
| 24 | Notifications | Partial | Durable queue, retries, SMTP, per-message records, and now scheduled reminders (study nudge, deadline warning) that survive restarts, never double-send and withdraw themselves when the reason disappears; **no SMS/push, no unsubscribe or quiet hours** |
| 25 | Analytics and reporting | Partial | Learner aggregates; no institution reports, no metric definitions |
| 26 | Search and discovery | Partial | Prefix search across the catalogue and syllabus, public, with ranking that is identical on both engines and visibility re-checked against the source tables on every query; **no stemming, no typo tolerance, no question-bank search, no relevance evaluation** |
| 27 | AI capabilities | Not started | Deliberately last, per the blueprint |
| 28 | Security and resilience | Partial | Threat model with named residual risks, reachable-dependency scanning and secret scanning in CI, trusted-proxy fix, and now **`/metrics` with fourteen alert rules** (backups, reconciliation, webhooks, errors, latency); **no 2FA, no alert delivery, no incident runbook** |
| 29 | Performance and QA | Partial | **Measured, not assumed**: a load generator over the whole attempt lifecycle, capacity numbers on PostgreSQL (300 concurrent learners, 264 req/s, p95 8.5 ms, zero errors), seven invariants proved to hold afterwards in CI, and accessibility now gated on contrast and structure; **no device matrix, no screen-reader or keyboard-only pass, no production-sized dataset, no soak run** |
| 30 | Pilot and launch | Not started | No staging, pilot, on-call or incident process |

Live classes are **Partial**: scheduling, credits-or-discount joining, capacity, presence, roster with avatars,
an encrypted whiteboard with ordered replay, a targeted signalling relay, an attendance report, a shareable link
and a **fifty-seat room with a six-seat stage** all work and are covered by tests on both database engines — but
no TURN relay is deployed, the media forwarder makes media hop-by-hop rather than end-to-end, and the mobile app
opens the invite rather than hosting the room. Free sessions are capped at 45 minutes on the server. See
[`VIRTUAL_CLASSES.md`](./VIRTUAL_CLASSES.md).

Synchronised exams are **Partial**: one paper, one window, the same duration for everybody, a per-candidate
permutation, and a server-recorded start, submission and score — but there is no proctoring and no scheduled
result release. See [`EXAMS.md`](./EXAMS.md).

**Nine of thirty phases are done or effectively done**, and Phase 29 is now partially done: the system has
been measured under load and its invariants proved to survive it. The remaining work is real, not polish. Phase 8 and Phase 20 are now partially done: the model exists and is enforced, the reports do
not.

---

## Detail: the phases with substance

### Phase 3 — Repository and developer environment · Done

`apps/api` (Go), `apps/web` (Next.js), `apps/mobile` (Expo), `packages/shared`, `packages/contracts`,
`infra`, `docs`, `scripts`. Pinned toolchains, `make` targets for every routine task, `.env.example`
covering both engines, and `README.md` carrying the commands CI actually runs. Verified by CI doing a
clean install on every push.

### Phase 4 — Infrastructure and CI/CD · rollback is no longer a hypothesis

| Item | Status |
| --- | --- |
| CI quality gates | gofmt, vet, contract check, Go tests (`-race`) on **SQLite and PostgreSQL 17**, web typecheck/tests/`next build`, mobile typecheck + Metro bundle, HTTP smoke against both engines, load + invariants, restore + backup + PITR drills, **deploy and rollback rehearsal** |
| Migration execution policy | `store/migrate.go`: checksummed, applied once, one transaction per migration, refuses an edited-after-apply file |
| Migration safety | **New.** Destructive DDL (drop/rename) fails the build unless the file declares `-- rehearsal: destructive: <reason>` |
| Migrate-then-serve | **New.** `edumart-api -migrate-only` applies pending migrations and exits, so a rollout migrates before any replica serves |
| Concurrent boot | **New.** A session-level advisory lock (PostgreSQL) or an flock (SQLite) serialises migrations across processes; six replicas start together in CI and every migration is recorded exactly once. Before this, **three of four replicas exited with status 1** on a fresh database |
| Rollback | **Rehearsed on every pull request.** Previous release built from git, migrated forward, then rolled back onto the newer schema and proved to read the newer release's data |
| Rolling rollout | **New.** `scripts/rollout-rehearsal.py` replaces a four-replica fleet under continuous traffic (surge, no-surge), **aborts the rollout halfway and recovers under traffic**, and fails deterministically when a replica joins the rotation before it is ready or a stopped replica is left in it. Both faults are injected in CI to prove the gate still fails |
| Container images | **Both, tested together.** `apps/api/Dockerfile` (cgo-capable Debian, non-root `10001`, CA bundle, build-stamped version) and `apps/web/Dockerfile` (Next standalone, non-root `10002`, static-asset healthcheck). CI builds both, asserts neither runs as root, and runs them **as one stack** on a Docker network: a page renders, `/v1` is proxied by name, a 401 and a `Set-Cookie` survive the hop. No registry, no signing, no SBOM |
| Deployment manifests | **New.** `infra/k8s` — `restricted` Pod Security, migration Job, API and web Deployments (`maxSurge 1`/`maxUnavailable 0`, `runAsNonRoot`, read-only root filesystem, graceful shutdown), Services, TLS Ingress, PDBs, default-deny NetworkPolicies. `scripts/check-manifests.py` enforces ten invariants against the application's own environment surface and **proves each check can fail with fourteen injected faults**; `kubectl --dry-run=client` parses them in CI. **Never applied to a cluster** |
| Environments | Development and CI are real. **Staging and production are still not provisioned**: there are manifests and images, but no cluster, no registry push, no secrets manager and no TLS issuance. The rollout rehearsal performs the swap against processes, not a controller performing it against pods |

**A second thing Phase 4 was hiding: a fleet could not start.** Six replicas launched within 20 ms of each
other against an empty database left **three of them dead**, each with
`duplicate key value violates unique constraint "pg_type_typname_nsp_index"` — `CREATE TABLE IF NOT EXISTS` is
not atomic against a concurrent creator, and the ledger is written *after* the DDL, so it could not arbitrate.
A first deploy, a new environment or a rolling restart would have shipped with replicas crashlooping until
their restart backoff happened to queue them behind the winner. Migrations now take a lock before their first
statement (PostgreSQL advisory lock on a pinned connection; flock for SQLite) and re-read the ledger after
acquiring it. `scripts/fleet-start.py` starts six real processes against one database in CI and checks all
three properties — boots together, migrate-first, and fail-closed. See [`DEPLOYMENT.md`](./DEPLOYMENT.md).

The gate that was missing is now executed rather than described. Phase 4's gap was never "we have no CI" —
it was *deploy and rollback are unproven*, and a rollback plan nobody has run is a hypothesis that gets tested
for the first time during an incident. `scripts/deploy-rehearsal.py` builds **two real releases** (the
previous one from a git ref, this one from the working tree), deploys the old one, migrates, proves the old
one still serves the migrated database, deploys the new one, writes an order with it, **rolls back**, and
reads that order with the old code. Seven stages, both engines, in CI. See
[`DEPLOYMENT.md`](./DEPLOYMENT.md).

**A fourth thing: the web container half worked, and only a container could show it.** The `/v1` proxy was a
`next build` rewrite, so its destination was frozen at build time while server-rendered pages read the same
variable at run time. Built with the default and run with the right URL, the pages rendered and every API call
went to `127.0.0.1:8080`; built the other way round, the opposite. That is a container whose broken half
depends on how it was built, and it cannot be caught by any test that does not build and run the image. The
proxy is now a route handler with nine tests, and the CI job runs both images as a stack.

**A third thing: the rollout gate was passing when it should have failed.** The first version of the rolling
rollout asserted only "no client request failed", and its deliberately broken rollout — a replica entering the
rotation before its process was listening — *passed*. The window is tens of milliseconds; whether a client
lands in it is luck. The gate now checks readiness **at the moment a replica is registered** and checks that
every replica still in the rotation answers `/readyz`, so both injected faults fail with a named replica
instead of depending on timing. A test that passes because the machine is fast is not a gate.

Two things it found immediately, which is the point of running something rather than writing about it:

- **The de-facto deploy order was wrong.** Migrations run when a process opens the database, so N replicas
  starting together all claim the same pending migration — the ledger stops them diverging, but the losers
  exit on a duplicate row and crashloop. `-migrate-only` gives the rollout the right order; replicas still
  migrate at boot, so this is mitigation, not a lock.
- **`sqlite` is `sqlite3` to database/sql.** The first run on the SQLite path died with
  `unknown driver "sqlite"` — a reminder that a rehearsal with one engine misses what the other one catches.

What is still missing, and not claimed: **no hosting target, no staging environment, no container image, no
automated rollback trigger, and no data rollback** (reverting a schema change loses what the newer code wrote
into it).

### Phase 5 — Database and persistence foundation · restore, backup and PITR all demonstrated

Both dialects are exercised by the whole suite. Constraints exist where they protect a real invariant
(unique email, FK cascades, ledger append-only triggers where applicable). Timestamps are UTC RFC3339
text.

**A restore has now been demonstrated, by destroying the database rather than by copying it.**
`scripts/restore-drill.py` (run by the `drill` Make target and by the *Restore drill* CI job) does this
for both engines:

1. start the API against an empty database, so migrations, seeding and `/readyz` are part of the evidence;
2. record a fingerprint — the row count of all 43 tables (198 rows with the demo seed) and a real login;
3. stop the server and snapshot consistently (`VACUUM INTO` for SQLite, `pg_dump --format=custom` for
   PostgreSQL — a plain `cp` of a database in use is not a backup);
4. **destroy** it (delete the file / `dropdb`), and assert it is gone;
5. restore, start the API again, and compare fingerprints table by table, then log in with an account
   that predates the snapshot.

The drill found a real bug the first time it ran: the learner dashboard used SQLite's `julianday()` and
returned `500` on PostgreSQL. Every test passed because no fixture had a completed attempt inside the
dashboard's seven-day window. Study minutes are now subtracted in Go, and
`TestDashboardAnswersOnEveryEngine` / `TestDashboardStudyMinutesComeFromServerTimestamps` cover it.

**A snapshot is now something that exists, and one can be replayed to a chosen second.** The operational
half — schedule, retention, off-site copy, point-in-time recovery — is
[`BACKUP_AND_RECOVERY.md`](./BACKUP_AND_RECOVERY.md), implemented in `scripts/backup.py` and proven by
`scripts/backup-drill.py` and `scripts/pitr-drill.py`, both run in CI:

| Capability | Proven by |
| --- | --- |
| A snapshot taken while the API keeps serving restores every row, and the restored database then serves a real login | `backup-drill.py` claim 1 — all 45 tables match the counts the backup recorded |
| Corruption is caught **before** a restore is attempted | claim 2 — one flipped byte, `--verify` fails on the checksum |
| Retention cannot delete the only copy | claim 3 — `--keep-daily 0 --keep-weekly 0` still keeps the newest |
| The off-site copy is the same bytes | claim 4 — second destination re-read and compared, with its own manifest |
| Recovery to a chosen second | `pitr-drill.py` — archive WAL, `pg_basebackup`, **delete the data directory**, replay to the target, promote; the account created before the target exists and the mistake made after it does not |

Three decisions are worth recording because each is where backup systems usually fail: the dump is
consistent rather than a file copy; retention is two windows (daily for a bad deploy, weekly for "nobody
looked for a fortnight") rather than "the last seven files"; and verification compares the restored rows to
the counts the backup recorded about itself, which is what makes it a test rather than a restatement.

Alerting now hangs off the heartbeat the job writes (`edumart_backup_age_seconds`, plus a verification
flag), so a backup that stops running, or one that was never restored, pages instead of aging quietly.

Still open, and named in the runbook rather than implied: no encryption at
rest by this tooling, no cross-region replication, no drill against production-sized data, and the
environment secrets (webhook, SMTP, verification) are not in the backup — restoring the database without
them is half a restore.

### Phase 6 — API platform and shared contracts · Done

78 routes in `packages/contracts/openapi.yaml`, enforced in both directions against the router by
`scripts/check-contract.py`. One error envelope with stable codes (`VALIDATION_ERROR`,
`UNAUTHENTICATED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `RATE_LIMITED`, `ASSESSMENT_NOT_OPEN`,
`SESSION_EXPIRED`, `SESSION_CLOSED`, `PAYMENT_PENDING`, `EMAIL_NOT_VERIFIED`, `INTERNAL_ERROR`).
Request ids on every response. Idempotency keys on retry-sensitive writes. `packages/shared` is one
TypeScript client used by web and mobile.

### Phase 7 — Identity and account lifecycle · Partial

| Item | Status |
| --- | --- |
| Registration, login, logout | Done |
| Password hashing | **Argon2id** (`golang.org/x/crypto/argon2`), m=19 MiB, t=2, p=1 — the OWASP baseline. Parameters live inside each digest, so raising them later rehashes rows on their next sign-in rather than locking anyone out. PBKDF2 digests still verify and are upgraded on use |
| Password recovery | Done, enumeration-safe, invalidates every session |
| Sessions and revocation | Done — opaque tokens, server-side revocation, `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}` |
| **Email and phone verification** | **Done** — `POST /v1/auth/verification/request` and `/confirm`; HMAC-protected, single-use, 15-minute, attempt-limited codes; E.164 normalisation; per-user throttling; delivery through the durable courier (Phase 24), so a relay outage retries instead of losing the code |
| Verification gate | Checkout (`POST /v1/orders`) refuses an unverified address with `EMAIL_NOT_VERIFIED` |
| Rate limits | Per-route, **in-process** — resets on restart and does not span instances |
| Refresh tokens / rotation | **Done, and now used.** `POST /v1/auth/refresh` exchanges a single-use, 30-day refresh token for a new pair (**1-hour** access token + new refresh token). Reuse past a 20-second grace window revokes the whole session family (`SESSION_REUSED`). Browser sign-ins also get an `HttpOnly`, `SameSite=Lax` refresh cookie **scoped to `Path=/v1/auth`**; the web middleware rotates it transparently before a page renders, and the Expo app keeps the pair in secure storage and refreshes single-flight on a 401. Sign-out kills the family from either credential and clears both cookies at their own paths |
| **Account suspension** | **Done** — `POST /v1/admin/users/{id}/suspend` and `/reinstate`, platform administrators only, never against an administrator or yourself |
| Moderation lookup | `GET /v1/admin/users?q=&status=` — an administrator cannot suspend an account they cannot find |
| Security-event audit trail | Suspension and reinstatement write `user.suspended` / `user.reinstated` to `audit_logs` in the same transaction as the change |

**The gap that closed here was in the clients, not the server.** Everything in the row above was already
implemented and tested, and nothing used it: the web kept a seven-day access cookie and the app kept only the
access token, so the refresh endpoint was dead code in production and every session really lasted a week. Both
now rotate, the access token is an hour, and `scripts/session-lifecycle.py` proves the experience end to end —
sign in, expire, renew, sign out, replay refused — against a real server and a real cookie jar, in CI, both
against the binary and through the web container. Two defects only surfaced by running it: the web middleware
renewed the session for the *next* request while the current one still redirected to `/login`
(`NextResponse.next` copies headers when constructed), and sign-out with an expired access cookie revoked
nothing. See [`SECURITY_AND_DURABILITY.md`](./SECURITY_AND_DURABILITY.md).

**Session rotation, and why the family is the unit.** A single long-lived token has one failure mode
that matters: once it leaks, the thief has the owner's standing for the token's whole life and nobody can
tell the two apart. Rotation turns that into a detectable event — every exchange retires the presented
token, so a token used twice means two parties hold it. The response is not "fail this request": it is
revoke every session descended from that sign-in, because there is no way to know which party is the
thief. Three details make it work rather than merely exist:

- **The retired hash is kept, not cleared.** Clearing it would make a replay look like a token that never
  existed, and the detection would quietly stop working. The hash is a one-way digest of a token that can
  no longer authenticate anything.
- **A 20-second grace window is not a weakening.** Two browser tabs refreshing at the same moment is a
  race; treating it as theft would sign people out of everything and teach them to distrust the
  mechanism. Inside the window the duplicate is refused without revoking the family; outside it, the
  family dies.
- **The access token dies on rotation too.** After an exchange there is exactly one live pair, so "which
  token is current?" has an answer.

Proven by `TestRefreshRotatesAndRetiresTheOldToken`, `TestReusingARefreshTokenKillsTheWholeFamily`,
`TestRefreshingTwiceInTheGraceWindowDoesNotRevokeTheFamily`, `TestSigningOutEndsTheWholeFamily`,
`TestSignInIssuesARotatingPair`, `TestRefreshRejectsASuspendedAccount`,
`TestSessionsListShowsOneDeviceAfterRotation`, `TestFamilyRowsShareOneIdentifier`, and the
`token rotation` section of the smoke run.

Password hashing is proven by `TestPasswordsAreHashedWithArgon2id`, `TestALegacyPBKDF2HashStillSignsIn`,
`TestSignInUpgradesAnOldPasswordHash`, `TestAbsurdArgon2ParametersAreRejected` and
`TestLongPasswordsAreRefused`. The last of those is not paranoia: a digest arrives from the database, and
a tampered row could otherwise ask the process to allocate a gigabyte per sign-in attempt.

Verification is proven by `TestEmailVerificationJourney`, `TestCheckoutRequiresVerifiedEmail`,
`TestVerificationAttemptsAreLimited`, `TestVerificationResendIsThrottled`, `TestPhoneVerification`,
`TestVerificationRejectsBadInput`, `TestUndeliveredCodeIsRetriedThenWithdrawn`,
`TestDeliveryIsRecordedAndVisibleToStaff`, plus the live smoke run and a manual outage drill against a
real SMTP relay.

**What verification does not yet do:** it is not a required onboarding step, so an unverified account
can still practise, take mocks and hold points. Only money is gated. That is a deliberate, documented
scope decision — not an oversight.

**What suspension guarantees**, and what it does not:

- Status, the reason, the actor, every live session and any outstanding verification code change in one
  transaction. A suspension that leaves a working session behind is not a suspension.
- `AuthenticateSession` also refuses any non-active account, so the rule survives a restored backup, a
  replica that has not caught up, or a bug in the revocation path.
- Reinstatement does **not** restore sessions. The person signs in again, which re-establishes that they
  still control the mailbox.
- Proven by `TestSuspensionEndsSessionsAndBlocksSignIn`, `TestSuspensionRevokesOutstandingCodes`,
  `TestSuspensionIsReservedForAdministrators`, `TestSuspensionIsStoredOnTheAccount`,
  `TestReinstateRequiresASuspendedAccount`, `TestStaffCanFindAnAccountToModerate`, and the moderation
  section of the smoke run.
- Not covered: there is no admin web console for moderation yet (the API is what exists), no suspension
  expiry or graduated limits, and no appeal workflow.

### Phase 8 — Authorization and multi-tenancy · Partial

**Correction:** an earlier version of this page claimed there was no `organizations` table. That was
wrong — `organizations`, `organization_memberships` and `invitations` were in the baseline schema all
along, unused by any Go code. What was missing was the model, not the tables.

Implemented now (`internal/orgs`):

| Item | Evidence |
| --- | --- |
| Organisations and membership | `POST/GET /v1/organizations`, owner created with the organisation |
| Approval flow | New organisations start `pending_verification`; only a platform administrator can approve (`/v1/admin/organizations/{id}/verify`) |
| Invitations | Single-use, email-locked, seven-day tokens stored as hashes; withdrawing is separate from expiry; a new invitation supersedes an older open one |
| Role assignment | Membership roles (`owner`, `admin`, `teacher`, `student`); a teacher invitation is the one path that grants the platform teacher role, because an owner deliberately issued it |
| Role revocation | Removing a membership deletes the membership and the teacher role when no other organisation still grants it — access ends in the same transaction |
| Tenant scoping | Every organisation query is scoped by membership; non-members get 404, not 403, so the API cannot be used to enumerate institutions |
| Audit trail | `organization.*` events written to `audit_logs` with actor, resource and metadata, inside the same transaction as the change |
| Institution classes | `POST /v1/classes` accepts `organizationId`; the caller must own or administer that institution. `GET /v1/organizations/{id}/classes` lists them for its owners and administrators |
| Negative tests | `TestOrganizationLifecycle`, `TestInvitationIsEmailLockedAndSingleUse`, `TestInvitationRevocationAndExpiry`, `TestCrossTenantIsolation`, `TestRosterIsForAdministrators`, `TestRemovingMembershipRemovesAccess`, `TestLastOwnerCannotBeRemoved`, `TestInstitutionClassesBelongToTheInstitution`, `TestPersonalClassesAreUnaffected` |

Class ownership is deliberately narrow: a teacher's personal class is theirs and never appears in an
institution's reporting, and a class is only attached to an organisation when an owner or administrator
creates it that way.

Still missing: org-scoped reporting and analytics, membership suspension, ownership transfer, an admin
surface that lists pending organisations, organisation-level billing, and SSO.

### Virtual classes — the room, and the two things it refuses to do

| Piece | Where | Test |
| --- | --- | --- |
| Scheduling, price in credits or free | `POST /v1/virtual-classes`, `apps/api/internal/virtualclass` | `TestLiveClassJoinChargesCreditsOnce`, `TestLiveClassIsFreeWhenTheHostPays` |
| Discount codes | `POST /v1/virtual-classes/{id}/discount-codes` | `TestLiveClassDiscountCodeIsSingleUse` |
| Capacity and idempotent join | lock + unique attendee + ledger key | `TestLiveClassCapacityIsAFact`, `TestLiveClassRefusesWhenCreditsAreShort` |
| Host-only start/end/attendance | per-class authority | `TestOnlyTheHostRunsTheRoom` |
| Encrypted whiteboard with replay | `…/board`, dense `seq` | `TestWhiteboardIsStoredAsOpaqueOrderedCiphertext` |
| Presence as heartbeat | 75 s window | `TestPresenceIsAHeartbeatNotAFlag` |
| Signalling relay | `…/signal`, members only | `TestSignallingRelayIsTargetedAndMemberOnly` |
| Shareable link | `GET /v1/join/{slug}` | `TestInviteLinkResolvesWithoutAnAccount` |
| Session tracking | `…/attendance` | `TestAttendanceRecordsTheSession` |
| Fifty seats, really | `apps/api/internal/media` (forwarder), capacity clamped at the door | `TestRoomHoldsFiftyPeople` |
| Six-seat stage, server-assigned | `…/stage`, `…/media` | `TestStageGrantsTheSlotAndTheMediaServerAgrees` |
| A free class is 45 minutes | create-time rule **and** `requireOpen` at the door | `TestFreeClassIsCappedAtFortyFiveMinutes` |
| Consistency without a counter | `GET /v1/consistency`, derived from activity rows | `TestConsistencyIsDerivedFromActivity` |

**What changed about the encryption claim, and why it had to.** A mesh cannot hold fifty people: every
participant uploads one stream per peer. Media is now forwarded by an in-process media server
(`pion/webrtc`), which means DTLS-SRTP protects each leg but the server is in the path — **hop by hop, not end to
end**. The forwarder has no recording, no disk write and no egress, but that is a promise kept by the code rather
than a property of the design, and [`VIRTUAL_CLASSES.md`](./VIRTUAL_CLASSES.md) says so on the page its readers
will look at. The whiteboard keeps its end-to-end property: its key travels in the join link's URL fragment,
which no browser sends to a server, so it is *stronger* than the data-channel hand-off it replaced.

### Synchronised exams

An exam is a sitting: one paper, one window, the same duration for everybody, and a per-candidate permutation so
two neighbours see the same questions in different orders. It is a thin layer over the existing assessment
engine, which already owns the immutable snapshot, the deadline, the scoring policy and the single finalisation
path — see [`EXAMS.md`](./EXAMS.md).

| Rule | Where it lives | Test |
| --- | --- | --- |
| Same paper, different order per candidate | `shuffle_questions` + seeded permutation in `StartDefinition` | `TestExamIsOnePaperForEverybodyInADifferentOrder` |
| The window wins over the duration | deadline clamp at `availability_end` | `TestExamDeadlineNeverOutlivesItsWindow` |
| Outside the window means refused | `POST /v1/exams/{id}/start` | `TestExamRefusesCandidatesOutsideTheWindow` |
| A result belongs to its owner | attempt scoping and host-only report | same two tests (`403` / not `200`) |
| The host does not sit their own paper | start path | `TestExamIsOnePaperForEverybodyInADifferentOrder` |

Not built: proctoring, and a scheduled result release (the schema supports it; there is no UI).

### Phase 19 — Classrooms and assignments · Partial

Classes, rosters, assignments and completion tracking work (`GET /v1/classes`,
`POST /v1/classes/join`, `GET /v1/classes/{id}/roster`, `GET /v1/classes/{id}/assignments`).

A class code used to be the first six letters of the class name plus two random characters, valid
forever. That is a naming convention, not a secret: anyone who knows the school can narrow the search to
about 1,300 guesses, and a code read aloud in a corridor stayed a key to the classroom for good. Two
things changed:

- **Codes are now six characters from a cryptographic source**, over an alphabet without `I`, `O`, `0` or
  `1` — a code is read aloud, written on a whiteboard and typed by a fourteen-year-old.
- **Codes expire after 30 days**, and `POST /v1/classes/{id}/join-code` reissues one. Rotation replaces
  the code rather than adding a second, so the old one stops working at that moment. An expired code
  returns `410 JOIN_CODE_EXPIRED` rather than `404`, because the remedy is "ask your teacher for the
  current one", not "retype this one".

Who may rotate: the teacher who owns the class, a teacher in it, an administrator of the institution that
owns it, or platform staff. A learner in the class gets `403` — whoever can rotate the code can hand out
access to their own classroom.

Proven by `TestJoinCodesAreNotGuesses`, `TestRotatingAJoinCodeRevokesTheOldOne`,
`TestAnExpiredJoinCodeSaysItExpired`, `TestInstitutionAdminCanRotateAClassCode`, and the `class codes`
section of the smoke run.

Still missing: usage limits or per-student invite links for institution invitations, and a per-class
"who has not joined yet" view.

### Phase 20 — Institutions and administration · Partial

| Item | Evidence |
| --- | --- |
| Approval queue | `GET /v1/admin/organizations?status=` — pending institutions oldest-first, with the applicant's contact address. Platform administrators only |
| Refusal with a record | `POST /v1/admin/organizations/{id}/reject` `{reason}` → `rejected`, the reason in `audit_logs`, and the record kept. A rejected institution can still be approved later: refusing an application is a decision, not a permanent ban |
| Approval | `POST /v1/admin/organizations/{id}/verify` — the only path from `pending_verification` to `active` |
| Institution classes | `GET /v1/organizations/{id}/classes` with member and assignment counts (Phase 19) |
| Institution dashboard | `GET /v1/organizations/{id}/report` — members by role, classes, published assignments, submissions, active learners and a 30-day average, plus a per-class breakdown. Every query is scoped by organisation id in SQL; teachers get `403`, non-members `404`. Shown on `/institutions` in the web app |

Proven by `TestApprovalQueueIsTheStaffView`, `TestRejectingAnInstitutionKeepsTheRecord`,
`TestInstitutionReportCountsWhatHappened`, `TestInstitutionReportIsForAdministrators`, and the
institution, approval and reporting sections of the smoke run.

Two decisions in the report are worth stating because they are the difference between a dashboard and a
misleading one. The window is a fixed 30 days rather than "this term", because the product has no calendar
concept yet and a window that silently means "everything ever" makes every trend look flat. And an average
with no submissions is `null`, not `0`: zero is a claim about performance, null is the absence of data.

Still missing: organisation-level data export and retention rules,
a platform-wide view of suspended institutions (`status = 'suspended'` is defined but nothing sets it),
and a web console — the API exists, the screen does not.

### Phase 9 — Design system and shells · Partial

One token set (`packages/shared/src/design-tokens.ts`) drives Tailwind on web and React Native styles
on mobile. `apps/web/components/ui` follows shadcn conventions; `apps/mobile/src/components/ui.tsx`
mirrors it. Loading, empty, error, offline and permission-denied states exist on every P0 screen.
Accessibility is designed for WCAG 2.2 AA and the mechanical half is now enforced on every build:
contrast is computed from the real tokens and cross-checked against the stylesheet, axe checks structure on
the primitives, the shell and the high-stakes screens, and a static scan rejects a bare `outline-none`. It
found seven contrast failures that had shipped (the amber subject chip at 2.72:1, the field border at
1.55:1) and two structural defects. **No screen-reader or keyboard-only pass has been done**, so the exit
gate remains unmet — see [`ACCESSIBILITY.md`](./ACCESSIBILITY.md).

### Phase 10 — Student onboarding and profiles · Partial

The five-step wizard is implemented on web and mobile, with server-persisted drafts
(`signup_drafts`) that exclude passwords and verification secrets. An interrupted signup resumes at the
same step. The blueprint's step 2 is *verification*; today verification is offered after account
creation (settings) rather than inside the wizard, and the account is created at the end of step 5.
That ordering difference is the main gap.

### Phases 13–16 — The learning engine · Partial, and the strongest part of the build

Server-owned deadlines; immutable per-attempt question snapshots; versioned scoring rules; idempotent
answer writes with `409` + `current_version` on stale writes; acknowledgement only after commit;
transactional submission with re-check inside the transaction; double-submit awarding points once;
practice-only corrections that leak no correctness; deterministic re-scoring from stored responses.

Gaps: no assessment templates or sections (a mock is assembled from subject/topic rules), no negative
marking, no load test of concurrent starts or autosaves, and no syllabus-coverage view on the
dashboard.

One correctness fix worth recording: the dashboard's "studied this week" number was computed with
SQLite's `julianday()`, which PostgreSQL does not have. It returned `500` on the engine the product
ships on, and nothing caught it because every fixture left that window empty. The arithmetic now happens
in Go, where the two engines cannot disagree.

### Phase 17 — PWA and offline · Partial

Installable manifest, branded icons, cache-versioned service worker, offline fallback page, and a
connection indicator. There is **no pending-write queue**: a save that fails is retried by the client
while the screen is open, and an answer that never reaches the server is visibly unsaved rather than
silently queued. Offline exams are explicitly out of scope until the separate design exists.

### Phases 22–23 — Commerce and rewards · Partial

Webhook-only payment truth with HMAC verification, provider-event dedupe and amount/currency checks.
Entitlements granted in the same transaction as the verified event. Purchase now also requires a
verified email. Points and commission ledgers are separate and append-only, with balances derived from
entries.

### Phase 22 — Refunds and reconciliation · the correction path

The problem this phase solves is not "let money go back". It is that **a ledger that can only grow is not
a ledger**: once an order is paid, the previous design had no way to say that some of that money came
back, so the books could only ever describe the platform's best day.

Refunds are therefore **append-only compensating entries**, never edits of the charge:

| Rule | Where it lives | Why |
| --- | --- | --- |
| The charge is never rewritten | `orders.total_minor` is untouched; the refund rows are the correction | A record that overwrites the original is not a record |
| The order status is derived | `paid` → `partially_refunded` → `refunded`, recomputed inside the refund transaction | One fact, one place: the status cannot disagree with the rows |
| Over-refund is impossible | `409 CONFLICT` with `{totalMinor, refundedMinor, remainingMinor, attemptedMinor}` and the amounts in words (`₦5,000.00`) | The refusal has to say what is left, or an operator retries blindly |
| Omitted amount means the remainder | `amountMinor <= 0` ⇒ refund what is left | What an operator almost always means |
| An order that took nothing cannot be refunded | `409` before any row is written | A zero-refund row describes no money movement |
| Retries are safe | `idempotency_key` unique; a replay returns the original refund | Providers and operators retry timed-out requests |
| Money and product move together | A full refund revokes the entitlements that order granted | A refund must not be a way to keep what you were paid for |
| Commission is reversed, not deleted | Negative `commission_ledger_entries` row, `source_type='refund'`, proportional to the refunded share | Phase 23 keeps commission in its own ledger; deleting history would make payouts unexplainable |
| Every refund is auditable | `payment.refund_requested`, then `payment.refunded` / `payment.refund_failed` / `payment.refund_needs_review` in `audit_logs`, with amount, refund id and provider reference — no customer PII | Money moves must be attributable to a person and a reason, and an attempt that failed is still a fact |
| A reason is mandatory | Request validation, `400` | An unexplained refund is a loss nobody can account for |
| Finance moves money, support reads history | Refund and reconciliation require finance or platform admin; the refund list also allows support | Answering "where is my refund?" must not need a permission that can move money |

`GET /v1/admin/finance/reconciliation` is deliberately **not a revenue report**. It asks where two records
of the same money disagree, and it reports how many checks ran, because a report that silently checked
nothing is worse than no report:

| Check | Contradiction it names |
| --- | --- |
| `paid_without_entitlement` | Money was taken and nothing was granted |
| `entitlement_without_payment` | Something was granted for an order that is not paid |
| `refund_exceeds_total` | More was returned than was charged |
| `refund_unsettled` | A refund has been in flight for over a day, so someone is waiting for money nobody is watching |
| `succeeded_attempt_unpaid` | The provider says the charge succeeded; the order is not paid |
| `paid_without_attempt` | The order is paid but no payment attempt recorded the money |

Proven by `TestRefundIsRecordedNotRewritten`, `TestFullRefundEndsAccess`,
`TestRefundCannotExceedTheOrderTotal`, `TestRefundIsIdempotent`, `TestFreeOrderCannotBeRefunded`,
`TestRefundRequiresFinanceOrAdmin`, `TestRefundingAnUnpaidOrderIsRefused`,
`TestRefundReversesTheReferralCommission`,
`TestReconciliationReportsContradictionsAndIsQuietWhenThereAreNone`,
`TestReconciliationRejectsANonsenseWindow`, and the `refunds and reconciliation` section of the smoke run,
on **both** SQLite and PostgreSQL.

**Refunds move money, and the lifecycle is the honest part.** A recorded refund used to mean "money went
back" because the only provider was the sandbox, which answers instantly. A real provider does not: it
accepts, processes, and confirms minutes or days later — or fails. So a refund now has a state, and the
money effects hang off *settlement*, not the request:

| State | What it means | What changed |
| --- | --- | --- |
| `pending` | The provider accepted it; the money has not moved | The amount is reserved against the order total, so it cannot be promised twice. Order status, entitlements and commission are untouched |
| `succeeded` | The provider confirmed it | Order recomputed from settled refunds, entitlements revoked on a full refund, commission reversed proportionally, receipt queued |
| `failed` | The provider refused it | Nothing moved; the reservation is released and the amount can be refunded again |
| `needs_review` | The answer was ambiguous — a timeout, a 5xx, an unmatchable id | **Nothing is retried.** A blind retry against an API that may already have paid out is how a customer is refunded twice |

That last row is the design's centre of gravity, and it is why the adapter distinguishes a *refusal* from a
*silence*: `ProviderError.Definite` is true only when the provider answered and said no. The ambiguous case
is closed by a person — `POST /v1/admin/refunds/{id}/resolve` with what they found on the provider's
dashboard — and the decision is audited against them. Refunds left unresolved for a day appear in the
reconciliation report as `refund_unsettled`, and any `needs_review` pages (see `docs/OBSERVABILITY.md`).

The provider seam is `RefundProvider`: the sandbox settles in-process, `PaystackRefunds` posts to
`/refund` with a bounded HTTP client, and a fake in the tests can answer slowly, refuse, or fail
ambiguously. Paystack's own callback envelope is normalised (`PaystackWebhook`) and verified with its
HMAC-SHA512 scheme, so the provider's payload stops at the adapter and the money logic has one entry point.

Proven by `TestARefundInFlightDoesNotMoveMoney`, `TestAProviderConfirmationSettlesTheRefund`,
`TestAnAmbiguousProviderAnswerIsNeverRetried`, `TestADefiniteRefusalLeavesTheMoneyAvailable`,
`TestAPendingRefundCannotBeSpentTwice`, `TestResolvingANeedsReviewRefundRecordsWhoDecided`,
`TestAPaystackShapedWebhookSettlesARefund`, `TestReconciliationSeesMoneyInFlight`, and the adapter's own
tests (`TestPaystackDistinguishesARefusalFromASilence`, `TestPaystackSignatureIsCheckedOverTheRawBody`,
`TestPaystackEventsAreNormalised`).

Gaps, stated plainly: no credit notes or PDFs, no disputes or chargebacks, no automatic retry of a failed
refund (deliberate — the operator retries, because the provider's balance is a human problem), no scheduled
reconciliation job (the report is pulled, not pushed), and no commission approval or payout workflow. The
Paystack adapter has never been exercised against the live API from this repository: its contract with the
provider is covered by a fake server and its own documented shapes, which is not the same as a real refund
having gone through it.

### Phase 29 — Performance and QA · measured, not assumed

The readiness document had been repeating the honest version of this gap for months: *no measurement of
concurrent attempts against a realistic PostgreSQL instance*. [`PERFORMANCE.md`](./PERFORMANCE.md) is that
measurement.

**The workload is the whole attempt lifecycle** — browse, start an attempt, save three answers, submit, read
it back — not one endpoint. A benchmark of `GET /products` would measure the cheapest thing in the system and
say nothing about the write path, where the server owns deadlines, optimistic versions and duplicate-submission
protection. Reads are 30% of journeys, virtual users hold keep-alive connections, and each carries its own
`X-Forwarded-For` so a cohort is not collapsed into one client's rate-limit budget.

| Engine | Users | Throughput | p50 | p95 | p99 | Errors |
| --- | --- | --- | --- | --- | --- | --- |
| SQLite | 32 | 31 req/s | 0.9 ms | 2.2 ms | 3.2 ms | 0% |
| PostgreSQL | 96 | 88 req/s | 3.1 ms | 6.6 ms | 8.7 ms | 0% |
| PostgreSQL | 300 | 264 req/s | 3.4 ms | 8.5 ms | 16.1 ms | 0% |

p95 barely moves from 96 to 300 users while throughput triples, so **the ceiling was not found** — which is
the honest way to report it. The numbers are from a 2-vCPU sandbox with everything on loopback and 50 seeded
questions; they are evidence about what the design costs, not a capacity plan.

**Two defects the load found, both fixed:**

- The PostgreSQL connection pool was **unbounded** — Go's default. Under load the process would open a
  connection per in-flight query until the server refused, turning peak traffic into a wave of 500s, and each
  additional API replica would make it worse. Now bounded (`EDUMART_DB_MAX_OPEN_CONNS`, default 20) and
  refusing values that would make it worse (`0` means *unlimited* to `database/sql`). Measured cost: none.
- The **demo seed violated the Phase 14 deadline rule**: its historical attempt was submitted ten minutes
  after its own deadline, a state the server refuses to produce. The integrity check caught it on the first
  run; the seed now produces a state the engine could actually have produced.

**The half that matters is the invariants**, checked in SQL against the database the load just wrote to:

| Check | What breaking looks like |
| --- | --- |
| `sessions_scored_once` | A submitted attempt with no score |
| `responses_belong_to_a_session` | An orphaned answer row |
| `no_duplicate_responses` | Two answer rows for one question in one attempt |
| `deadline_respected` | Accepting a submission after the deadline the server set |
| `points_ledger_traces_to_something` | A points entry with no owner |
| `no_duplicate_ledger_keys` | Ledger idempotency keys colliding |
| `reconciliation_clean` | Any of the six finance contradictions |

All held at 300 concurrent users with 1,812 attempts submitted, and `reconciliation` reported zero
contradictions across its six checks. The `Load and invariants` CI job runs both engines with a deliberately
loose budget (p95 400 ms, error rate 0.5%): a tight p95 on a shared runner measures the runner, and a flaky
gate gets switched off.

**Accessibility, the other half of the gate, moved from "designed for" to "measured"** — see
[`ACCESSIBILITY.md`](./ACCESSIBILITY.md). Contrast is now computed from the real tokens on every build
(WCAG 1.4.3 at 4.5:1 for text, 1.4.11 at 3:1 for control boundaries), and it found **seven failures that had
already shipped**: the amber subject chip at 2.72:1, the field border at 1.55:1, the focus ring against a
filled button at 2.24:1, and muted text on a muted panel at 4.39:1 among them. axe against real markup found
two structural defects, the worse being the question navigator in a live attempt, which used
`role="listitem"` on a `<button>` and so announced itself as a list that was not a list — on the screen where
a learner can least afford to lose their place.

Still open, and named in [`PERFORMANCE.md`](./PERFORMANCE.md): **the ceiling is unknown**, no run has used a
production-sized dataset (index behaviour at 10⁵–10⁶ rows is unmeasured, and a plan that is fine at 50 rows
can be a sequential scan at a million), nothing has been soaked beyond 45 seconds, and the web app has never
been load-tested. Still open, and named in [`ACCESSIBILITY.md`](./ACCESSIBILITY.md): **no screen-reader pass,
no keyboard-only pass, no zoom or reflow check, and the device matrix is not started** — nothing has run on a
low-end Android phone on 3G, which is the device most learners will actually use.

### Phase 26 — Search and discovery · the endpoint exists, the relevance is unproven

Search was the last "lists only" gap in the product: a learner could browse six subjects and thirty topics,
and had no way to ask a question. `GET /v1/search` answers it, publicly — somebody deciding whether to sign up
should be able to check whether the thing they want exists.

Three decisions carry the implementation, and each is in [`SEARCH.md`](./SEARCH.md) with the reasoning:

- **The index is a projection, not the truth.** Tokens decide *matching*; `products.status` and
  `subjects.active` decide *visibility*, re-checked on every query. A stale index costs recall, never
  correctness. A test withdraws a product, deliberately skips the reindex, and asserts it disappears.
- **Tokens, not `LIKE '%term%'`.** A leading wildcard cannot use an index on either engine, so the token
  table turns a search into a prefix range scan — and keeps one SQL statement working on SQLite and
  PostgreSQL. On PostgreSQL the index is `text_pattern_ops`, because without it the planner ignores a B-tree
  for `LIKE 'prefix%'` and quietly sequential-scans.
- **Ranking in Go.** `ts_rank` and FTS5 rank on different scales, so the two engines would order the same
  query differently and no test would catch it. One scorer, one stable total order.

The tokeniser keeps diacritics: an ASCII-only implementation, which is the obvious one, makes a correctly
spelled Yoruba word unsearchable. That was a real bug in the first draft and a test now pins it.

Two smaller things the tests forced, both of which would have shipped as annoyances: an empty result set was
serialised as `null` rather than `[]` (every client then has to special-case it), and a search for `%` was a
wildcard that would enumerate the corpus — it is now escaped and returns nothing.

**What is not done, and is not claimed:** no stemming beyond prefix matching, no typo tolerance, no semantic
search, no relevance evaluation against real queries at all. The weights (title 3, subtitle 2, body 1, exact
title +5) were chosen by argument, not by looking at a page of results and agreeing they were the right ones.
The corpus is under 100 documents, so nothing here says how search behaves against a production syllabus.

### Phase 28 — Security and resilience · Partial, and now measured

The phase's exit gate is "critical vulnerabilities are closed, residual risks have named owners, recovery
targets are tested, and incident procedures are operational". This slice delivers the first two, and says
plainly which parts of the last two are still missing.

**A threat model, not a checklist.** [`THREAT_MODEL.md`](./THREAT_MODEL.md) names the assets, the
adversaries (including insiders, because a refund and an account suspension are legitimate staff actions
rather than bugs), the controls that actually exist with the file or test that proves each one, and eleven
residual risks with severity, consequence and an owner.

**Two scanners, both wired into CI as the `Security` job:**

| Gate | What it catches | Why it is credible |
| --- | --- | --- |
| `scripts/scan-secrets.py` | Provider keys, private keys, JWTs, credentials in URLs, and file shapes that should never be tracked (`.env`, `*.pem`, database dumps) | `--self-test` asserts 14 fixtures: what must be caught is caught, and local development defaults are left alone. A scanner nobody has shown a secret is a scanner nobody should trust |
| `govulncheck ./...` | Vulnerabilities **reachable** from our code, via the Go vulnerability database | Reports the call path, so a finding is actionable rather than a version diff |
| `npm audit --omit=dev --audit-level=high` | Reachable production JavaScript advisories | Test-only tooling is excluded, because failing on a vulnerable test runner pushes people to skip the gate |

The Go gate immediately earned its place too: against a `go 1.24` directive it reported reachable
standard-library advisories across `net/http`, `crypto/x509` and CORS header handling, all fixed in later
releases. The project now builds on **Go 1.27.3**, and the scan reports nothing reachable. This is the
argument for the gate in one line: the toolchain had quietly aged, and no test could have told us.

The npm gate immediately earned its place: it found a **high-severity advisory in `postcss@8.4.31`** nested
inside Next 15 (arbitrary file read via attacker-controlled `sourceMappingURL`). Fixed with an `overrides`
pin to `postcss@8.5.32` rather than a framework major upgrade, and the web build, typecheck and unit tests
were re-run against the patched tree. Advisory count is now zero.

**A real vulnerability, found by the review and closed:** `X-Forwarded-For` was believed unconditionally.
Every rate limit in the system was therefore keyed on a value the caller chose — rotating one header gave
unlimited password attempts against any known address. Forwarding headers are now honoured only when
`EDUMART_TRUST_PROXY=true` says a proxy is genuinely in front of the process, and
`TestAForwardingHeaderCannotBuyMorePasswordAttempts` proves the difference (it fails if the switch is
flipped). The same review added:

- `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and
  `Cache-Control: no-store` on **every** API response, asserted on a success, a refusal and a 404.
- Budgets on the routes an unauthenticated caller could previously hammer without limit: signup drafts,
  referral-code lookup, and the webhook endpoint that verifies an HMAC on every unsigned request.
- `TestASelfSelectedRoleGrantsNothing` and `TestLoginFailuresDoNotRevealWhichPartWasWrong`, two Phase 8
  rules that were implemented but had no test standing behind them.

**Monitoring, since this slice:** `GET /metrics` publishes fourteen metric families (requests by route
pattern and status, error codes, webhook outcomes, refunds, reconciliation state, backup freshness, login
failures), it fails closed, and no identifier is ever a label. `infra/observability/` holds the scrape
config and fourteen alert rules, and `scripts/check-alerts.py` — run in CI — fails the build when a rule
names a metric the API does not emit. That check is the part that matters: an alert referencing a renamed
metric does not error anywhere, it just never fires, and nothing looks more like a healthy system than a
pager that is not connected to anything. See [`OBSERVABILITY.md`](./OBSERVABILITY.md).

Not done, and not claimed: no device-level 2FA or risk-based sign-in for staff, no alert *delivery* (the
rules exist; routing them to a phone is deployment configuration), no monitoring of audit rows, so a refund
or suspension spree is still visible only to someone reading the table, no WAF or bot protection, no
incident-response runbook, and no dependency-update automation. Each is a named residual risk in the threat
model rather than a sentence in a wish list.

### Phase 24 — Notifications · Partial (delivery is now real)

`internal/notify` owns delivery end to end:

| Item | Evidence |
| --- | --- |
| Durable queue | `notification_deliveries` (migration `0004`), written **before** the first attempt, so a crash between deciding to send and sending still retries |
| Retries and backoff | 30 s → 2 m → 10 m → 30 m, five attempts, swept by a worker that survives restarts — proven live: a stopped relay recovered on its own at ~50 s |
| Real provider | `SMTPSender` (STARTTLS / implicit TLS / plain), verified against a socket in `smtp_test.go` and against a sandbox relay in the smoke run |
| Templates | Code, not database rows: a message body editable at runtime is a phishing vector aimed at our own users |
| Credential hygiene | An abandoned delivery **deletes** the verification code or reset token it carried; delivered bodies are cleared; `GET /v1/admin/deliveries` never returns a body |
| Messages that actually go out | Verification codes, password resets, organisation invitations, purchase receipts |
| Observability | `GET /v1/admin/deliveries` with `{sent, pending, failed}`; `FailedSender` injections are covered by tests |
| Scheduled reminders | `scheduled_notifications` (migration `0007`): a promise recorded when it is decided, claimed before it is sent, revalidated before it goes out, and retained when it is withdrawn. Two kinds — a daily study nudge and a deadline warning. `GET /v1/admin/reminders`, `POST /v1/admin/reminders/sweep` |

**Reminders are a different promise from a delivery, and the code treats them that way.** A delivery is
recorded microseconds before it is attempted; a reminder is decided on Monday and due on Thursday. Three
rules make that safe:

- **The decision is a row, not a timer in memory.** A restart on Wednesday cannot silently cancel a
  message the learner was promised — the queue lives in the database, like everything else durable here.
- **One reminder per learner per window, enforced by a unique index** on the dedupe key, not by a check in
  Go. The planner runs on a timer and re-deciding the same thing is its normal state; the second decision
  must be a no-op rather than a second email.
- **Claim before send, revalidate after the claim.** `pending → claimed` is an `UPDATE … WHERE status =
  'pending'` with a row-count check, so two workers sweeping at the same moment cannot both deliver the
  same reminder. Then the reason is re-checked: a warning about an assessment the learner has since
  submitted is settled as `skipped` with the note `already submitted`, and nothing is sent. A reminder
  written on Monday must not tell a learner to submit on Thursday if they submitted on Tuesday.

Who hears from us is a decision the learner made: only verified, active accounts **with a study plan** get
nudges, and a deadline warning only goes to a class member who has not submitted. Not every learner gets
mail, because "engagement" is not a reason to email someone who never asked.

Proven by `TestAReminderIsPlannedOnceAndSentOnce`, `TestADeadlineReminderIsWithdrawnWhenTheLearnerSubmits`,
`TestTwoSweepsDoNotSendTheSameReminderTwice`, `TestRemindersAreStaffOnly`,
`TestALearnerWithoutAPlanIsNotNudged`, and the `reminders` section of the smoke run.

**Not done, and not claimed:** SMS and push channels (no provider wired; `notify.Channel` already has the
seam), digests, bounce and complaint feedback loops, per-tenant sender identities, per-learner quiet hours
or an unsubscribe preference (the study nudge is the first message anyone might want to turn off, and there
is no way to yet), and no scheduling of reminders outside the two kinds above.

---

## Recommended next three steps

1. ~~Alerting on the things that are now measurable.~~ **Done.** `/metrics`, fourteen alert rules and a CI
   check that every rule references a real metric. What is left is deployment: run Prometheus and route the
   `severity: page` rules somewhere a person will see them.
2. **A real refund adapter (Paystack).** Refunds are now recorded correctly but move no money. Wiring the
   provider call, and marking a refund `pending` until the provider confirms it, is the next honest step —
   and it is the difference between a ledger that describes money and one that describes intentions.
3. **Phase 9/20: an operations console and accessibility pass.** The approval queue, moderation, refunds
   and reminders are API-complete but only partly surfaced, and nothing has been checked with a screen
   reader.

## Feature completion checklist

Applied to email/phone verification as a worked example of the standard:

| # | Condition | Verification |
| --- | --- | --- |
| 1 | Requirements and acceptance criteria documented | yes — blueprint Phase 7 + contract |
| 2 | UX covers loading/empty/error/success | yes — settings card with sending, sent, error, verified states |
| 3 | Contract documented and validated | yes — two routes, CI-checked |
| 4 | Server-side authn/authz | yes — authenticated endpoints, codes bound to user + channel |
| 5 | DB constraints and migration | yes — 0002 on both dialects, CHECK on channel |
| 6 | Input validation, safe errors | yes — one error shape, no code or destination leakage |
| 7 | Idempotency and concurrency defined | yes — single-use claimed by affected-row count |
| 8 | Unit and integration tests pass | yes — 7 API tests + phone/mask unit tests, both engines |
| 9 | Web and mobile integration | web: yes; **mobile: not yet** (API and shared client are ready) |
| 10 | Accessibility checked | automated: contrast maths against the tokens, axe structure, focus-outline scan; **manual: not done** — no screen-reader or keyboard-only pass |
| 11 | Logs, metrics, audit events | logs yes; no metric, no audit stream |
| 12 | Documentation updated | yes — README, ARCHITECTURE, API, PHASE_STATUS, contract |
| 13 | QA acceptance and release owner approval | no |
