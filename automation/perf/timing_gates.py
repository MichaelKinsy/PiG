#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Run every timing-gate test (automation/perf/timing-gates.json) as a blocking gate, one package at a time.

Pull-request CI sets PIG_PERF_TIMING_REPORT_ONLY=1 so a timing gate warns instead of failing (internal/testenv.TimingGate). This is the release perf run for quiet pinned hardware: the variable is removed, so a missed bar fails, and a gate that did not run or skipped fails too.
"""
import json
import os
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
GOOS = {"darwin": "darwin", "linux": "linux", "win32": "windows"}.get(sys.platform, sys.platform)


def main():
    env = {k: v for k, v in os.environ.items() if k != "PIG_PERF_TIMING_REPORT_ONLY"}
    # PIG_SERIAL_TIMING enables the ratio gates that skip beside parallel tests; SESSIONSCALE_GATE the 300 MB session-scale gates.
    env.update(PIG_SERIAL_TIMING="1", SESSIONSCALE_GATE="1")
    failed = False
    for entry in json.loads((ROOT / "automation/perf/timing-gates.json").read_text()):
        if entry.get("goos", GOOS) != GOOS:
            continue
        names = entry["tests"]
        pattern = "^(" + "|".join(re.escape(name) for name in names) + ")$"
        command = ["go", "test", "-json", "-count=1", "-p", "1", "-parallel", "1"]
        if entry.get("tags"):
            command += ["-tags", entry["tags"]]
        command += [entry["package"], "-run", pattern]
        result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, env=env)
        passed = set()
        for line in result.stdout.splitlines():
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                print(line)
                continue
            if event.get("Output"):
                print(event["Output"], end="")
            if event.get("Action") == "pass" and event.get("Test") in names:
                passed.add(event["Test"])
        missing = sorted(set(names) - passed)
        if result.returncode or missing:
            print(f"timing gates failed, skipped or did not run: {entry['package']}: {missing}", file=sys.stderr)
            failed = True
    if failed:
        return 1
    print("timing gates: every listed gate ran and met its bar")
    return 0


if __name__ == "__main__":
    sys.exit(main())
