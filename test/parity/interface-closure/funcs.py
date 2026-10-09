#!/usr/bin/env python3
"""Show unclosed upstream functions beside same-name Go functions with type-checked callers, and emit a close.py spec.

Usage: python3 test/parity/interface-closure/funcs.py <go dir> <refs.json> [--accept a,b] [--public-api a,b] [--emit spec.json] Name[=GoName]...
--public-api names accepted functions that have no production caller: an exported function of a public package whose Pi counterpart is a public export closes as public-api-tested, with its test callers as evidence.
refs.json comes from `go run ./test/parity/cmd/methodrefs -funcs <import path>.<Func>,... -out refs.json ./...`.
For each upstream function name (all unclosed IDs ending in #name, including subpath aliases such as ai/compat) the output
lists the upstream signature, the Go signature, the first production caller and the tests that call the Go function
(upstream-ported tests first). Review the two signatures: the spec is emitted only for names listed in --accept (comma-separated),
so nothing closes without a signature review. The emitted production/evidence come from go/types, not name matching.
"""
import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
args = sys.argv[1:]
emit = accept = public = None
for flag in ("--emit", "--accept", "--public-api"):
    if flag in args:
        i = args.index(flag)
        val = args[i + 1]
        del args[i : i + 2]
        if flag == "--emit":
            emit = val
        elif flag == "--public-api":
            public = set(val.split(","))
        else:
            accept = set(val.split(","))
godir, refs_path, *names = args
refs = json.loads(Path(refs_path).read_text())
base = root / "test/parity/interfaces"
upstream = json.loads((base / "upstream-v1.1.0.json").read_text())["interfaces"]
mapping = {x["id"]: x["disposition"] for x in json.loads((base / "mapping-v1.1.0.json").read_text())["mappings"]}
src = {f: f.read_text() for f in sorted((root / godir).glob("*.go")) if not f.name.endswith("_test.go")}
specs = []
for item in names:
    name, _, goname = item.partition("=")
    goname = goname or name[0].upper() + name[1:]
    ids = [x["id"] for x in upstream if x["kind"] == "function" and "::" not in x["id"] and x["id"].endswith("#" + name) and mapping[x["id"]] != "ported"]
    sig = next((x["shape"]["type"] for x in upstream if x["id"] == (ids[0] if ids else "")), "")
    gosig = None
    for f, text in src.items():
        m = re.search(rf"^func {goname}(\[[^\]]*\])?\(.*$", text, re.M)
        if m:
            gosig, gofile = m.group(0)[:170], f
            break
    r = refs.get(goname, {}).get("_call", {})
    prod = next((p for p in r.get("prod") or [] if p.split("#")[1] != goname), None)
    tests = [t for t in r.get("tests") or [] if t.split("#")[1].startswith("Test")]
    tests.sort(key=lambda t: (0 if "upstream" in t.split("#")[0] else 1, t))
    print(f"{name}: ids={len(ids)}\n   ts: {sig[:170]}\n   go: {gosig}\n   prod={prod} tests={[t.split('/')[-1] for t in tests[:2]]}")
    public_api = bool(public) and name in public and not prod and tests and goname[0].isupper()
    if accept and name in accept and ids and gosig and (prod or public_api) and tests:
        rel = str(gofile.relative_to(root))
        production = [f"public-api:{rel}#{goname}"] if public_api else [f"call:{prod}"]
        specs.append({"ids": ids, "targets": [f"{rel}#{goname}"], "production": production, "evidence": [f"test:{t}" for t in tests[:2]],
                      "async": "An awaited Promise is a blocking Go call that returns the value or error; AbortSignal is the context.Context argument; no detached goroutine.",
                      "rationale": f"{name}: Go {goname} has the upstream parameters and result (signatures reviewed); callers attributed by go/types."})
if emit:
    Path(emit).write_text(json.dumps(specs, indent=1))
    print(f"wrote {len(specs)} entries")
