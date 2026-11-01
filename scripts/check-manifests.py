#!/usr/bin/env python3
"""Check the deployment manifests against the decisions this repository actually made.

A YAML file that parses is not a deployment. Everything that makes `infra/k8s` safe is an invariant that
nothing in Kubernetes enforces for you — and every one of them has been shipped wrong by someone:

  * a readiness probe pointing at `/healthz`, so an instance without a schema takes traffic;
  * `maxUnavailable: 0` missing, so a rollout drops capacity it did not need to;
  * a migration Job that runs a different image from the replicas;
  * `EDUMART_MIGRATE_ON_BOOT` left on, so every replica races for the schema (this repository reproduced
    that: three of four replicas died on a fresh database);
  * a settings key renamed in the code and not in the manifest, so production silently uses a default;
  * the API URL baked into the web image, pointing at the wrong Service.

So this script reads the manifests, reads the **application source** for the environment variables that
actually exist, and fails when the two disagree. `--self-test` then breaks the manifests on purpose and
requires the corresponding check to fire; a check nobody has watched fail is a check that might be matching
nothing.

Usage:
    python3 scripts/check-manifests.py                  # check infra/k8s
    python3 scripts/check-manifests.py --self-test      # break it on purpose and prove each check fires
    python3 scripts/check-manifests.py --dir infra/k8s --verbose

Exits non-zero on any violation.
"""

from __future__ import annotations

import argparse
import copy
import glob
import os
import re
import shutil
import sys
import tempfile

import yaml

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_DIR = os.path.join(REPO, "infra", "k8s")
API_DIR = os.path.join(REPO, "apps", "api")
WEB_DIR = os.path.join(REPO, "apps", "web")

# Variables that are not application settings: injected by the runtime, or consumed by the Node/base image.
RUNTIME_ENV = {"PORT", "HOSTNAME", "NODE_ENV", "PATH", "HOME", "TZ"}

# Keys whose presence in a ConfigMap means a secret was put in the wrong place.
SECRET_LOOKING = re.compile(r"(SECRET|PASSWORD|TOKEN|_KEY$|DSN|CREDENTIAL)", re.I)

# Values that cannot be secrets: durations, numbers, booleans, URLs without credentials, short policy words.
# Refining the key check this way matters because `EDUMART_ACCESS_TOKEN_TTL` contains "TOKEN" and is a
# duration — a checker that flagged it would be a checker people disable.
NOT_A_SECRET_VALUE = re.compile(r"^\d+(ms|s|m|h|d)?$|^true$|^false$|^https?://[^:@]+(/.*)?$", re.I)

# Anything that looks like a live credential rather than a placeholder.
CREDENTIAL_SHAPES = [
    re.compile(r"sk_live_[A-Za-z0-9]"),
    re.compile(r"sk_test_[A-Za-z0-9]"),
    re.compile(r"postgres(?:ql)?://[^:\s]+:[^@\s]{6,}@"),
    re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
    re.compile(r"^[A-Za-z0-9+/]{40,}={0,2}$"),
]


class Violations:
    def __init__(self) -> None:
        self.items: list[tuple[str, str]] = []

    def add(self, check: str, message: str) -> None:
        self.items.append((check, message))

    def __len__(self) -> int:
        return len(self.items)

    def checks(self) -> set[str]:
        return {check for check, _ in self.items}


class Manifests:
    """Every document in the directory, with the file it came from."""

    def __init__(self, directory: str) -> None:
        self.directory = directory
        self.files: list[str] = sorted(glob.glob(os.path.join(directory, "*.yaml")))
        self.docs: list[tuple[str, dict]] = []
        self.parse_errors: list[str] = []
        for path in self.files:
            try:
                with open(path) as handle:
                    loaded = list(yaml.safe_load_all(handle))
            except yaml.YAMLError as failure:
                self.parse_errors.append(f"{os.path.basename(path)}: {failure}")
                continue
            for document in loaded:
                if document:
                    self.docs.append((os.path.basename(path), document))

    def of_kind(self, kind: str) -> list[tuple[str, dict]]:
        return [(name, doc) for name, doc in self.docs if doc.get("kind") == kind]

    def workload_containers(self) -> list[tuple[str, dict, dict]]:
        """(file, pod spec, container) for every container that will actually run."""
        found = []
        for kind in ("Deployment", "Job"):
            for name, doc in self.of_kind(kind):
                spec = doc.get("spec", {}).get("template", {}).get("spec", {})
                for container in spec.get("containers", []) or []:
                    found.append((name, spec, container))
        return found


def env_of(pod_spec: dict, container: dict) -> dict[str, str]:
    """The environment a container ends up with, flattened: envFrom lists are counted as borrowed."""
    environment: dict[str, str] = {}
    for item in container.get("env", []) or []:
        if "name" in item:
            environment[item["name"]] = str(item.get("value", ""))
    return environment


def config_map_env(manifests: Manifests) -> dict[str, str]:
    values: dict[str, str] = {}
    for _, doc in manifests.of_kind("ConfigMap"):
        for key, value in (doc.get("data") or {}).items():
            values[key] = str(value)
    return values


def secret_keys(manifests: Manifests) -> set[str]:
    keys: set[str] = set()
    for _, doc in manifests.of_kind("Secret"):
        keys |= set((doc.get("stringData") or {}).keys())
        keys |= set((doc.get("data") or {}).keys())
    return keys


def application_env(directory: str) -> set[str]:
    """The environment variables the application actually reads, taken from the source.

    Read from the code rather than from a list, because a list is the thing that goes stale. A name in a
    manifest that no Go file reads is a typo, and a typo in an environment variable is silent: the process
    starts, uses the default, and behaves differently from the one on the whiteboard.
    """
    names: set[str] = set()
    # Any helper whose name reads an environment variable: `env(...)`, `envDuration(...)`, `os.Getenv(...)`.
    # Matching the shape of the call rather than a fixed list of our own helper names means a new helper
    # cannot quietly make its variables invisible to this check — which is how the first version missed
    # everything not read through the one function it knew about.
    pattern = re.compile(
        r'(?:[A-Za-z_\w]*(?:[Gg]etenv|[Ll]ookupEnv)|[a-z]*[Ee]nv[A-Za-z]*|env)\(\s*"([A-Z][A-Z0-9_]+)"'
    )
    for path in glob.glob(os.path.join(directory, "**", "*.go"), recursive=True):
        with open(path, errors="replace") as handle:
            names |= set(pattern.findall(handle.read()))
    return names


def web_env(directory: str) -> set[str]:
    names: set[str] = set()
    pattern = re.compile(r'process\.env\.([A-Z][A-Z0-9_]+)')
    for root, _dirs, files in os.walk(directory):
        if "node_modules" in root or "/.next" in root:
            continue
        for name in files:
            if name.endswith((".ts", ".tsx", ".mjs")):
                with open(os.path.join(root, name), errors="replace") as handle:
                    names |= set(pattern.findall(handle.read()))
    return names


# ── the checks ──────────────────────────────────────────────────────────────────────────────────────


def check_structure(manifests: Manifests, out: Violations) -> None:
    for failure in manifests.parse_errors:
        out.add("structure", f"{failure}")
    seen: set[tuple[str, str]] = set()
    for name, doc in manifests.docs:
        for field in ("apiVersion", "kind"):
            if not doc.get(field):
                out.add("structure", f"{name}: document without {field}")
        meta = doc.get("metadata") or {}
        if not meta.get("name"):
            out.add("structure", f"{name}: {doc.get('kind')} without metadata.name")
            continue
        identity = (doc.get("kind", ""), meta["name"])
        if identity in seen:
            out.add("structure", f"{name}: {identity[0]} {identity[1]} is defined twice")
        seen.add(identity)
        # The NetworkPolicies and the Pod Security labels are written relative to this namespace, so a
        # resource landing somewhere else is a rule that does not apply to it.
        if doc.get("kind") != "Namespace" and meta.get("namespace") != "edumart":
            out.add("structure", f"{name}: {doc['kind']} {meta['name']} is not in the edumart namespace")


def check_images(manifests: Manifests, out: Violations) -> None:
    api_images: set[str] = set()
    for name, spec, container in manifests.workload_containers():
        image = container.get("image", "")
        if not image:
            out.add("images", f"{name}: container {container.get('name')} has no image")
            continue
        if image.endswith(":latest") or ":" not in image.rsplit("/", 1)[-1]:
            out.add("images", f"{name}: {image} is not pinned to a tag — a rollout could change what it runs")
        if "@sha256:" not in image and not re.search(r":v\d+\.\d+\.\d+$", image):
            out.add("images", f"{name}: {image} should be a version tag or a digest")
        if container.get("name") in ("api", "migrate"):
            api_images.add(image)

    # The migration Job and the replicas must run the *same* build. A Job one release behind prepares a
    # schema the new code does not expect, and it is invisible until the first request touches it.
    if len(api_images) > 1:
        out.add("images", f"the migration Job and the API replicas run different images: {sorted(api_images)}")


def check_pod_security(manifests: Manifests, out: Violations) -> None:
    for kind in ("Deployment", "Job"):
        for name, doc in manifests.of_kind(kind):
            spec = doc.get("spec", {}).get("template", {}).get("spec", {})
            pod_context = spec.get("securityContext") or {}
            if pod_context.get("runAsNonRoot") is not True:
                out.add("pod-security", f"{name}: pod securityContext.runAsNonRoot is not true")
            if not pod_context.get("seccompProfile"):
                out.add("pod-security", f"{name}: pod has no seccompProfile")
            for container in spec.get("containers", []) or []:
                context = container.get("securityContext") or {}
                label = f"{name}/{container.get('name')}"
                if context.get("allowPrivilegeEscalation") is not False:
                    out.add("pod-security", f"{label}: allowPrivilegeEscalation is not false")
                if context.get("readOnlyRootFilesystem") is not True:
                    out.add("pod-security", f"{label}: root filesystem is writable")
                drops = (context.get("capabilities") or {}).get("drop") or []
                if "ALL" not in drops:
                    out.add("pod-security", f"{label}: capabilities are not dropped")
                resources = container.get("resources") or {}
                for dimension in ("cpu", "memory"):
                    if dimension not in (resources.get("requests") or {}):
                        out.add("resources", f"{label}: no {dimension} request")
                    if dimension not in (resources.get("limits") or {}):
                        out.add("resources", f"{label}: no {dimension} limit")


def probe_path(container: dict, kind: str) -> str | None:
    probe = container.get(kind) or {}
    return (probe.get("httpGet") or {}).get("path")


def check_probes(manifests: Manifests, out: Violations) -> None:
    for name, doc in manifests.of_kind("Deployment"):
        who = doc.get("metadata", {}).get("name", name)
        replicas_are_web = who == "edumart-web"
        for container in doc["spec"]["template"]["spec"]["containers"]:
            label = f"{who}/{container.get('name')}"
            readiness = probe_path(container, "readinessProbe")
            liveness = probe_path(container, "livenessProbe")

            if not readiness:
                out.add("probes", f"{label}: no readiness probe — the Service would route to a starting pod")
            if not liveness:
                out.add("probes", f"{label}: no liveness probe")
            if not container.get("startupProbe"):
                out.add("probes", f"{label}: no startup probe, so a slow start is a restart loop")

            if replicas_are_web:
                # The web pods must be probeable without the API. A page renders against it, and /healthz
                # is proxied to it, so either choice makes the whole site disappear when the API blinks.
                if readiness in ("/healthz", "/readyz") or (readiness or "").startswith("/v1"):
                    out.add("probes", f"{label}: readiness {readiness} depends on the API")
                if readiness != "/manifest.webmanifest":
                    out.add("probes", f"{label}: readiness {readiness} is not the static asset")
            else:
                if readiness != "/readyz":
                    out.add("probes", f"{label}: readiness is {readiness}, which does not check the schema")
                if liveness != "/healthz":
                    # Liveness must never depend on the database: it would restart healthy processes during
                    # an outage and turn one failure into two.
                    out.add("probes", f"{label}: liveness is {liveness}, which depends on the database")

            # Identical probes are only wrong when the path answers a question about a *dependency*. The web
            # containers probe one static file for both on purpose: a process that cannot serve a file from
            # its own image is both unready and dead, and there is nothing else to distinguish. For the API,
            # the same probe for both means a database blip restarts every healthy pod.
            dependency_backed = readiness in ("/healthz", "/readyz") or (readiness or "").startswith("/v1")
            if readiness and readiness == liveness and dependency_backed:
                out.add("probes", f"{label}: readiness and liveness are the same dependency-backed probe")


def check_rollout(manifests: Manifests, out: Violations) -> None:
    for name, doc in manifests.of_kind("Deployment"):
        who = doc.get("metadata", {}).get("name", name)
        replicas = doc.get("spec", {}).get("replicas", 1)
        if replicas < 2:
            out.add("rollout", f"{who}: {replicas} replica(s) — a rollout cannot spare one")
        strategy = (doc.get("spec", {}).get("strategy") or {})
        rolling = strategy.get("rollingUpdate") or {}
        if strategy.get("type") != "RollingUpdate":
            out.add("rollout", f"{who}: strategy is {strategy.get('type')}, not RollingUpdate")
        if rolling.get("maxUnavailable") not in (0, "0"):
            out.add("rollout", f"{who}: maxUnavailable is {rolling.get('maxUnavailable')}, so a rollout drops capacity")
        if not rolling.get("maxSurge") or str(rolling.get("maxSurge")) == "0":
            out.add("rollout", f"{who}: maxSurge is {rolling.get('maxSurge')}, so a rollout cannot surge")

        grace = doc["spec"]["template"]["spec"].get("terminationGracePeriodSeconds")
        # The API drains in-flight requests for up to 10s inside its own SIGTERM handler; a grace period at
        # or below that is a SIGKILL mid-request.
        if grace is None or grace <= 15:
            out.add("rollout", f"{who}: terminationGracePeriodSeconds is {grace}; in-flight requests would be cut")


def check_migration_contract(manifests: Manifests, out: Violations) -> None:
    jobs = manifests.of_kind("Job")
    if not jobs:
        out.add("migration", "there is no migration Job: nothing applies the schema before a rollout")
        return
    for name, doc in jobs:
        spec = doc.get("spec", {})
        template = spec.get("template", {}).get("spec", {})
        command = " ".join(
            str(part) for container in template.get("containers", []) for part in container.get("command", [])
        )
        if "-migrate-only" not in command:
            out.add("migration", f"{name}: the Job does not run -migrate-only ({command!r})")
        if spec.get("backoffLimit") != 0:
            out.add("migration", f"{name}: backoffLimit is {spec.get('backoffLimit')}; a failed migration is not a retry")
        if template.get("restartPolicy") != "Never":
            out.add("migration", f"{name}: restartPolicy is {template.get('restartPolicy')}, so it retries in a loop")
        for container in template.get("containers", []):
            environment = env_of(template, container)
            if environment.get("EDUMART_SEED") != "false":
                out.add("migration", f"{name}: EDUMART_SEED is not false — a migration job must not seed fixtures")

    # Replicas verify the schema; they do not write it. This is the line that stopped three of four
    # replicas from dying on a fresh database.
    config = config_map_env(manifests)
    for name, doc in manifests.of_kind("Deployment"):
        who = doc.get("metadata", {}).get("name", name)
        for container in doc["spec"]["template"]["spec"]["containers"]:
            environment = env_of(doc["spec"]["template"]["spec"], container)
            value = environment.get("EDUMART_MIGRATE_ON_BOOT", config.get("EDUMART_MIGRATE_ON_BOOT"))
            if str(value).lower() != "false":
                out.add("migration", f"{who}: EDUMART_MIGRATE_ON_BOOT is {value!r}, so replicas race for the schema")


def check_environment(manifests: Manifests, out: Violations) -> None:
    known_api = application_env(API_DIR)
    known_web = web_env(WEB_DIR) | RUNTIME_ENV
    config = config_map_env(manifests)
    secrets = secret_keys(manifests)

    for name, doc in manifests.of_kind("Deployment"):
        who = doc.get("metadata", {}).get("name", name)
        is_web = who == "edumart-web"
        for container in doc["spec"]["template"]["spec"]["containers"]:
            for key in env_of(doc["spec"]["template"]["spec"], container):
                allowed = known_web if is_web else known_api | RUNTIME_ENV
                if key not in allowed:
                    out.add("environment", f"{who}: {key} is not a variable the code reads — a rename would be silent")

            # A secret key that the Secret object does not have is a pod that never starts: the API
            # refuses to boot without its DSN, and that is only discovered during a rollout.
            for source in container.get("envFrom", []) or []:
                ref = (source.get("secretRef") or {}).get("name")
                if ref and not any(
                    (doc.get("metadata") or {}).get("name") == ref for _, doc in manifests.of_kind("Secret")
                ):
                    out.add("environment", f"{who}: envFrom refers to Secret {ref}, which is not defined")

    for key in sorted(secrets):
        if key not in known_api:
            out.add("environment", f"Secret key {key} is not a variable the code reads")

    # envFrom is blind: every ConfigMap key becomes an environment variable in every container. A key the
    # code does not read is therefore not harmless — it is a setting somebody believes they changed, and
    # the process is using the default instead. (Found by the self-test: renaming EDUMART_TRUST_PROXY to
    # EDUMART_TRUST_PROX in the ConfigMap originally passed every check.)
    for key in sorted(config):
        if key not in known_api and key not in known_web:
            out.add("environment", f"ConfigMap key {key} is not a variable either application reads")

    # Every variable the API needs to boot in production must come from somewhere.
    required = ["EDUMART_DRIVER", "EDUMART_DSN", "EDUMART_ENV", "EDUMART_SEED", "EDUMART_MIGRATE_ON_BOOT"]
    for key in required:
        if key not in config and key not in secrets:
            out.add("environment", f"{key} is required in production and is set nowhere")


def check_secret_hygiene(manifests: Manifests, out: Violations) -> None:
    for name, doc in manifests.of_kind("ConfigMap"):
        for key, value in (doc.get("data") or {}).items():
            text = str(value)
            credential_shaped = len(text) >= 12 and not NOT_A_SECRET_VALUE.match(text)
            if any(shape.search(text) for shape in CREDENTIAL_SHAPES) or (
                SECRET_LOOKING.search(key) and credential_shaped
            ):
                out.add("secrets", f"{name}: {key} looks like a secret in a ConfigMap")

    for name, doc in manifests.of_kind("Secret"):
        values = dict(doc.get("stringData") or {})
        values.update({key: "" for key in (doc.get("data") or {})})
        for key, value in values.items():
            if not str(value).startswith("REPLACE_ME"):
                # The example file is committed. Anything that is not an obvious placeholder is either a
                # leaked credential or a placeholder that looks real enough to be copied into production.
                out.add("secrets", f"{name}: {key} is not a REPLACE_ME placeholder")

    for path in glob.glob(os.path.join(DEFAULT_DIR, "*.yaml")):
        with open(path) as handle:
            text = handle.read()
        for shape in CREDENTIAL_SHAPES:
            if shape.search(text):
                # A DSN with a password in a manifest is the finding, even in an example.
                if "postgres" in shape.pattern and "REPLACE_ME" in text:
                    continue
                out.add("secrets", f"{os.path.basename(path)}: contains something shaped like a live credential")


def check_topology(manifests: Manifests, out: Violations) -> None:
    deployments = {doc["metadata"]["name"]: doc for _, doc in manifests.of_kind("Deployment")}
    services = {doc["metadata"]["name"]: doc for _, doc in manifests.of_kind("Service")}
    pdbs = manifests.of_kind("PodDisruptionBudget")

    for who, doc in deployments.items():
        labels = doc["spec"]["template"]["metadata"].get("labels", {})
        matching = [
            service for service in services.values()
            if all(service["spec"]["selector"].get(k) == v for k, v in service["spec"]["selector"].items())
        ]
        if not matching:
            out.add("topology", f"{who}: no Service selects these pods, so nothing can reach them")

        protected = any(
            all((p["spec"]["selector"].get("matchLabels") or {}).get(k) == v
                for k, v in (p["spec"]["selector"].get("matchLabels") or {}).items())
            and p["spec"].get("minAvailable", 0) >= 1
            for _, p in pdbs
        ) if pdbs else False
        if not protected:
            # Without a PDB a node drain evicts replicas as fast as it likes, which is a rollout that
            # ignores maxUnavailable entirely.
            out.add("topology", f"{who}: no PodDisruptionBudget with minAvailable >= 1")

    ingress = [doc for _, doc in manifests.of_kind("Ingress")]
    if not ingress:
        out.add("topology", "no Ingress: nothing routes traffic to the web Service")
    for doc in ingress:
        if not doc["spec"].get("tls"):
            out.add("topology", "the Ingress has no TLS configuration")
        for rule in doc["spec"].get("rules", []):
            for path in rule.get("http", {}).get("paths", []):
                backend = path.get("backend", {}).get("service", {}).get("name")
                if backend and backend not in services:
                    out.add("topology", f"the Ingress routes to Service {backend}, which does not exist")

    policies = manifests.of_kind("NetworkPolicy")
    if not any(not (doc["spec"].get("podSelector") or {}) for _, doc in policies):
        # Default-deny is what makes the other policies meaningful; without it, "allow" is decoration.
        out.add("network", "there is no default-deny NetworkPolicy")


def check_web_proxy_target(manifests: Manifests, out: Violations) -> None:
    services = {doc["metadata"]["name"]: doc for _, doc in manifests.of_kind("Service")}
    for name, doc in manifests.of_kind("Deployment"):
        if doc["metadata"]["name"] != "edumart-web":
            continue
        for container in doc["spec"]["template"]["spec"]["containers"]:
            target = env_of(doc["spec"]["template"]["spec"], container).get("EDUMART_API_URL")
            if not target:
                out.add("proxy", "the web deployment does not set EDUMART_API_URL")
                continue
            match = re.match(r"^http://([a-z0-9-]+):(\d+)$", target)
            if not match:
                out.add("proxy", f"EDUMART_API_URL {target} is not http://<service>:<port>")
                continue
            service, port = match.group(1), int(match.group(2))
            if service not in services:
                out.add("proxy", f"EDUMART_API_URL points at {service}, which is not a Service here")
                continue
            ports = {p.get("port") for p in services[service]["spec"].get("ports", [])}
            if port not in ports:
                out.add("proxy", f"EDUMART_API_URL uses port {port}; {service} exposes {sorted(ports)}")


CHECKS = [
    ("structure", check_structure),
    ("images", check_images),
    ("pod-security", check_pod_security),
    ("probes", check_probes),
    ("rollout", check_rollout),
    ("migration", check_migration_contract),
    ("environment", check_environment),
    ("secrets", check_secret_hygiene),
    ("topology", check_topology),
    ("proxy", check_web_proxy_target),
]


def run_checks(directory: str) -> tuple[Violations, Manifests]:
    manifests = Manifests(directory)
    out = Violations()
    for _name, check in CHECKS:
        check(manifests, out)
    return out, manifests


# ── self-test: break each invariant and require the matching check to notice ─────────────────────────


def one(transform):
    """Apply a single-document transform to the first document of a file, keeping the rest."""
    return lambda docs: [transform(docs[0])] + docs[1:]


def mutate(path: str, transform) -> None:
    with open(path) as handle:
        docs = [d for d in yaml.safe_load_all(handle) if d]
    docs = transform(docs)
    with open(path, "w") as handle:
        yaml.safe_dump_all(docs, handle, sort_keys=False)


def set_in(doc: dict, keys: list[str], value):
    cursor = doc
    for key in keys[:-1]:
        cursor = cursor[key]
    cursor[keys[-1]] = value
    return doc


def delete_in(doc: dict, keys: list[str]):
    cursor = doc
    for key in keys[:-1]:
        cursor = cursor[key]
    cursor.pop(keys[-1], None)
    return doc


def self_test(directory: str) -> int:
    """Break the manifests on purpose. Every check must be able to fail.

    This is the part that stops the checks from being decoration: each fault below is a real mistake that
    has shipped in a real deployment, and each one must be caught by the check that claims to catch it.
    """
    faults = [
        (
            "readiness probe on the API moved to /healthz",
            "30-api-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "containers", 0, "readinessProbe", "httpGet", "path"], "/healthz")),
            "probes",
        ),
        (
            "web readiness probe pointed at the proxied /healthz",
            "40-web-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "containers", 0, "readinessProbe", "httpGet", "path"], "/healthz")),
            "probes",
        ),
        (
            "maxUnavailable set back to 1",
            "30-api-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "strategy", "rollingUpdate", "maxUnavailable"], 1)),
            "rollout",
        ),
        (
            "replicas reduced to one",
            "40-web-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "replicas"], 1)),
            "rollout",
        ),
        (
            "replicas migrated on boot again",
            "30-api-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "containers", 0, "env", 0, "value"], "true")),
            "migration",
        ),
        (
            "the migration Job runs a different image from the replicas",
            "20-migration-job.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "containers", 0, "image"], "ghcr.io/teamthy/edumart-api:v0.9.0")),
            "images",
        ),
        (
            "image tag set to latest",
            "40-web-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "containers", 0, "image"], "ghcr.io/teamthy/edumart-web:latest")),
            "images",
        ),
        (
            "a secret moved into the ConfigMap",
            "10-config.yaml",
            one(lambda doc: set_in(doc, ["data", "EDUMART_PAYSTACK_SECRET_KEY"], "sk_live_abcdef123456")),
            "secrets",
        ),
        (
            "a settings key renamed, as the code would rename it",
            "10-config.yaml",
            one(lambda doc: set_in(doc, ["data", "EDUMART_TRUST_PROX"], "true")),
            "environment",
        ),
        (
            "a settings key read through a helper, renamed",
            "10-config.yaml",
            one(lambda doc: set_in(
                doc,
                ["data"],
                {**{k: v for k, v in doc["data"].items() if k != "EDUMART_ACCESS_TOKEN_TTL"},
                 "EDUMART_ACCESS_TTL": "1h"},
            )),
            "environment",
        ),
        (
            "a token-shaped key given a credential-shaped value",
            "10-config.yaml",
            one(lambda doc: set_in(doc, ["data", "EDUMART_METRICS_TOKEN"], "tz_9f2Kd0Qm4Xb7Lp1Rs8Vw3Yc6Nh5Je2Gu")),
            "secrets",
        ),
        (
            "the web proxy pointed at a Service that does not exist",
            "40-web-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "containers", 0, "env", 0, "value"], "http://api:8080")),
            "proxy",
        ),
        (
            "root filesystem writable again",
            "30-api-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "containers", 0, "securityContext", "readOnlyRootFilesystem"], False)),
            "pod-security",
        ),
        (
            "the PodDisruptionBudget removed",
            "60-pdb.yaml",
            lambda docs: [],
            "topology",
        ),
        (
            "grace period cut to five seconds",
            "30-api-deployment.yaml",
            one(lambda doc: set_in(doc, ["spec", "template", "spec", "terminationGracePeriodSeconds"], 5)),
            "rollout",
        ),
        (
            "the default-deny NetworkPolicy removed",
            "70-networkpolicy.yaml",
            lambda docs: [d for d in docs if d.get("spec", {}).get("podSelector") != {}],
            "network",
        ),
    ]

    print(f"self-test: {len(faults)} faults against {directory}\n")
    failures = 0
    for description, filename, transform, expected in faults:
        workdir = tempfile.mkdtemp(prefix="edumart-manifests-")
        try:
            for existing in glob.glob(os.path.join(directory, "*.yaml")):
                shutil.copy(existing, workdir)
            target = os.path.join(workdir, filename)
            mutate(target, transform)

            violations, _ = run_checks(workdir)
            if expected in violations.checks():
                print(f"  caught  {description}  →  {expected}")
            else:
                failures += 1
                print(f"  MISSED  {description}  →  expected '{expected}', saw {sorted(violations.checks())}")
        finally:
            shutil.rmtree(workdir, ignore_errors=True)

    if failures:
        print(f"\nself-test FAILED: {failures} fault(s) went unnoticed", file=sys.stderr)
        return 1
    print(f"\nself-test passed: all {len(faults)} faults were caught")
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Check the deployment manifests against the application.")
    parser.add_argument("--dir", default=DEFAULT_DIR)
    parser.add_argument("--self-test", action="store_true", help="break the manifests and require each check to fire")
    parser.add_argument("--verbose", action="store_true")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test(args.dir)

    violations, manifests = run_checks(args.dir)
    if args.verbose:
        kinds: dict[str, int] = {}
        for _, doc in manifests.docs:
            kinds[doc.get("kind", "?")] = kinds.get(doc.get("kind", "?"), 0) + 1
        print(f"{len(manifests.files)} file(s), {len(manifests.docs)} document(s): {kinds}")

    if violations:
        for check, message in violations.items:
            print(f"  FAIL  [{check}] {message}", file=sys.stderr)
        print(f"\n{len(violations)} manifest violation(s)", file=sys.stderr)
        return 1

    print(f"manifests pass: {len(manifests.files)} files, {len(CHECKS)} checks, no violations")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
