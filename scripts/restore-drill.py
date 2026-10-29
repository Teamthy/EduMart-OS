#!/usr/bin/env python3
"""Demonstrate that a backup of Edumart can actually be restored.

The point of this script is not to produce a backup. It is to *destroy the database and get the data
back*, because a backup nobody has restored is a hypothesis. Phase 5's exit gate asks for exactly that:
"a restore has been demonstrated" — and a later phase must not bypass a failed earlier gate.

The drill is deliberately end-to-end rather than unit-level:

  1. Start the real API binary against an empty database, so migrations, seeding and the server's own
     readiness check are part of the evidence.
  2. Record a fingerprint: the row count of every table plus a login, over HTTP.
  3. Stop the server and take a snapshot (``VACUUM INTO`` for SQLite, ``pg_dump`` for PostgreSQL).
  4. **Destroy the database** — delete the file, or drop and recreate the database.
  5. Restore from the snapshot and start the API again against it.
  6. Compare fingerprints, and prove the restored server accepts a login for an account created before
     the snapshot. Then stop it.

Usage:
    python3 scripts/restore-drill.py sqlite      # default
    python3 scripts/restore-drill.py postgres --dsn 'postgres://user@host/db?sslmode=disable'
    python3 scripts/restore-drill.py both --dsn '...'

Environment:
    EDUMART_API_BIN   path to a built API binary (built automatically when absent)
    PGBIN             directory holding pg_dump/pg_restore/psql/dropdb/createdb
                      (defaults to PATH, then the sandbox's pgserver install)

Exits non-zero if any step fails, including a single lost row.
"""

from __future__ import annotations

import argparse
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


class DrillError(RuntimeError):
    pass


# ── process helpers ──────────────────────────────────────────────────────────────────────────────


def run(command: list[str], *, env: dict[str, str] | None = None, check: bool = True, cwd: str | None = None) -> str:
    merged = {**os.environ, **(env or {})}
    result = subprocess.run(command, capture_output=True, text=True, env=merged, cwd=cwd)
    if check and result.returncode != 0:
        raise DrillError(f"{' '.join(command)} failed ({result.returncode}): {result.stderr.strip()}")
    return result.stdout


def api_binary() -> str:
    """Build the API the way CI does, or reuse a binary the caller already built."""
    configured = os.environ.get("EDUMART_API_BIN")
    if configured and os.path.exists(configured):
        return configured
    target = os.path.join(tempfile.gettempdir(), "edumart-api-restore-drill")
    run(["go", "build", "-o", target, "./cmd/api"], env={"GOFLAGS": "-mod=mod"}, cwd=API_DIR)
    return target


def wait_for_ready(base: str, process: subprocess.Popen, timeout: float = 60.0) -> None:
    """Wait on /readyz, never on the port: readiness proves migrations and the database are actually up.

    A server that has already exited is never going to become ready, so stop waiting immediately rather
    than burning the full timeout on a process that is gone.
    """
    deadline = time.time() + timeout
    last = "no response"
    while time.time() < deadline:
        if process.poll() is not None:
            raise DrillError(f"the API exited with status {process.returncode}")
        try:
            with urllib.request.urlopen(base + "/readyz", timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, OSError) as caught:  # the server may not be listening yet
            last = str(caught)
        time.sleep(0.4)
    raise DrillError(f"the API never became ready: {last}")


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


class Server:
    """The API binary, run against one database, with its logs kept for the failure message."""

    def __init__(self, binary: str, driver: str, dsn: str, port: int, log_path: str, notify: str = "log"):
        self.binary = binary
        self.driver = driver
        self.dsn = dsn
        self.base = f"http://127.0.0.1:{port}"
        self.log_path = log_path
        self.env = {
            "EDUMART_DRIVER": driver,
            "EDUMART_DSN": dsn,
            "PORT": str(port),
            "EDUMART_NOTIFY": notify,
            "EDUMART_ENV": "development",
            # The drill seeds the demo fixtures: a restore that brings back an empty database is not
            # a restore, and the fixture accounts are what the login check proves survives.
            "EDUMART_SEED": "true",
            "EDUMART_SEED_DEMO_USERS": "true",
        }
        self.process: subprocess.Popen | None = None
        self.log = open(log_path, "w")

    def __enter__(self) -> "Server":
        self.process = subprocess.Popen(
            [self.binary], env={**os.environ, **self.env}, stdout=self.log, stderr=subprocess.STDOUT
        )
        try:
            wait_for_ready(self.base, self.process)
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
                self.process.wait(timeout=10)
        self.log.close()

    def __exit__(self, *_exc) -> None:
        self.stop()


# ── fingerprints ────────────────────────────────────────────────────────────────────────────────


def table_counts(driver: str, dsn: str, workdir: str) -> dict[str, int]:
    """Row count of every table, read straight from the database rather than through the API.

    Reading through the API would only prove the endpoint still works; the question here is whether
    the rows came back.
    """
    if driver == "sqlite3":
        import sqlite3

        connection = sqlite3.connect(dsn)
        try:
            names = [
                row[0]
                for row in connection.execute(
                    "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
                )
            ]
            return {name: connection.execute(f'SELECT COUNT(*) FROM "{name}"').fetchone()[0] for name in names}
        finally:
            connection.close()

    dsn_sql = pg_dsn_psql(dsn)
    tables = run(
        [pg_tool("psql", workdir), dsn_sql, "-At", "-c",
         "SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename"]
    ).split()
    counts: dict[str, int] = {}
    for table in tables:
        counts[table] = int(
            run([pg_tool("psql", workdir), dsn_sql, "-At", "-c", f'SELECT COUNT(*) FROM "{table}"']).strip()
        )
    return counts


def http_fingerprint(server: Server) -> dict:
    """What a user would notice: the fixture accounts can sign in and the data endpoints answer."""
    status, body = http(server.base, "POST", "/v1/auth/login", {"email": "teacher@edumart.ng", "password": "Teacher123!"})
    if status != 200:
        raise DrillError(f"the pre-snapshot account cannot sign in: {status} {body}")
    token = body["token"]
    status, dashboard = http(server.base, "GET", "/v1/dashboard", token=token)
    if status != 200:
        raise DrillError(f"the dashboard does not answer: {status} {dashboard}")
    return {"user": body["user"]["id"], "pathway": dashboard.get("pathway") if isinstance(dashboard, dict) else None}


def compare(before: dict[str, int], after: dict[str, int]) -> list[str]:
    problems: list[str] = []
    for table in sorted(set(before) | set(after)):
        if before.get(table) != after.get(table):
            problems.append(f"{table}: {before.get(table, 'missing')} rows before, {after.get(table, 'missing')} after")
    return problems


# ── PostgreSQL plumbing ─────────────────────────────────────────────────────────────────────────


PGBIN_CANDIDATES = [
    "/usr/local/lib/python3.11/dist-packages/pgserver/pginstall/bin",
    "/usr/lib/postgresql/17/bin",
    "/usr/lib/postgresql/16/bin",
    "/usr/lib/postgresql/15/bin",
]


def pg_tool(name: str, workdir: str) -> str:
    configured = os.environ.get("PGBIN")
    if configured and os.path.exists(os.path.join(configured, name)):
        return os.path.join(configured, name)
    found = shutil.which(name)
    if found:
        return found
    for candidate in PGBIN_CANDIDATES:
        if os.path.exists(os.path.join(candidate, name)):
            return os.path.join(candidate, name)
    raise DrillError(
        f"{name} not found. Install the PostgreSQL client tools, or point PGBIN at a directory that has them."
    )


def pg_dsn_psql(dsn: str) -> str:
    """psql/pg_dump want a libpq URL; the Go driver accepts the same shape, so pass it through."""
    return dsn


def pg_maintenance_dsn(dsn: str) -> str:
    """DROP DATABASE cannot run from the database being dropped, so connect to 'postgres' instead.

    The query string carries host, port and TLS settings, so it has to survive the rewrite.
    """
    base, _, query = dsn.partition("?")
    head, _, _path = base.rpartition("/")
    return head + "/postgres" + (("?" + query) if query else "")


# ── the drill ───────────────────────────────────────────────────────────────────────────────────


def drill_sqlite(binary: str, workdir: str, port: int) -> None:
    database = os.path.join(workdir, "restored.db")
    backup = os.path.join(workdir, "snapshot.db")
    dsn = database

    print("sqlite: start the API on an empty database and seed it")
    with Server(binary, "sqlite3", dsn, port, os.path.join(workdir, "before.log")) as server:
        # The server is stopped before the snapshot so nothing writes mid-copy. In production that is
        # what a maintenance window — or WAL archiving, which this project does not have yet — buys.
        before_identity = http_fingerprint(server)
    before = table_counts("sqlite3", dsn, workdir)
    print(f"sqlite: {sum(before.values())} rows across {len(before)} tables; {before_identity}")

    print("sqlite: snapshot with VACUUM INTO (a consistent copy, unlike cp)")
    import sqlite3

    connection = sqlite3.connect(dsn)
    try:
        connection.execute("VACUUM INTO ?", (backup,))
    finally:
        connection.close()
    if not os.path.exists(backup):
        raise DrillError("VACUUM INTO produced no file")

    print("sqlite: destroy the database")
    for suffix in ("", "-wal", "-shm"):
        path = dsn + suffix
        if os.path.exists(path):
            os.remove(path)
    if os.path.exists(dsn):
        raise DrillError("the database file is still there; the drill would prove nothing")

    print("sqlite: restore it")
    shutil.copyfile(backup, dsn)
    after = table_counts("sqlite3", dsn, workdir)
    problems = compare(before, after)
    if problems:
        raise DrillError("restored data differs:\n  " + "\n  ".join(problems))

    print("sqlite: the restored database serves requests")
    with Server(binary, "sqlite3", dsn, port, os.path.join(workdir, "after.log")) as server:
        after_identity = http_fingerprint(server)
    if after_identity["user"] != before_identity["user"]:
        raise DrillError("the restored database has a different account behind the same email")
    print(f"sqlite: OK — {sum(after.values())} rows restored, login works, /readyz healthy")


def drill_postgres(binary: str, workdir: str, port: int, dsn: str) -> None:
    database = os.path.join(workdir, "snapshot.dump")
    maintenance = pg_maintenance_dsn(dsn)
    sql = pg_dsn_psql(dsn)

    print("postgres: recreate the target from nothing")
    reset_pg_database(dsn, workdir)

    print("postgres: migrations and seed data on the target database")
    with Server(binary, "postgres", dsn, port, os.path.join(workdir, "before.log")) as server:
        before_identity = http_fingerprint(server)
    before = table_counts("postgres", dsn, workdir)
    print(f"postgres: {sum(before.values())} rows across {len(before)} tables; {before_identity}")

    print("postgres: pg_dump the database")
    run([pg_tool("pg_dump", workdir), sql, "--format=custom", "--no-owner", "--file", database])
    if not os.path.exists(database) or os.path.getsize(database) == 0:
        raise DrillError("pg_dump produced no data")

    print("postgres: drop the database")
    run([pg_tool("dropdb", workdir), "--force", "--if-exists", "--maintenance-db", maintenance, dsn_name(dsn)])
    counts_after_drop = run(
        [pg_tool("psql", workdir), maintenance, "-At", "-c",
         f"SELECT COUNT(*) FROM pg_database WHERE datname = '{dsn_name(dsn)}'"]
    ).strip()
    if counts_after_drop != "0":
        raise DrillError("the database was not actually dropped")

    print("postgres: recreate and restore")
    run([pg_tool("createdb", workdir), "--maintenance-db", maintenance, dsn_name(dsn)])
    run([pg_tool("pg_restore", workdir), "--dbname", sql, "--no-owner", "--exit-on-error", database])

    after = table_counts("postgres", dsn, workdir)
    problems = compare(before, after)
    if problems:
        raise DrillError("restored data differs:\n  " + "\n  ".join(problems))

    print("postgres: the restored database serves requests")
    with Server(binary, "postgres", dsn, port, os.path.join(workdir, "after.log")) as server:
        after_identity = http_fingerprint(server)
    if after_identity["user"] != before_identity["user"]:
        raise DrillError("the restored database has a different account behind the same email")
    print(f"postgres: OK — {sum(after.values())} rows restored, login works, /readyz healthy")


def reset_pg_database(dsn: str, workdir: str) -> None:
    """Start from an empty database, so "the data came back" cannot mean "the data was never gone"."""
    maintenance = pg_maintenance_dsn(dsn)
    name = dsn_name(dsn)
    run([pg_tool("dropdb", workdir), "--force", "--if-exists", "--maintenance-db", maintenance, name])
    run([pg_tool("createdb", workdir), "--maintenance-db", maintenance, name])


def dsn_name(dsn: str) -> str:
    base, _, _query = dsn.partition("?")
    return base.rpartition("/")[2]


def main() -> int:
    parser = argparse.ArgumentParser(description="Destroy a database and prove the backup brings it back.")
    parser.add_argument("engine", nargs="?", default="sqlite", choices=["sqlite", "postgres", "both"])
    parser.add_argument("--dsn", help="PostgreSQL DSN; a throwaway database is created and destroyed")
    parser.add_argument("--port", type=int, default=8095)
    parser.add_argument("--keep", action="store_true", help="keep the working directory for inspection")
    args = parser.parse_args()

    workdir = tempfile.mkdtemp(prefix="edumart-restore-")
    print(f"working directory: {workdir}\n")
    try:
        binary = api_binary()
        if args.engine in ("sqlite", "both"):
            drill_sqlite(binary, workdir, args.port)
            print()
        if args.engine in ("postgres", "both"):
            if not args.dsn:
                raise DrillError("the postgres drill needs --dsn pointing at a throwaway database")
            drill_postgres(binary, workdir, args.port + 1, args.dsn)
        print("\nrestore drill passed: the database was destroyed and the data came back")
        return 0
    except DrillError as caught:
        print(f"\nrestore drill FAILED: {caught}", file=sys.stderr)
        if args.keep:
            print(f"logs kept in {workdir}", file=sys.stderr)
        return 1
    finally:
        if not args.keep:
            shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
