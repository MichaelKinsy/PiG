#!/usr/bin/env python3
"""List, for Go type names in one directory, the upstream-ported tests that mention each and one production use with its enclosing function.

Usage: python3 test/parity/interface-closure/findrefs.py <go dir> TypeName...
The output is a starting point for a spec's evidence and production references; read the test before citing it.
"""
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
godir = root / sys.argv[1]
files = sorted(godir.glob("*.go"))


def funcs(path):
    """Yield (line, enclosing func name as Recv.Name or Name) for each line."""
    cur = None
    for line in path.read_text().splitlines():
        m = re.match(r"func (?:\(\w+ \*?(\w+)(?:\[[^\]]*\])?\) )?(\w+)", line)
        if m:
            cur = f"{m.group(1)}.{m.group(2)}" if m.group(1) else m.group(2)
        yield line, cur


for name in sys.argv[2:]:
    pat = re.compile(rf"\b{name}\b")
    tests, prod = [], None
    for f in files:
        is_test = f.name.endswith("_test.go")
        seen = set()
        for line, fn in funcs(f):
            if fn is None or not pat.search(line) or line.lstrip().startswith(("//", "type ")):
                continue
            if is_test:
                if fn.startswith("Test") and (f.name, fn) not in seen:
                    seen.add((f.name, fn))
                    tests.append((0 if "upstream" in f.name else 1, f.name, fn))
            elif prod is None and not re.match(rf"func .*\b{name}\b", line) and not fn.endswith(name):
                prod = f"{f.relative_to(root)}#{fn}"
    tests.sort()
    print(f"{name}: prod={prod} tests={[f'{f}#{t}' for _, f, t in tests[:3]]}")
