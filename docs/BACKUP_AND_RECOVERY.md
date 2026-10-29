# Backup and recovery

Phase 5's exit gate is "a restore has been demonstrated". `scripts/restore-drill.py` met the narrow
reading of it: the database is destroyed and comes back. This document covers the wider question a backup
exists to answer — **which backup, kept for how long, and can we get back to 14:07 rather than to
midnight?**

Three capabilities, three different guarantees:

| Capability | What it answers | Bounded by |
| --- | --- | --- |
| Nightly logical dump (`scripts/backup.py`) | "A disk died. Get us yesterday." | RPO = the schedule interval (24 h) |
| WAL archiving + base backups (`scripts/pitr-drill.py` demonstrates it) | "A deploy corrupted rows at 14:07. Get us to 14:06." | RPO = `archive_timeout` (seconds), not the dump schedule |
| Verification (`--verify`, both drills) | "Is the file we are about to restore actually good?" | Runs on every backup, not on the night we need it |

## What runs

```bash
# A backup, with retention and an off-site copy that is read back and checksummed.
python3 scripts/backup.py --engine postgres --dsn "$DSN" \
    --dest /var/backups/edumart --mirror /mnt/offsite/edumart \
    --keep-daily 7 --keep-weekly 4

# Restore the newest backup into a scratch database and count its rows against the manifest.
python3 scripts/backup.py --verify --dest /var/backups/edumart

# What do we have?
python3 scripts/backup.py --list --dest /var/backups/edumart
```

Schedules live in `infra/backup/`:

| Target | File | Notes |
| --- | --- | --- |
| systemd | `edumart-backup.service` + `.timer` | Nightly 02:17 UTC, `Persistent=true` so a machine that was off runs the backup when it returns |
| Kubernetes | `k8s-cronjob.yaml` | Same schedule, `concurrencyPolicy: Forbid`, and the verify step is part of the job |

Both run `--verify` as part of the schedule. A schedule that only dumps is a schedule that has never been
read back.

## Why the design looks like this

**The snapshot is taken while the server is serving.** SQLite uses `VACUUM INTO`, which produces a
transactionally consistent copy; `cp` of a live SQLite file can capture a torn page plus half a WAL and
restores to a corrupt database on the worst possible day. PostgreSQL uses `pg_dump --format=custom`, which
is consistent without a maintenance window and still allows a single table to be restored.

**Retention is two windows, not "keep the last seven".** Daily backups cover a bad deploy; weekly ones
cover "nobody noticed for a fortnight". A policy that only keeps the last N files loses the monthly
restore point the first time nothing runs for eight days. The rule also cannot delete the newest backup
under any setting, including `--keep-daily 0 --keep-weekly 0` — a retention policy that can delete the
only copy is a data-loss bug wearing a policy's clothes.

**Verification is against the backup's own claim.** Every backup records its per-table row counts and a
SHA-256 in `manifest.json`. `--verify` restores into a scratch database and compares the counts to what
the manifest said at backup time, so it is a test rather than a restatement. A single flipped byte is
caught on the checksum **before** a restore is attempted.

**Off-site means copied and re-read.** `--mirror` copies to a second destination and re-reads the
checksum there. The script can prove the bytes arrived; only the operator knows whether that mount is
another machine. Pointing `--mirror` at a directory on the same disk gives you two copies of the same bad
disk, and this document will not call that off-site.

## Point-in-time recovery

Demonstrated by `scripts/pitr-drill.py`, which CI runs: it builds a real cluster with `wal_level=replica`
and an `archive_command`, migrates and seeds with the real API binary, takes a `pg_basebackup`, records a
recovery target, makes a deliberate mistake after it, **deletes the data directory**, then rebuilds from
the base backup, replays WAL up to the target, promotes, and asserts that the account created before the
target exists and the one created after it does not.

The assertion that matters is the negative one: if the post-target account survives, the recovery target
was ignored and "PITR" is a word being used without the capability.

### Production configuration

```conf
# postgresql.conf — the part that is easy to get subtly wrong.
wal_level = replica
archive_mode = on                      # requires a restart, not just a reload
# test ! -f first: an archive command that can overwrite a stored segment is how a corrupt segment
# silently replaces a good one.
archive_command = 'test ! -f /mnt/offsite/edumart-wal/%f && cp %p /mnt/offsite/edumart-wal/%f'
archive_timeout = 60                   # bounds RPO between base backups; 60s, not 5 min
wal_keep_size = 2GB                    # enough that a base backup can always stream its own WAL
max_wal_senders = 4
```

Ship the archive off the database host — an archive on the same disk as the data directory is not a
backup. For object storage, `archive_command` becomes something like
`aws s3 cp %p s3://edumart-wal/%f` (or `rclone copyto`), and the restore command becomes the reverse.

Also take a **weekly `pg_basebackup`**: WAL alone cannot start a restore, only extend one.

```bash
pg_basebackup -D /mnt/offsite/edumart-base/$(date -u +%Y%m%dT%H%M%SZ) -Ft -z -X stream -h /var/run/postgresql
```

### Restoring to a point in time

1. Stop the API. Make the database unreachable so nobody writes into the recovery.
2. Extract the newest base backup that predates the incident:
   `mkdir /var/lib/postgresql/16/restore && tar -xzf base.tar.gz -C … && tar -xzf pg_wal.tar.gz -C …/pg_wal`
3. `chmod 0700` the data directory — PostgreSQL refuses one anyone else could read.
4. Append to `postgresql.auto.conf`:
   ```conf
   restore_command = 'aws s3 cp s3://edumart-wal/%f %p'
   recovery_target_time = '2026-10-29 14:06:30 UTC'
   recovery_target_action = 'promote'
   ```
5. `touch recovery.signal` (PostgreSQL 12+; there is no `recovery.conf` any more).
6. Start PostgreSQL, confirm `SELECT pg_is_in_recovery()` returns `f`, then spot-check the data **before**
   pointing the API at it: the whole point is that the last minute is wrong.
7. Point the API at the recovered cluster, run `python3 scripts/backup.py --verify --dest …` against the
   *new* backups directory, and only then resume traffic.

## Recording that it ran

A schedule that fails quietly is worse than no schedule, because it is believed. The backup job writes a
heartbeat on every run:

```bash
python3 scripts/backup.py --engine postgres --dsn "$DSN" --dest /var/backups/edumart \
    --heartbeat /var/lib/edumart/backup-heartbeat.json
python3 scripts/backup.py --verify --dest /var/backups/edumart \
    --heartbeat /var/lib/edumart/backup-heartbeat.json
```

The first write says `"verifiedAt": false`; only the restore-and-compare step flips it. The API is pointed at
the file with `EDUMART_BACKUP_HEARTBEAT` and re-reads it on every scrape, so the freshness of the backup and
the freshness of the database are answered by the same request. A missing file is not an error the API
reports — it means "no backup has ever been recorded", which is what the `absent()` alert says, in words a
person can act on.

## Targets, stated honestly

| | Target | Status |
| --- | --- | --- |
| RPO, dumps only | 24 h | Achieved by the nightly schedule |
| RPO, with WAL archiving | ≤ 60 s (`archive_timeout`) | Configuration above; replay demonstrated in CI |
| RTO, small database | < 30 min | The drill restores in seconds; the runbook (fetch, extract, replay, verify, repoint) is the real cost |
| RTO at scale | Unmeasured | **Not measured.** No drill has run against a production-sized dataset |

## What is still missing

Named so nobody assumes otherwise:

- ~~No alerting on backup failure.~~ **Closed.** `backup.py --heartbeat` records each run where the API
  reads it, and `/metrics` publishes `edumart_backup_age_seconds`, `edumart_backup_rows` and
  `edumart_backup_last_run_succeeded`. Three rules in `infra/observability/alerts.yml` fire on a missing
  heartbeat, an age over 36 hours, and a run that was never verified. See
  [`OBSERVABILITY.md`](./OBSERVABILITY.md#backup-stale). What remains is delivery: routing Alertmanager to a
  phone is deployment configuration, not code, and is not in this repository.
- **No encryption at rest by this tooling.** Backups rely on volume or object-store encryption; the dumps
  themselves are not encrypted with a key we manage.
- **No cross-region replication.** `--mirror` copies once, to one destination.
- **No automated *scheduled* verification against production data.** `--verify` restores into a scratch
  database on the same server; it proves the dump is complete, not that a fresh host could rebuild from it.
- **Secrets are not in the backup.** `EDUMART_WEBHOOK_SECRET`, the SMTP password and the verification
  secret live in the environment. Restoring the database without them restores the data and loses the
  ability to verify a webhook — keep them in a secret manager with its own backup, or a restore is only
  half a restore.
- **No retention test against the real destination.** Retention is asserted in the drill against a
  synthetic manifest; the production directory's growth is unmonitored.

## How this is tested

| Claim | Where |
| --- | --- |
| The database survives its own destruction (both engines) | `scripts/restore-drill.py`, CI job *Restore drill* |
| A live-backup restores every row and then serves a login | `scripts/backup-drill.py`, claim 1 |
| A single flipped byte is refused before any restore | `scripts/backup-drill.py`, claim 2 |
| Retention keeps the newest copy under every setting | `scripts/backup-drill.py`, claim 3 |
| The off-site copy is byte-identical and carries its own manifest | `scripts/backup-drill.py`, claim 4 |
| Recovery to a chosen second, including the mistake being gone | `scripts/pitr-drill.py`, CI job *Restore drill* |
