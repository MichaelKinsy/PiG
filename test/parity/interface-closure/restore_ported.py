#!/usr/bin/env python3
"""Re-promote rows a reviewed earlier ledger closed, when the current upstream shape is identical.

Usage: python3 test/parity/interface-closure/restore_ported.py <restore-spec.json> [--version 1.1.0]

An upstream version leap resets every `ported` row to `pending` (interfaceinventory carriedDispositions), because a
shape hash does not see behaviour. A restore spec records the review that makes a re-promotion safe: which
owning upstream files were diffed between the two versions and what changed in them. The script copies each row
that was `ported` in spec.from with an identical shape hash, appends spec.note to its rationale (spec.noteWhenChanged
for IDs whose id contains one of spec.changedModules), and adds spec.extraEvidence to the rows whose ID starts with
a listed prefix. It is re-runnable. `make interface-mapping-quality` validates every reference afterwards.
"""
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
args = sys.argv[1:]
version = "1.1.0"
if "--version" in args:
    i = args.index("--version")
    version = args[i + 1]
    del args[i : i + 2]
if len(args) != 1:
    sys.exit(__doc__)
spec = json.loads(Path(args[0]).read_text())
base = root / "test/parity/interfaces"
old = {x["id"]: x for x in json.loads((base / f"mapping-v{spec['from']}.json").read_text())["mappings"]}
path = base / f"mapping-v{version}.json"
ledger = json.loads(path.read_text())
done = same = 0
for row in ledger["mappings"]:
    prior = old.get(row["id"])
    if not prior or prior["disposition"] != "ported" or prior["upstreamShapeHash"] != row["upstreamShapeHash"]:
        continue
    changed = any(m in row["id"] for m in spec["changedModules"])
    note = spec["noteWhenChanged"] if changed else spec["note"]
    new = dict(prior)
    new["rationale"] = ((prior.get("rationale", "") + " " + note)).strip()
    for prefix in spec["extraEvidence"]:
        if row["id"].startswith(prefix):
            new["evidence"] = prior["evidence"] + spec["extraEvidence"][prefix]
    if row == new:
        same += 1
        continue
    row.clear()
    row.update(new)
    done += 1
path.write_text(json.dumps(ledger, indent=2, ensure_ascii=False) + "\n")
print(f"restored {done} rows, {same} already restored")
