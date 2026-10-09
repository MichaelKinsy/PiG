#!/usr/bin/env python3
"""Print an upstream interface's members beside the Go struct fields they would map to, for review before a spec is written.

Usage: python3 test/parity/interface-closure/review.py pkg:agent/.#AgentContext [Go struct source file#Type]...
"""
import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
upstream = {x["id"]: x for x in json.loads((root / "test/parity/interfaces/upstream-v1.1.0.json").read_text())["interfaces"]}
for arg in sys.argv[1:]:
    if arg.startswith("pkg:"):
        print(arg)
        for k, x in upstream.items():
            if k.startswith(arg + "::property:") and k.count("::") == 1:
                s = x["shape"]
                print(f"  {k.split('::property:')[1]}{'?' if s.get('optional') else ''}: {s['type']}")
    else:
        path, typ = arg.split("#")
        src = (root / path).read_text()
        m = re.search(rf"type {typ} struct \{{(.*?)\n\}}", src, re.S)
        print(arg)
        for line in (m.group(1).splitlines() if m else ["  <not found>"]):
            if line.strip() and not line.strip().startswith("//"):
                print("  ", line.strip())
