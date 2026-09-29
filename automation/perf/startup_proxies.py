#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Run named deterministic startup guards; missing or skipped guards fail."""
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]


def main():
    package = "./coding/extension/host/subprocess"
    inventory = json.loads(subprocess.check_output(["go", "list", "-json", package], cwd=ROOT))
    embedded = inventory.get("EmbedFiles", [])
    if "runtime-node.zip" not in embedded or any(name.startswith("runtime-node/") for name in embedded):
        raise SystemExit("production must embed only the compressed Node runtime, not its source tree")
    manifest = json.loads((ROOT / "automation/perf/startup-proxies.json").read_text())
    for package, names in manifest.items():
        if not names or len(names) != len(set(names)):
            raise SystemExit(f"empty or duplicate startup guard list: {package}")
        pattern = "^(" + "|".join(re.escape(name) for name in names) + ")$"
        command = ["go", "test", "-json", "-count=1", package, "-run", pattern]
        result = subprocess.run(command, cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
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
        missing = set(names) - passed
        if result.returncode or missing:
            print(f"startup guards failed or did not run: {package}: {sorted(missing)}", file=sys.stderr)
            return 1
    print("startup proxies: all named guards executed and passed; no wall-clock thresholds")
    return 0


if __name__ == "__main__":
    sys.exit(main())
