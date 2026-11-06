#!/usr/bin/env python3
"""Bring the deployment manifests up on a real cluster, and check that it serves.

For a long time the honest answer about `infra/k8s` was "written, checked, never applied". Checks over YAML find
the mistakes that are visible in YAML; they cannot find out whether the Pod Security profile admits these
workloads, whether the migration Job finishes before the replicas want a schema, whether `/readyz` is reachable
through the probe that gates the Service, or whether the web container can reach the API by its Service name.

This script answers those questions by doing it: `kind` creates a cluster, both images are built and loaded into
it, `infra/k8s/overlays/staging` is applied, and then — with port-forwards, because the rehearsal has no ingress
controller — it asks for a page and for an API response.

    python3 scripts/cluster-rehearsal.py                 # create, apply, verify, delete
    python3 scripts/cluster-rehearsal.py --keep          # leave the cluster for inspection
    python3 scripts/cluster-rehearsal.py --skip-build    # reuse the images already in the cluster

Needs `docker`, `kind` and `kubectl`. It is run by the `Cluster rehearsal (kind)` job in CI on every pull request,
which is the only claim being made: that this deployment has been applied to a cluster and served requests. What
it does not prove — NetworkPolicy enforcement, real ReadWriteMany semantics, the Ingress, a managed database — is
listed in `infra/k8s/overlays/staging/kustomization.yaml` rather than left to be inferred from a green check.
"""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OVERLAY = os.path.join("infra", "k8s", "overlays", "staging")
FAILURES: list[str] = []
FATAL = False


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok   {message}")
        return
    FAILURES.append(message)
    print(f"  FAIL {message}")


def run(args: list[str], *, check_status: bool = True, timeout: int = 600) -> subprocess.CompletedProcess[str]:
    print(f"  $ {' '.join(args)}")
    result = subprocess.run(args, cwd=REPO, capture_output=True, text=True, timeout=timeout)
    if check_status and result.returncode != 0:
        print(result.stdout[-4000:])
        print(result.stderr[-4000:])
        # A command that died is the failure, and the workflow's log is not always obtainable — so the command,
        # its exit code and the end of its output go out as an annotation as well.
        global FATAL
        FATAL = True
        detail = (result.stdout[-1500:] + result.stderr[-1500:]).strip()
        print(f"::error title=Cluster rehearsal::{' '.join(args)} exited {result.returncode}")
        for line in detail.splitlines()[-12:]:
            print(f"::warning title=Cluster rehearsal output::{line}")
        raise SystemExit(f"command failed ({result.returncode}): {' '.join(args)}")
    return result


def kubectl(*args: str, check_status: bool = True) -> subprocess.CompletedProcess[str]:
    return run(["kubectl", *args], check_status=check_status)


def wait_for(description: str, probe, timeout: int, interval: float = 3.0) -> bool:
    """Poll a predicate, printing a heartbeat so a slow cluster does not look like a hung one."""
    deadline = time.time() + timeout
    last_report = 0.0
    while time.time() < deadline:
        try:
            if probe():
                return True
        except Exception:  # noqa: BLE001 - a probe that throws is a probe that is not true yet
            pass
        if time.time() - last_report > 20:
            print(f"  … waiting for {description}")
            last_report = time.time()
        time.sleep(interval)
    return False


def http_get(url: str, timeout: int = 10) -> tuple[int, str]:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as response:
            return response.status, response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode("utf-8", "replace")
    except urllib.error.URLError as error:
        return 0, str(error)


def start_port_forward(service: str, local: int, remote: int) -> subprocess.Popen[str]:
    process = subprocess.Popen(
        ["kubectl", "-n", "edumart", "port-forward", f"svc/{service}", f"{local}:{remote}"],
        cwd=REPO,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    # Waiting for the port to answer is more reliable than waiting for the forwarder's own log line.
    if not wait_for(f"port-forward {service}:{remote}", lambda: http_get(f"http://127.0.0.1:{local}/", 2)[0] != 0, 60):
        process.terminate()
        raise SystemExit(f"port-forward for {service} never came up")
    return process


def annotate(title: str, text: str, limit: int = 8) -> None:
    """Emit the end of something as workflow-command annotations.

    The full output stays in the log; this is what a person opening the pull request actually sees, and the log of a
    failed job is not always obtainable — which is how the first three runs of this job were debugged.

    These are warnings, not errors, and that is deliberate: a check run keeps only the first ten error annotations.
    Marking several log lines as errors filled that budget once, and the annotation that named the failed check —
    emitted at the end — was silently dropped. The one annotation that must never be crowded out is the reason.
    """
    lines = [line for line in text.splitlines() if line.strip()][-limit:]
    for line in lines:
        print(f"::warning title={title}::{line[:900]}")


def diagnose() -> None:
    """Print what a person would need to see, and annotate the part that explains the failure."""
    for args in (
        ("-n", "edumart", "get", "pods", "-o", "wide"),
        ("-n", "edumart", "get", "pvc,jobs,deployments,services"),
        ("-n", "edumart", "get", "events", "--sort-by=.lastTimestamp"),
    ):
        result = kubectl(*args, check_status=False)
        print(result.stdout[-6000:] or result.stderr[-2000:])

    # Why a pod is not Ready, in the two forms that answer it: what the container is doing, and what it said.
    for pod in kubectl("-n", "edumart", "get", "pods", "-o", "name", check_status=False).stdout.split():
        state = kubectl("-n", "edumart", "get", pod, "-o",
                        "jsonpath={range .status.containerStatuses[*]}{.name}={.state}{'\n'}{end}",
                        check_status=False).stdout
        annotate(f"pod {pod.split('/')[-1]}", state, limit=4)
    annotate("migration Job logs", kubectl(
        "-n", "edumart", "logs", "job/edumart-migrate", "--all-containers=true", "--tail=30",
        check_status=False).stdout, limit=15)
    logs = kubectl("-n", "edumart", "logs", "-l", "app.kubernetes.io/name=edumart-api", "--tail=12",
                   "--all-containers=true", check_status=False)
    annotate("edumart-api logs", logs.stdout + logs.stderr, limit=12)
    described = kubectl("-n", "edumart", "describe", "pods", "-l", "app.kubernetes.io/name=edumart-api",
                        check_status=False)
    annotate("edumart-api describe", described.stdout, limit=12)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cluster-name", default="edumart-staging")
    parser.add_argument("--keep", action="store_true", help="Do not delete the cluster at the end.")
    parser.add_argument("--skip-build", action="store_true", help="Use images already loaded into the cluster.")
    args = parser.parse_args()

    for tool in ("docker", "kubectl", "kind"):
        if shutil.which(tool) is None:
            raise SystemExit(f"{tool} is required for the cluster rehearsal")

    forwards: list[subprocess.Popen[str]] = []
    try:
        print("1. a cluster")
        existing = run(["kind", "get", "clusters"], check_status=False).stdout
        if args.cluster_name not in existing.split():
            run(["kind", "create", "cluster", "--name", args.cluster_name, "--wait", "180s"], timeout=900)
        check(args.cluster_name in run(["kind", "get", "clusters"], check_status=False).stdout,
              f"cluster {args.cluster_name} exists")

        if not args.skip_build:
            print("2. the images CI builds, built again here and loaded into the cluster")
            run(["docker", "build", "-f", "apps/api/Dockerfile", "--build-arg", "VERSION=sha-rehearsal",
                 "-t", "edumart-api:rehearsal", "apps/api"], timeout=1800)
            run(["docker", "build", "-f", "apps/web/Dockerfile", "-t", "edumart-web:rehearsal", "."], timeout=1800)
            run(["kind", "load", "docker-image", "--name", args.cluster_name,
                 "edumart-api:rehearsal", "edumart-web:rehearsal"], timeout=900)
            check(True, "both images are in the cluster's image store")

        print("3. the manifests, applied to it")
        base = kubectl("kustomize", OVERLAY, check_status=False)
        check(base.returncode == 0 and "kind: Deployment" in base.stdout,
              "kustomize renders the overlay (kubectl apply -k would refuse it otherwise)")
        # First pass also creates the namespace and the PostgreSQL fixture. The migration Job will usually start
        # before PostgreSQL can accept a connection and fail once — which is why the Job is recreated below, after
        # the database is up. That is the deploy order a real release uses too (`-migrate-only` runs when the
        # database is known to be reachable); pretending otherwise would only hide the race here.
        kubectl("apply", "-k", OVERLAY, check_status=False)
        check(wait_for("the PostgreSQL fixture", lambda: kubectl(
            "-n", "edumart", "get", "deploy", "postgres-staging",
            "-o", "jsonpath={.status.availableReplicas}", check_status=False).stdout.strip() == "1", 240),
            "the staging database is available")

        # The database must accept a query, not just report a ready pod: `pg_isready` answers while the entrypoint
        # is still creating roles, and a migration Job that arrives in that window fails once and — with
        # `backoffLimit: 0` — stays failed.
        accepts = wait_for("PostgreSQL to accept a query", lambda: kubectl(
            "-n", "edumart", "exec", "deploy/postgres-staging", "--",
            "psql", "-U", "edumart", "-d", "edumart", "-c", "select 1", check_status=False).returncode == 0, 240)
        check(accepts, "the staging database accepts queries")

        kubectl("-n", "edumart", "delete", "job", "edumart-migrate", "--ignore-not-found")
        kubectl("apply", "-k", OVERLAY)
        job = kubectl("-n", "edumart", "wait", "--for=condition=complete", "job/edumart-migrate",
                      "--timeout=300s", check_status=False)
        check(job.returncode == 0, "the migration Job completed (schema matches the binary)")
        if job.returncode != 0:
            # The Job's own account of itself: which migration it died on is the whole answer, and it is not
            # visible anywhere else.
            annotate("migration Job logs", kubectl(
                "-n", "edumart", "logs", "job/edumart-migrate", "--all-containers=true", "--tail=40",
                check_status=False).stdout, limit=20)
            annotate("migration Job state", kubectl(
                "-n", "edumart", "describe", "job/edumart-migrate", check_status=False).stdout, limit=12)

        api = kubectl("-n", "edumart", "rollout", "status", "deployment/edumart-api", "--timeout=300s",
                      check_status=False)
        check(api.returncode == 0, "three API replicas rolled out")
        web = kubectl("-n", "edumart", "rollout", "status", "deployment/edumart-web", "--timeout=300s",
                      check_status=False)
        check(web.returncode == 0, "two web replicas rolled out")

        ready = kubectl("-n", "edumart", "get", "pods", "-l", "app.kubernetes.io/name=edumart-api",
                        "-o", "jsonpath={.items[*].status.containerStatuses[0].ready}", check_status=False).stdout
        check(len([value for value in ready.split() if value == "true"]) == 3,
              f"all three API pods are Ready (admitted under Pod Security restricted): {ready.strip()!r}")

        pvc = kubectl("-n", "edumart", "get", "pvc", "edumart-media",
                      "-o", "jsonpath={.status.phase}", check_status=False).stdout.strip()
        check(pvc == "Bound", f"the media claim is bound, so recordings have somewhere to land ({pvc})")

        print("4. and it serves")
        # The API is reached through its Service. A port-forward is only how *this script* gets in; the web pod
        # reaches it the way the cluster does, by name, and steps 4 and 5 check that through the proxy.
        forwards.append(start_port_forward("edumart-api", 18080, 8080))
        status, body = http_get("http://127.0.0.1:18080/readyz")
        check(status == 200, f"the API answers /readyz through its Service ({status})")
        try:
            payload = json.loads(body)
        except ValueError:
            payload = {}
        check(payload.get("status") == "ok", f"and says it is ready ({payload.get('status')})")
        names = {item.get("name") for item in payload.get("checks", [])}
        check({"database", "migrations"} <= names,
              f"readiness is checked against the database and the migration ledger ({sorted(names)})")

        forwards.append(start_port_forward("edumart-web", 13000, 3000))
        status, _ = http_get("http://127.0.0.1:13000/")
        check(status == 200, f"the web container serves a page ({status})")
        status, body = http_get("http://127.0.0.1:13000/v1/subjects")
        check(status == 200, f"and proxies /v1 to the API by Service name ({status})")
        try:
            proxied = json.loads(body)
        except ValueError:
            proxied = {}
        check(isinstance(proxied.get("subjects"), list) and len(proxied["subjects"]) > 0,
              "the proxied response is the API's own (a real subject list)")
        status, _ = http_get("http://127.0.0.1:13000/v1/exams/link/AAAAAAAABBBBBBBB")
        check(status == 404, f"an unknown exam code is a 404 through the proxy too ({status})")

        print("5. rolling one replica keeps the Service served")
        addresses_before = kubectl("-n", "edumart", "get", "endpoints", "edumart-api",
                                   "-o", "jsonpath={.subsets[*].addresses[*].ip}", check_status=False).stdout.split()
        check(len(addresses_before) == 3, f"three ready endpoints before the restart ({len(addresses_before)})")

        kubectl("-n", "edumart", "rollout", "restart", "deployment/edumart-api")
        # Sampled, every two seconds, for as long as the restart takes. `scripts/rollout-rehearsal.py` is the
        # continuous version of this against processes; this one checks that the cluster's own rollout
        # (maxSurge 1 / maxUnavailable 0, readiness probes) holds the same line.
        samples: list[tuple[int, int]] = []
        deadline = time.time() + 300
        while time.time() < deadline:
            addresses = kubectl("-n", "edumart", "get", "endpoints", "edumart-api",
                                "-o", "jsonpath={.subsets[*].addresses[*].ip}", check_status=False).stdout.split()
            status, _ = http_get("http://127.0.0.1:13000/v1/subjects", 5)
            samples.append((len(addresses), status))
            done = kubectl("-n", "edumart", "rollout", "status", "deployment/edumart-api", "--timeout=5s",
                           check_status=False).returncode == 0
            if done and len(addresses) == 3:
                break
            time.sleep(2)
        check(len(samples) >= 2, f"the restart was observed ({len(samples)} sample(s))")
        check(all(count > 0 for count, _ in samples),
              f"the Service never dropped to zero endpoints ({[count for count, _ in samples]})")
        check(all(status == 200 for _, status in samples),
              f"and the proxied request kept answering ({[status for _, status in samples]})")
        ready_after = kubectl("-n", "edumart", "get", "pods", "-l", "app.kubernetes.io/name=edumart-api",
                              "-o", "jsonpath={.items[*].status.containerStatuses[0].ready}",
                              check_status=False).stdout
        check(len([value for value in ready_after.split() if value == "true"]) == 3,
              f"three ready replicas after the restart ({ready_after.strip()!r})")
    finally:
        for forward in forwards:
            forward.terminate()
        if FAILURES:
            # First, because a check run keeps only the first ten annotations and the diagnosis below produces
            # dozens of lines of log. A red check whose annotation is the fifteenth line of an API log is a red
            # check nobody can act on.
            print()
            print(f"{len(FAILURES)} check(s) failed:")
            for failure in FAILURES:
                print(f"  - {failure}")
                print(f"::error title=Cluster rehearsal::{failure}")
        if FAILURES or FATAL:
            print()
            print("the cluster state at the point of failure:")
            diagnose()
        if not args.keep:
            run(["kind", "delete", "cluster", "--name", args.cluster_name], check_status=False, timeout=300)

    if FAILURES:
        return 1
    print("cluster rehearsal verified")
    return 0


if __name__ == "__main__":
    sys.exit(main())
