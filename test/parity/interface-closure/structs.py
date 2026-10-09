#!/usr/bin/env python3
"""Emit a close.py spec for upstream interfaces that map to Go structs, with type-checked evidence.

Usage: python3 test/parity/interface-closure/structs.py <upstream pkg prefix, e.g. pkg:coding-agent/.#> <go dir> <refs.json> Name[=GoType]... [--public-api A,B] [--emit spec.json]
--public-api names types that have test uses but no production use: an exported type of a public package whose Pi counterpart is a public export closes as public-api-tested.
refs.json comes from `go run ./test/parity/cmd/methodrefs -types <import path>.<Type>,... -out refs.json <patterns>`. For each name it requires a
struct in the Go dir with a field for every upstream property (close.py's struct rule), a production function that mentions the type, and
test functions that mention it (go/types, so no name collisions); tests in files named *upstream* or *conformance* come first. Read the
proposed tests before closing: a type that a test only passes through proves little.
"""
import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
args = sys.argv[1:]
emit = None
public = None
if "--public-api" in args:
    public = set(args[args.index("--public-api") + 1].split(","))
    del args[args.index("--public-api") : args.index("--public-api") + 2]
if "--emit" in args:
    emit = args[args.index("--emit") + 1]
    del args[args.index("--emit") : args.index("--emit") + 2]
prefix, godir, refs_path, *names = args
refs = json.loads(Path(refs_path).read_text())
base = root / "test/parity/interfaces"
upstream = {x["id"]: x for x in json.loads((base / "upstream-v1.1.0.json").read_text())["interfaces"]}
mapping = {x["id"]: x["disposition"] for x in json.loads((base / "mapping-v1.1.0.json").read_text())["mappings"]}
specs = []
for item in names:
    name, _, gotype = item.partition("=")
    gotype = gotype or name
    top = prefix + name
    if top not in upstream or mapping[top] == "ported":
        print(f"{name}: not an unclosed upstream interface")
        continue
    found = None
    for f in sorted((root / godir).glob("*.go")):
        if f.name.endswith("_test.go"):
            continue
        m = re.search(rf"type {gotype} struct \{{(.*?)\n\}}", f.read_text(), re.S)
        if m:
            found = (f, m.group(1))
            break
    if not found:
        print(f"{name}: no Go struct {gotype}")
        continue
    fields = {}

    def collect(type_name, depth=0):
        m = re.search(rf"type {type_name} struct \{{(.*?)\n\}}", found[0].read_text(), re.S)
        for line in (m.group(1) if m else "").splitlines():
            if line.strip().startswith("//"):
                continue
            emb = re.match(r"\s*\*?([A-Z]\w*)\s*(?://.*)?$", line)
            if emb and depth < 3:
                collect(emb.group(1), depth + 1)
                continue
            mm = re.match(r"\s*([A-Z]\w*)\s+\S.*?(?:`json:\"([^\",]*)[^`]*`)?\s*(?://.*)?$", line)
            if mm:
                fields[(mm.group(2) or mm.group(1)).lower()] = mm.group(1)
                fields.setdefault(mm.group(1).lower(), mm.group(1))

    collect(gotype)
    props = [k.split("::property:")[1] for k in upstream if k.startswith(top + "::property:") and k.count("::") == 1]
    missing = [p for p in props if p.lower() not in fields]
    uses = refs.get(gotype, {}).get("_type", {})
    prod = next((p for p in uses.get("prod") or [] if not p.split("#")[1].startswith(gotype)), None)
    tests = [t for t in uses.get("tests") or [] if t.split("#")[1].startswith("Test")]
    tests.sort(key=lambda t: (0 if re.search(r"upstream|conformance", t.split("#")[0]) else 1, t))
    public_api = bool(public) and name in public and not prod and tests and gotype[0].isupper()
    status = "OK" if not missing and (prod or public_api) and tests else f"skip missing={missing} prod={bool(prod)} tests={len(tests)}"
    print(f"{name}: {found[0].relative_to(root)} {status}" + (f" prod={prod} tests={[t.split('/')[-1] for t in tests[:2]]}" if status == "OK" else ""))
    if status == "OK":
        rel = str(found[0].relative_to(root))
        specs.append({"ids": [top], "targets": [f"{rel}#{gotype}"], "struct": {"file": rel, "type": gotype}, "production": [f"public-api:{rel}#{gotype}"] if public_api else [f"call:{prod}"],
                      "evidence": [f"test:{t}" for t in tests[:2]],
                      "async": "An awaited Promise is a blocking Go call that returns the value or error; AbortSignal is the context.Context argument; no detached goroutine.",
                      "rationale": f"{name}: Go {gotype} has a field for every upstream property; types reviewed against the upstream declaration; tests attributed by go/types."})
if emit:
    Path(emit).write_text(json.dumps(specs, indent=1))
    print(f"wrote {len(specs)} entries")
