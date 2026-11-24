# SPDX-License-Identifier: MIT
"""The vendored source build of Pi's durable and chord packages (third_party/pi-packs).

The Cloudflare host (durable/core/host/cf), durable/interop and the benchmark image's pi-durable project install Pi's
`@earendil-works/pi-durable` and the packages it runs on. npm publishes releases, not commits, so for a pin that is a commit
(or a release whose package differs from its source build) the registry holds the wrong bytes. `npm pack` of the pinned source
build is reproducible, so its outputs are committed: third_party/pi-packs/*.tgz, installed through `file:` dependencies and
overrides whose locks carry each pack's sha512.

  vendor.py sync      pack the pinned build (make vendor-pi-packs), write manifest.json, LICENSE, the consumers' package.json
                      and package-lock.json
  vendor.py check     offline gate: manifest and packs at the pin, no consumer reaches a Pi package through the registry
  vendor.py check --rebuild   also pack the pinned build again and require the vendored bytes (members and gzip) to equal it

check_installed is the same gate on a consumer's node_modules, and override_tree builds, outside the repository, the packs and
the install of another Pi commit for a run that compares with it.
"""
from __future__ import annotations

import base64
import hashlib
import json
import os
import re
import shutil
import subprocess
import tarfile
import tempfile
from pathlib import Path

import pinlib
from pinlib import ROOT, Pin, PinError

PACKS = Path("third_party/pi-packs")
MANIFEST = "manifest.json"
LICENSE = "LICENSE"
DURABLE = "@earendil-works/pi-durable"

# The projects that install Pi's packages from the vendored packs, and the packs each names directly (the others come in through
# their dependencies, which the overrides point at the same files).
CONSUMERS = {
    "durable/core/host/cf": ["@earendil-works/chord", "@earendil-works/pi-ai", DURABLE, "@earendil-works/pi-telemetry"],
    "durable/interop": ["@earendil-works/chord", "@earendil-works/pi-ai", DURABLE, "@earendil-works/pi-telemetry"],
    "bench/k8s/image/pi-durable": ["@earendil-works/chord", "@earendil-works/pi-ai", DURABLE],
}

# Directories of the tree that name a Pi package and are NOT consumers of the vendored packs, with the reason.
EXEMPT = {
    "extensions/sdk-ts/": "the peer dependency of @michaelkinsy/pig-extension-types stays the published release (a declaration drift test compares it with the pinned build)",
    "automation/images/ci-parity/": "the CI image builds the pinned commit from source (ARG PI_COMMIT) or installs the published release of a tag pin",
    "bench/k8s/image/pi-agent/": "the competitor-side measuring tool: Pi's published install pair (a commit pin installs the frozen closure and the build store's packs)",
    "bench/evals/locks/": "release locks of other Pi versions, the competitor references",
    "bench/bolt-aot/": "extension fixtures",
    "coding/extension/host/subprocess/runtime-node/shims/pi-dist/": "Pi's own dist modules, vendored by make vendor-pi-dist",
    "chord/nextnode/plugins/node/pi/": "Pi's chord sources, vendored by make vendor-chord-plugins-node",
    "test/parity/corpus/": "locks of third-party extensions",
}
# Copies of a pack's shipped files elsewhere in the tree: path -> package. Each must equal the pack's members.
COPIES = {"chord/nextnode/plugins/node/pi/node_modules/@earendil-works/pi-telemetry": "@earendil-works/pi-telemetry"}

DEP_FIELDS = ("dependencies", "devDependencies", "peerDependencies", "optionalDependencies", "overrides")
PI_PACKAGE = re.compile(r"^@earendil-works/(chord|pi-[a-z-]+)$")
SKIP_DIRS = {"node_modules", ".git", ".upstream", "tmp", ".cache"}
NPM_FLAGS = ["--ignore-scripts", "--no-audit", "--no-fund"]


def pack_file(name: str, version: str) -> str:
    return name.removeprefix("@").replace("/", "-") + "-" + version + ".tgz"


def integrity(data: bytes) -> str:
    return "sha512-" + base64.b64encode(hashlib.sha512(data).digest()).decode()


def read_manifest(root: Path) -> dict:
    path = root / PACKS / MANIFEST
    if not path.is_file():
        raise PinError(f"{PACKS / MANIFEST} is missing: run `make vendor-pi-packs`")
    return json.loads(path.read_text(encoding="utf-8"))


def members(tgz: bytes) -> dict[str, tuple[int, bytes]]:
    import io

    with tarfile.open(fileobj=io.BytesIO(tgz)) as archive:
        return {m.name: (m.mode, archive.extractfile(m).read()) for m in archive if m.isfile()}


def rel_spec(consumer: str, file: str) -> str:
    return "file:" + os.path.relpath(PACKS / file, consumer).replace(os.sep, "/")


# --- the scan ---------------------------------------------------------------------------------------------------

def exempt(rel: str) -> bool:
    return any(rel.startswith(prefix) for prefix in EXEMPT)


def pi_named(manifest: dict) -> list[str]:
    names = []
    for field in DEP_FIELDS:
        names += [name for name in manifest.get(field, {}) if PI_PACKAGE.match(name)]
    return sorted(set(names))


def scan(root: Path) -> dict[str, list[str]]:
    """Every package.json of the tree outside EXEMPT that names a Pi package, with the names (relative directory -> names)."""
    found: dict[str, list[str]] = {}
    for current, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        if "package.json" not in files:
            continue
        rel = Path(current).relative_to(root).as_posix()
        if exempt(rel + "/"):
            continue
        try:
            names = pi_named(json.loads((Path(current) / "package.json").read_text(encoding="utf-8")))
        except (ValueError, OSError):
            continue
        if names:
            found[rel] = names
    return found


# --- the gate ---------------------------------------------------------------------------------------------------

def check_consumer(root: Path, consumer: str, manifest: dict) -> list[str]:
    problems = []
    packs = manifest["packs"]
    by_name = {meta["name"]: file for file, meta in packs.items()}
    package = json.loads((root / consumer / "package.json").read_text(encoding="utf-8"))
    for field in DEP_FIELDS:
        for name, spec in package.get(field, {}).items():
            if PI_PACKAGE.match(name) and (name not in by_name or spec != rel_spec(consumer, by_name[name])):
                problems.append(f"{consumer}/package.json {field} names {name} as {spec!r}, not the vendored pack ({rel_spec(consumer, by_name[name]) if name in by_name else 'no such pack'})")
    for name, file in by_name.items():
        if package.get("overrides", {}).get(name) != rel_spec(consumer, file):
            problems.append(f"{consumer}/package.json overrides {name} as {package.get('overrides', {}).get(name)!r}, not {rel_spec(consumer, file)}")
    lock = json.loads((root / consumer / "package-lock.json").read_text(encoding="utf-8"))
    seen = set()
    for key, entry in lock.get("packages", {}).items():
        name = key.rpartition("node_modules/")[2]
        if not PI_PACKAGE.match(name):
            continue
        seen.add(name)
        file = by_name.get(name)
        resolved = entry.get("resolved", "")
        if file is None:
            problems.append(f"{consumer}/package-lock.json {key} is a Pi package the vendored set does not hold")
        elif resolved != rel_spec(consumer, file) or entry.get("integrity") != packs[file]["integrity"] or entry.get("version") != packs[file]["version"]:
            problems.append(f"{consumer}/package-lock.json {key} resolves {resolved!r} {entry.get('integrity')!r} (version {entry.get('version')}), the vendored pack is {rel_spec(consumer, file)} {packs[file]['integrity']} (version {packs[file]['version']})")
    for name in package.get("dependencies", {}):
        if PI_PACKAGE.match(name) and name not in seen:
            problems.append(f"{consumer}/package-lock.json has no entry for {name}")
    return problems


def check_copies(root: Path, manifest: dict) -> list[str]:
    problems = []
    packs = {meta["name"]: file for file, meta in manifest["packs"].items()}
    for path, name in COPIES.items():
        directory = root / path
        if name not in packs or not directory.is_dir():
            problems.append(f"{path} copies {name}, which the vendored set or the tree lacks")
            continue
        shipped = {n[len("package/"):]: data for n, (_mode, data) in members((root / PACKS / packs[name]).read_bytes()).items()
                   if n == "package/package.json" or (n.startswith("package/dist/") and n.endswith(".js") and not n.startswith("package/dist/testing/"))}
        have = {p.relative_to(directory).as_posix(): p.read_bytes() for p in directory.rglob("*") if p.is_file()}
        for rel in sorted(set(shipped) | set(have)):
            if shipped.get(rel) != have.get(rel):
                problems.append(f"{path}/{rel} differs from the vendored {name} pack")
    return problems


def check(pin: Pin, root: Path = ROOT) -> list[str]:
    """Offline part of the gate: the problems found (empty when the vendored packs are the pin's and consumed by every consumer)."""
    manifest = read_manifest(root)
    problems = []
    if manifest.get("commit") != pin.commit:
        problems.append(f"{PACKS / MANIFEST} is for Pi commit {manifest.get('commit')}, the pin is {pin.commit}: run `make vendor-pi-packs`")
    if manifest.get("package_version") != pin.package_version:
        problems.append(f"{PACKS / MANIFEST} is for package version {manifest.get('package_version')}, the pin needs {pin.package_version}")
    directory = root / PACKS
    on_disk = {p.name for p in directory.glob("*.tgz")}
    for file in sorted(on_disk - set(manifest["packs"])):
        problems.append(f"{PACKS / file} is not in the manifest")
    for file, meta in sorted(manifest["packs"].items()):
        path = directory / file
        if not path.is_file():
            problems.append(f"{PACKS / file} is missing")
        elif integrity(path.read_bytes()) != meta["integrity"]:
            problems.append(f"{PACKS / file} differs from the manifest's {meta['integrity']}")
    licence = directory / LICENSE
    if not licence.is_file() or hashlib.sha256(licence.read_bytes()).hexdigest() != manifest["license"]["sha256"]:
        problems.append(f"{PACKS / LICENSE} is missing or differs from the manifest")
    for consumer in CONSUMERS:
        problems += check_consumer(root, consumer, manifest)
    for rel, names in sorted(scan(root).items()):
        if rel not in CONSUMERS:
            problems.append(f"{rel}/package.json names Pi packages ({', '.join(names)}) but is neither a vendored consumer (automation/pin/vendor.py CONSUMERS) nor exempt (EXEMPT)")
    problems += check_copies(root, manifest)
    return problems


def check_installed(consumer: str, root: Path = ROOT) -> list[str]:
    """The problems of a consumer's node_modules as an install of the vendored packs (empty when it is one): a real directory,
    not a link to another tree; every Pi package in it recorded by npm with its pack's version and sha512; and the files of
    each equal to the pack's members."""
    manifest = read_manifest(root)
    packs = {meta["name"]: (file, meta) for file, meta in manifest["packs"].items()}
    modules = root / consumer / "node_modules"
    if modules.is_symlink():
        return [f"{consumer}/node_modules is a link to {os.readlink(modules)}, not an install of the vendored packs"]
    hidden = modules / ".package-lock.json"
    if not hidden.is_file():
        return [f"{consumer}/node_modules holds no npm install"]
    problems = []
    seen = set()
    for key, entry in sorted(json.loads(hidden.read_text(encoding="utf-8")).get("packages", {}).items()):
        name = key.rpartition("node_modules/")[2]
        if not PI_PACKAGE.match(name):
            continue
        seen.add(name)
        if name not in packs:
            problems.append(f"{consumer}/{key} is a Pi package the vendored set does not hold")
            continue
        file, meta = packs[name]
        if entry.get("integrity") != meta["integrity"] or entry.get("version") != meta["version"]:
            problems.append(f"{consumer}/{key} was installed from {entry.get('resolved')!r} {entry.get('integrity')!r}, not from {PACKS / file} {meta['integrity']}")
            continue
        directory = root / consumer / key
        shipped = {n[len("package/"):]: data for n, (_mode, data) in members((root / PACKS / file).read_bytes()).items()}
        have = {p.relative_to(directory).as_posix(): p for p in directory.rglob("*") if p.is_file() and "node_modules" not in p.relative_to(directory).parts}
        differing = [rel for rel in sorted(set(shipped) | set(have)) if rel not in have or rel not in shipped or have[rel].read_bytes() != shipped[rel]]
        if differing:
            problems.append(f"{consumer}/{key} differs from {PACKS / file}: {', '.join(differing[:10])}")
    if DURABLE not in seen:
        problems.append(f"{consumer}/node_modules holds no {DURABLE}")
    return problems


def source_of(pin: Pin) -> Path:
    import oracle

    return oracle.ensure(pin).parent / "src"


def pack_pinned(pin: Pin, destination: Path) -> dict[str, Path]:
    import oracle

    return oracle.pack(source_of(pin), destination, DURABLE)


def check_rebuild(pin: Pin, root: Path = ROOT) -> list[str]:
    """Pack the pinned build again: each vendored pack must equal it, by tar members and by bytes."""
    manifest = read_manifest(root)
    problems = []
    with tempfile.TemporaryDirectory() as scratch:
        fresh = pack_pinned(pin, Path(scratch) / "packs")
        files = {path.name: path for path in fresh.values()}
        for file in sorted(set(files) | set(manifest["packs"])):
            if file not in files:
                problems.append(f"{PACKS / file} is not a package of the pinned build's durable closure")
                continue
            if file not in manifest["packs"]:
                problems.append(f"the pinned build packs {file}, which is not vendored")
                continue
            vendored, built = (root / PACKS / file).read_bytes(), files[file].read_bytes()
            if vendored == built:
                continue
            a, b = members(vendored), members(built)
            differing = [n for n in sorted(set(a) | set(b)) if a.get(n) != b.get(n)]
            problems.append(f"{PACKS / file} differs from the pinned build's pack: " + (f"members {', '.join(differing[:10])}" if differing else "same members, other gzip bytes"))
    return problems


# --- sync -------------------------------------------------------------------------------------------------------

def licence_text(source: Path) -> bytes:
    return (source / "LICENSE").read_bytes()


def write_json(path: Path, data: dict) -> None:
    path.write_text(json.dumps(data, indent="\t", ensure_ascii=False) + "\n", encoding="utf-8")


def write_packs(pin: Pin, directory: Path) -> tuple[dict, dict[str, str]]:
    """Pack the pinned build into directory with its LICENSE and manifest.json; returns the manifest and the pack file of each package."""
    source = source_of(pin)
    directory.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as scratch:
        fresh = pack_pinned(pin, Path(scratch) / "packs")
        for old in directory.glob("*.tgz"):
            old.unlink()
        packs = {}
        for name, path in sorted(fresh.items()):
            data = path.read_bytes()
            files = members(data)
            package = json.loads(files["package/package.json"][1])
            (directory / path.name).write_bytes(data)
            packs[path.name] = {"name": name, "version": package["version"], "license": package.get("license"), "bytes": len(data), "integrity": integrity(data)}
    licence = licence_text(source)
    (directory / LICENSE).write_bytes(licence)
    manifest = {
        "commit": pin.commit,
        "version": pin.version,
        "package_version": pin.package_version,
        "license": {"file": LICENSE, "sha256": hashlib.sha256(licence).hexdigest(), "text": "Pi's LICENSE at the pinned commit"},
        "packs": packs,
    }
    write_json(directory / MANIFEST, manifest)
    return manifest, {meta["name"]: file for file, meta in packs.items()}


def sync(pin: Pin, root: Path = ROOT) -> None:
    manifest, by_name = write_packs(pin, root / PACKS)
    for consumer in CONSUMERS:
        rewrite_consumer(root, consumer, manifest, by_name)
    packs = manifest["packs"]
    print(f"vendor: {len(packs)} packs of Pi {pin.version} in {PACKS} ({sum(m['bytes'] for m in packs.values())} bytes)")


# --- the tree of a PI_UPSTREAM_REF override ---------------------------------------------------------------------

INTEROP = "durable/interop"
READY = ".ready"


def override_tree(pin: Pin, root: Path = ROOT) -> Path:
    """The tree the oracle tests of a run at another Pi commit read in place of the repository's own Pi (internal/pioracle,
    PIG_PI_OVERRIDE_TREE): `packages`, a link to the packages of the commit's source build; third_party/pi-packs, the packs of
    that build; and durable/interop, a copy of the repository's manifest and lock pointed at those packs, with its installed
    node_modules. The tree is under the commit's entry of the build store, one per source build and interop lock. It is built
    in a scratch directory and gets its name by one rename, so a build that stops part-way leaves no tree, and nothing in the
    repository is written."""
    import oracle

    source = source_of(pin)
    digest = hashlib.sha256(source.parent.name.encode())
    for name in ("package.json", "package-lock.json"):
        digest.update((root / INTEROP / name).read_bytes())
    tree = oracle.entry(pin) / f"durable-{digest.hexdigest()[:12]}"
    if (tree / READY).is_file():
        return tree
    work = Path(tempfile.mkdtemp(prefix=tree.name + ".", dir=tree.parent))
    try:
        manifest, by_name = write_packs(pin, work / PACKS)
        (work / INTEROP).mkdir(parents=True)
        for name in ("package.json", "package-lock.json"):
            shutil.copyfile(root / INTEROP / name, work / INTEROP / name)
        rewrite_consumer(work, INTEROP, manifest, by_name)
        subprocess.run([shutil.which("npm") or "npm", "ci", *NPM_FLAGS], cwd=work / INTEROP, check=True, timeout=1800)
        (work / "packages").symlink_to(source / "packages", target_is_directory=True)
        (work / READY).write_text(pin.commit + "\n", encoding="utf-8")
        work.chmod(0o755)
        try:
            work.rename(tree)
        except OSError:
            if not (tree / READY).is_file():  # only another builder of the same tree may hold the name
                raise
    finally:
        shutil.rmtree(work, ignore_errors=True)
    return tree


def rewrite_consumer(root: Path, consumer: str, manifest: dict, by_name: dict[str, str]) -> None:
    directory = root / consumer
    package = json.loads((directory / "package.json").read_text(encoding="utf-8"))
    for field in DEP_FIELDS[:-1]:
        for name in list(package.get(field, {})):
            if PI_PACKAGE.match(name):
                if name not in by_name:
                    raise PinError(f"{consumer}/package.json names {name}, which the pinned durable closure does not contain")
                package[field][name] = rel_spec(consumer, by_name[name])
    package["overrides"] = {**{k: v for k, v in package.get("overrides", {}).items() if not PI_PACKAGE.match(k)}, **{name: rel_spec(consumer, file) for name, file in sorted(by_name.items())}}
    write_json(directory / "package.json", package)
    lock_path = directory / "package-lock.json"
    lock = json.loads(lock_path.read_text(encoding="utf-8"))
    for key in [k for k in lock["packages"] if PI_PACKAGE.match(k.rpartition("node_modules/")[2])]:
        del lock["packages"][key]
    write_json(lock_path, lock)
    exe = shutil.which("npm") or "npm"
    subprocess.run([exe, "install", "--package-lock-only", *NPM_FLAGS], cwd=directory, check=True, timeout=900)
    lock = json.loads(lock_path.read_text(encoding="utf-8"))
    for key, entry in lock["packages"].items():
        name = key.rpartition("node_modules/")[2]
        if PI_PACKAGE.match(name):
            meta = manifest["packs"][by_name[name]]
            entry["version"], entry["resolved"], entry["integrity"] = meta["version"], rel_spec(consumer, by_name[name]), meta["integrity"]
    write_json(lock_path, lock)
