#!/usr/bin/env python3
"""Rehearse a rolling rollout: replace every replica while traffic keeps arriving.

`deploy-rehearsal.py` answers "can I roll back?" — one process at a time, against a database. It says
nothing about the thing an orchestrator actually does, which is **replace instances while the service stays
up**. Those are different failures:

  * a replica that is switched into the rotation before it is ready serves 503s it would never have served
    had anyone asked /readyz first;
  * a rollout that stops the last old replica before a new one is ready has a window with zero capacity,
    and no amount of "the tests pass" makes that window invisible to users;
  * a rollout that cannot be **aborted halfway** is a rollout where a bad build has to be run to completion
    before it can be fixed.

So this script runs a real fleet behind a real load balancer and performs the swap:

  1. **rolling** — four replicas, replaced one at a time with a surge of one. A client thread requests the
     API continuously. The gate is that no client request ever fails, and the last observation is the new
     build.
  2. **no-surge** — a replacement order with no spare capacity (stop, then start, same port). Capacity dips
     to three of four, which is allowed; errors are not.
  3. **abort** — the rollout is stopped halfway through and every already-replaced replica is put back on
     the old binary. This is the operator's escape hatch, and it has to leave a fleet that is entirely one
     release again.

The load balancer here refuses to route to a replica that is not ready *and* records it when it would have,
because the interesting assertion is not "the balancer is polite" — it is "a tenant of this system never saw
an error during a deploy".

Usage:
    python3 scripts/rollout-rehearsal.py                       # SQLite, four replicas
    python3 scripts/rollout-rehearsal.py --dsn 'postgres://…'  # PostgreSQL (recommended)
    python3 scripts/rollout-rehearsal.py --from HEAD~1 --keep

Environment:
    EDUMART_API_BIN   reuse a built binary for the *previous* release
    PGBIN             directory holding createdb/psql
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
API_DIR = os.path.join(REPO, "apps", "api")


class RolloutError(RuntimeError):
    pass


# A gate nobody has seen fail is a gate that might be asserting nothing. These two faults are the failure
# modes this script exists to catch, injected on purpose so the CI job can prove it still catches them:
#
#   early  — a replicas joins the rotation before its process is listening. Real symptom: connection
#            refused on a request the balancer believed it could serve.
#   orphan — a retired replica stays in the rotation. Real symptom: a request lands on a stopped process.
FAULT = "none"


def run(command: list[str], *, cwd: str | None = None, check: bool = True, env: dict | None = None) -> str:
    result = subprocess.run(command, capture_output=True, text=True, cwd=cwd, env={**os.environ, **(env or {})})
    if check and result.returncode != 0:
        raise RolloutError(f"{' '.join(command)} failed ({result.returncode}): {(result.stderr or result.stdout).strip()}")
    return result.stdout.strip()


def free_port(start: int) -> int:
    """A port nothing is listening on. Asking the kernel beats guessing, and a collision here reads as a
    mysterious connection refused halfway through a rollout."""
    for port in range(start, start + 200):
        with socket.socket() as probe:
            try:
                probe.bind(("127.0.0.1", port))
            except OSError:
                continue
            return port
    raise RolloutError(f"no free port between {start} and {start + 200}")


def get(url: str, timeout: float = 5.0, method: str = "GET", body: dict | None = None,
        token: str | None = None) -> tuple[int, dict]:
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as caught:
        try:
            return caught.code, json.loads(caught.read() or b"{}")
        except json.JSONDecodeError:
            return caught.code, {}
    except (urllib.error.URLError, OSError) as failure:
        return 0, {"error": str(failure)}


# ── the fleet ───────────────────────────────────────────────────────────────────────────────────────


def forward(url: str, method: str, payload: bytes | None, headers) -> tuple[int, dict]:
    """Proxy one request to one replica, preserving method and body."""
    request = urllib.request.Request(url, data=payload, method=method)
    if headers.get("Content-Type"):
        request.add_header("Content-Type", headers["Content-Type"])
    if headers.get("Authorization"):
        request.add_header("Authorization", headers["Authorization"])
    if headers.get("Cookie"):
        request.add_header("Cookie", headers["Cookie"])
    try:
        with urllib.request.urlopen(request, timeout=20.0) as response:
            return response.status, json.loads(response.read() or b"{}")
    except urllib.error.HTTPError as caught:
        try:
            return caught.code, json.loads(caught.read() or b"{}")
        except json.JSONDecodeError:
            return caught.code, {}
    except (urllib.error.URLError, OSError) as failure:
        return 0, {"error": str(failure)}


class Replica:
    """One API process. `release` is the label the script prints, `binary` is what it actually runs."""

    # Every replica ever started, so the failure path can stop them all. Without this a failed rehearsal
    # leaves four API processes listening: the next run then collides on a port and reports it as
    # "the replica never became ready", which is a debugging session nobody deserves.
    started: list["Replica"] = []

    def __init__(self, name: str, binary: str, release: str, port: int, common_env: dict, log_dir: str):
        self.name = name
        self.binary = binary
        self.release = release
        self.port = port
        self.base = f"http://127.0.0.1:{port}"
        self.env = {**common_env, "PORT": str(port)}
        self.log_path = os.path.join(log_dir, f"{name}.log")
        self.process: subprocess.Popen | None = None
        self.started_at = 0.0
        Replica.started.append(self)
        self.ready_at = 0.0

    def start(self, timeout: float = 60.0) -> "Replica":
        handle = open(self.log_path, "a")
        self.started_at = time.time()
        self.process = subprocess.Popen([self.binary], env={**os.environ, **self.env},
                                        stdout=handle, stderr=subprocess.STDOUT)
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self.process.poll() is not None:
                raise RolloutError(f"{self.name} exited with {self.process.returncode}:\n{self.log_tail()}")
            status, _ = get(self.base + "/readyz", timeout=1.0)
            if status == 200:
                self.ready_at = time.time()
                return self
            time.sleep(0.05)
        raise RolloutError(f"{self.name} never became ready:\n{self.log_tail()}")

    def stop(self, timeout: float = 20.0) -> None:
        if self.process and self.process.poll() is None:
            # SIGTERM, not SIGKILL: the API drains in-flight requests on SIGTERM, and a rollout that kills
            # connections is a rollout that drops whatever was in flight. If draining hangs, the timeout
            # below is what stops a stuck process from wedging the deploy.
            self.process.terminate()
            try:
                self.process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=10)
        self.process = None
        self.ready_at = 0.0

    def is_ready(self) -> bool:
        return get(self.base + "/readyz", timeout=0.5)[0] == 200

    def version(self) -> str:
        status, body = get(self.base + "/healthz", timeout=2.0)
        if status != 200:
            return f"unreachable({status})"
        return str(body.get("version", "?"))

    def log_tail(self, limit: int = 1200) -> str:
        try:
            with open(self.log_path) as handle:
                return handle.read()[-limit:]
        except OSError:
            return "(no log)"


class Balancer:
    """A load balancer over the ready replicas, with the assertions built in.

    Deliberately naïve about routing (round robin) and pedantic about one thing: a replica is only a
    candidate while its own readiness endpoint says so. The interesting counters are `not_ready_hits`
    (a request that would have gone to an unready replica) and `no_backend` (a moment with zero capacity).
    """

    started: list["Balancer"] = []

    def __init__(self, port: int):
        self.port = port
        Balancer.started.append(self)
        self.base = f"http://127.0.0.1:{port}"
        self.replicas: list[Replica] = []
        self.lock = threading.Lock()
        self.cursor = 0
        self.served = 0
        self.no_backend = 0
        self.not_ready_hits = 0
        self.unreachable = 0
        self.registered_unready: list[str] = []
        self.statuses: dict[int, int] = {}
        self.latencies: list[float] = []
        self.versions_seen: dict[str, int] = {}
        balancer = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *_args):
                pass

            def do_GET(self):  # noqa: N802 — the base class decides the name
                balancer.handle(self)

            def do_POST(self):  # noqa: N802
                balancer.handle(self)

            def do_PUT(self):  # noqa: N802
                balancer.handle(self)

            def do_DELETE(self):  # noqa: N802
                balancer.handle(self)

        self.server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
        self.server.daemon_threads = True

    def set_replicas(self, replicas: list[Replica]) -> None:
        """Replace the rotation, and check the one thing that must be true of every member.

        A replica is added only when it is ready, so the check happens here rather than being inferred from
        traffic later: waiting for a client to be unlucky is not a gate, it is a test that passes when the
        machine is fast.
        """
        for replica in replicas:
            if not replica.is_ready():
                self.registered_unready.append(replica.name)
        with self.lock:
            self.replicas = list(replicas)

    def pick(self) -> Replica | None:
        with self.lock:
            candidates = list(self.replicas)
            if not candidates:
                self.no_backend += 1
                return None
            for offset in range(len(candidates)):
                replica = candidates[(self.cursor + offset) % len(candidates)]
                if replica.is_ready():
                    self.cursor = (self.cursor + offset + 1) % len(candidates)
                    return replica
                self.not_ready_hits += 1
                if os.environ.get("EDUMART_ROLLOUT_DEBUG"):
                    alive = replica.process is not None and replica.process.poll() is None
                    print(f"    skip {replica.name} (port {replica.port}, process alive={alive})")
            self.no_backend += 1
            return None

    def handle(self, handler: BaseHTTPRequestHandler) -> None:
        replica = self.pick()
        if replica is None:
            handler.send_response(503)
            handler.send_header("Content-Type", "application/json")
            handler.send_header("Content-Length", "33")
            handler.end_headers()
            handler.wfile.write(b'{"error":"service unavailable"}\n')
            return
        started = time.time()
        length = int(handler.headers.get("Content-Length") or 0)
        # The body has to be forwarded, not dropped: a rollout where every read succeeds and every write
        # arrives empty is a rollout that looks green and loses money.
        payload_in = handler.rfile.read(length) if length else None
        status, body = forward(replica.base + handler.path, handler.command, payload_in, handler.headers)
        elapsed = time.time() - started
        with self.lock:
            self.served += 1
            if status == 0:
                # The balancer could not reach a replica it had chosen. Counting this as a status code
                # would let a rollout of dead endpoints report a clean run.
                self.unreachable += 1
            self.statuses[status] = self.statuses.get(status, 0) + 1
            self.latencies.append(elapsed)
        payload = json.dumps(body).encode()
        handler.send_response(status)
        handler.send_header("Content-Type", "application/json")
        handler.send_header("Content-Length", str(len(payload)))
        handler.end_headers()
        handler.wfile.write(payload)

    def start(self) -> "Balancer":
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        return self

    def stop(self) -> None:
        self.server.shutdown()
        self.server.server_close()

    def summary(self) -> str:
        with self.lock:
            if not self.latencies:
                return "served 0 requests"
            ordered = sorted(self.latencies)
            p95 = ordered[min(len(ordered) - 1, int(len(ordered) * 0.95))]
            return (f"served {self.served} requests, p95 {p95 * 1000:.0f} ms, "
                    f"statuses {self.statuses}")


class Traffic:
    """Continuous client traffic during a rollout.

    Sequential on purpose: the point is not throughput (there is a load generator for that) but that
    *every* request succeeded, and a dropped connection is easier to attribute when there is one at a time.
    """

    def __init__(self, base: str, token: str):
        self.base = base
        self.token = token
        self.failures: list[str] = []
        self.count = 0
        self.running = False
        self.thread: threading.Thread | None = None

    def _loop(self) -> None:
        while self.running:
            status, body = get(self.base + "/v1/subjects", token=self.token, timeout=25.0)
            self.count += 1
            if status != 200:
                self.failures.append(f"GET /v1/subjects → {status} {body}")
                if len(self.failures) > 5:
                    return
            time.sleep(0.05)

    def start(self) -> "Traffic":
        self.running = True
        self.thread = threading.Thread(target=self._loop, daemon=True)
        self.thread.start()
        return self

    def stop(self) -> None:
        self.running = False
        if self.thread:
            self.thread.join(timeout=30)


# ── rollout shapes ──────────────────────────────────────────────────────────────────────────────────


# How long a removed replica keeps serving before it is stopped. Long enough for a request that was
# already routed to it, short enough that a four-replica rollout is still seconds rather than minutes.
DRAIN_SECONDS = 0.4


def versions_of(replicas: list[Replica]) -> dict[str, int]:
    """How many replicas are on each release. Asked of the processes, not of this script's bookkeeping."""
    seen: dict[str, int] = {}
    for replica in replicas:
        reported = replica.version()
        seen[reported] = seen.get(reported, 0) + 1
    return seen


def sign_in(base: str) -> str:
    status, body = get(base + "/v1/auth/login", method="POST",
                       body={"email": "student@edumart.ng", "password": "Student123!"})
    if status != 200 or not body.get("token"):
        raise RolloutError(f"the demo learner cannot sign in through the balancer ({status}) {body}")
    return body["token"]


def require_clean(description: str, traffic: Traffic, balancer: Balancer) -> None:
    if balancer.registered_unready:
        raise RolloutError(
            f"{description}: {len(balancer.registered_unready)} replica(s) were put in the rotation before "
            f"they were ready: {sorted(set(balancer.registered_unready))}"
        )
    # Every member of the rotation must still be reachable *now*. A stale endpoint that nobody happened to
    # hit during a two-second window is still a broken rotation.
    stale = [replica.name for replica in balancer.replicas if not replica.is_ready()]
    if stale:
        raise RolloutError(f"{description}: stopped or unready replicas are still in the rotation: {sorted(set(stale))}")
    if balancer.unreachable:
        raise RolloutError(
            f"{description}: the balancer chose a replica it could not reach {balancer.unreachable} time(s). "
            "An endpoint was in the rotation before it could serve."
        )
    if traffic.failures:
        raise RolloutError(f"{description}: a client saw an error during the rollout: {traffic.failures[:3]}")
    if balancer.not_ready_hits:
        raise RolloutError(
            f"{description}: the balancer had to skip {balancer.not_ready_hits} unready replica(s). A replica "
            "was in the rotation before it could serve."
        )
    if balancer.no_backend:
        raise RolloutError(
            f"{description}: the fleet had zero capacity {balancer.no_backend} time(s). Capacity may dip; it "
            "may not reach zero."
        )
    print(f"  ok  {traffic.count} client requests, none failed — {balancer.summary()}")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Replace every replica under live traffic and prove nothing broke.")
    parser.add_argument("--from", dest="previous", help="git ref of the release being replaced (default origin/main)")
    parser.add_argument("--engine", default="postgres", choices=["postgres", "sqlite"])
    parser.add_argument("--dsn", help="throwaway PostgreSQL database")
    parser.add_argument("--replicas", type=int, default=4)
    parser.add_argument("--port", type=int, default=8120, help="first replica port; the balancer follows the fleet")
    parser.add_argument("--keep", action="store_true", help="keep the working directory")
    parser.add_argument("--fault", default="none", choices=["none", "early", "orphan"],
                        help="inject a known rollout failure to prove the checks catch it")
    args = parser.parse_args(argv)

    global FAULT
    FAULT = args.fault
    if FAULT != "none":
        print(f"FAULT INJECTION: {FAULT} — this run is expected to FAIL\n")

    ref = args.previous or "origin/main"
    previous_sha = run(["git", "-C", REPO, "rev-parse", "--short", ref])
    current_sha = run(["git", "-C", REPO, "rev-parse", "--short", "HEAD"])
    if previous_sha == current_sha:
        print(f"note: {ref} is the same commit as HEAD, so the two releases differ only by the build\n"
              "      stamp. That still exercises the swap; it does not exercise a schema change.")

    workdir = tempfile.mkdtemp(prefix="edumart-rollout-")
    worktree = os.path.join(workdir, "previous")
    try:
        print(f"rollout: {ref} ({previous_sha}) → working tree ({current_sha})")
        print(f"  working directory: {workdir}")

        print("\nbuild")
        run(["git", "-C", REPO, "worktree", "add", "--detach", worktree, ref])
        old_bin = os.environ.get("EDUMART_API_BIN")
        if old_bin:
            print(f"  reusing {old_bin} as the previous release")
        else:
            old_bin = os.path.join(workdir, "api-previous")
            print("  building the previous release")
            ldflags_old = ["-ldflags", f"-X main.version=old-{previous_sha}", "-o", old_bin, "./cmd/api"]
            run(["go", "build", *ldflags_old], cwd=os.path.join(worktree, "apps", "api"))
        new_bin = os.path.join(workdir, "api-current")
        print("  building this release")
        run(["go", "build", "-ldflags", f"-X main.version=new-{current_sha}", "-o", new_bin, "./cmd/api"],
            cwd=API_DIR)
        old_version = f"old-{previous_sha}"
        new_version = f"new-{current_sha}"

        engine = "sqlite3" if args.engine == "sqlite" else "postgres"
        if args.engine == "postgres":
            if not args.dsn:
                raise RolloutError("--engine postgres needs --dsn pointing at a throwaway database")
            dsn = args.dsn
            reset_postgres(dsn)
        else:
            dsn = os.path.join(workdir, "rollout.db")

        common_env = {
            "EDUMART_DRIVER": engine,
            "EDUMART_DSN": dsn,
            "EDUMART_ENV": "development",
            "EDUMART_NOTIFY": "log",
            "EDUMART_SEED": "true",
            "EDUMART_SEED_DEMO_USERS": "true",
            "EDUMART_ALLOW_SANDBOX_PAYMENTS": "true",
        }

        # The schema is migrated once, before any replica starts — the deploy step, not a boot side effect.
        print("\n0. migrate, as a deploy step")
        out = run([new_bin, "-migrate-only"], cwd=REPO, env=common_env)
        if "migrations applied" not in out:
            raise RolloutError(f"the migration job said something unexpected: {out}")
        print(f"  ok  {out.splitlines()[-1]}")

        ports = [free_port(args.port + index * 10) for index in range(args.replicas)]
        balancer = Balancer(free_port(args.port + args.replicas * 10 + 5)).start()
        fleet: list[Replica] = []

        print(f"\n1. bring up {args.replicas} replicas of the previous release behind a balancer")
        for index, port in enumerate(ports):
            replica = Replica(f"old-{index + 1}", old_bin, old_version, port, common_env, workdir)
            replica.start()
            fleet.append(replica)
        balancer.set_replicas(fleet)
        print(f"  ok  {args.replicas} ready on {', '.join(str(p) for p in ports)}")

        token = sign_in(balancer.base)
        traffic = Traffic(balancer.base, token).start()
        print("  ok  a client signed in through the balancer; traffic started")

        # ── scenario 1: rolling replacement with a surge of one ─────────────────────────────────
        print("\n2. rolling: replace one replica at a time, with one spare")
        # `list(fleet)`: the loop body removes the replica it just handled, so iterating the live list
        # skipped every second one the first time this ran.
        for index, retired in enumerate(list(fleet)):
            fresh = Replica(f"new-{index + 1}", new_bin, new_version, free_port(args.port + 100 + index * 10),
                            common_env, workdir)
            if FAULT == "early":
                # In rotation before it is listening: the classic cause of a deploy that "only" produces a
                # few hundred 502s in the dashboards and no failing test.
                balancer.set_replicas(fleet + [fresh])
            fresh.start()
            # Surge first: the new replica joins the rotation while the old one is still serving, so
            # capacity never dips. This is the shape a Deployment with maxSurge=1 performs.
            fleet.append(fresh)
            balancer.set_replicas(fleet)
            fleet.remove(retired)
            if FAULT != "orphan":
                # The one line that has to be right in every rolling update: a replica that has stopped is
                # removed from the rotation, and it is removed *before* it is stopped.
                balancer.set_replicas(fleet)
                # Then drained. A request can already have been routed to this replica while it was still
                # a candidate, and stopping it immediately kills that request — which is why every real
                # orchestrator has a termination grace period and every real proxy has a drain delay.
                time.sleep(DRAIN_SECONDS)
            retired.stop()
            print(f"  {retired.name} → {fresh.name} (port {retired.port} → {fresh.port})")
            # Let traffic actually flow between swaps. Without this the whole rollout finishes inside a
            # handful of requests and proves very little.
            time.sleep(0.5)
        time.sleep(0.5)
        live = versions_of(fleet)
        if live != {new_version: args.replicas}:
            raise RolloutError(f"after the rollout the fleet is {live}, expected every replica on {new_version}")
        print(f"  ok  every replica now runs {new_version}")
        require_clean("rolling", traffic, balancer)

        # ── scenario 2: the same shape with no spare capacity ──────────────────────────────────
        print("\n3. no-surge: stop the old replica before starting its replacement, same port")
        for index, retired in enumerate(list(fleet)):
            fleet.remove(retired)
            balancer.set_replicas(fleet)
            time.sleep(DRAIN_SECONDS)
            retired.stop()
            fresh = Replica(f"old-again-{index + 1}", old_bin, old_version, retired.port, common_env, workdir)
            fresh.start()
            fleet.append(fresh)
            balancer.set_replicas(fleet)
        time.sleep(0.5)
        require_clean("no-surge", traffic, balancer)
        print("  ok  capacity dipped but never reached zero")

        # ── scenario 3: abort the rollout halfway ──────────────────────────────────────────────
        print("\n4. abort: begin the rollout again and stop it halfway")
        replaced = 0
        for index, retired in enumerate(list(fleet)):
            if replaced >= args.replicas // 2:
                break
            # A spare port, exactly like the rolling scenario: two processes cannot bind one port, so
            # "replace in place" here means the replacement comes up beside the one it replaces.
            fresh = Replica(f"new-abort-{index + 1}", new_bin, new_version,
                            free_port(args.port + 300 + index * 10), common_env, workdir)
            fresh.start()
            fleet.append(fresh)
            balancer.set_replicas(fleet)
            fleet.remove(retired)
            balancer.set_replicas(fleet)
            time.sleep(DRAIN_SECONDS)
            retired.stop()
            replaced += 1
            time.sleep(0.5)
        print(f"  {replaced} of {args.replicas} replicas replaced when the abort was called")

        # The escape hatch: put every replaced replica back on the old binary. Doing this *under traffic*
        # is the point — an abort that requires downtime is an abort nobody performs.
        for index, replica in enumerate(list(fleet)):
            if replica.release == new_version:
                # A spare port, not the one the new replica still holds: an abort is a rollout in the
                # opposite direction, and it gets the same surge. Binding the same port here fails with
                # "address already in use" and would have turned the escape hatch into an outage.
                restored = Replica(f"restored-{replica.name}", old_bin, old_version,
                                   free_port(args.port + 200 + index * 10), common_env, workdir)
                restored.start()
                fleet.append(restored)
                balancer.set_replicas(fleet)
                fleet.remove(replica)
                balancer.set_replicas(fleet)
                time.sleep(DRAIN_SECONDS)
                replica.stop()
        time.sleep(0.5)
        live = versions_of(fleet)
        # The question is "is anything still on the new build?", not "is everything on the old label".
        # A release built before this script existed has no build stamp, so it honestly reports nothing —
        # and a gate that demanded the new label would fail for the one rollout it must always allow,
        # the one back to an older binary.
        if new_version in live:
            raise RolloutError(f"after the abort the fleet is {live}, still serving {new_version}")
        print(f"  ok  nothing is left on {new_version} (fleet: {live})")
        require_clean("abort", traffic, balancer)

        # Data written before the rollout has to still be there afterwards: a rollout is not a migration
        # event, and a version that quietly resets state is a much worse bug than an outage.
        status, me = get(balancer.base + "/v1/me", token=token)
        if status != 200 or not me.get("user", {}).get("email"):
            raise RolloutError(f"the session issued before the rollout no longer works ({status}) {me}")
        print(f"\n  ok  the session and data from before the rollout survived it ({me['user']['email']})")

        traffic.stop()
        print(f"  ok  {traffic.count} requests total, {len(traffic.failures)} failures")

        print("\nrollout rehearsal passed: replaced, downgraded and aborted a fleet without dropping a request")
        return 0
    except RolloutError as failure:
        print(f"\nrollout FAILED: {failure}", file=sys.stderr)
        return 1
    finally:
        for replica in Replica.started:
            replica.stop(timeout=5)
        for balancer in Balancer.started:
            try:
                balancer.stop()
            except Exception:  # noqa: BLE001 — a socket that is already closed must not hide the real failure
                pass
        subprocess.run(["git", "-C", REPO, "worktree", "remove", "--force", worktree], capture_output=True)
        if not args.keep:
            shutil.rmtree(workdir, ignore_errors=True)
        else:
            print(f"kept {workdir}")


def reset_postgres(dsn: str) -> None:
    psql = shutil.which("psql") or _pgbin("psql")
    if not psql:
        raise RolloutError("psql is not on PATH; install the PostgreSQL client or set PGBIN")
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
