#!/usr/bin/env python3
"""Fail when the OpenAPI contract and the Go router disagree.

    python3 scripts/check-contract.py

A contract nobody validates rots within a sprint: a route gets renamed, the spec keeps advertising
the old path, and both clients build against a lie. This reads `packages/contracts/openapi.yaml`
and `apps/api/internal/api/router.go` and compares path+method pairs in both directions.

Deliberately a text check, not a full OpenAPI validator: it needs no network and no extra
dependency, which means CI can run it on every push.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = ROOT / "packages" / "contracts" / "openapi.yaml"
ROUTER = ROOT / "apps" / "api" / "internal" / "api" / "router.go"

METHODS = {"get", "post", "put", "patch", "delete"}


def spec_routes(text):
    """Collect (method, path) from the `paths:` block using indentation."""
    routes = set()
    in_paths = False
    current = None
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        if re.match(r"^paths:\s*$", line):
            in_paths = True
            continue
        if in_paths and re.match(r"^[A-Za-z]", line):
            break  # next top-level key ends the block
        if not in_paths:
            continue
        path_match = re.match(r"^  (/\S*):\s*$", line)
        if path_match:
            current = path_match.group(1)
            continue
        method_match = re.match(r"^    ([a-z]+):\s*$", line)
        if method_match and current and method_match.group(1) in METHODS:
            routes.add((method_match.group(1).upper(), normalise(current)))
    return routes


def router_routes(text):
    """Collect (method, path) from chi registrations."""
    routes = set()
    # v1.Get("/me", …), g.Post("/practice-sessions", …), r.Get("/healthz", …)
    for match in re.finditer(r'\b(?:v1|g|r)\.(Get|Post|Put|Patch|Delete)\("([^"]+)"', text):
        method, path = match.group(1).upper(), match.group(2)
        if not path.startswith("/v1") and path not in ("/healthz", "/readyz"):
            path = "/v1" + path
        routes.add((method, normalise(path)))
    return routes


def normalise(path):
    """`{id}` and `:id` are the same parameter to a client."""
    path = re.sub(r"\{[^}]+\}", "{param}", path)
    path = re.sub(r":[A-Za-z][A-Za-z0-9_]*", "{param}", path)
    return path.rstrip("/") or "/"


def main():
    spec = spec_routes(SPEC.read_text())
    router = router_routes(ROUTER.read_text())

    undocumented = sorted(router - spec)
    phantom = sorted(spec - router)

    for method, path in undocumented:
        print(f"undocumented route : {method:6} {path}")
    for method, path in phantom:
        print(f"contract only      : {method:6} {path}  (no handler)")

    if undocumented or phantom:
        print(
            f"\n{len(undocumented)} undocumented, {len(phantom)} phantom route(s). "
            "Update packages/contracts/openapi.yaml or the router so they agree.",
            file=sys.stderr,
        )
        return 1

    print(f"contract matches the router: {len(router)} documented route(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
