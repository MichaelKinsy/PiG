#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Decide whether the CI shards wait for the bookkeeping gates.

Every job that spends real minutes starts only when the Bookkeeping job's `shards-blocked` output is `false`. The output is `false` when
`make -k ci-bookkeeping` passed, and when its only failed gate is one of TOLERATED: that gate still fails the Bookkeeping job and the
CI result (the final job requires Bookkeeping to succeed), but it does not stop the shards from running, so one open gate cannot hide the
results of everything behind it. Any other failed gate, a failure the log names no gate for, or a missing outcome blocks the shards.

Usage: bookkeeping_shards.py --outcome success|failure --log LOG
Writes `blocked=true|false` to $GITHUB_OUTPUT and the reason to stdout.
"""
from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import autofix  # noqa: E402  (failed_gates reads the same make error lines)

# Gates whose failure alone does not stop the shards. Owner decision 2026-11-08: known-gaps-drift stays red until the pending upstream test
# files are ported, and the shards must still run meanwhile. Every other bookkeeping gate keeps blocking.
TOLERATED = ("known-gaps-drift",)


def decide(outcome: str, log: str) -> tuple[bool, str]:
    """Returns (blocked, reason) for the gates step's outcome and its log."""
    if outcome == "success":
        return False, "the bookkeeping gates passed"
    if outcome != "failure":
        return True, f"the bookkeeping gates have no verdict (outcome {outcome!r})"
    failed = autofix.failed_gates(log)
    if not failed:
        return True, "the bookkeeping gates failed and the log names no failed gate"
    blocking = [gate for gate in failed if gate not in TOLERATED]
    if blocking:
        return True, "failed gates that block the shards: " + ", ".join(blocking)
    return False, "only " + ", ".join(failed) + " failed; the shards run and the Bookkeeping job stays red"


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--outcome", required=True)
    parser.add_argument("--log", required=True)
    args = parser.parse_args(argv)
    log = Path(args.log).read_text(encoding="utf-8", errors="replace") if Path(args.log).exists() else ""
    blocked, reason = decide(args.outcome, log)
    print(f"bookkeeping shards: blocked={str(blocked).lower()}: {reason}")
    target = os.environ.get("GITHUB_OUTPUT")
    if target:
        with open(target, "a", encoding="utf-8") as stream:
            stream.write(f"blocked={str(blocked).lower()}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
