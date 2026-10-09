#!/usr/bin/env python3
"""Print the per-package closure table for docs/parity/gap-closure/gap-interface-ledger.md from the current mapping."""
import collections
import json
from pathlib import Path

root = Path(__file__).resolve().parents[3]
m = json.loads((root / "test/parity/interfaces/mapping-v1.1.0.json").read_text())["mappings"]
c = collections.Counter((x["id"].split("/")[0] if x["id"].startswith("pkg") else "cli", x["disposition"]) for x in m)
for key, name in [("pkg:ai", "ai"), ("pkg:coding-agent", "coding-agent"), ("pkg:tui", "tui"), ("pkg:agent", "agent"), ("pkg:mcp", "mcp"), ("pkg:codemode", "codemode"), ("cli", "cli")]:
    extra = " (+2 designed-out)" if key == "pkg:ai" else ""
    print(f"| {name} | 0 | {c[(key, 'ported')]} | {c[(key, 'deferred')]} | {c[(key, 'pending')]}{extra} |")
t = collections.Counter(x["disposition"] for x in m)
print(f"| total | 0 | {t['ported']} | {t['deferred']} | {t['pending']} (+2 designed-out) |")
tested = collections.Counter((x["id"].split("/")[0] if x["id"].startswith("pkg") else "cli") for x in m if x["disposition"] == "ported" and x.get("layers", {}).get("production") == "public-api-tested")
print("public-api-tested (a public library API proven by test callers, no PiG call site): " + (", ".join(f"{k[4:] if k.startswith('pkg:') else k} {v}" for k, v in sorted(tested.items())) or "0"))
print(f"moved to ported: {3543 - t['pending']} pending and {6405 - t['deferred']} deferred")
