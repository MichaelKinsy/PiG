#!/usr/bin/env python3
"""Run every Go test named as evidence in the given specs, grouped by package directory, and fail if any fails or none runs.

Usage: python3 test/parity/interface-closure/runevidence.py spec.json...
"""
import collections
import json
import subprocess
import sys

tests = collections.defaultdict(set)
for path in sys.argv[1:]:
    for entry in json.load(open(path)):
        for group in [entry] + list(entry.get("properties", {}).values()):
            for ev in group.get("evidence", []):
                kind, ref = ev.split(":", 1)
                file, name = ref.split("#")
                if kind == "test" and file.endswith(".go"):
                    tests[file.rsplit("/", 1)[0]].add(name)
bad = 0
for pkg, names in sorted(tests.items()):
    run = "^(" + "|".join(sorted(names)) + ")$"
    r = subprocess.run(["go", "test", "./" + pkg, "-run", run, "-count=1"], capture_output=True, text=True)
    print(pkg, len(names), "ok" if r.returncode == 0 else "FAIL")
    if r.returncode:
        print(r.stdout[-1500:], r.stderr[-500:])
        bad = 1
sys.exit(bad)
