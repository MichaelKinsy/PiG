#!/usr/bin/env python3
"""Close reviewed interface IDs in test/parity/interfaces/mapping-v<version>.json.

Usage: python3 test/parity/interface-closure/close.py <spec.json>... [--version 1.1.0]

An `ExtensionAPI.on` call overload is written with the eleven extension API layers. A spec is a JSON list of {ids, targets, production, evidence, rationale, async?, properties?}.
`struct` {file, type, fields?} instead of `properties` maps every property to the Go field with that json tag or
name (fields overrides a name); the spec's evidence applies to each property. `properties` maps a property name to its own {targets, evidence, production?, rationale?}; when present it must list every
property of each ID, because a parent is closed only with all of its children.
For each listed ID the script closes the ID and every ::call/::property child that is a call overload of it
(`<id>::call:N`), writing disposition "ported" with the shape/production/behavior layers complete. When every production reference is `public-api:<path>#<ExportedSymbol>` the production layer is written as `public-api-tested` instead: a public library API whose Pi counterpart is a public export, proven by test callers only.
It refuses an unknown ID, a stale shape hash, or a Promise/AbortSignal shape without an async contract.
A row that already equals the result is left alone, so a spec can be re-run. The references are then
validated by `make interface-mapping-quality`; the script writes no reference it has not been given.
"""
import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
args = sys.argv[1:]
version = "1.1.0"
if "--version" in args:
    i = args.index("--version")
    version = args[i + 1]
    del args[i : i + 2]
if not args:
    sys.exit(__doc__)
base = root / "test/parity/interfaces"


def struct_properties(spec, top, upstream):
    """Map every property of `top` to the Go field of spec["struct"] {file, type} whose json tag, or whose name
    ignoring case, equals the property name. A property without a field, or a field claimed twice, stops the run."""
    src = (root / spec["struct"]["file"]).read_text()
    body = re.search(rf"type {spec['struct']['type']} struct \{{(.*?)\n\}}", src, re.S)
    if not body:
        sys.exit(f"{spec['struct']['file']}: no struct {spec['struct']['type']}")
    def collect(type_name, depth=0):
        """json name or lowercase name -> (declaring type, Go field), including fields of embedded structs in the same file."""
        m = re.search(rf"type {type_name} struct \{{(.*?)\n\}}", src, re.S)
        if not m:
            sys.exit(f"{spec['struct']['file']}: no struct {type_name}")
        found = {}
        for line in m.group(1).splitlines():
            if line.strip().startswith("//"):
                continue
            emb = re.match(r"\s*\*?([A-Z]\w*)\s*(?://.*)?$", line)
            if emb and depth < 3:
                for k, v in collect(emb.group(1), depth + 1).items():
                    found.setdefault(k, v)
                continue
            f = re.match(r"\s*([A-Z]\w*)\s+\S.*?(?:`json:\"([^\",]*)[^`]*`)?\s*(?://.*)?$", line)
            if f:
                found[f.group(2) or f.group(1)] = (type_name, f.group(1))
                found.setdefault(f.group(1).lower(), (type_name, f.group(1)))
        return found

    fields = collect(spec["struct"]["type"])
    out = {}
    overrides = spec["struct"].get("fields", {})
    for k in upstream:
        if k.startswith(top + "::property:") and k.count("::") == 1:
            name = k.split("::property:")[1]
            hit = fields.get(name) or fields.get(name.lower())
            if overrides.get(name):
                hit = (spec["struct"]["type"], overrides[name])
            if not hit:
                sys.exit(f"{k}: no field in {spec['struct']['type']}")
            out[name] = {"targets": [f"{spec['struct']['file']}#{hit[0]}.{hit[1]}"]}
    return out

mapping_path = base / f"mapping-v{version}.json"
ledger = json.loads(mapping_path.read_text())
upstream = {x["id"]: x for x in json.loads((base / f"upstream-v{version}.json").read_text())["interfaces"]}
cli_path = base / f"cli-v{version}.json"
if cli_path.exists():
    # The command-line inventory: a row has no declaration shape, and its layers are the parser, the help text, the
    # consumer of the parsed value and the behavior (interfaceinventory requiredMappingLayers).
    for x in json.loads(cli_path.read_text())["interfaces"]:
        upstream[x["id"]] = {"shapeHash": x["shapeHash"], "shape": {}}
rows = {x["id"]: x for x in ledger["mappings"]}
closed = unchanged = 0
for spec_path in args:
    for spec in json.loads(Path(spec_path).read_text()):
        for top in spec["ids"]:
            if top not in rows:
                sys.exit(f"unknown interface ID {top}")
            props = dict(spec.get("properties", {}))
            if "struct" in spec and not props:
                props = struct_properties(spec, top, upstream)
            prop_ids = {top + "::property:" + name: v for name, v in props.items()}
            every = [k for k in upstream if k.startswith(top + "::property:") and k.count("::") == 1]
            if every and set(every) != set(prop_ids):
                sys.exit(f"{top}: a parent is closed with every property or none; missing {sorted(set(every) - set(prop_ids))}")
            for gid in [top] + [k for k in upstream if k.startswith(top + "::call:")] + list(prop_ids) + [k for pid in prop_ids for k in upstream if k.startswith(pid + "::call:")]:
                row, shape = rows[gid], json.dumps(upstream[gid]["shape"])
                own = prop_ids.get(gid) or prop_ids.get(gid.split("::call:")[0]) or spec
                if row["upstreamShapeHash"] != upstream[gid]["shapeHash"]:
                    sys.exit(f"{gid}: shape hash is stale")
                new = {"id": gid, "disposition": "ported", "upstreamShapeHash": row["upstreamShapeHash"],
                       "rationale": own["rationale"] if "rationale" in own else spec["rationale"], "pigTargets": own.get("targets", spec["targets"]),
                       "layers": {"shape": "complete", "production": "complete", "behavior": "complete"},
                       "production": own.get("production", spec["production"]), "evidence": own.get("evidence", spec["evidence"])}
                if gid.startswith("cli:"):
                    new["layers"] = {layer: "complete" for layer in ("parser", "help", "consumer", "behavior")}
                if "#ExtensionAPI::property:on::call:" in gid:
                    # An `on` overload carries the extension API layers (interfaceinventory requiredMappingLayers): the Go API member,
                    # the wire, the host dispatch, the three SDKs, and isolated and packed conformance.
                    new["layers"] = {layer: "complete" for layer in ("shape", "api", "wire", "host-dispatch", "sdk-go", "sdk-rust", "sdk-python", "isolated-conformance", "packed-conformance", "production", "behavior")}
                if new["production"] and all(ref.startswith("public-api:") for ref in new["production"]):
                    # public-api-tested: a public library API with test callers and no PiG call site (see interfaceinventory).
                    new["layers"]["production"] = "public-api-tested"
                if "Promise<" in shape or "AbortSignal" in shape:
                    if not spec.get("async"):
                        sys.exit(f"{gid}: Promise/AbortSignal shape needs an async contract")
                    new["asyncContract"] = spec["async"]
                if row == new:
                    unchanged += 1
                    continue
                row.clear()
                row.update(new)
                closed += 1
mapping_path.write_text(json.dumps(ledger, indent=2, ensure_ascii=False) + "\n")
print(f"closed {closed} rows, {unchanged} already closed")
