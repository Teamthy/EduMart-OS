#!/usr/bin/env python3
"""Prove point-in-time recovery: recover to a moment before a mistake, not to last night.

    python3 scripts/pitr-drill.py
    python3 scripts/pitr-drill.py --keep      # leave the cluster behind to inspect it

A nightly dump answers "can we get back to midnight?". It cannot answer the question that actually
gets asked after an incident: *can we get back to 14:07, before the bad deploy wrote to the table?* Only
continuous WAL archiving can, and an archive configuration that has never been replayed is a
configuration, not a capability.

The drill builds a real cluster, and then:

  1. Initialises it with ``wal_level=replica`` and an ``archive_command`` that copies each WAL segment into
     an archive directory, the way a production deployment would ship them to object storage.
  2. Runs the **real API binary** to migrate and seed, so the database under test has the schema and
     fixture data the product actually ships.
  3. Takes a **base backup** with ``pg_basebackup`` and forces the WAL it depends on into the archive.
  4. Records a recovery target time, then makes a deliberate mistake **after** that time: a user that
     must not survive.
  5. Destroys the cluster completely — data directory and all.
  6. Rebuilds from the base backup, replays WAL up to the target time only, and promotes.
  7. Asserts the account created *before* the target exists, the one created after it does not, and that
     the restored cluster still serves a login through the API.

If any of that fails the drill exits non-zero. The point is the negative: an account that should have
been lost and is still present means the target time did not apply and "PITR" is a word we are using
without the capability.
"""

from __future__ import annotations

import argparse
import datetime as dt
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

PGBIN_CANDIDATES = [
    "/usr/local/lib/python3.11/dist-packages/pgserver/pginstall/bin",
    "/usr/lib/postgresql/17/bin",
    "/usr/lib/postgresql/16/bin",
    "/usr/lib/postgresql/15/bin",
]

BEFORE_EMAIL = "pitr-before@example.com"
AFTER_EMAIL = "pitr-after@example.com"


class DrillError(RuntimeError):
    pass


_STARTED = time.time()


def step(message: str) -> None:
    """Print a numbered step with the elapsed time, so a slow one is visible rather than mysterious."""
    print(f"[{time.time() - _STARTED:6.1f}s] {message}")


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
    raise DrillError(
        f"{name} not found. This drill needs PostgreSQL *server* binaries (initdb, pg_ctl, "
        f"pg_basebackup), not only the client tools."
    )


def run(command: list[str], *, check: bool = True, env: dict | None = None,
        input_text: str | None = None, cwd: str | None = None):
    result = subprocess.run(command, capture_output=True, text=True, cwd=cwd,
                            env={**os.environ, **(env or {})}, input=input_text)
    if check and result.returncode != 0:
        raise DrillError(f"{' '.join(command)} failed ({result.returncode}):\n{result.stderr.strip()}")
    return result.stdout.strip()


class Cluster:
    """One PostgreSQL server, with its own data directory, socket directory and port."""

    def __init__(self, datadir: str, socketdir: str, port: int, logfile: str):
        self.datadir = datadir
        self.socketdir = socketdir
        self.port = port
        self.logfile = logfile
        self.started = False

    @property
    def dsn(self) -> str:
        return f"postgres://postgres@/edumart?host={self.socketdir}&port={self.port}&sslmode=disable"

    @property
    def maintenance_dsn(self) -> str:
        return f"postgres://postgres@/postgres?host={self.socketdir}&port={self.port}&sslmode=disable"

    def start(self) -> None:
        options = f"-k {self.socketdir} -p {self.port} -c listen_addresses=''"
        run([pg_tool("pg_ctl"), "-D", self.datadir, "-l", self.logfile, "-o", options, "-w", "start"])
        self.started = True

    def stop(self) -> None:
        if not self.started:
            return
        run([pg_tool("pg_ctl"), "-D", self.datadir, "-m", "fast", "-w", "stop"], check=False)
        self.started = False

    def psql(self, sql: str, database: str = "postgres") -> str:
        dsn = self.dsn if database == "edumart" else self.maintenance_dsn
        return run([pg_tool("psql"), dsn, "-At", "-c", sql])


def configure_archiving(datadir: str, archive_dir: str) -> None:
    """Turn on WAL archiving the way a production deployment would.

    ``archive_command`` uses ``test ! -f`` first: an archive command that can overwrite a segment it has
    already stored is how a corrupt segment silently replaces a good one.
    """
    with open(os.path.join(datadir, "postgresql.conf"), "a", encoding="utf-8") as handle:
        handle.write(
            "\n# ── the drill's archiving configuration ──\n"
            "wal_level = replica\n"
            "archive_mode = on\n"
            f"archive_command = 'test ! -f {archive_dir}/%f && cp %p {archive_dir}/%f'\n"
            "archive_timeout = 5\n"
            "max_wal_senders = 4\n"
            "wal_keep_size = 64MB\n"
            # pg_basebackup forces a checkpoint, and the default completion target spreads it over 90% of
            # checkpoint_timeout — which turned a 12 MB checkpoint into nearly two minutes of the writer
            # sleeping. Zero means "finish it now", which is right for a disposable drill cluster.
            "checkpoint_completion_target = 0.0\n"
            "checkpoint_timeout = 1min\n"
            # Durability against power loss is not what this drill tests — the archive and the replay are
            # — and the sandbox's fsync is slow. A production configuration keeps both of these on.
            "fsync = off\n"
            "full_page_writes = off\n"
            "synchronous_commit = off"
        )


def wait_for_archive(psql, expected_wal: str | None = None, timeout: float = 30.0) -> str:
    """Block until the archiver has stored a segment. Returns the last archived WAL name."""
    deadline = time.time() + timeout
    last = ""
    while time.time() < deadline:
        last = psql("SELECT COALESCE(last_archived_wal, '') FROM pg_stat_archiver")
        failed = int(psql("SELECT failed_count FROM pg_stat_archiver"))
        if failed:
            raise DrillError("the archiver reported a failure; the archive_command is broken")
        if last and (expected_wal is None or last >= expected_wal):
            return last
        time.sleep(0.3)
    raise DrillError(f"no WAL segment was archived within {timeout}s (last: {last!r})")


def api_binary() -> str:
    configured = os.environ.get("EDUMART_API_BIN")
    if configured and os.path.exists(configured):
        return configured
    target = os.path.join(tempfile.gettempdir(), "edumart-api-pitr-drill")
    run(["go", "build", "-o", target, "./cmd/api"], env={"GOFLAGS": "-mod=mod"}, cwd=API_DIR)
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


def serve(binary: str, dsn: str, port: int, logfile: str):
    """Run the API against a database for one short burst, and return its base URL."""
    process = subprocess.Popen(
        [binary],
        env={**os.environ, "EDUMART_DRIVER": "postgres", "EDUMART_DSN": dsn, "PORT": str(port),
             "EDUMART_NOTIFY": "log", "EDUMART_ENV": "development",
             "EDUMART_SEED": "true", "EDUMART_SEED_DEMO_USERS": "true"},
        stdout=open(logfile, "w"), stderr=subprocess.STDOUT,
    )
    base = f"http://127.0.0.1:{port}"
    try:
        wait_ready(base, process)
    except DrillError:
        process.terminate()
        with open(logfile) as handle:
            raise DrillError(f"the API did not start:\n{handle.read()[-2000:]}")
    return process, base


def stop_api(process: subprocess.Popen) -> None:
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()


def register(base: str, email: str) -> None:
    status, body = http(base, "POST", "/v1/auth/register", {
        "displayName": "PITR " + email, "email": email, "password": "Str0ngPassw0rd!", "client": "web",
    })
    if status != 201:
        raise DrillError(f"could not create {email}: {status} {body}")


def main() -> int:
    parser = argparse.ArgumentParser(description="Demonstrate point-in-time recovery.")
    parser.add_argument("--port", type=int, default=5440, help="port for the drill cluster")
    parser.add_argument("--api-port", type=int, default=8140)
    parser.add_argument("--keep", action="store_true", help="keep the cluster and archives for inspection")
    args = parser.parse_args()

    workdir = tempfile.mkdtemp(prefix="edumart-pitr-")
    datadir = os.path.join(workdir, "cluster")
    socketdir = os.path.join(workdir, "socket")
    archivedir = os.path.join(workdir, "wal")
    backupdir = os.path.join(workdir, "base")
    recovered = os.path.join(workdir, "recovered")
    for directory in (socketdir, archivedir, backupdir):
        os.makedirs(directory, exist_ok=True)
    logfile = os.path.join(workdir, "server.log")

    print(f"working directory: {workdir}")
    cluster = Cluster(datadir, socketdir, args.port, logfile)
    recovery = Cluster(recovered, os.path.join(workdir, "recovery-socket"), args.port + 1,
                       os.path.join(workdir, "recovery.log"))
    os.makedirs(recovery.socketdir, exist_ok=True)

    try:
        step("1. initialise a cluster with WAL archiving on")
        run([pg_tool("initdb"), "-D", datadir, "-U", "postgres", "--auth=trust"])
        configure_archiving(datadir, archivedir)
        cluster.start()
        run([pg_tool("createdb"), "--maintenance-db", cluster.maintenance_dsn, "edumart"])
        print(f"   archive_command copies segments to {archivedir}")

        step("2. migrate and seed with the real API binary")
        binary = api_binary()
        process, base = serve(binary, cluster.dsn, args.api_port, os.path.join(workdir, "api.log"))
        stop_api(process)
        tables = cluster.psql("SELECT COUNT(*) FROM pg_tables WHERE schemaname='public'", "edumart")
        print(f"   schema applied: {tables} tables")

        step("3. take a base backup and archive the WAL it depends on")
        cluster.psql("SELECT pg_switch_wal()", "edumart")
        wait_for_archive(lambda sql: cluster.psql(sql))
        # -Ft -z: base.tar.gz plus pg_wal.tar.gz, with -X stream so the backup's own WAL is included.
        # Without -X stream the backup can reference segments that were never archived.
        run([pg_tool("pg_basebackup"), "-D", backupdir, "-Ft", "-z", "-X", "stream", "-U", "postgres",
             "-h", socketdir, "-p", str(args.port), "--no-password"])
        print(f"   base backup: {sorted(os.listdir(backupdir))}")

        step("4. create an account before the target, then a deliberate mistake after it")
        process, base = serve(binary, cluster.dsn, args.api_port, os.path.join(workdir, "api2.log"))
        register(base, BEFORE_EMAIL)
        stop_api(process)
        # The target is a real timestamp taken from the server, with a pause either side so the
        # boundary is unambiguous — a recovery target in the same microsecond as a commit is a coin flip.
        time.sleep(1)
        target = cluster.psql("SELECT to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US')")
        time.sleep(1)
        process, base = serve(binary, cluster.dsn, args.api_port, os.path.join(workdir, "api3.log"))
        register(base, AFTER_EMAIL)
        stop_api(process)
        print(f"   recovery target: {target} UTC")
        print(f"   {BEFORE_EMAIL} is before it; {AFTER_EMAIL} is the mistake")

        cluster.psql("SELECT pg_switch_wal()", "edumart")
        wait_for_archive(lambda sql: cluster.psql(sql))
        segments = len([name for name in os.listdir(archivedir) if name.endswith(".history") or len(name) == 24])
        print(f"   {segments} WAL segment(s) archived")

        step("5. destroy the cluster")
        cluster.stop()
        shutil.rmtree(datadir)
        if os.path.exists(datadir):
            raise DrillError("the data directory is still there; the drill would prove nothing")
        print("   data directory deleted")

        step("6. rebuild from the base backup, replay WAL up to the target, promote")
        os.makedirs(recovered, exist_ok=True)
        run(["tar", "-xzf", os.path.join(backupdir, "base.tar.gz"), "-C", recovered])
        wal_tar = os.path.join(backupdir, "pg_wal.tar.gz")
        if os.path.exists(wal_tar):
            run(["tar", "-xzf", wal_tar, "-C", os.path.join(recovered, "pg_wal")])
        with open(os.path.join(recovered, "postgresql.auto.conf"), "a", encoding="utf-8") as handle:
            handle.write(
                f"restore_command = 'cp {archivedir}/%f %p'\n"
                f"recovery_target_time = '{target} UTC'\n"
                "recovery_target_action = 'promote'\n"
            )
        # PostgreSQL 12+ signals recovery with a file rather than recovery.conf.
        open(os.path.join(recovered, "recovery.signal"), "w").close()
        os.chmod(recovered, 0o700)  # PostgreSQL refuses a data directory anyone else could read
        recovery.start()
        # `pg_ctl -w start` returns as soon as the server accepts connections, which in recovery can be
        # before the target is reached and the server promotes. Wait for the promotion rather than
        # racing it — a drill that fails intermittently teaches people to re-run it instead of reading it.
        deadline = time.time() + 60
        state = "t"
        while time.time() < deadline:
            state = recovery.psql("SELECT pg_is_in_recovery()")
            if state == "f":
                break
            time.sleep(0.5)
        if state != "f":
            raise DrillError("the recovered cluster never finished recovery within 60s")
        print("   cluster promoted out of recovery")

        step("7. assert what survived, and what did not")
        present = int(recovery.psql(
            f"SELECT COUNT(*) FROM users WHERE email_normalized = '{BEFORE_EMAIL}'", "edumart"))
        absent = int(recovery.psql(
            f"SELECT COUNT(*) FROM users WHERE email_normalized = '{AFTER_EMAIL}'", "edumart"))
        if present != 1:
            raise DrillError(f"the account created before the target is missing ({present}); "
                             f"WAL was not replayed")
        if absent != 0:
            raise DrillError("the account created after the target survived: the recovery target time "
                             "was ignored, which is the failure that matters")
        print(f"   {BEFORE_EMAIL} recovered from WAL; {AFTER_EMAIL} correctly absent")

        step("8. the recovered cluster serves the product")
        process, base = serve(binary, recovery.dsn, args.api_port, os.path.join(workdir, "api-recovered.log"))
        try:
            status, body = http(base, "POST", "/v1/auth/login",
                                {"email": "teacher@edumart.ng", "password": "Teacher123!"})
            if status != 200:
                raise DrillError(f"the recovered cluster refuses a login: {status} {body}")
        finally:
            stop_api(process)
        print("   login works against the recovered cluster")

        print("\nPITR drill passed: the cluster was destroyed and recovered to a chosen second")
        return 0
    except DrillError as caught:
        print(f"\nPITR drill FAILED: {caught}", file=sys.stderr)
        for label, path in (("recovery log", os.path.join(workdir, "recovery.log")),
                            ("server log", logfile)):
            if os.path.exists(path):
                with open(path) as handle:
                    print(f"\n{label}:\n{handle.read()[-1500:]}", file=sys.stderr)
        if args.keep:
            print(f"cluster kept in {workdir}", file=sys.stderr)
        return 1
    finally:
        recovery.stop()
        cluster.stop()
        if not args.keep and not os.environ.get("EDUMART_KEEP_DRILL"):
            shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
