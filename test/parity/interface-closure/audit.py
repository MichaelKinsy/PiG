#!/usr/bin/env python3
"""List closed rows whose `call:` production reference is not reachable from the PiG binary.

Usage: go run ./test/parity/cmd/prodreach -out /tmp/reach.json -tags pig_experimental ./cmd/pig ./cmd/pig-experimental
       python3 test/parity/interface-closure/audit.py /tmp/reach.json [--fix]
A production reference `call:<file>#<Func>` claims a PiG call site, so <Func> must be reachable from a PiG main function
(rapid type analysis) and must not be a parity-harness probe (`parity_harness.go`). A row that fails is not production-reachable by that reference. Without --fix the script only
reports; with --fix it rewrites every row whose references are all unreachable and whose targets are exported Go symbols to
`public-api:` references with the production layer `public-api-tested`, and reports the rest.
"""
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
reach = set(json.loads(Path(sys.argv[1]).read_text()))
fix = "--fix" in sys.argv
path = root / "test/parity/interfaces/mapping-v1.1.0.json"
ledger = json.loads(path.read_text())
bad = []
for row in ledger["mappings"]:
    if row["disposition"] != "ported":
        continue
    # A parity-harness probe runs only under PIG_PARITY_HARNESS=1, so it is test scaffolding, not a production caller.
    calls = [p for p in row["production"] if p.startswith("call:") and not p.split("#")[0].endswith("parity_harness.go")]
    if not calls and not any(p.startswith("call:") for p in row["production"]):
        continue
    if calls and any(p[5:] in reach for p in calls):
        continue
    bad.append(row)
print(f"{len(bad)} closed rows whose call references are all unreachable from the PiG binary")
changed = 0
for row in bad:
    print("  ", row["id"], row["production"][0])
    if fix:
        targets = [t for t in row["pigTargets"] if t.split("#")[1][:1].isupper() and "/internal/" not in "/" + t.split("#")[0]]
        if targets and all(not t.startswith(("internal/",)) for t in targets):
            row["production"] = ["public-api:" + t for t in targets[:1]]
            row["layers"]["production"] = "public-api-tested"
            changed += 1
if fix:
    path.write_text(json.dumps(ledger, indent=2, ensure_ascii=False) + "\n")
    print(f"rewrote {changed} rows as public-api-tested")
