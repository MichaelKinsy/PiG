#!/usr/bin/env python3
"""Turn a spec whose parent entries lack `properties` into one close.py accepts, when every child is already ported.

Usage: python3 test/parity/interface-closure/parent_spec.py <spec.json> [--write]
close.py closes a parent only together with every `::property:` child. When the children were closed earlier, an entry for the
parent alone fails. For each entry with a single parent id that has `::property:` children, this fills `properties` from the
children's current ported rows (a child already in `properties` is kept) (targets, evidence, production, rationale), so the children are re-written unchanged.
It refuses a child that is not ported: close that first.
"""
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
spec_path = Path(sys.argv[1])
spec = json.loads(spec_path.read_text())
rows = {r["id"]: r for r in json.loads((root / "test/parity/interfaces/mapping-v1.1.0.json").read_text())["mappings"]}
changed = 0
for entry in spec:
    if "struct" in entry or len(entry["ids"]) != 1 or "::" in entry["ids"][0]:
        continue
    parent = entry["ids"][0]
    children = sorted(i for i in rows if i.startswith(parent + "::property:") and i.count("::") == 1)
    if not children:
        continue
    props = entry.get("properties", {})
    for child in children:
        if child.split("::property:")[1] in props:
            continue
        row = rows[child]
        if row["disposition"] != "ported":
            sys.exit(f"{child} is {row['disposition']}; close it first")
        props[child.split("::property:")[1]] = {
            "targets": row["pigTargets"], "evidence": row["evidence"], "production": row["production"], "rationale": row["rationale"],
        }
        if row.get("asyncContract") and "async" not in entry:
            entry["async"] = row["asyncContract"]
    entry["properties"] = props
    changed += 1
print(f"filled properties for {changed} parent entries")
if "--write" in sys.argv:
    spec_path.write_text(json.dumps(spec, indent=1))
