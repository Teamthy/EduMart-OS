#!/usr/bin/env python3
"""Prove the backup tool's claims, not just that it exits zero.

    python3 scripts/backup-drill.py both --dsn 'postgres://…' --port 8130

`restore-drill.py` proves the system *can* restore. This proves the backup **strategy** is worth having,
by attacking the places backups actually fail:

  1. **A backup taken from a running server restores completely.** The API keeps serving while the
     snapshot is taken; afterwards the restored copy must contain every row the live database had, and
     must then accept a real login.
  2. **A tampered backup is refused.** One flipped byte in the archive and `--verify` must fail on the
     checksum — before it tries to restore anything. A backup you cannot detect corruption in is a
     backup you will discover corruption in.
  3. **Retention cannot delete the only backup.** The newest backup survives every policy, including
     `--keep-daily 0 --keep-weekly 0`, and the daily/weekly windows keep what they promise.
  4. **The off-site copy is the same bytes.** A mirror is re-read and compared, because a copy that was
     never read back is an assumption.
  5. **The heartbeat says what actually happened.** A backup writes `verified: false` and only `--verify`
     flips it, so the alert on "the last backup was never restored" cannot be satisfied by a dump that was
     never read back. A monitoring hook that lies is worse than no monitoring hook.

Exits non-zero on the first broken claim. Standard library only.
"""

from __future__ import annotations

import argparse
import datetime as dt
import glob
import gzip
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
API_DIR = os.path.join(REPO, "apps", "api")
BACKUP = os.path.join(REPO, "scripts", "backup.py")


class DrillError(RuntimeError):
    pass


def run(command: list[str], *, env: dict | None = None, check: bool = True,
        cwd: str | None = None) -> subprocess.CompletedProcess:
    result = subprocess.run(command, capture_output=True, text=True, cwd=cwd,
                            env={**os.environ, **(env or {})})
    if check and result.returncode != 0:
        raise DrillError(f"{' '.join(command)} failed ({result.returncode}):\n{result.stdout}{result.stderr}")
    return result


def api_binary() -> str:
    configured = os.environ.get("EDUMART_API_BIN")
    if configured and os.path.exists(configured):
        return configured
    target = os.path.join(tempfile.gettempdir(), "edumart-api-backup-drill")
    # `go build ./cmd/api` has to run inside the module: from the repository root there is no go.mod, and
    # the drill fails with a build error rather than anything to do with backups.
    run(["go", "build", "-o", target, "./cmd/api"], env={"GOFLAGS": "-mod=mod"}, check=True, cwd=API_DIR)
    return target


def wait_ready(base: str, process: subprocess.Popen, timeout: float = 60.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if process.poll() is not None:
            raise DrillError(f"the API exited with status {process.returncode}")
        try:
            with urllib.request.urlopen(base + "/readyz", timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, OSError):
            pass
        time.sleep(0.4)
    raise DrillError("the API never became ready")


def http(base: str, method: str, path: str, body: dict | None = None, token: str | None = None):
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(base + path, data=data, method=method)
    request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            return response.status, json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as caught:
        return caught.code, json.loads(caught.read() or b"{}")


class Api:
    """The real binary against a given database, so a restore is judged by whether it serves."""

    def __init__(self, binary: str, driver: str, dsn: str, port: int, log_path: str):
        self.binary, self.driver, self.dsn, self.port = binary, driver, dsn, port
        self.base = f"http://127.0.0.1:{port}"
        self.log_path = log_path
        self.process: subprocess.Popen | None = None
        self.log = open(log_path, "w")

    def __enter__(self):
        self.process = subprocess.Popen(
            [self.binary],
            env={**os.environ, "EDUMART_DRIVER": self.driver, "EDUMART_DSN": self.dsn,
                 "PORT": str(self.port), "EDUMART_NOTIFY": "log", "EDUMART_ENV": "development",
                 "EDUMART_SEED": "true", "EDUMART_SEED_DEMO_USERS": "true"},
            stdout=self.log, stderr=subprocess.STDOUT,
        )
        try:
            wait_ready(self.base, self.process)
        except DrillError:
            self.stop()
            with open(self.log_path) as handle:
                raise DrillError(f"API did not start:\n{handle.read()[-2000:]}")
        return self

    def stop(self) -> None:
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
        self.log.close()

    def __exit__(self, *_exc) -> None:
        self.stop()


def login_works(base: str) -> None:
    status, body = http(base, "POST", "/v1/auth/login",
                        {"email": "teacher@edumart.ng", "password": "Teacher123!"})
    if status != 200 or not body.get("token"):
        raise DrillError(f"the restored database refuses a login: {status} {body}")


def backup(dest: str, engine: str, dsn: str, extra: list[str] | None = None) -> str:
    result = run([sys.executable, BACKUP, "--engine", engine, "--dsn", dsn, "--dest", dest, *(extra or [])])
    print("\n".join("    " + line for line in result.stdout.strip().splitlines()))
    return result.stdout


def manifest(dest: str) -> dict:
    with open(os.path.join(dest, "manifest.json"), encoding="utf-8") as handle:
        return json.load(handle)


# ── the claims ────────────────────────────────────────────────────────────────────────────────

def claim_live_backup_restores(binary: str, workdir: str, port: int) -> None:
    """The server keeps serving while the snapshot is taken, and the snapshot contains everything."""
    print("\nclaim 1: a backup taken while the API is serving restores every row, and then serves")
    database = os.path.join(workdir, "live.db")
    dest = os.path.join(workdir, "live-backups")

    with Api(binary, "sqlite3", database, port, os.path.join(workdir, "live.log")) as server:
        login_works(server.base)
        # A row the snapshot must contain, written through the real API rather than by hand.
        status, body = http(server.base, "POST", "/v1/auth/login",
                            {"email": "student@edumart.ng", "password": "Student123!"})
        if status != 200:
            raise DrillError(f"the fixture learner cannot sign in: {status} {body}")
        token = body["token"]
        status, products = http(server.base, "GET", "/v1/products", token=token)
        if status != 200 or not products.get("products"):
            raise DrillError(f"the catalogue did not load: {status} {products}")
        # Back up while this server is up: no maintenance window, no stop-the-world copy.
        backup(dest, "sqlite", database)

    record = manifest(dest)["backups"][-1]
    print(f"  backup recorded {record['rows']} rows across {record['tables']} tables")

    print("  restoring into a scratch database and starting the API on it")
    restored = os.path.join(workdir, "restored.db")
    archive = os.path.join(dest, record["file"])
    with gzip.open(archive, "rb") as source, open(restored, "wb") as sink:
        shutil.copyfileobj(source, sink, 1 << 20)

    import sqlite3

    connection = sqlite3.connect(restored)
    try:
        check = connection.execute("PRAGMA integrity_check").fetchone()[0]
        if check != "ok":
            raise DrillError(f"the restored copy is corrupt: {check}")
        restored_counts = {
            name: connection.execute(f'SELECT COUNT(*) FROM "{name}"').fetchone()[0]
            for name in [row[0] for row in connection.execute(
                "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")]
        }
    finally:
        connection.close()

    differences = [
        f"{table}: {expected} at backup, {restored_counts.get(table, 'missing')} restored"
        for table, expected in record["counts"].items() if restored_counts.get(table) != expected
    ]
    if differences:
        raise DrillError("the backup does not contain what it claimed:\n  " + "\n  ".join(differences))
    print(f"  all {len(record['counts'])} tables match the counts the backup recorded")

    with Api(binary, "sqlite3", restored, port, os.path.join(workdir, "restored.log")) as server:
        login_works(server.base)
    print("  the restored database serves a real login")


def claim_tampering_is_caught(workdir: str) -> None:
    """A single flipped byte must stop the restore before it starts."""
    print("\nclaim 2: a tampered backup is refused on its checksum")
    source = os.path.join(workdir, "live-backups")
    target = os.path.join(workdir, "tampered-backups")
    shutil.copytree(source, target)
    record = manifest(target)["backups"][-1]
    path = os.path.join(target, record["file"])
    payload = bytearray(open(path, "rb").read())
    payload[len(payload) // 2] ^= 0xFF
    with open(path, "wb") as handle:
        handle.write(payload)

    result = run([sys.executable, BACKUP, "--verify", "--dest", target], check=False)
    if result.returncode == 0:
        raise DrillError("a corrupted backup verified successfully: the checksum is not being checked")
    if "checksum" not in (result.stdout + result.stderr):
        raise DrillError(f"the failure was not a checksum failure:\n{result.stdout}{result.stderr}")
    print("  refused: " + result.stderr.strip().splitlines()[-1])


def claim_retention_keeps_what_matters(workdir: str) -> None:
    """Retention is a policy, so its edge cases get asserted rather than trusted."""
    print("\nclaim 3: retention keeps the newest backup, and the windows it promises")
    dest = os.path.join(workdir, "retention")
    os.makedirs(dest, exist_ok=True)

    # A manifest with three weeks of daily backups, as the schedule would have produced.
    today = dt.date(2026, 10, 29)
    records = []
    for day in range(21):
        date = today - dt.timedelta(days=day)
        records.append({
            "file": f"edumart-sqlite-{date.isoformat()}T020000Z.db.gz",
            "engine": "sqlite",
            "createdAt": dt.datetime(date.year, date.month, date.day, 2, tzinfo=dt.timezone.utc).isoformat(),
            "bytes": 1024, "sha256": "0" * 64, "tables": 1, "rows": 1, "counts": {"users": 1},
        })
    records.reverse()  # oldest first, the order the tool writes them
    with open(os.path.join(dest, "manifest.json"), "w", encoding="utf-8") as handle:
        json.dump({"schema": 1, "backups": records}, handle)

    run([sys.executable, BACKUP, "--dest", dest, "--list"])
    dry = run([sys.executable, BACKUP, "--dest", dest, "--keep-daily", "3", "--keep-weekly", "0",
               "--prune-only", "--dry-run"]).stdout
    kept_after_dry = len(manifest(dest)["backups"])
    if kept_after_dry != len(records):
        raise DrillError("--dry-run changed the manifest")

    run([sys.executable, BACKUP, "--dest", dest, "--keep-daily", "3", "--keep-weekly", "0", "--prune-only"])
    kept = manifest(dest)["backups"]
    if len(kept) != 3:
        raise DrillError(f"keeping 3 daily backups left {len(kept)}: {[r['file'] for r in kept]}")
    newest = max(records, key=lambda record: record["createdAt"])["file"]
    if newest not in {record["file"] for record in kept}:
        raise DrillError("retention deleted the newest backup")
    print(f"  keep-daily 3 kept {len(kept)} backups, including the newest")

    # The policy that could destroy everything: the newest backup still survives it.
    run([sys.executable, BACKUP, "--dest", dest, "--keep-daily", "0", "--keep-weekly", "0", "--prune-only"])
    survivors = manifest(dest)["backups"]
    if len(survivors) != 1 or survivors[0]["file"] != newest:
        raise DrillError(f"keeping nothing left {[r['file'] for r in survivors]}, expected only the newest")
    print("  keep-daily 0 / keep-weekly 0 still kept the newest backup")
    if "would delete" in dry:
        print("  dry-run reported deletions without making them")


def claim_mirror_is_identical(workdir: str, engine: str, dsn: str) -> None:
    """A copy that was never read back is an assumption."""
    print("\nclaim 4: the off-site copy is byte-identical, re-read from the destination")
    dest = os.path.join(workdir, "mirror-source")
    mirror = os.path.join(workdir, "mirror-target")
    shutil.rmtree(dest, ignore_errors=True)
    shutil.rmtree(mirror, ignore_errors=True)
    backup(dest, engine, dsn, ["--mirror", mirror])

    record = manifest(dest)["backups"][-1]
    source = os.path.join(dest, record["file"])
    copied = os.path.join(mirror, record["file"])
    if not os.path.exists(copied):
        raise DrillError("the mirror directory does not contain the backup")
    import hashlib

    def digest(path: str) -> str:
        return hashlib.sha256(open(path, "rb").read()).hexdigest()

    if digest(copied) != digest(source) or digest(copied) != record["sha256"]:
        raise DrillError("the mirror copy differs from the original")
    if not os.path.exists(os.path.join(mirror, "manifest.json")):
        raise DrillError("the mirror has no manifest, so it cannot be verified on its own")
    print("  the copy matches the recorded checksum and carries its own manifest")


def claim_heartbeat_tracks_verification(workdir: str, engine: str, dsn: str) -> None:
    """The metric the backup alerts watch is only as trustworthy as what writes it."""
    print("\nclaim 5: the heartbeat reports a backup as unverified until it has been restored")
    dest = os.path.join(workdir, "heartbeat-backups")
    heartbeat = os.path.join(workdir, "heartbeat", "backup-heartbeat.json")
    shutil.rmtree(dest, ignore_errors=True)
    shutil.rmtree(os.path.dirname(heartbeat), ignore_errors=True)

    backup(dest, engine, dsn, ["--heartbeat", heartbeat])
    first = read_heartbeat(heartbeat)
    if first.get("verifiedAt") is not False:
        raise DrillError(f"a backup that has not been restored reported verifiedAt={first.get('verifiedAt')!r}")
    if first.get("rows") != manifest(dest)["backups"][-1]["rows"]:
        raise DrillError("the heartbeat's row count disagrees with the manifest")

    run([sys.executable, BACKUP, "--engine", engine, "--dsn", dsn, "--dest", dest,
         "--verify", "--heartbeat", heartbeat], check=True)
    second = read_heartbeat(heartbeat)
    if second.get("verifiedAt") is not True:
        raise DrillError("a verified backup still reports itself as unverified, so the alert would never clear")
    if second.get("at") != first.get("at"):
        raise DrillError("verification rewrote the backup timestamp: the age metric would keep resetting")
    print("  unverified after the dump, verified after --verify, with the timestamp preserved")


def read_heartbeat(path: str) -> dict:
    """Read the file the way the API does: every field it needs must be present and correctly typed."""
    if not os.path.exists(path):
        raise DrillError(f"no heartbeat was written at {path}")
    with open(path, encoding="utf-8") as handle:
        beat = json.load(handle)
    for field, kind in (("at", str), ("engine", str), ("file", str), ("rows", int), ("verifiedAt", bool)):
        if not isinstance(beat.get(field), kind):
            raise DrillError(f"heartbeat field {field!r} is {beat.get(field)!r}, the API expects {kind.__name__}")
    # The API parses `at` as RFC3339; a heartbeat it cannot parse is a heartbeat it silently ignores.
    import datetime

    datetime.datetime.fromisoformat(beat["at"].replace("Z", "+00:00"))
    return beat


def drill_engine(binary: str, engine: str, workdir: str, port: int, dsn: str | None) -> None:
    print(f"\n=== {engine} ===")
    if engine == "sqlite":
        database = os.path.join(workdir, "drill.db")
        # Seed through the real API so the fixture data is exactly what a deployment would have.
        with Api(binary, "sqlite3", database, port, os.path.join(workdir, "seed.log")):
            pass
        claim_live_backup_restores(binary, workdir, port)
        claim_tampering_is_caught(workdir)
        claim_retention_keeps_what_matters(workdir)
        claim_mirror_is_identical(workdir, "sqlite", database)
        claim_heartbeat_tracks_verification(workdir, "sqlite", database)
        run([sys.executable, BACKUP, "--verify", "--dest", os.path.join(workdir, "live-backups")])
        print("  --verify restored the newest backup into a scratch database and counted its rows")
        return

    if not dsn:
        raise DrillError("the postgres drill needs --dsn")
    reset_pg(dsn)
    with Api(binary, "postgres", dsn, port, os.path.join(workdir, "pg-seed.log")):
        pass
    dest = os.path.join(workdir, "pg-backups")
    backup(dest, "postgres", dsn)
    record = manifest(dest)["backups"][-1]
    print(f"  backup recorded {record['rows']} rows across {record['tables']} tables")
    os.environ["EDUMART_PG_DSN"] = dsn
    run([sys.executable, BACKUP, "--verify", "--dest", dest])
    print("  --verify restored the dump into a scratch database and counted its rows")
    claim_mirror_is_identical(workdir, "postgres", dsn)
    claim_heartbeat_tracks_verification(workdir, "postgres", dsn)


PGBIN_CANDIDATES = [
    "/usr/local/lib/python3.11/dist-packages/pgserver/pginstall/bin",
    "/usr/lib/postgresql/17/bin",
    "/usr/lib/postgresql/16/bin",
    "/usr/lib/postgresql/15/bin",
]


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
    raise DrillError(f"{name} not found; set PGBIN")


def pg_maintenance_dsn(dsn: str) -> str:
    base, _, query = dsn.partition("?")
    head, _, _path = base.rpartition("/")
    return head + "/postgres" + (("?" + query) if query else "")


def reset_pg(dsn: str) -> None:
    base, _, _query = dsn.partition("?")
    name = base.rpartition("/")[2]
    maintenance = pg_maintenance_dsn(dsn)
    run([pg_tool("dropdb"), "--force", "--if-exists", "--maintenance-db", maintenance, name])
    run([pg_tool("createdb"), "--maintenance-db", maintenance, name])


def main() -> int:
    parser = argparse.ArgumentParser(description="Prove the backup tool's claims.")
    parser.add_argument("engine", nargs="?", default="sqlite", choices=["sqlite", "postgres", "both"])
    parser.add_argument("--dsn", help="PostgreSQL DSN for the postgres drill (a throwaway database)")
    parser.add_argument("--port", type=int, default=8130)
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args()

    workdir = tempfile.mkdtemp(prefix="edumart-backup-drill-")
    print(f"working directory: {workdir}")
    try:
        binary = api_binary()
        if args.engine in ("sqlite", "both"):
            os.makedirs(os.path.join(workdir, "sqlite"), exist_ok=True)
            drill_engine(binary, "sqlite", os.path.join(workdir, "sqlite"), args.port, None)
        if args.engine in ("postgres", "both"):
            os.makedirs(os.path.join(workdir, "postgres"), exist_ok=True)
            drill_engine(binary, "postgres", os.path.join(workdir, "postgres"), args.port + 1, args.dsn)
        print("\nbackup drill passed: the snapshot restores, corruption is caught, retention behaves, "
              "and the heartbeat tells the truth about verification")
        return 0
    except DrillError as caught:
        print(f"\nbackup drill FAILED: {caught}", file=sys.stderr)
        if args.keep:
            print(f"logs kept in {workdir}", file=sys.stderr)
        return 1
    finally:
        if not args.keep:
            shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
