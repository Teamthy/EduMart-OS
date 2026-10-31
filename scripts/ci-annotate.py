#!/usr/bin/env python3
"""Re-emit failing test output as CI annotations.

A failing test is only useful if whoever opens the pull request can read it. Workflow logs can be large,
slow, or (as happened while this was written) unobtainable through the API — while check-run annotations are
always attached to the commit. This reads a captured test log and prints one `::error::` per failure block.

Usage:
    go test ./... -race 2>&1 | tee /tmp/test.log; python3 scripts/ci-annotate.py /tmp/test.log

Exits 0 always: it is a reporting step, and a reporting step that fails hides the failure it was reporting.
"""

from __future__ import annotations

import sys

# Lines that begin a failure block worth quoting. A Go test failure reads `--- FAIL: TestName (0.01s)` and is
# followed by the offending assertions; a panic reads `panic: ...` and is followed by a stack.
FAILURE_MARKERS = ("--- FAIL", "FAIL\t", "panic:", "DATA RACE")
CONTEXT_LINES = 25


def blocks(lines: list[str]) -> list[list[str]]:
    found: list[list[str]] = []
    for index, line in enumerate(lines):
        if any(line.startswith(marker) or marker in line for marker in FAILURE_MARKERS):
            found.append(lines[index:index + CONTEXT_LINES])
    return found


def escape(text: str) -> str:
    """GitHub's workflow commands are one line each, with percent-encoding for newlines."""
    return text.replace("%", "%25").replace("\r", "").replace("\n", "%0A")


def main(argv: list[str]) -> int:
    if not argv:
        print("usage: ci-annotate.py <log file>", file=sys.stderr)
        return 0
    try:
        with open(argv[0], errors="replace") as handle:
            lines = handle.read().splitlines()
    except OSError as failure:
        print(f"::error::could not read {argv[0]}: {failure}")
        return 0

    found = blocks(lines)
    if not found:
        # Silence here would look like "no annotation because nothing failed". Say what actually happened.
        print(f"::error::the step failed but no test failure was found in {argv[0]}; last lines follow")
        print("::error::" + escape("\n".join(lines[-CONTEXT_LINES:])))
        return 0

    for block in found:
        print("::error::" + escape("\n".join(block)))
    print(f"annotated {len(found)} failure block(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
