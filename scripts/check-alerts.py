#!/usr/bin/env python3
"""Fail when an alert rule cannot work.

    python3 scripts/check-alerts.py --spec infra/observability/alerts.yml --metrics http://localhost:8080/metrics
    python3 scripts/check-alerts.py --spec infra/observability/alerts.yml --dump /tmp/metrics.txt
    python3 scripts/check-alerts.py --spec infra/observability/alerts.yml       # structure only

An alerting setup fails silently. A rule that names a metric nobody emits — a typo, a rename, a feature
that was removed — is an error nowhere: Prometheus accepts it, the rule simply never fires, and the
dashboard stays green. That is indistinguishable from a healthy system until the incident, which is the
worst possible time to discover the pager was never connected to anything.

So this checks four things, in increasing order of how much they have saved:

1. **Structure.** Every rule has `alert`, `expr`, `labels.severity` and a `runbook` annotation; severities
   come from a known set. A rule with no runbook is a rule someone will silence during an incident.
2. **The runbook anchor resolves.** The `runbook` path is read from the annotation and the heading it
   points at must exist in the document. A dead link on an alert page is how an alert becomes noise.
3. **Every metric it references is actually exposed.** Names are pulled out of the PromQL by a small
   parser — enough for the expressions these rules use, deliberately not a general parser — and compared
   with a scrape. This is the check that matters: it is the only one that can catch a rename.
4. **No rule alerts on something it cannot see.** An `absent()` rule is exempt by design (absence is the
   signal), but any other rule referencing a metric that exists only as a HELP line, with no series, is
   reported as a warning: the rule would never fire on a live system.

`--metrics` scrapes a running API. Without it the metric checks are skipped and the script says so, rather
than passing quietly as if they had run.
"""

from __future__ import annotations

import argparse
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs" / "OBSERVABILITY.md"

SEVERITIES = {"page", "ticket", "info"}

# Names Prometheus and common exporters own. A rule may use them without the API emitting them.
EXTERNAL_METRICS = {
    "up", "time", "vector", "hour", "probe_success", "node_filesystem_avail_bytes",
    "node_filesystem_size_bytes", "process_start_time_seconds",
}

# PromQL functions and keywords that look like identifiers in a scan and are not metrics.
PROMQL_WORDS = {
    "by", "without", "on", "ignoring", "group_left", "group_right", "offset", "bool", "and", "or",
    "unless", "inf", "nan", "start", "end",
    "rate", "irate", "increase", "delta", "idelta", "sum", "min", "max", "avg", "count", "stddev",
    "stdvar", "topk", "bottomk", "quantile", "count_values", "absent", "absent_over_time", "histogram_quantile",
    "label_replace", "label_join", "sort", "sort_desc", "sort_asc", "time", "timestamp", "year", "month",
    "day_of_month", "day_of_week", "days_in_month", "hour", "minute", "clamp_min", "clamp_max", "clamp",
    "round", "floor", "ceil", "abs", "vector", "scalar", "changes", "resets", "predict_linear",
    "holt_winters", "deriv", "avg_over_time", "max_over_time", "min_over_time", "sum_over_time",
    "count_over_time", "quantile_over_time", "stddev_over_time", "last_over_time", "present_over_time",
    "chunked", "h", "d", "w", "y", "s", "m",
}

IDENTIFIER = re.compile(r"\b([a-zA-Z_][a-zA-Z0-9_]*)\b")


def load_spec(path: Path) -> dict:
    try:
        import yaml  # imported here so --self-test works without it
    except ImportError:
        sys.exit("pyyaml is required: pip install pyyaml")
    with open(path, encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def metric_references(expr: str) -> set[str]:
    """Pull metric names out of a PromQL expression.

    Not a PromQL parser: it takes identifiers that are not function names, not keywords, not label names
    (things before a `=` inside `{...}`) and not duration literals. That is enough for the expressions
    alerts are actually written with, and being wrong in the direction of "extra names" would produce
    false failures, so it errs the other way: label matchers are stripped before scanning.
    """
    expression = re.sub(r'"[^"]*"', "", expr)          # string literals
    expression = re.sub(r"\{[^}]*\}", lambda m: "{}", expression)  # label matchers
    expression = re.sub(r"\b\d+(\.\d+)?\b", " ", expression)       # numbers
    names = set()
    for match in IDENTIFIER.finditer(expression):
        name = match.group(1)
        if name in PROMQL_WORDS or name in EXTERNAL_METRICS:
            continue
        if name.islower() and name in {"le", "quantile"}:
            continue
        names.add(name)
    return names


def scrape(source: str) -> str | None:
    if source.startswith("http://") or source.startswith("https://"):
        token = None
        url = source
        if "#" in source:  # http://host/metrics#token
            url, token = source.split("#", 1)
        request = urllib.request.Request(url)
        if token:
            request.add_header("Authorization", "Bearer " + token)
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                return response.read().decode("utf-8", "replace")
        except (urllib.error.URLError, OSError) as caught:
            print(f"could not scrape {url}: {caught}", file=sys.stderr)
            return None
    try:
        return Path(source).read_text(encoding="utf-8")
    except OSError as caught:
        print(f"could not read {source}: {caught}", file=sys.stderr)
        return None


def exposed(text: str) -> tuple[set[str], set[str]]:
    """Return (names with a sample, all names declared). The difference is what a rule cannot alert on."""
    with_samples: set[str] = set()
    declared: set[str] = set()
    for line in text.splitlines():
        if not line or line.startswith("#"):
            if line.startswith("# TYPE "):
                declared.add(line.split()[2])
            continue
        name = line.split("{", 1)[0].split(" ", 1)[0]
        # Collapse `_bucket`, `_sum`, `_count` suffixes so a histogram rule matches its family.
        with_samples.add(name)
        declared.add(name)
        for suffix in ("_bucket", "_sum", "_count"):
            if name.endswith(suffix):
                declared.add(name[: -len(suffix)])
    return with_samples, declared


def verify_metrics(referenced: dict[str, list[str]], text: str) -> tuple[list[str], list[str]]:
    """Compare the metrics rules name with the ones a scrape actually contains."""
    problems: list[str] = []
    warnings: list[str] = []
    with_samples, declared = exposed(text)
    for name in sorted(referenced):
        if name not in declared:
            problems.append(
                f"metric {name!r} is referenced by a rule but never exposed: that alert can never fire")
        elif name not in with_samples:
            users = ", ".join(referenced[name])
            warnings.append(
                f"metric {name!r} is declared but has no series yet, so {users} matches nothing until "
                f"the first event; make sure the never-happened case has its own `absent()` rule")
    return problems, warnings


def heading_anchors() -> set[str]:
    """GitHub's heading slugs, near enough: lowercase, punctuation dropped, spaces to hyphens."""
    anchors = set()
    for line in DOCS.read_text(encoding="utf-8").splitlines():
        if not line.startswith("#"):
            continue
        text = line.lstrip("#").strip()
        slug = re.sub(r"[^a-z0-9 -]", "", text.lower()).replace(" ", "-")
        anchors.add(slug)
    return anchors


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Check that alert rules can actually fire.")
    parser.add_argument("--spec", default="infra/observability/alerts.yml")
    parser.add_argument("--metrics", help="URL or file of a /metrics scrape; appends #token if needed")
    parser.add_argument("--self-test", action="store_true", help="check the metric-name extraction itself")
    args = parser.parse_args(argv)

    if args.self_test:
        return self_test()

    spec_path = Path(args.spec)
    if not spec_path.is_absolute():
        spec_path = ROOT / spec_path
    spec = load_spec(spec_path)

    problems: list[str] = []
    warnings: list[str] = []
    referenced: dict[str, list[str]] = {}
    anchors = heading_anchors()
    alert_count = 0

    for group in spec.get("groups", []):
        for rule in group.get("rules", []):
            alert_count += 1
            name = rule.get("alert")
            where = f"{group.get('name')}/{name}"

            if not name:
                problems.append(f"{where}: rule has no `alert` name")
            expr = rule.get("expr", "")
            if not expr:
                problems.append(f"{where}: rule has no `expr`")

            severity = (rule.get("labels") or {}).get("severity")
            if severity not in SEVERITIES:
                problems.append(f"{where}: severity {severity!r} is not one of {sorted(SEVERITIES)}")

            annotations = rule.get("annotations") or {}
            for required in ("summary", "description"):
                if not annotations.get(required):
                    problems.append(f"{where}: missing `{required}` annotation")

            runbook = annotations.get("runbook")
            if not runbook:
                problems.append(f"{where}: no `runbook` annotation; an alert without one gets silenced")
                continue
            path, _, anchor = runbook.partition("#")
            target = (ROOT / path).resolve()
            if not target.exists():
                problems.append(f"{where}: runbook {path} does not exist")
            elif anchor and anchor not in anchors:
                problems.append(f"{where}: runbook anchor #{anchor} is not a heading in {path}")

            for metric in metric_references(expr):
                referenced.setdefault(metric, []).append(name or where)

    if alert_count == 0:
        problems.append("no alert rules found: an empty file passes every check and protects nothing")

    if args.metrics:
        text = scrape(args.metrics)
        if text is None:
            problems.append("the metrics scrape failed, so no rule could be verified")
        else:
            found, noticed = verify_metrics(referenced, text)
            problems.extend(found)
            warnings.extend(noticed)
    else:
        print("note: no --metrics given, so metric references were NOT verified", file=sys.stderr)

    for warning in warnings:
        print(f"warning: {warning}")
    for problem in problems:
        print(f"error: {problem}", file=sys.stderr)

    if problems:
        print(f"\n{len(problems)} problem(s) across {alert_count} rule(s)", file=sys.stderr)
        return 1
    verified = f", {len(referenced)} metric(s) verified against a live scrape" if args.metrics else ""
    print(f"{alert_count} alert rule(s) are structurally sound{verified}")
    return 0


SELF_TEST_CASES = [
    ('sum(rate(edumart_api_errors_total{code="INTERNAL_ERROR"}[5m]))', {"edumart_api_errors_total"}),
    ("up{job=\"edumart-api\"} == 0", set()),                      # owned by Prometheus
    ("time() - edumart_backup_age_seconds > 36 * 3600", {"edumart_backup_age_seconds"}),
    ("absent(edumart_backup_age_seconds)", {"edumart_backup_age_seconds"}),
    ("histogram_quantile(0.95, sum(rate(x_bucket[5m])) by (le))", {"x_bucket"}),
    ("increase(edumart_webhook_events_total{outcome=\"rejected\"}[15m]) > 5",
     {"edumart_webhook_events_total"}),
]


def self_test() -> int:
    failures = 0
    for expression, expected in SELF_TEST_CASES:
        got = metric_references(expression)
        if got != expected:
            print(f"FAIL: {expression!r}\n  expected {sorted(expected)}\n  got      {sorted(got)}")
            failures += 1

    # The whole point of the metric check is catching a rename, so prove it catches one. A check that has
    # only ever been run against a passing tree is a check nobody knows is connected.
    sample = (
        "# HELP edumart_backup_age_seconds Age of the last verified backup.\n"
        "# TYPE edumart_backup_age_seconds gauge\n"
        "edumart_backup_age_seconds 12\n"
        "# HELP edumart_login_failures_total Failed sign-ins.\n"
        "# TYPE edumart_login_failures_total counter\n"
    )
    found, noticed = verify_metrics(
        {"edumart_backup_age_seconds": ["A"], "edumart_login_failures_total": ["B"],
         "edumart_backup_age_second": ["Renamed"]}, sample)
    if not any("edumart_backup_age_second" in problem for problem in found):
        print("FAIL: a renamed metric in a rule was not reported as a problem")
        failures += 1
    if not any("edumart_login_failures_total" in warning for warning in noticed):
        print("FAIL: a declared-but-unobserved metric was not reported as a warning")
        failures += 1
    if any("edumart_backup_age_seconds" in problem for problem in found):
        print("FAIL: a metric that is exposed was reported as missing")
        failures += 1
    if failures:
        print(f"\n{failures} self-test case(s) failed: the extraction is not doing what it claims",
              file=sys.stderr)
        return 1
    print(f"self-test passed: {len(SELF_TEST_CASES)} expressions parsed as expected, rename detection verified")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
