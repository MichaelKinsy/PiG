#!/usr/bin/env python3
"""Propose closures for the members of one upstream class or interface that map to Go methods of one receiver type.

Usage: python3 test/parity/interface-closure/methods.py pkg:coding-agent/.#SettingsManager <go dir> <GoReceiver> [--emit spec.json]
For every `<id>::property:<name>` it looks for a Go method named Name (first letter upper-cased) on the receiver, a production
caller outside tests, and tests that mention the method. Only members with a method, a production caller and a test are
proposed. --upstream keeps only tests in files named *upstream*. --public-api also proposes a member that has test callers but no production caller, as public-api-tested (an exported method of a public package whose Pi counterpart is a public export); the production reference is then `public-api:`. --refs <file> takes callers from `go run ./test/parity/cmd/methodrefs`, which attributes each call by receiver type with go/types; use it for any generic method name. Print mode is a review aid; --emit writes a spec for close.py. A test is attributed to a method by its name within the Go directory, not by receiver type, so a generic name (Render, Model, Messages) matches other types: close only members whose name is unique to the receiver, or confirm the test by reading it. Review each proposal's signature before closing it:
the script checks names, not behaviour.
"""
import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
args = [a for a in sys.argv[1:] if a not in ("--simple", "--upstream", "--public-api")]
public = "--public-api" in sys.argv
refs_path = None
if "--refs" in args:
    refs_path = args[args.index("--refs") + 1]
    del args[args.index("--refs") : args.index("--refs") + 2]
typed = json.loads(Path(refs_path).read_text()) if refs_path else None
emit = args[args.index("--emit") + 1] if "--emit" in args else None
if emit:
    del args[args.index("--emit") : args.index("--emit") + 2]
cls, godir, recv = args
base = root / "test/parity/interfaces"
upstream = {x["id"]: x for x in json.loads((base / "upstream-v1.1.0.json").read_text())["interfaces"]}
mapping = {x["id"]: x["disposition"] for x in json.loads((base / "mapping-v1.1.0.json").read_text())["mappings"]}
files = sorted((root / godir).glob("*.go"))
text = {f: f.read_text() for f in files}


def enclosing(path, lineno):
    cur = None
    for i, line in enumerate(text[path].splitlines(), 1):
        m = re.match(r"func (?:\(\w+ \*?(\w+)(?:\[[^\]]*\])?\) )?(\w+)", line)
        if m:
            cur = f"{m.group(1)}.{m.group(2)}" if m.group(1) else m.group(2)
        if i == lineno:
            return cur


def go_sig(path, recv, name):
    m = re.search(rf"^func \(\w+ \*?{recv}\) {name}\(([^)]*)\)\s*([^{{]*)\{{", text[path], re.M)
    return (m.group(1).strip(), m.group(2).strip()) if m else ("?", "?")


KINDS = {"boolean": ("bool",), "number": ("int", "int64", "float64", "uint"), "string": ("string",)}


def kind_ok(up, go):
    """Same scalar kind: boolean/number/string, with a string-literal union matching string or a named type."""
    up = up.strip()
    if up in KINDS:
        return go in KINDS[up]
    if re.fullmatch(r'("[^"]*"( \| )?)+', up):
        return go == "string" or bool(re.fullmatch(r"[A-Z]\w*", go))
    return False


def simple_match(up_type, sig):
    """Accept `() => T`, `(x: T) => void` and `(x: T) => void` with an optional trailing error against scalar Go signatures."""
    params, ret = sig
    m = re.fullmatch(r"\(\) => (.+)", up_type)
    if m:
        return params == "" and kind_ok(m.group(1), ret)
    m = re.fullmatch(r"\(\w+: ([^,()]+)\) => void", up_type)
    if m:
        ptype = params.split()[-1] if params else ""
        return len(params.split(",")) == 1 and kind_ok(m.group(1), ptype) and ret in ("", "error")
    return False


simple = "--simple" in sys.argv
upstream_only = "--upstream" in sys.argv
specs = []
for pid in upstream:
    if not pid.startswith(cls + "::property:") or pid.count("::") != 1 or mapping[pid] == "ported":
        continue
    name = pid.split("::property:")[1]
    go = name[0].upper() + name[1:]
    decl = None
    for f in files:
        if re.search(rf"^func \(\w+ \*?{recv}\) {go}\(", text[f], re.M) and not f.name.endswith("_test.go"):
            decl = f
    if not decl:
        continue
    prod, tests = None, []
    if typed is not None:
        r = typed.get(go, {})
        prod = next((p for p in r.get("prod") or [] if not p.endswith("#" + recv + "." + go)), None)
        tests = [t for t in r.get("tests") or [] if t.split("#")[1].startswith("Test")]
        if not tests:
            continue
    for f in [] if typed is not None else files:
        for i, line in enumerate(text[f].splitlines(), 1):
            if not re.search(rf"\.{go}\(", line) or line.lstrip().startswith("//"):
                continue
            fn = enclosing(f, i)
            if f.name.endswith("_test.go"):
                if fn and fn.startswith("Test") and f"{f.relative_to(root)}#{fn}" not in tests:
                    tests.append(f"{f.relative_to(root)}#{fn}")
            elif prod is None and fn and not fn.endswith("." + go):
                prod = f"{f.relative_to(root)}#{fn}"
    if upstream_only:
        tests = [t for t in tests if "upstream" in t.split("#")[0]]
    public_api = public and typed is not None and not prod and tests and recv[0].isupper() and not str(decl.relative_to(root)).startswith(("internal/", "cmd/"))
    if (prod or public_api) and tests:
        sig = upstream[pid]["shape"]["type"][:70]
        if simple and not simple_match(upstream[pid]["shape"]["type"], go_sig(decl, recv, go)):
            continue
        print(f"{name}: prod={prod.split('#')[1] if prod else 'PUBLIC-API'} tests={len(tests)} up={sig} go={go_sig(decl, recv, go)}")
        specs.append({"ids": [pid], "targets": [f"{decl.relative_to(root)}#{recv}.{go}"], "production": [f"public-api:{decl.relative_to(root)}#{recv}.{go}"] if public_api else [f"call:{prod}"],
                      "evidence": [f"test:{t}" for t in tests[:2]],
                      "rationale": f"{cls.split('#')[1]}.{name} is the Go method {recv}.{go}; signature reviewed against the upstream declaration."})
if emit:
    Path(emit).write_text(json.dumps(specs, indent=1))
    print(f"wrote {len(specs)} entries to {emit}")
