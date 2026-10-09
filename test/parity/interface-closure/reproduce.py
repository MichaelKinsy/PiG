#!/usr/bin/env python3
"""Re-point closed rows whose `call:` production reference is unreachable from the PiG binary.

Usage: go run ./test/parity/cmd/prodreach -out /tmp/reach.json -tags pig_experimental ./cmd/pig ./cmd/pig-experimental
       python3 test/parity/interface-closure/reproduce.py /tmp/reach.json [--apply]
For each row whose call references are all outside the reachable set, it finds the Go symbol the row targets, asks
methodrefs (go/types) for every caller of that symbol, and picks the first caller that is reachable. It reports, per row,
one of: repointed (a reachable caller exists), public (an exported symbol of a public package with no reachable caller:
a public-api-tested candidate), or unreachable (neither). --apply writes the repointed rows; --public also rewrites the
public rows as public-api-tested (a public library API proven by test callers only). Run it after close.py: the specs
carry the first caller go/types found, not the first reachable one.
"""
import collections
import json
import re
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
reach = set(json.loads(Path(sys.argv[1]).read_text()))
apply = "--apply" in sys.argv
mod = "github.com/MichaelKinsy/PiG"
path = root / "test/parity/interfaces/mapping-v1.1.0.json"
ledger = json.loads(path.read_text())
bad = [r for r in ledger["mappings"] if r["disposition"] == "ported"
       and any(p.startswith("call:") for p in r["production"]) and not any(p[5:] in reach for p in r["production"] if p.startswith("call:"))]
want = collections.defaultdict(lambda: {"types": set(), "funcs": set()})
rowkey = {}
for r in bad:
    target = r["pigTargets"][0]
    file, frag = target.split("#")
    d = str(Path(file).parent)
    text = (root / file).read_text()
    owner = frag.split(".")[0]
    if "." not in frag and re.search(rf"^func {frag}[\[(]", text, re.M):
        want[d]["funcs"].add(frag); rowkey[r["id"]] = (d, "func", frag, None)
    else:
        want[d]["types"].add(owner); rowkey[r["id"]] = (d, "type", owner, frag.split(".")[1] if "." in frag else None)
refs = {}
for d, w in want.items():
    imp = f"{mod}/{d}" if d != "." else mod
    args = ["go", "run", "./test/parity/cmd/methodrefs", "-out", "/tmp/rp.json"]
    if w["types"]:
        args += ["-types", ",".join(f"{imp}.{n}" for n in sorted(w["types"]))]
    if w["funcs"]:
        args += ["-funcs", ",".join(f"{imp}.{n}" for n in sorted(w["funcs"]))]
    subprocess.run(args + ["./..."], cwd=root, check=True, capture_output=True)
    refs[d] = json.loads(Path("/tmp/rp.json").read_text())
status = collections.Counter()
for r in bad:
    d, kind, name, member = rowkey[r["id"]]
    entry = refs[d].get(name, {})
    if kind == "func":
        cands = (entry.get("_call") or {}).get("prod") or []
    else:
        cands = (entry.get(member) or {}).get("prod") or [] if member else (entry.get("_type") or {}).get("prod") or []
    hit = next((c for c in cands if c in reach and c.split("#")[1] != name), None)
    if hit:
        status["repointed"] += 1
        if apply:
            r["production"] = ["call:" + hit]
    else:
        public = not d.startswith(("internal", "cmd")) and name[:1].isupper() and any(e.startswith("test:") for e in r["evidence"])
        status["public" if public else "unreachable"] += 1
        if public and "--public" in sys.argv:
            r["production"] = ["public-api:" + r["pigTargets"][0]]
            r["layers"]["production"] = "public-api-tested"
        print(("public     " if public else "unreachable"), r["id"], r["pigTargets"][0])
print(dict(status))
if apply:
    path.write_text(json.dumps(ledger, indent=2, ensure_ascii=False) + "\n")
