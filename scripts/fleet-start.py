#!/usr/bin/env python3
"""Start a fleet of API replicas against one fresh database and prove they all come up.

This exists because the thing it checks was broken and nobody had looked: four processes starting together
against an empty PostgreSQL database each created the ledger and each ran 0001_init. PostgreSQL refused three
of them with `duplicate key value violates unique constraint "pg_type_typname_nsp_index"`, because
`CREATE TABLE IF NOT EXISTS` is not atomic against a concurrent creator. Three replicas exited with status 1
and would have crashlooped until their restart backoff happened to put them behind the winner.

A single-process test suite cannot see that, and neither can any amount of code review: it is a property of
*timing between processes*. So it is checked by starting processes.

Three scenarios, each with a fresh database:

    boot           N replicas start together, each migrating on boot. All must become ready, and every
                   migration must be recorded exactly once.
    migrate-first  `api -migrate-only`, then N replicas with EDUMART_MIGRATE_ON_BOOT=false. All must become
                   ready — this is the rollout order the runbook recommends.
    fail-closed    N replicas with EDUMART_MIGRATE_ON_BOOT=false against a database nobody migrated. None may
                   become ready, and readiness must name migrations as the reason. A replica that serves
                   traffic against a schema it does not have is worse than one that refuses.

Usage:
    python3 scripts/fleet-start.py                                # SQLite is not a fleet: use --engine postgres
    python3 scripts/fleet-start.py --dsn 'postgres://…/fleet'      # a throwaway database; its contents are destroyed
    python3 scripts/fleet-start.py --replicas 8 --port 8600

Environment:
    EDUMART_API_BIN   reuse a built binary instead of building one
    PGBIN             directory holding createdb/dropdb/psql
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


class FleetError(RuntimeError):
    pass


def run(command: list[str], *, cwd: str | None = None, env: dict | None = None) -> str:
    result = subprocess.run(command, capture_output=True, text=True, cwd=cwd, env={**os.environ, **(env or {})})
    if result.returncode != 0:
        raise FleetError(f"{' '.join(command)} failed ({result.returncode}): {(result.stderr or result.stdout).strip()}")
    return result.stdout.strip()


def api_binary() -> str:
    configured = os.environ.get("EDUMART_API_BIN")
    if configured and os.path.exists(configured):
        return configured
    target = os.path.join(tempfile.gettempdir(), "edumart-api-fleet")
    print("  building the API")
    run(["go", "build", "-o", target, "./cmd/api"], cwd=API_DIR, env={"GOFLAGS": "-mod=mod"})
    return target


def psql(dsn: str, sql: str) -> str:
    binary = shutil.which("psql") or _pgbin("psql")
    if not binary:
        raise FleetError("psql is not on PATH; install the PostgreSQL client or set PGBIN")
    return run([binary, dsn, "-tA", "-v", "ON_ERROR_STOP=1", "-c", sql])


def _pgbin(tool: str) -> str | None:
    for directory in (os.environ.get("PGBIN"), "/usr/local/lib/python3.11/dist-packages/pgserver/pginstall/bin"):
        if directory and os.path.exists(os.path.join(directory, tool)):
            return os.path.join(directory, tool)
    return None


def reset(dsn: str) -> None:
    """Drop everything: this script must never run against a database somebody cares about."""
    psql(dsn, "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;")


class Replica:
    def __init__(self, binary: str, dsn: str, index: int, port: int, workdir: str, migrate_on_boot: bool):
        self.name = f"replica-{index}"
        self.port = port
        self.base = f"http://127.0.0.1:{port}"
        self.log_path = os.path.join(workdir, f"{self.name}.log")
        self.log = open(self.log_path, "w")
        self.process = subprocess.Popen(
            [binary],
            env={
                **os.environ,
                "EDUMART_DRIVER": "postgres",
                "EDUMART_DSN": dsn,
                "PORT": str(port),
                "EDUMART_SEED": "false",  # a fleet does not seed; the fixtures belong to a developer
                "EDUMART_MIGRATE_ON_BOOT": "true" if migrate_on_boot else "false",
                "EDUMART_NOTIFY": "log",
            },
            stdout=self.log,
            stderr=subprocess.STDOUT,
        )

    def stop(self) -> None:
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        self.log.close()

    def ready(self, timeout: float = 45.0) -> bool:
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self.process.poll() is not None:
                return False
            try:
                with urllib.request.urlopen(self.base + "/readyz", timeout=2) as response:
                    if response.status == 200:
                        return True
            except urllib.error.HTTPError as caught:
                if caught.code == 503:
                    # Still starting, or permanently unready — the caller decides which by waiting.
                    pass
            except (urllib.error.URLError, OSError):
                pass
            time.sleep(0.1)
        return False

    def readiness_detail(self) -> dict:
        try:
            with urllib.request.urlopen(self.base + "/readyz", timeout=3) as response:
                return json.loads(response.read() or b"{}")
        except urllib.error.HTTPError as caught:
            try:
                return json.loads(caught.read() or b"{}")
            except Exception:  # noqa: BLE001 — a non-JSON body is itself the finding
                return {}
        except (urllib.error.URLError, OSError):
            return {}

    def log_tail(self, limit: int = 1200) -> str:
        with open(self.log_path) as handle:
            return handle.read()[-limit:]


def start_fleet(binary: str, dsn: str, replicas: int, port: int, workdir: str, migrate_on_boot: bool) -> list[Replica]:
    # The window between the first and last `Popen` is the whole point of the test, so it is kept as small as
    # possible and reported: a spread of seconds would hide a race that resolves itself.
    started = time.time()
    fleet = [
        Replica(binary, dsn, index, port + index, workdir, migrate_on_boot)
        for index in range(1, replicas + 1)
    ]
    spread = time.time() - started
    print(f"  started {len(fleet)} replicas in {spread * 1000:.0f}ms")
    return fleet


def stop_fleet(fleet: list[Replica]) -> None:
    for replica in fleet:
        replica.stop()


def ledger_duplicates(dsn: str) -> list[str]:
    rows = psql(dsn, "SELECT version FROM schema_migrations GROUP BY version HAVING count(*) > 1;")
    return [row for row in rows.splitlines() if row.strip()]


def ledger_count(dsn: str) -> int:
    return int(psql(dsn, "SELECT count(*) FROM schema_migrations;") or 0)


def scenario_boot(binary: str, dsn: str, replicas: int, port: int, workdir: str) -> None:
    print("\nscenario: every replica migrates on boot")
    reset(dsn)
    fleet = start_fleet(binary, dsn, replicas, port, workdir, migrate_on_boot=True)
    try:
        statuses = [replica.ready() for replica in fleet]
        if not all(statuses):
            failed = [replica.name for replica, ok in zip(fleet, statuses) if not ok]
            for replica in fleet:
                if replica.process.poll() is not None:
                    print(f"\n  {replica.name} exited with {replica.process.returncode}:\n{replica.log_tail()}\n")
            raise FleetError(f"replicas never became ready: {', '.join(failed)}")
        print(f"  ok  all {len(fleet)} replicas reached readiness")

        duplicates = ledger_duplicates(dsn)
        if duplicates:
            raise FleetError(f"a migration was recorded twice: {duplicates}")
        applied = ledger_count(dsn)
        if applied == 0:
            raise FleetError("no migrations were applied at all, so this proves nothing")
        print(f"  ok  {applied} migrations applied once each, no duplicates")

        # Every replica must agree about the schema. Disagreement here means two of them ran a different
        # subset, which is the beginning of an environment that cannot be reasoned about.
        details = {replica.name: replica.readiness_detail() for replica in fleet}
        for name, detail in details.items():
            migration_check = next(
                (check for check in detail.get("checks", []) if check.get("name") == "migrations"), None
            )
            if not migration_check or migration_check.get("status") != "ok":
                raise FleetError(f"{name} does not report its migrations as up to date: {migration_check}")
        print(f"  ok  every replica reports {applied} applied migrations")
    finally:
        stop_fleet(fleet)


def scenario_migrate_first(binary: str, dsn: str, replicas: int, port: int, workdir: str) -> None:
    print("\nscenario: migrate first, then start replicas that do not migrate")
    reset(dsn)
    output = run([binary, "-migrate-only"], env={
        "EDUMART_DRIVER": "postgres",
        "EDUMART_DSN": dsn,
        "EDUMART_SEED": "false",
    })
    print(f"  ok  migration job: {output.splitlines()[-1] if output else '(no output)'}")

    fleet = start_fleet(binary, dsn, replicas, port, workdir, migrate_on_boot=False)
    try:
        statuses = [replica.ready() for replica in fleet]
        if not all(statuses):
            failed = [replica.name for replica, ok in zip(fleet, statuses) if not ok]
            for replica in fleet:
                if replica.process.poll() is not None:
                    print(f"\n  {replica.name} exited with {replica.process.returncode}:\n{replica.log_tail()}\n")
            raise FleetError(f"replicas never became ready after the migration job: {', '.join(failed)}")
        print(f"  ok  all {len(fleet)} replicas reached readiness without migrating")
        if ledger_duplicates(dsn):
            raise FleetError("a migration was recorded twice")
    finally:
        stop_fleet(fleet)


def scenario_fail_closed(binary: str, dsn: str, replicas: int, port: int, workdir: str) -> None:
    print("\nscenario: replicas that may not migrate, against a database nobody migrated")
    reset(dsn)
    fleet = start_fleet(binary, dsn, replicas, port, workdir, migrate_on_boot=False)
    try:
        # Short timeout on purpose: they must never become ready, so waiting the full 45 seconds only
        # lengthens the build.
        statuses = [replica.ready(timeout=12) for replica in fleet]
        if any(statuses):
            raise FleetError("a replica became ready against a database with no schema")
        print(f"  ok  none of the {len(fleet)} replicas claimed readiness")

        alive = [replica for replica in fleet if replica.process.poll() is None]
        if len(alive) != len(fleet):
            raise FleetError(
                "a replica exited instead of staying unready; an orchestrator would then restart it in a loop "
                "instead of leaving it out of the load balancer")
        print("  ok  they stayed alive and out of rotation, rather than crashlooping")

        detail = fleet[0].readiness_detail()
        checks = {check.get("name"): check for check in detail.get("checks", [])}
        migrations = checks.get("migrations")
        if not migrations or migrations.get("status") != "fail":
            raise FleetError(f"readiness did not name migrations as the failing check: {detail}")
        print(f"  ok  readiness says why: {migrations.get('detail', '')[:90]}")
    finally:
        stop_fleet(fleet)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Start a fleet of replicas and prove they all come up.")
    parser.add_argument("--dsn", required=False,
                        help="throwaway PostgreSQL database; its contents are destroyed")
    parser.add_argument("--replicas", type=int, default=6)
    parser.add_argument("--port", type=int, default=8600)
    parser.add_argument("--only", choices=["boot", "migrate-first", "fail-closed"],
                        help="run one scenario instead of all three")
    parser.add_argument("--keep", action="store_true", help="keep the logs")
    args = parser.parse_args(argv)

    if not args.dsn:
        print("refusing to run: --dsn is required. SQLite is one process by definition, and a fleet is the "
              "whole point of this check.", file=sys.stderr)
        return 2

    workdir = tempfile.mkdtemp(prefix="edumart-fleet-")
    print(f"fleet check: {args.replicas} replicas\nworking directory: {workdir}")
    scenarios = {
        "boot": scenario_boot,
        "migrate-first": scenario_migrate_first,
        "fail-closed": scenario_fail_closed,
    }
    chosen = [args.only] if args.only else list(scenarios)
    try:
        binary = api_binary()
        for name in chosen:
            scenarios[name](binary, args.dsn, args.replicas, args.port, workdir)
        print(f"\nfleet check passed: {len(chosen)} scenario(s), {args.replicas} replicas each")
        return 0
    except FleetError as failure:
        print(f"\nfleet check FAILED: {failure}", file=sys.stderr)
        return 1
    finally:
        if args.keep:
            print(f"kept {workdir}")
        else:
            shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
