#!/usr/bin/env python3
"""Return closed rows to the state they had in a git revision, for IDs that start with a prefix and contain a substring.

Usage: python3 test/parity/interface-closure/revert.py <git rev> <id prefix> <substring>
Remove the same entries from their spec so close.py does not close them again.
"""
import json
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(root / "automation/pin"))
import pinlib  # noqa: E402
V = pinlib.effective()[0].version
rev, prefix, needle = sys.argv[1:4]
path = root / f"test/parity/interfaces/mapping-v{V}.json"
old = {x["id"]: x for x in json.loads(subprocess.run(["git", "show", f"{rev}:test/parity/interfaces/mapping-v{V}.json"], cwd=root, capture_output=True, text=True, check=True).stdout)["mappings"]}
ledger = json.loads(path.read_text())
n = 0
for row in ledger["mappings"]:
    rid = row["id"]
    if rid.startswith(prefix) and needle in rid and row["disposition"] == "ported":
        row.clear()
        row.update(old[rid])
        n += 1
path.write_text(json.dumps(ledger, indent=2, ensure_ascii=False) + "\n")
print(f"reverted {n} rows")
