#!/usr/bin/env python3
"""Dry run of close.py's `struct` mapping: for upstream interface names and a Go directory, report which interfaces have a Go struct
of the same name (or --go NAME=GoType) whose fields cover every property.

Usage: python3 test/parity/interface-closure/mapcheck.py <pkg:prefix e.g. pkg:tui/.#> <go dir> Name[=GoType]...
"""
import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
upstream = {x["id"]: x for x in json.loads((root / "test/parity/interfaces/upstream-v1.1.0.json").read_text())["interfaces"]}
prefix, godir = sys.argv[1], root / sys.argv[2]
for arg in sys.argv[3:]:
    name, _, gotype = arg.partition("=")
    gotype = gotype or name
    found = None
    for f in sorted(godir.glob("*.go")):
        if f.name.endswith("_test.go"):
            continue
        m = re.search(rf"type {gotype} struct \{{(.*?)\n\}}", f.read_text(), re.S)
        if m:
            found = (f, m.group(1))
            break
    props = [k.split("::property:")[1] for k in upstream if k.startswith(prefix + name + "::property:") and k.count("::") == 1]
    if not found:
        print(f"{name}: no Go struct {gotype}")
        continue
    fields = {}
    for line in found[1].splitlines():
        mm = re.match(r"\s*([A-Z]\w*)\s+\S.*?(?:`json:\"([^\",]*)[^`]*`)?\s*(?://.*)?$", line)
        if mm and not line.strip().startswith("//"):
            fields[(mm.group(2) or mm.group(1)).lower()] = mm.group(1)
            fields.setdefault(mm.group(1).lower(), mm.group(1))
    missing = [p for p in props if p.lower() not in fields]
    print(f"{name}: {found[0].relative_to(root)} {'OK' if not missing else 'missing ' + ','.join(missing)} ({len(props)} props)")
