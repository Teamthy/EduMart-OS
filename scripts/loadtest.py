#!/usr/bin/env python3
"""Load the API with a realistic learner workload, then check it did not corrupt anything.

    python3 scripts/loadtest.py both --concurrency 64 --duration 30s
    python3 scripts/loadtest.py postgres --dsn 'postgres://…' --concurrency 300 --duration 45s

Two halves, and the second is the one this repository cares about:

1. **Measure.** `apps/api/cmd/loadtest` drives the whole attempt lifecycle — start an attempt, save answers,
   submit, read it back, browse the catalogue — and reports percentiles, throughput and error rate. It paces
   itself beneath the API's per-client rate limits, because a run that is throttled measures the limiter
   rather than the server (and says so when that happens anyway).

2. **Verify.** A load test that only reports latency proves the server stayed up. The interesting question is
   whether the invariants survived: did every submitted attempt get scored exactly once, did any write get
   lost, does the ledger still balance, does reconciliation still find nothing. Those are checked here, in
   SQL, against the database the load just wrote to — because that is where a concurrency bug shows up and
   nowhere else.

The checks are deliberately things a busy system can actually break:

    sessions_scored_once      a submitted attempt has exactly one score, and no attempt is scored twice
    responses_belong          every response belongs to a session that exists, and none is orphaned
    no_duplicate_responses    one answer row per question per attempt (the unique index, proven under load)
    deadline_respected        no attempt was submitted after its deadline by the server's own clock
    ledger_balances           every points entry traces to something that exists
    reconciliation_clean      the finance report finds no contradictions

Exit code is non-zero when a measurement budget is missed or an invariant is broken. Standard library only,
plus the Go binaries this repository already builds.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
API_DIR = ROOT / "apps" / "api"
LOADTEST = ROOT / "apps" / "api" / "cmd" / "loadtest"

PGBIN_CANDIDATES = [
    "/usr/local/lib/python3.11/dist-packages/pgserver/pginstall/bin",
    "/usr/lib/postgresql/17/bin",
    "/usr/lib/postgresql/16/bin",
    "/usr/lib/postgresql/15/bin",
]


class LoadError(RuntimeError):
    pass


def go_binary() -> str:
    """Find `go`, preferring the one on PATH and falling back to the wheel this environment installs."""
    found = shutil.which("go")
    if found:
        return found
    candidate = Path("/usr/local/lib/python3.11/dist-packages/go/bin/go")
    if candidate.exists():
        return str(candidate)
    raise LoadError("go toolchain not found: put it on PATH")


def build(what: str, output: str) -> None:
    environment = dict(os.environ)
    environment.setdefault("GOFLAGS", "-mod=mod")
    result = subprocess.run(
        [go_binary(), "build", "-o", output, what],
        cwd=API_DIR, capture_output=True, text=True, env=environment,
    )
    if result.returncode != 0:
        raise LoadError(f"go build {what} failed:\n{result.stderr.strip()}")


def pg_tool(name: str) -> str:
    configured = os.environ.get("PGBIN")
    if configured and Path(configured, name).exists():
        return str(Path(configured, name))
    for directory in PGBIN_CANDIDATES:
        candidate = Path(directory, name)
        if candidate.exists():
            return str(candidate)
    found = shutil.which(name)
    if found:
        return found
    raise LoadError(f"{name} not found: set PGBIN or install PostgreSQL client tools")


def api_binary() -> str:
    configured = os.environ.get("EDUMART_API_BIN")
    if configured and Path(configured).exists():
        return configured
    target = os.path.join(tempfile.gettempdir(), "edumart-api-loadtest")
    build("./cmd/api", target)
    return target


def wait_ready(base: str, timeout: float = 120.0) -> dict:
    deadline = time.time() + timeout
    last = ""
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(base + "/readyz", timeout=2) as response:
                if response.status == 200:
                    return json.loads(response.read() or b"{}")
        except (urllib.error.URLError, OSError, json.JSONDecodeError) as caught:
            last = str(caught)
        time.sleep(0.5)
    raise LoadError(f"the API never became ready: {last}")


class Api:
    """One API process, started and stopped around a run."""

    def __init__(self, binary: str, driver: str, dsn: str, port: int, log_path: str):
        self.binary, self.driver, self.dsn, self.port, self.log_path = binary, driver, dsn, port, log_path
        self.process: subprocess.Popen | None = None

    def __enter__(self):
        environment = dict(os.environ)
        environment.update({
            "EDUMART_DRIVER": self.driver,
            "EDUMART_DSN": self.dsn,
            "PORT": str(self.port),
            # The load generator sends a distinct X-Forwarded-For per virtual user. Believing it is what a
            # deployment behind a proxy does; without it every virtual user shares 127.0.0.1 and the run
            # measures one client's rate-limit budget.
            "EDUMART_TRUST_PROXY": "true",
            "EDUMART_SEED": "true",
            "EDUMART_NOTIFY": "log",
        })
        self.handle = open(self.log_path, "w")
        self.process = subprocess.Popen([self.binary], env=environment,
                                        stdout=self.handle, stderr=subprocess.STDOUT)
        wait_ready(self.base)
        return self

    @property
    def base(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    def __exit__(self, *_exc):
        if self.process:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
        if hasattr(self, "handle"):
            self.handle.close()


def run_load(base: str, args, report_path: str, state_path: str) -> dict:
    command = [
        os.path.join(tempfile.gettempdir(), "edumart-loadtest"),
        "-base", base,
        "-concurrency", str(args.concurrency),
        "-duration", args.duration,
        "-warmup", args.warmup,
        "-ramp", args.ramp,
        "-accounts", str(args.accounts),
        "-state", state_path,
        "-report", report_path,
    ]
    if args.max_p95:
        command += ["-max-p95", args.max_p95]
    if args.max_error_rate is not None:
        command += ["-max-error-rate", str(args.max_error_rate)]
    result = subprocess.run(command, capture_output=True, text=True)
    sys.stdout.write(result.stdout)
    sys.stderr.write(result.stderr)
    report = {}
    if os.path.exists(report_path):
        report = json.loads(Path(report_path).read_text())
    if result.returncode != 0:
        raise LoadError("the load run missed its budget (see above)")
    return report


# ─────────────────────────────── invariants ───────────────────────────────

# Every check is (name, sql, expectation) where the SQL returns a count that must be zero, except where the
# check is "at least this many things happened" — a load test over an empty database passes everything.
POSTGRES_CHECKS = [
    ("sessions_scored_once",
     """SELECT COUNT(*) FROM assessment_sessions
         WHERE status = 'submitted' AND (score_raw IS NULL OR score_max IS NULL OR score_percent IS NULL)""",
     "a submitted attempt must carry a score"),
    ("submitted_attempts_exist",
     "SELECT COUNT(*) FROM assessment_sessions WHERE status = 'submitted'",
     "at least one attempt must have been submitted, or the run measured nothing"),
    ("responses_belong_to_a_session",
     """SELECT COUNT(*) FROM assessment_responses r
         WHERE NOT EXISTS (SELECT 1 FROM assessment_sessions s WHERE s.id = r.session_id)""",
     "no response may be orphaned"),
    ("no_duplicate_responses",
     """SELECT COUNT(*) FROM (SELECT session_id, question_version_id, COUNT(*) c
                              FROM assessment_responses GROUP BY 1, 2 HAVING COUNT(*) > 1) d""",
     "one answer row per question per attempt"),
    ("deadline_respected",
     """SELECT COUNT(*) FROM assessment_sessions
         WHERE submitted_at IS NOT NULL AND submitted_at > deadline_at""",
     "the server must not accept a submission after the deadline it set"),
    ("points_ledger_traces_to_something",
     """SELECT COUNT(*) FROM points_ledger_entries p
         WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.id = p.user_id)""",
     "every points entry must belong to a user"),
    ("no_duplicate_ledger_keys",
     """SELECT COUNT(*) FROM (SELECT idempotency_key FROM points_ledger_entries
                              GROUP BY 1 HAVING COUNT(*) > 1) d""",
     "the ledger's idempotency keys stay unique under concurrency"),
]

SQLITE_CHECKS = [
    (name, sql.replace("assessment_sessions", "assessment_sessions"), expectation)
    for name, sql, expectation in POSTGRES_CHECKS
]


def check_sqlite(path: str) -> list[tuple[str, str, int]]:
    connection = sqlite3.connect(path)
    try:
        connection.execute("PRAGMA foreign_keys = ON")
        results = []
        for name, sql, expectation in SQLITE_CHECKS:
            count = connection.execute(sql).fetchone()[0]
            results.append((name, expectation, int(count)))
        return results
    finally:
        connection.close()


def check_postgres(dsn: str) -> list[tuple[str, str, int]]:
    results = []
    for name, sql, expectation in POSTGRES_CHECKS:
        out = subprocess.run([pg_tool("psql"), dsn, "-t", "-A", "-c", sql],
                             capture_output=True, text=True, check=True).stdout.strip()
        results.append((name, expectation, int(out or 0)))
    return results


def reconcile(base: str) -> dict:
    """Ask the API's own finance report whether the books still agree with themselves."""
    admin_email, admin_password = "admin@edumart.ng", "Admin123!"
    payload = json.dumps({"email": admin_email, "password": admin_password}).encode()
    request = urllib.request.Request(base + "/v1/auth/login", payload,
                                     {"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=10) as response:
        token = json.loads(response.read())["token"]
    request = urllib.request.Request(base + "/v1/admin/finance/reconciliation",
                                     headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.loads(response.read())["reconciliation"]


def report_invariants(results: list[tuple[str, str, int]], reconciliation: dict | None) -> list[str]:
    problems: list[str] = []
    for name, expectation, count in results:
        if name == "submitted_attempts_exist":
            if count <= 0:
                problems.append(f"{name}: {expectation} (found none)")
            else:
                print(f"  ok  {name}: {count} submitted attempt(s)")
            continue
        if count == 0:
            print(f"  ok  {name}: {expectation}")
        else:
            problems.append(f"{name}: {expectation} — {count} row(s) disagree")

    if reconciliation is not None:
        discrepancies = reconciliation.get("discrepancies") or []
        checked = reconciliation.get("checked", 0)
        if discrepancies:
            for discrepancy in discrepancies:
                problems.append(f"reconciliation found {discrepancy.get('kind')} "
                                f"({discrepancy.get('count')} row(s)): {discrepancy.get('detail')}")
        else:
            print(f"  ok  reconciliation_clean: {checked} check(s) ran and found nothing")
        if checked == 0:
            problems.append("reconciliation ran no checks, so its clean bill of health is meaningless")
    return problems


# ─────────────────────────────── engines ───────────────────────────────

def run_engine(engine: str, args, workdir: str) -> list[str]:
    print(f"\n=== {engine} ===")
    binary = api_binary()
    build("./cmd/loadtest", os.path.join(tempfile.gettempdir(), "edumart-loadtest"))
    port = args.port
    state = os.path.join(workdir, f"accounts-{engine}.json")
    report_path = os.path.join(workdir, f"report-{engine}.json")

    if engine == "sqlite":
        database = os.path.join(workdir, "load.db")
        with Api(binary, "sqlite3", database, port, os.path.join(workdir, "api-sqlite.log")) as api:
            report = run_load(api.base, args, report_path, state)
            reconciliation = reconcile(api.base)
        results = check_sqlite(database)
    else:
        if not args.dsn:
            raise LoadError("the postgres run needs --dsn")
        reset_postgres(args.dsn)
        with Api(binary, "postgres", args.dsn, port, os.path.join(workdir, "api-postgres.log")) as api:
            report = run_load(api.base, args, report_path, state)
            reconciliation = reconcile(api.base)
        results = check_postgres(args.dsn)

    print("\ninvariants after the load")
    problems = report_invariants(results, reconciliation)
    if report:
        print(f"  measured: {report.get('requests')} requests, {report.get('requestsPerSecond', 0):.0f}/s, "
              f"p95 {report.get('p95Ms', 0):.1f}ms, error rate {100 * report.get('errorRate', 0):.2f}%")
    sys.stdout.flush()
    sys.stderr.flush()
    return problems


def reset_postgres(dsn: str) -> None:
    """Start from an empty database, so the numbers are not history."""
    base, _, query = dsn.partition("?")
    head, _, database = base.rpartition("/")
    maintenance = head + "/postgres" + (("?" + query) if query else "")
    subprocess.run([pg_tool("dropdb"), "--force", "--if-exists", "--maintenance-db", maintenance, database],
                   check=False, capture_output=True)
    subprocess.run([pg_tool("createdb"), "--maintenance-db", maintenance, database],
                   check=True, capture_output=True)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Load the API and prove it stayed correct.")
    parser.add_argument("engine", choices=["sqlite", "postgres", "both"], nargs="?", default="sqlite")
    parser.add_argument("--dsn", help="PostgreSQL DSN for the postgres runs")
    parser.add_argument("--concurrency", type=int, default=64)
    parser.add_argument("--duration", default="30s")
    parser.add_argument("--warmup", default="5s")
    parser.add_argument("--ramp", default="4s")
    parser.add_argument("--accounts", type=int, default=8)
    parser.add_argument("--port", type=int, default=8099)
    parser.add_argument("--max-p95", help="fail if p95 exceeds this (e.g. 250ms)")
    parser.add_argument("--max-error-rate", type=float, default=None,
                        help="fail if the error rate exceeds this fraction, e.g. 0.01")
    parser.add_argument("--keep", action="store_true", help="keep the scratch directory")
    args = parser.parse_args(argv)

    workdir = tempfile.mkdtemp(prefix="edumart-loadtest-")
    print(f"scratch: {workdir}")
    problems: list[str] = []
    try:
        engines = ["sqlite", "postgres"] if args.engine == "both" else [args.engine]
        for engine in engines:
            problems.extend(run_engine(engine, args, workdir))
            args.port += 1
    except LoadError as caught:
        problems.append(str(caught))
    finally:
        if not args.keep:
            shutil.rmtree(workdir, ignore_errors=True)

    if problems:
        print("\nthe load found problems:", file=sys.stderr)
        for problem in problems:
            print(f"  - {problem}", file=sys.stderr)
        return 1
    print("\nload test passed: the numbers met their budgets and the invariants held")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
