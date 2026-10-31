#!/usr/bin/env python3
"""Rehearse a deploy, then roll it back, against a real database.

Phase 4's gap was never "we have no CI" — it was "deploy and rollback are unproven". A rollback plan nobody
has executed is a hypothesis, and the moment it gets tested is the worst possible moment. This script
executes it:

  1. **Build two releases.** The previous one from a git ref, the current one from the working tree. Not
     stubs, not mocks: the same binaries a deploy would ship.
  2. **Deploy the previous release** onto an empty database and run the full smoke test. It applies its own
     migrations at boot, so the schema is exactly what that release produced.
  3. **Migrate first, as a separate step**, exactly as the runbook says (`-migrate-only`). Then prove the
     *previous* release still serves the migrated database — the earliest possible signal that a rollout is
     not survivable, and it arrives before anything new is serving traffic.
  4. **Deploy the new release** over it. Its migrations are already applied, so this stage is about the
     binary, not the schema.
  5. **Write data with the new release.**
  6. **Roll back**: stop the new release, start the previous one *against the migrated database*, and make
     it serve. This is the gate. An expand-only migration is the only kind that survives it, and a
     migration that drops a column or rewrites a table the old code reads fails here — loudly, in CI,
     before anybody is on call.
  7. **Read the new release's data with the old code.** A rollback that boots but loses the last hour's
     orders is not a rollback.

It also fails on **destructive DDL** (drop/rename) anywhere in the migrations, because those are precisely
the statements a rollback cannot survive, and they are easy to add in a hurry.

Usage:
    python3 scripts/deploy-rehearsal.py                                  # previous release = origin/main
    python3 scripts/deploy-rehearsal.py --from HEAD~1                    # for a local experiment
    python3 scripts/deploy-rehearsal.py --dsn 'postgres://…/rehearsal'   # against PostgreSQL (recommended)
    python3 scripts/deploy-rehearsal.py --check-only                     # DDL scan only: no build, no DB

Environment:
    EDUMART_API_BIN   reuse an already-built binary for the *previous* release (skips that build)
    PGBIN             directory holding createdb/dropdb/psql

Exits non-zero if any stage fails, including the rollback.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
API_DIR = os.path.join(REPO, "apps", "api")
MIGRATIONS = os.path.join(API_DIR, "internal", "store", "migrations")

# Statements a rollback cannot survive. Not "never allowed" — sometimes a column genuinely has to go — but a
# deliberate decision that has to be written down, because the rollback for that release is "forward only",
# and whoever is on call at 3am needs to know that before they try.
DESTRUCTIVE = [
    (re.compile(r"\bdrop\s+table\b", re.I), "DROP TABLE"),
    (re.compile(r"\bdrop\s+column\b", re.I), "DROP COLUMN"),
    (re.compile(r"\brename\s+(to|column)\b", re.I), "RENAME"),
    (re.compile(r"\bdrop\s+constraint\b", re.I), "DROP CONSTRAINT"),
]
# The escape hatch is a comment, in the file, next to the statement it excuses.
ALLOW_MARKER = "-- rehearsal: destructive"


class RehearsalError(RuntimeError):
    pass


# ── process and HTTP helpers ─────────────────────────────────────────────────────────────────────


def run(command: list[str], *, cwd: str | None = None, check: bool = True, env: dict | None = None) -> str:
    result = subprocess.run(command, capture_output=True, text=True, cwd=cwd, env={**os.environ, **(env or {})})
    if check and result.returncode != 0:
        raise RehearsalError(f"{' '.join(command)} failed ({result.returncode}): {(result.stderr or result.stdout).strip()}")
    return result.stdout.strip()


def wait_for_ready(base: str, process: subprocess.Popen, timeout: float = 90.0) -> None:
    """Wait on /readyz, never on the port: readiness is what proves migrations and the database are up.

    A process that has already exited is never going to become ready, so a crash is reported as a crash
    with its log, not as a sixty-second timeout with no explanation.
    """
    deadline = time.time() + timeout
    while time.time() < deadline:
        if process.poll() is not None:
            raise RehearsalError(f"the API exited with status {process.returncode}")
        try:
            with urllib.request.urlopen(base + "/readyz", timeout=2) as response:
                if response.status == 200:
                    return
        except (urllib.error.URLError, OSError):
            pass
        time.sleep(0.3)
    raise RehearsalError("the API never became ready")


def http(base: str, method: str, path: str, body: dict | None = None, token: str | None = None):
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(base + path, data=data, method=method)
    request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            return response.status, json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as caught:
        return caught.code, json.loads(caught.read() or b"{}")


def driver_for(engine: str) -> str:
    """The store's driver names are not the engine names: `sqlite` is `sqlite3` to database/sql.

    Passing the engine straight through produced `unknown driver "sqlite"` the first time this ran on the
    SQLite path — a five-second fix, and exactly the kind of thing a rehearsal with only one engine misses.
    """
    return "sqlite3" if engine == "sqlite" else engine


class Release:
    """One build of the API, run against one database."""

    def __init__(self, name: str, binary: str, driver: str, dsn: str, port: int, log_path: str):
        self.name = name
        self.binary = binary
        self.base = f"http://127.0.0.1:{port}"
        self.log_path = log_path
        self.env = {
            "EDUMART_DRIVER": driver,
            "EDUMART_DSN": dsn,
            "PORT": str(port),
            "EDUMART_ENV": "development",
            "EDUMART_NOTIFY": "log",
            "EDUMART_SEED": "true",
            "EDUMART_SEED_DEMO_USERS": "true",
            "EDUMART_ALLOW_SANDBOX_PAYMENTS": "true",
        }
        self.process: subprocess.Popen | None = None
        self.log = None

    def start(self) -> "Release":
        self.log = open(self.log_path, "w")
        self.process = subprocess.Popen(
            [self.binary], env={**os.environ, **self.env}, stdout=self.log, stderr=subprocess.STDOUT
        )
        try:
            wait_for_ready(self.base, self.process)
        except RehearsalError as failure:
            self.stop()
            with open(self.log_path) as handle:
                raise RehearsalError(f"{self.name} did not start: {failure}\n{handle.read()[-2500:]}") from None
        return self

    def stop(self) -> None:
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=10)
        if self.log:
            self.log.close()
            self.log = None

    def __enter__(self) -> "Release":
        return self.start()

    def __exit__(self, *_exc) -> None:
        self.stop()


# ── the checks ───────────────────────────────────────────────────────────────────────────────────


def scan_migrations() -> int:
    """Fail on DDL a rollback cannot survive. Runs without a database, in a fraction of a second."""
    print("migration safety")
    offenders = []
    versioned = 0
    for engine in ("sqlite", "postgres"):
        directory = os.path.join(MIGRATIONS, engine)
        for filename in sorted(os.listdir(directory)):
            if not filename.endswith(".sql"):
                continue
            versioned += 1
            path = os.path.join(directory, filename)
            with open(path) as handle:
                text = handle.read()
            allowed = ALLOW_MARKER in text
            for pattern, label in DESTRUCTIVE:
                for match in pattern.finditer(text):
                    line = text[: match.start()].count("\n") + 1
                    if allowed:
                        print(f"  note  {engine}/{filename}:{line} {label} — declared forward-only by the file")
                    else:
                        offenders.append(f"{engine}/{filename}:{line} {label}")
    for offender in offenders:
        print(f"  FAIL  {offender} — a rollback cannot survive this")
    if offenders:
        raise RehearsalError(
            "destructive DDL without a documented decision. Either migrate in two steps (add the new shape, "
            f"stop using the old one, drop it in a later release) or add '{ALLOW_MARKER}: <reason>' to the file "
            "so the next person knows the rollback is forward-only."
        )
    print(f"  ok  {versioned} migration files, no destructive DDL")
    return 0


def resolve_previous_ref(explicit: str | None) -> str:
    candidates = [explicit] if explicit else ["origin/main", "main", "HEAD~1"]
    for candidate in candidates:
        if not candidate:
            continue
        result = subprocess.run(
            ["git", "-C", REPO, "rev-parse", "--verify", "--quiet", candidate + "^{commit}"],
            capture_output=True, text=True,
        )
        if result.returncode == 0:
            return candidate
    raise RehearsalError(
        "no previous release to build. Pass --from <ref>; a rehearsal with no previous release proves nothing."
    )


def migration_versions(ref: str) -> set[str]:
    result = subprocess.run(
        ["git", "-C", REPO, "ls-tree", "-r", "--name-only", ref, "apps/api/internal/store/migrations"],
        capture_output=True, text=True,
    )
    return {os.path.basename(line) for line in result.stdout.splitlines() if line.endswith(".sql")}


def build_release(name: str, source: str, target: str, worktree: str | None) -> str:
    """Build one release. `source` is either a worktree path or the repo itself."""
    api = os.path.join(source, "apps", "api") if worktree else API_DIR
    print(f"  building {name} from {worktree or 'the working tree'}")
    run(["go", "build", "-o", target, "./cmd/api"], cwd=api)
    return target


# Endpoints whose availability should be *identical* before a deploy and after a rollback. Deliberately a
# mix of public and authenticated routes, and deliberately not a list of "features this release has": the
# script must work for any pair of refs, including two that are identical.
FINGERPRINT_ROUTES = [
    "/healthz",
    "/v1/products",
    "/v1/search?q=algebra",
    "/v1/subjects",
    "/v1/me",
    "/v1/library",
    "/v1/rewards",
    "/v1/dashboard",
    "/v1/orders",
]


def fingerprint(release: Release, token: str) -> dict[str, int]:
    """The release's observable surface, as status codes. Rollback has to restore exactly this."""
    print_codes: dict[str, int] = {}
    for path in FINGERPRINT_ROUTES:
        authenticated = path not in ("/healthz", "/v1/products", "/v1/search?q=algebra", "/v1/subjects")
        status, _ = http(release.base, "GET", path, token=token if authenticated else None)
        print_codes[path] = status
    return print_codes


def journey(release: Release) -> dict:
    """A short learner journey: the smallest thing that proves this release can actually serve traffic."""
    status, body = http(release.base, "POST", "/v1/auth/login",
                        {"email": "student@edumart.ng", "password": "Student123!"})
    if status != 200 or not body.get("token"):
        raise RehearsalError(f"{release.name}: the demo learner cannot sign in ({status}) {body}")
    token = body["token"]

    for path in ("/v1/me", "/v1/products", "/v1/subjects", "/v1/library"):
        status, _ = http(release.base, "GET", path, token=token)
        if status != 200:
            raise RehearsalError(f"{release.name}: GET {path} returned {status}")

    # A write, not just reads: a schema migration that breaks the write path is the one that matters.
    status, subjects = http(release.base, "GET", "/v1/subjects", token=token)
    subject_id = next((s["id"] for s in subjects["subjects"] if s.get("questionCount", 0) > 0), None)
    if not subject_id:
        raise RehearsalError(f"{release.name}: no subject has questions, so no write can be proven")
    status, started = http(release.base, "POST", "/v1/practice-sessions",
                           {"subjectId": subject_id, "count": 2}, token=token)
    if status != 201:
        raise RehearsalError(f"{release.name}: a practice session could not be started ({status}) {started}")
    session_id = started["session"]["session_id"]
    status, submitted = http(release.base, "POST", f"/v1/assessment-sessions/{session_id}/submit", None, token=token)
    if status != 200:
        raise RehearsalError(f"{release.name}: an attempt could not be submitted ({status}) {submitted}")

    return {"token": token}


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Deploy a release, then roll it back, and prove both.")
    parser.add_argument("--from", dest="previous", help="git ref of the release being rolled back to (default origin/main)")
    parser.add_argument("--engine", default="postgres", choices=["postgres", "sqlite"])
    parser.add_argument("--dsn", help="throwaway PostgreSQL database; its contents are destroyed")
    parser.add_argument("--port", type=int, default=8110)
    parser.add_argument("--check-only", action="store_true", help="run the DDL scan and stop")
    parser.add_argument("--keep", action="store_true", help="keep the working directory for inspection")
    args = parser.parse_args(argv)

    try:
        scan_migrations()
        if args.check_only:
            print("\nmigration safety check passed")
            return 0

        ref = resolve_previous_ref(args.previous)
        previous_sha = run(["git", "-C", REPO, "rev-parse", "--short", ref])
        current_sha = run(["git", "-C", REPO, "rev-parse", "--short", "HEAD"])
        new_migrations = migration_versions("HEAD") - migration_versions(ref)

        print(f"\nrehearsal: {ref} ({previous_sha}) → working tree ({current_sha})")
        if new_migrations:
            print(f"  schema change: {', '.join(sorted(new_migrations))}")
        else:
            print("  no new migrations between these releases — this proves operational rollback only")

        workdir = tempfile.mkdtemp(prefix="edumart-deploy-")
        worktree = os.path.join(workdir, "previous")
        print(f"  working directory: {workdir}")
        try:
            print("\nbuild")
            run(["git", "-C", REPO, "worktree", "add", "--detach", worktree, ref])
            previous_bin = os.environ.get("EDUMART_API_BIN") or os.path.join(workdir, "api-previous")
            if not os.environ.get("EDUMART_API_BIN"):
                build_release("the previous release", worktree, previous_bin, worktree)
            current_bin = build_release("this release", REPO, os.path.join(workdir, "api-current"), None)

            # A throwaway database per run: the rehearsal destroys data, so it must never be pointed at
            # something whose contents somebody needs.
            if args.engine == "postgres":
                if not args.dsn:
                    raise RehearsalError("--engine postgres needs --dsn pointing at a throwaway database")
                dsn = args.dsn
                reset_postgres(dsn)
            else:
                dsn = os.path.join(workdir, "rehearsal.db")

            print("\n1. deploy the previous release onto an empty database")
            previous = Release("previous release", previous_bin, driver_for(args.engine), dsn, args.port,
                               os.path.join(workdir, "previous.log"))
            with previous:
                baseline = fingerprint(previous, journey(previous)["token"])
                print("  ok  it serves traffic and wrote its own schema")
                print(f"  ok  recorded {len(baseline)} endpoint statuses as the baseline to restore")

            print("\n2. migrate, as a separate step (the deploy job)")
            # The same binary, the same migrations, but nothing serving: a rollout that migrates first has
            # exactly one process touching the schema, instead of N replicas racing each other.
            output = run([current_bin, "-migrate-only"], env={
                "EDUMART_DRIVER": driver_for(args.engine),
                "EDUMART_DSN": dsn,
                # A migration job must not seed demo fixtures into a database it was pointed at.
                "EDUMART_SEED": "false",
            })
            if "migrations applied" not in output:
                raise RehearsalError(f"the migration job said something unexpected: {output}")
            print(f"  ok  {output.splitlines()[-1]}")

            print("\n3. the previous release still serves the migrated database")
            # Before any new code is deployed. If this fails, the rollout is already unsafe and nobody has
            # been sent a 500 yet.
            post_migration = Release("previous release (post-migration)", previous_bin, driver_for(args.engine), dsn,
                                     args.port, os.path.join(workdir, "post-migration.log"))
            with post_migration:
                journey(post_migration)
                print("  ok  old code, new schema — a rollback would still work")

            print("\n4. deploy this release")
            current = Release("this release", current_bin, driver_for(args.engine), dsn, args.port + 1,
                              os.path.join(workdir, "current.log"))
            with current:
                current_tokens = journey(current)
                print("  ok  it serves a database that was migrated while it was not running")

                print("\n5. write data with this release")
                status, order = http(current.base, "POST", "/v1/orders",
                                     {"productId": next_product(current)}, current_tokens["token"])
                if status != 201:
                    raise RehearsalError(f"an order could not be created ({status}) {order}")
                order_id = order["order"]["id"]
                print(f"  ok  order {order_id} written by this release")

            print("\n6. roll back: the previous release against the migrated database")
            # This is the stage the whole script exists for. The database is now one release ahead of the
            # binary that has to serve it, which is exactly the state a bad deploy leaves behind.
            rolled_back = Release("previous release (rollback)", previous_bin, driver_for(args.engine), dsn, args.port,
                                  os.path.join(workdir, "rollback.log"))
            with rolled_back:
                tokens = journey(rolled_back)

                print("  ok  the previous release serves the newer schema")

                # A rollback must restore the *whole* surface of the release being restored, not just the
                # routes the journey happens to touch. A feature that survives a rollback it should not, or
                # disappears from one that never had it, means the deploy moved more than the binary.
                restored = fingerprint(rolled_back, tokens["token"])
                changed = {path: (baseline.get(path), restored.get(path))
                           for path in set(baseline) | set(restored) if baseline.get(path) != restored.get(path)}
                if changed:
                    detail = ", ".join(f"{path} {before}→{after}" for path, (before, after) in sorted(changed.items()))
                    raise RehearsalError(f"the rollback did not restore the previous release's surface: {detail}")
                print(f"  ok  all {len(restored)} endpoint statuses match the pre-deploy baseline")

                print("\n7. read the new release's data with the old code")
                status, fetched = http(rolled_back.base, "GET", f"/v1/orders/{order_id}", token=tokens["token"])
                if status != 200:
                    raise RehearsalError(
                        f"after the rollback the order written by the new release is unreadable ({status}). "
                        "The rollback boots but loses data, which is the worst of both outcomes."
                    )
                print(f"  ok  order {order_id} still readable ({fetched['order']['status']})")

            print("\ndeploy rehearsal passed: deployed forward, rolled back, and lost nothing")
            return 0
        finally:
            subprocess.run(["git", "-C", REPO, "worktree", "remove", "--force", worktree], capture_output=True)
            if not args.keep:
                shutil.rmtree(workdir, ignore_errors=True)
            else:
                print(f"kept {workdir}")
    except RehearsalError as failure:
        print(f"\nrehearsal FAILED: {failure}", file=sys.stderr)
        return 1


def next_product(release: Release) -> str:
    """A product the demo learner does not own, so the order is a genuine purchase attempt."""
    status, body = http(release.base, "GET", "/v1/products")
    if status != 200:
        raise RehearsalError(f"the catalogue is unreadable ({status})")
    for product in body["products"]:
        if not product.get("owned"):
            return product["id"]
    raise RehearsalError("every product is already owned, so no purchase can be rehearsed")


def reset_postgres(dsn: str) -> None:
    psql = shutil.which("psql") or _pgbin("psql")
    if not psql:
        raise RehearsalError("psql is not on PATH; install the PostgreSQL client or set PGBIN")
    print("  resetting the throwaway database")
    run([psql, dsn, "-v", "ON_ERROR_STOP=1", "-q", "-c",
         "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;"])


def _pgbin(tool: str) -> str | None:
    directory = os.environ.get("PGBIN")
    if directory and os.path.exists(os.path.join(directory, tool)):
        return os.path.join(directory, tool)
    bundled = "/usr/local/lib/python3.11/dist-packages/pgserver/pginstall/bin"
    if os.path.exists(os.path.join(bundled, tool)):
        return os.path.join(bundled, tool)
    return None


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
