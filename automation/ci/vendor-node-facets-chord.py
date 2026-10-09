#!/usr/bin/env python3
"""Materialize the runtime closure of the locked @earendil-works/chord dist into internal/experimental/node-facets/chord.

The version, tarball URL and integrity come from extensions/sdk-ts/package-lock.json, so the vendored
JavaScript always matches the Pi release the port tracks. The installed locked copy under
extensions/sdk-ts/node_modules is used when present; otherwise the locked tarball is downloaded and
verified against its integrity. Default mode rewrites the directory;
--check reports drift without writing. The closure is every module reachable from the roots the
isolated facet driver imports; the Node package compiler (bundle.js, package.js, bundler.js) is
not part of it.
"""
import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import re
import sys
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
LOCK = ROOT / "extensions/sdk-ts/package-lock.json"
LOCK_KEY = "node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord"
INSTALLED = ROOT / "extensions/sdk-ts" / LOCK_KEY / "dist"
TARGET = ROOT / "internal/experimental/node-facets/chord"
ROOTS = ("index.js", "node.js", "context/index.js", "services/state-internals.js", "services/provider.js")
IMPORT = re.compile(r'(?:from|import)\s*\(?\s*"(\.[^"]+)"')


def locked() -> dict:
    entry = json.loads(LOCK.read_text())["packages"][LOCK_KEY]
    return {"version": entry["version"], "resolved": entry["resolved"], "integrity": entry["integrity"]}


def fetch(entry: dict, tarball: str | None) -> bytes:
    if tarball:
        data = Path(tarball).read_bytes()
    else:
        with urllib.request.urlopen(entry["resolved"], timeout=60) as response:  # noqa: S310 - locked https registry URL
            data = response.read()
    algorithm, _, expected = entry["integrity"].partition("-")
    digest = base64.b64encode(hashlib.new(algorithm, data).digest()).decode()
    if digest != expected:
        raise SystemExit(f"integrity mismatch for {entry['resolved']}: {algorithm}-{digest} != {entry['integrity']}")
    return data


def closure(files: dict[str, bytes]) -> list[str]:
    seen: set[str] = set()
    stack = list(ROOTS)
    while stack:
        name = stack.pop()
        if name in seen:
            continue
        if name not in files:
            raise SystemExit(f"dist module {name} is missing from the locked tarball")
        seen.add(name)
        for match in IMPORT.finditer(files[name].decode()):
            stack.append(os.path.normpath(os.path.join(os.path.dirname(name), match.group(1))).replace(os.sep, "/"))
    return sorted(seen)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--check", action="store_true", help="fail when the vendored tree differs from the locked dist")
    parser.add_argument("--tarball", help="use a local copy of the locked tarball (integrity is still verified)")
    args = parser.parse_args()
    entry = locked()
    dist: dict[str, bytes] = {}
    if INSTALLED.is_dir() and not args.tarball:
        installed = json.loads((INSTALLED.parent / "package.json").read_text())["version"]
        if installed != entry["version"]:
            raise SystemExit(f"installed chord {installed} differs from locked {entry['version']}; run npm ci in extensions/sdk-ts")
        for path in INSTALLED.rglob("*"):
            if path.is_file() and path.name.endswith((".js", ".js.map")):
                dist[path.relative_to(INSTALLED).as_posix()] = path.read_bytes()
    else:
        archive = tarfile.open(fileobj=io.BytesIO(fetch(entry, args.tarball)), mode="r:gz")
        for member in archive.getmembers():
            if member.isfile() and member.name.startswith("package/dist/") and member.name.endswith((".js", ".js.map")):
                dist[member.name.removeprefix("package/dist/")] = archive.extractfile(member).read()
    modules = closure(dist)
    wanted = {name: dist[name] for module in modules for name in (module, module + ".map")}
    existing = {
        path.relative_to(TARGET).as_posix(): path.read_bytes()
        for path in TARGET.rglob("*")
        if path.is_file() and path.name != "LICENSE"
    }
    drift = sorted(name for name in wanted.keys() | existing.keys() if wanted.get(name) != existing.get(name))
    if args.check:
        if drift:
            print(f"internal/experimental/node-facets/chord differs from @earendil-works/chord {entry['version']}: {', '.join(drift)}", file=sys.stderr)
            print("run: python3 automation/ci/vendor-node-facets-chord.py", file=sys.stderr)
            return 1
        return 0
    for name in set(existing) - set(wanted):
        (TARGET / name).unlink()
    for name, content in wanted.items():
        path = TARGET / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    for directory in sorted((p for p in TARGET.rglob("*") if p.is_dir()), reverse=True):
        if not any(directory.iterdir()):
            directory.rmdir()
    print(f"vendored {len(modules)} modules of @earendil-works/chord {entry['version']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
