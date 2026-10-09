#!/usr/bin/env python3
"""Group unclosed interface IDs by the upstream source file that declares them, with that file's PORT_MAP status.

Usage: python3 test/parity/interface-closure/sources.py pkg:coding-agent [--ids <module substring>]
The inventory's `source.path` names the published .d.ts (dist/core/x.d.ts, or node_modules/@earendil-works/pi-tui/dist/y.d.ts
for a re-export); the declaring file is packages/<pkg>/src/<path>.ts.
"""
import collections
import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
prefix = sys.argv[1]
ids_filter = sys.argv[sys.argv.index("--ids") + 1] if "--ids" in sys.argv else None
base = root / "test/parity/interfaces"
upstream = json.loads((base / "upstream-v1.1.0.json").read_text())["interfaces"]
mapping = {x["id"]: x["disposition"] for x in json.loads((base / "mapping-v1.1.0.json").read_text())["mappings"]}
status = {}
for line in (root / "docs/parity/PORT_MAP.md").read_text().splitlines():
    m = re.match(r"\| `(packages/[^`]+)` \|.*\| (✅|🟡|⬜|n/a)[^|]*\|\s*$", line)
    if m:
        status[m.group(1)] = m.group(2)
pkg_of = {"@earendil-works/pi-tui": "tui", "@earendil-works/pi-ai": "ai", "@earendil-works/pi-agent-core": "agent"}


def declaring_file(path):
    m = re.match(r"node_modules/@earendil-works/pi-([\w-]+)/dist/(.*)\.d\.ts$", path)
    if m:
        pkg = {"tui": "tui", "ai": "ai", "agent-core": "agent"}.get(m.group(1), m.group(1))
        return f"packages/{pkg}/src/{m.group(2)}.ts"
    m = re.match(r"dist/(.*)\.d\.ts$", path)
    return f"packages/{prefix.split(':')[1]}/src/{m.group(1)}.ts" if m else path


rows = collections.defaultdict(collections.Counter)
for x in upstream:
    if x["id"].startswith(prefix) and mapping[x["id"]] != "ported":
        f = declaring_file(x["source"]["path"])
        if ids_filter and ids_filter in f:
            print(x["id"])
        rows[f][mapping[x["id"]]] += 1
if not ids_filter:
    for f, c in sorted(rows.items(), key=lambda kv: -sum(kv[1].values())):
        print(f"{sum(c.values()):5} {status.get(f, '?'):3} {dict(c)} {f}")
