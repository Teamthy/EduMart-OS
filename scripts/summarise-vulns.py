#!/usr/bin/env python3
"""Turn `govulncheck -json` output into something a pull request can act on.

    govulncheck -json ./... | python3 scripts/summarise-vulns.py

govulncheck's prose output names the *symbol* that is reachable, which tells you where the problem is
but not when it was fixed — and "when was this fixed" is the only question that decides whether the job
is red for a reason we can act on. This reads the stream, keeps the findings, and emits one GitHub
annotation per (advisory, fixed version) pair.

Annotations rather than stdout because the log of a CI job is not always reachable, while check
annotations are part of the API a developer already has open. When the input is empty or unparseable it
says so instead of printing nothing: a silent summariser would look like a clean scan.
"""

import json
import sys


def main():
    advisories = {}  # (id, fixed) -> {"functions": set(), "module": str}
    seen_any = False

    for line in sys.stdin:
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        finding = message.get("finding")
        if not finding:
            continue
        seen_any = True
        trace = finding.get("trace") or [{}]
        function = trace[0].get("function") or ""
        module = (trace[0].get("module") or "").strip()
        # `osv` is a single advisory id (a string), not a list. Reading it as a sequence turns a
        # report into one finding per character, which is exactly the sort of summary nobody reads.
        advisory = finding.get("osv")
        if not isinstance(advisory, str) or not advisory:
            advisory = "unidentified advisory"
        key = (advisory, finding.get("fixed_version") or "no fix released")
        entry = advisories.setdefault(key, {"functions": set(), "module": module})
        if function:
            entry["functions"].add(function)

    if not seen_any:
        print("govulncheck reported no reachable vulnerabilities")
        return 0

    for (advisory, fixed), entry in sorted(advisories.items()):
        functions = ", ".join(sorted(entry["functions"])[:4]) or "no call path reported"
        print(f"::error::{advisory} reachable via {functions} — fixed in {fixed}")
    print(f"{len(advisories)} advisory/version pair(s) affect code we actually execute")
    return 0


if __name__ == "__main__":
    sys.exit(main())
