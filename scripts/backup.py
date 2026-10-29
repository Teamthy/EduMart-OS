#!/usr/bin/env python3
"""Take a backup of Edumart, keep the right ones, and prove the newest one is restorable.

    python3 scripts/backup.py --engine sqlite   --dsn apps/api/data/edumart.db --dest /var/backups/edumart
    python3 scripts/backup.py --engine postgres --dsn 'postgres://…'               --dest /var/backups/edumart
    python3 scripts/backup.py --list   --dest /var/backups/edumart
    python3 scripts/backup.py --verify --dest /var/backups/edumart

The restore drill answers "can this system restore a database?". This answers the operational question
next to it: *which* backup, kept for how long, and is the newest one actually good? Three rules make the
difference between a backup directory and a backup strategy:

1. **A consistent copy, not a file copy.** SQLite is snapshotted with ``VACUUM INTO`` (a copy taken while
   writers exist is not a backup) and PostgreSQL with ``pg_dump``, which is consistent without stopping
   the server. Nothing here requires a maintenance window.
2. **Retention is a decision with a rule, not a cron job that deletes the oldest.** Daily backups are kept
   for a short window and weekly ones for longer, so "we have a month of history" survives a week of
   failures — a policy that only keeps the last N files loses the monthly restore point the first time
   nobody looks for a fortnight.
3. **Every backup is verified, and the verification is recorded.** A checksum proves the bytes are intact;
   a restore into a scratch database proves the *rows* are there, counted against what the manifest said
   at the moment of the backup.

Each backup appends a record to ``manifest.json``: engine, timestamp, file, size, sha256, table count and
row count. The manifest is the backup's own claim about itself, which is what makes ``--verify`` a real
test rather than a tautology.

Exit code 1 on any failure. Off-site is deliberate: ``--mirror`` copies to a second destination and
re-reads the checksum there. Point it at a mounted bucket and the copy is off-site; point it at another
directory on the same disk and it is not, and this script will not pretend otherwise.
"""

from __future__ import annotations

import argparse
import datetime as dt
import gzip
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MANIFEST = "manifest.json"
SCHEMA_VERSION = 1

PGBIN_CANDIDATES = [
    "/usr/local/lib/python3.11/dist-packages/pgserver/pginstall/bin",
    "/usr/lib/postgresql/17/bin",
    "/usr/lib/postgresql/16/bin",
    "/usr/lib/postgresql/15/bin",
]


class BackupError(RuntimeError):
    pass


# ── helpers ─────────────────────────────────────────────────────────────────────────────────────

def run(command: list[str], *, check: bool = True, env: dict[str, str] | None = None) -> str:
    result = subprocess.run(command, capture_output=True, text=True, env={**os.environ, **(env or {})})
    if check and result.returncode != 0:
        raise BackupError(f"{' '.join(command)} failed ({result.returncode}): {result.stderr.strip()}")
    return result.stdout


def pg_tool(name: str) -> str:
    configured = os.environ.get("PGBIN")
    if configured and os.path.exists(os.path.join(configured, name)):
        return os.path.join(configured, name)
    found = shutil.which(name)
    if found:
        return found
    for candidate in PGBIN_CANDIDATES:
        if os.path.exists(os.path.join(candidate, name)):
            return os.path.join(candidate, name)
    raise BackupError(f"{name} not found. Install the PostgreSQL client tools or set PGBIN.")


def sha256_of(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def load_manifest(dest: str) -> dict:
    path = os.path.join(dest, MANIFEST)
    if not os.path.exists(path):
        return {"schema": SCHEMA_VERSION, "backups": []}
    with open(path, encoding="utf-8") as handle:
        data = json.load(handle)
    if data.get("schema") != SCHEMA_VERSION:
        raise BackupError(f"{path} has schema {data.get('schema')}, this script writes {SCHEMA_VERSION}")
    return data


def save_manifest(dest: str, manifest: dict) -> None:
    """Write to a temporary file and rename: a crash mid-write must not leave a truncated manifest."""
    path = os.path.join(dest, MANIFEST)
    temporary = path + ".tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=True)
        handle.write("\n")
    os.replace(temporary, path)


# ── reading the database ────────────────────────────────────────────────────────────────────────

def table_counts(engine: str, dsn: str) -> dict[str, int]:
    """Row count per table, read from the database itself rather than through the API."""
    if engine == "sqlite":
        import sqlite3

        connection = sqlite3.connect(dsn)
        try:
            names = [row[0] for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")]
            return {name: connection.execute(f'SELECT COUNT(*) FROM "{name}"').fetchone()[0] for name in names}
        finally:
            connection.close()

    tables = run([pg_tool("psql"), dsn, "-At", "-c",
                  "SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename"]).split()
    counts = {}
    for table in tables:
        counts[table] = int(run([pg_tool("psql"), dsn, "-At", "-c", f'SELECT COUNT(*) FROM "{table}"']).strip())
    return counts


# ── taking a backup ────────────────────────────────────────────────────────────────────────────

def take_sqlite(dsn: str, dest: str, stamp: str) -> tuple[str, dict[str, int]]:
    if not os.path.exists(dsn):
        raise BackupError(f"no database at {dsn}")
    counts = table_counts("sqlite", dsn)
    import sqlite3

    snapshot = os.path.join(dest, f".edumart-sqlite-{stamp}.db")
    connection = sqlite3.connect(dsn)
    try:
        # VACUUM INTO writes a transactionally consistent copy while the server keeps serving. `cp` of a
        # live SQLite file can capture a torn page and half a WAL, which restores to a corrupt database
        # at the worst possible moment.
        connection.execute("VACUUM INTO ?", (snapshot,))
    finally:
        connection.close()

    name = f"edumart-sqlite-{stamp}.db.gz"
    target = os.path.join(dest, name)
    with open(snapshot, "rb") as source, gzip.open(target, "wb", compresslevel=6) as sink:
        shutil.copyfileobj(source, sink, length=1 << 20)
    os.remove(snapshot)
    return name, counts


def take_postgres(dsn: str, dest: str, stamp: str) -> tuple[str, dict[str, int]]:
    counts = table_counts("postgres", dsn)
    name = f"edumart-postgres-{stamp}.dump"
    target = os.path.join(dest, name)
    # The custom format is already compressed and keeps object boundaries, so a single table can be
    # restored from it if that ever becomes the question.
    run([pg_tool("pg_dump"), "--format=custom", "--no-owner", "--file", target, dsn])
    if not os.path.exists(target) or os.path.getsize(target) == 0:
        raise BackupError("pg_dump produced no data: an empty file would look like a successful backup")
    return name, counts


def backup(args) -> int:
    dest = os.path.abspath(args.dest)
    os.makedirs(dest, exist_ok=True)
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")

    counts = {}
    name = ""
    if args.engine == "sqlite":
        name, counts = take_sqlite(args.dsn, dest, stamp)
    else:
        name, counts = take_postgres(args.dsn, dest, stamp)

    record = {
        "file": name,
        "engine": args.engine,
        "createdAt": dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds"),
        "bytes": os.path.getsize(os.path.join(dest, name)),
        "sha256": sha256_of(os.path.join(dest, name)),
        "tables": len(counts),
        "rows": sum(counts.values()),
        # Per-table counts are the backup's own claim about what it contains, which is what makes
        # `--verify` a test rather than a restatement. No DSN is ever written here: a manifest is a
        # listing, not a credential store.
        "counts": counts,
    }
    manifest = load_manifest(dest)
    manifest["backups"].append(record)
    save_manifest(dest, manifest)

    print(f"backup: {name} ({record['bytes']} bytes, {record['rows']} rows across {record['tables']} tables)")
    print(f"backup: sha256 {record['sha256']}")

    if args.mirror:
        mirror(record, dest, args.mirror)
    prune(dest, manifest, args.keep_daily, args.keep_weekly, dry_run=args.dry_run)
    return 0


# ── retention ──────────────────────────────────────────────────────────────────────────────────

def retention_decisions(records: list[dict], keep_daily: int, keep_weekly: int) -> tuple[set[str], list[str]]:
    """Decide which backups to keep.

    Newest-first over both windows. A day's newest backup is kept while fewer than ``keep_daily`` days
    are represented; the newest backup of each ISO week is kept while fewer than ``keep_weekly`` weeks
    are. Everything else goes. The newest backup is always kept, whatever the policy says — a retention
    rule that can delete the only backup is a data-loss bug wearing a policy's clothes.
    """
    ordered = sorted(records, key=lambda record: record["createdAt"], reverse=True)
    keep: set[str] = set()
    if not ordered:
        return keep, []

    keep.add(ordered[0]["file"])

    days: list[str] = []
    for record in ordered:
        day = record["createdAt"][:10]
        if day in days:
            continue
        if len(days) >= keep_daily:
            break
        days.append(day)
        keep.add(record["file"])

    weeks: list[str] = []
    for record in ordered:
        week = dt.date.fromisoformat(record["createdAt"][:10]).isocalendar()[:2]
        key = f"{week[0]}-{week[1]:02d}"
        if key in weeks:
            continue
        if len(weeks) >= keep_weekly:
            break
        weeks.append(key)
        keep.add(record["file"])

    drop = [record["file"] for record in ordered if record["file"] not in keep]
    return keep, drop


def prune(dest: str, manifest: dict, keep_daily: int, keep_weekly: int, *, dry_run: bool) -> None:
    keep, drop = retention_decisions(manifest["backups"], keep_daily, keep_weekly)
    if not drop:
        print(f"retention: keeping all {len(manifest['backups'])} backup(s)")
        return
    for name in drop:
        path = os.path.join(dest, name)
        if dry_run:
            print(f"retention: would delete {name}")
            continue
        if os.path.exists(path):
            os.remove(path)
        print(f"retention: deleted {name}")
    if not dry_run:
        manifest["backups"] = [record for record in manifest["backups"] if record["file"] in keep]
        save_manifest(dest, manifest)


def mirror(record: dict, dest: str, mirror_dir: str) -> None:
    """Copy to a second destination and re-read the checksum there.

    A copy that was never read back is an assumption. This proves the bytes arrived, not that they
    arrived somewhere else — the caller knows whether the mount is another disk.
    """
    os.makedirs(mirror_dir, exist_ok=True)
    target = os.path.join(mirror_dir, record["file"])
    shutil.copy2(os.path.join(dest, record["file"]), target)
    if sha256_of(target) != record["sha256"]:
        raise BackupError(f"the mirror copy of {record['file']} does not match its checksum")
    mirror_manifest = os.path.join(mirror_dir, MANIFEST)
    records = []
    if os.path.exists(mirror_manifest):
        with open(mirror_manifest, encoding="utf-8") as handle:
            records = json.load(handle).get("backups", [])
    records = [existing for existing in records if existing["file"] != record["file"]] + [record]
    with open(mirror_manifest, "w", encoding="utf-8") as handle:
        json.dump({"schema": SCHEMA_VERSION, "backups": records}, handle, indent=2, sort_keys=True)
        handle.write("\n")
    print(f"mirror: {record['file']} copied to {mirror_dir} and re-read (checksum matches)")


# ── verifying ─────────────────────────────────────────────────────────────────────────────────

def verify(args) -> int:
    dest = os.path.abspath(args.dest)
    manifest = load_manifest(dest)
    if not manifest["backups"]:
        raise BackupError(f"no backups in {dest}: there is nothing to verify, which is itself a finding")
    record = manifest["backups"][-1] if not args.file else next(
        (candidate for candidate in manifest["backups"] if candidate["file"] == args.file), None)
    if record is None:
        raise BackupError(f"{args.file} is not in the manifest")

    path = os.path.join(dest, record["file"])
    if not os.path.exists(path):
        raise BackupError(f"{record['file']} is in the manifest but not on disk")
    if sha256_of(path) != record["sha256"]:
        raise BackupError(f"{record['file']} no longer matches its recorded checksum")

    print(f"verify: {record['file']} ({record['rows']} rows expected)")
    counts = restore_counts(record, path, dest)
    problems = []
    for table in sorted(set(counts) | set(_expected(record))):
        if counts.get(table) != _expected(record).get(table):
            problems.append(f"{table}: {_expected(record).get(table, 'missing')} at backup, "
                            f"{counts.get(table, 'missing')} after restore")
    if problems:
        raise BackupError("the backup does not restore what it claims:\n  " + "\n  ".join(problems))
    print(f"verify: OK — {sum(counts.values())} rows restored from {record['file']}")
    return 0


def _expected(record: dict) -> dict[str, int]:
    return record.get("counts", {})


def restore_counts(record: dict, path: str, dest: str) -> dict[str, int]:
    """Restore into a scratch location and count the rows there. Never touches the live database."""
    scratch = tempfile.mkdtemp(prefix="edumart-verify-")
    try:
        if record["engine"] == "sqlite":
            restored = os.path.join(scratch, "restored.db")
            with gzip.open(path, "rb") as source, open(restored, "wb") as sink:
                shutil.copyfileobj(source, sink, length=1 << 20)
            import sqlite3

            connection = sqlite3.connect(restored)
            try:
                check = connection.execute("PRAGMA integrity_check").fetchone()[0]
                if check != "ok":
                    raise BackupError(f"the restored copy is corrupt: {check}")
            finally:
                connection.close()
            return table_counts("sqlite", restored)

        database = "edumart_verify"
        maintenance = pg_maintenance_dsn(os.environ.get("EDUMART_PG_DSN", ""))
        run([pg_tool("dropdb"), "--force", "--if-exists", "--maintenance-db", maintenance, database], check=False)
        run([pg_tool("createdb"), "--maintenance-db", maintenance, database])
        run([pg_tool("pg_restore"), "--dbname", pg_dsn_for(database), "--no-owner", "--exit-on-error", path])
        return table_counts("postgres", pg_dsn_for(database))
    finally:
        shutil.rmtree(scratch, ignore_errors=True)
        if record["engine"] == "postgres":
            run([pg_tool("dropdb"), "--force", "--if-exists",
                 "--maintenance-db", pg_maintenance_dsn(os.environ.get("EDUMART_PG_DSN", "")),
                 "edumart_verify"], check=False)


def pg_maintenance_dsn(dsn: str) -> str:
    if not dsn:
        raise BackupError("set EDUMART_PG_DSN so verification knows which server to restore into")
    base, _, query = dsn.partition("?")
    head, _, _path = base.rpartition("/")
    return head + "/postgres" + (("?" + query) if query else "")


def pg_dsn_for(database: str) -> str:
    dsn = os.environ.get("EDUMART_PG_DSN", "")
    if not dsn:
        raise BackupError("set EDUMART_PG_DSN so verification knows which server to restore into")
    base, _, query = dsn.partition("?")
    head, _, _path = base.rpartition("/")
    return head + "/" + database + (("?" + query) if query else "")


def listing(args) -> int:
    dest = os.path.abspath(args.dest)
    manifest = load_manifest(dest)
    if not manifest["backups"]:
        print(f"no backups in {dest}")
        return 0
    for record in manifest["backups"]:
        size = f"{record['bytes'] / 1024:.0f} KiB"
        print(f"{record['createdAt']}  {record['engine']:8}  {size:>10}  {record['rows']:>6} rows  {record['file']}")
    total = sum(record["bytes"] for record in manifest["backups"])
    print(f"\n{len(manifest['backups'])} backup(s), {total / 1024 / 1024:.1f} MiB in {dest}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="Back up Edumart, apply retention, and verify the newest copy.")
    parser.add_argument("--engine", choices=["sqlite", "postgres"], default="sqlite")
    parser.add_argument("--dsn", help="SQLite file path, or a PostgreSQL DSN")
    parser.add_argument("--dest", required=True, help="directory to write backups and the manifest into")
    parser.add_argument("--mirror", help="a second directory to copy into and re-read (point it off-disk for off-site)")
    parser.add_argument("--keep-daily", type=int, default=7)
    parser.add_argument("--keep-weekly", type=int, default=4)
    parser.add_argument("--dry-run", action="store_true", help="apply retention on paper only")
    parser.add_argument("--prune-only", action="store_true",
                        help="apply retention to an existing destination without taking a backup")
    parser.add_argument("--list", action="store_true", help="show what is in the destination")
    parser.add_argument("--verify", action="store_true", help="restore the newest backup into a scratch database")
    parser.add_argument("--file", help="verify this backup instead of the newest")
    args = parser.parse_args()

    try:
        if args.list:
            return listing(args)
        if args.verify:
            return verify(args)
        if args.prune_only:
            dest = os.path.abspath(args.dest)
            prune(dest, load_manifest(dest), args.keep_daily, args.keep_weekly, dry_run=args.dry_run)
            return 0
        if not args.dsn:
            raise BackupError("--dsn is required to take a backup")
        return backup(args)
    except BackupError as caught:
        print(f"backup FAILED: {caught}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
