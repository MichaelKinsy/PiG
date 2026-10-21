# SPDX-License-Identifier: MIT
"""Build the Pi oracle for a pin from the pinned source, in the shape of a published install.

Parity tests and the vendored Pi runtime read `extensions/sdk-ts/node_modules`:
`@earendil-works/pi-coding-agent` with its dependencies nested under its own
node_modules, plus `.bin/pi` and `tsc`. A release tag installs that from npm
(automation/ci/npm-locked.py). A commit has no published package, so this module
builds Pi from the pin's tarball, packs the workspace packages `pi-coding-agent`
depends on, and installs a frozen dependency closure with those packs in place of
the registry packages. The closure is a package.json/package-lock.json pair under
automation/pin/frozen: for a release it is the lock of the published install
(`pin.py freeze-closure --from-published-lock`), so the third-party tree is the one
`npm install @earendil-works/pi-coding-agent@<version>` gave; for a commit it is
derived once from the packs with `npm install --before=<commit date>` and
frozen, so it is the tree a published install of that commit would have had.
Either way each workspace pack is pinned by its own integrity: a source pack never
has the published tarball's bytes (build output and file modes differ).
`npm ci` of that pair lays the tree out as `install-strategy=shallow` does, and
`check-oracle` compares the installed tree with the pair. The result is cached per
commit and frozen inputs in a build directory that is never changed once published
(see `ensure`), and `extensions/sdk-ts/node_modules` links to it.
"""
from __future__ import annotations

import base64
import contextlib
import glob
import gzip
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
from pathlib import Path

try:
    import fcntl
except ImportError:  # Windows
    fcntl = None
    import msvcrt

import pinlib
from pinlib import ROOT, Pin, PinError

SDK_TS = ROOT / "extensions" / "sdk-ts"
STAMP = ".pig-pi-oracle.json"
NPM_FLAGS = ["--ignore-scripts", "--no-audit", "--no-fund"]


def store() -> Path:
    if os.environ.get("PIG_PI_BUILD_STORE"):
        return Path(os.environ["PIG_PI_BUILD_STORE"])
    base = os.environ.get("PIG_DEV_HOME")
    if not base:
        cache = os.environ.get("XDG_CACHE_HOME") or str(Path.home() / ".cache")
        base = str(Path(cache) / "pig-dev")
    return Path(base) / "pi-builds"


def entry(pin: Pin) -> Path:
    return store() / pin.commit


# An entry is shared by every worktree and merge gate of the pin's commit, which may hold different frozen inputs:
#   build-<random>/        one build: src/, node_modules/ and the stamp. Once published it is never changed or removed,
#                          so a reader of its node_modules (a gate running Pi) is never disturbed by a later build.
#   oracle-<identity>.ref  the name of the published build of an identity (provider data and closure digests),
#   current.ref            the name of the build published last; both are replaced atomically.
#   .lock-<identity>       serializes the builders of one identity.


def identity(data_sha: str, closure_sha: str) -> str:
    return hashlib.sha256(f"{data_sha}\n{closure_sha}".encode()).hexdigest()[:12]


def published(pin: Pin, name: str) -> Path | None:
    """The build a ref of the entry names, or None."""
    try:
        build = (entry(pin) / f"{name}.ref").read_text().strip()
    except FileNotFoundError:
        return None
    return entry(pin) / build


@contextlib.contextmanager
def exclusive(path: Path):
    """Hold an exclusive lock on `path` (created if missing) for the block, waiting for any other holder."""
    with open(path, "a+b") as handle:
        if fcntl is not None:
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX)  # released when the handle closes
            yield
            return
        handle.seek(0)
        while True:
            try:
                msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
                break
            except OSError:
                time.sleep(0.5)
        try:
            yield
        finally:
            handle.seek(0)
            msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)



def npm(*args: str, cwd: Path, timeout: int = 1800) -> None:
    exe = shutil.which("npm") or "npm"
    print("+ npm " + " ".join(args), file=sys.stderr, flush=True)
    subprocess.run([exe, *args], cwd=cwd, check=True, timeout=timeout)


def extract_source(pin: Pin, destination: Path) -> None:
    path = pinlib.tarball(pin.commit, False)  # by commit for a tag too: the tarball root names the commit it holds
    commit, _ = pinlib.tarball_identity(path)
    if commit != pin.commit:
        raise PinError(f"{path} holds {commit}, not {pin.commit}")
    if destination.exists():
        shutil.rmtree(destination)
    destination.mkdir(parents=True)
    with tarfile.open(path) as archive:
        members = []
        for member in archive.getmembers():
            parts = member.name.split("/", 1)
            if len(parts) < 2 or not parts[1]:
                continue
            member.name = parts[1]
            members.append(member)
        archive.extractall(destination, members=members, filter="data")


# Pi regenerates packages/ai/src/providers/data/*.json from live models.dev when it builds, so two builds of one commit differ
# (the catalog and every provider list change with the day). Every pin therefore freezes that directory: the committed
# snapshot below is unpacked in place of the hydrate step, and its digest is the oracle's identity. A release tag's snapshot
# is the data its published package shipped (`pin.py freeze-oracle --from <dist/providers/data>`), a commit's is its first build's, which `build_source` freezes itself.
DATA_DIR = Path("packages/ai/src/providers/data")
FROZEN = ROOT / "automation" / "pin" / "frozen"


def frozen_path(pin: Pin) -> Path:
    return FROZEN / f"{pin.commit}.provider-data.tgz"


def data_digest(directory: Path) -> str:
    """Digest of the names and bytes of every file in the data directory, independent of archive framing."""
    digest = hashlib.sha256()
    for path in sorted(p for p in directory.iterdir() if p.is_file()):
        digest.update(path.name.encode() + b"\0" + hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def write_replacing(target: Path, data: bytes) -> None:
    """Write `target` whole or not at all: a reader never sees a partial file."""
    target.parent.mkdir(parents=True, exist_ok=True)
    partial = target.parent / f"{target.name}.{os.getpid()}-{threading.get_ident()}.partial"
    partial.write_bytes(data)
    os.replace(partial, target)


def write_snapshot(directory: Path, target: Path) -> None:
    """A reproducible archive: sorted entries, no owners, no timestamps. The digest marker is written before the archive,
    because the archive's presence is what every later build trusts."""
    digest = data_digest(directory)
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.PAX_FORMAT) as archive:
        for path in sorted(p for p in directory.iterdir() if p.is_file()):
            info = tarfile.TarInfo(path.name)
            info.size = path.stat().st_size
            info.mode = 0o644
            info.mtime = 0
            with path.open("rb") as handle:
                archive.addfile(info, handle)
    zipped = io.BytesIO()
    with gzip.GzipFile(fileobj=zipped, mode="wb", mtime=0, filename="") as handle:
        handle.write(raw.getvalue())
    write_replacing(target.parent / (target.name + ".sha256"), (digest + "\n").encode())
    write_replacing(target, zipped.getvalue())


def frozen_digest(pin: Pin) -> str | None:
    marker = frozen_path(pin).parent / (frozen_path(pin).name + ".sha256")
    return marker.read_text().strip() if marker.exists() else None


def unpack_snapshot(pin: Pin, source: Path) -> str:
    target = source / DATA_DIR
    target.mkdir(parents=True, exist_ok=True)
    for old in target.iterdir():
        old.unlink()
    with tarfile.open(frozen_path(pin)) as archive:
        archive.extractall(target, filter="data")
    got = data_digest(target)
    if got != frozen_digest(pin):
        raise PinError(f"{frozen_path(pin)} unpacks to digest {got}, its recorded digest is {frozen_digest(pin)}")
    return got


def build_source(pin: Pin, source: Path) -> str:
    """Build the pinned source and return the digest of the model data it was built with."""
    root = json.loads((source / "package.json").read_text())
    scripts = root.get("scripts", {})
    npm("ci", *NPM_FLAGS, cwd=source)
    if "hydrate:model-data" in scripts and "build:offline" in scripts:
        if frozen_path(pin).exists():
            digest = unpack_snapshot(pin, source)
        elif pin.kind == "tag":
            raise PinError(
                f"no frozen model data for release {pin.tag} ({frozen_path(pin).name}): a source build would hydrate live models.dev data and differ from the release. "
                "Freeze the data its published package shipped: `PIG_PARITY_PI_PUBLISHED=1 make parity-deps`, then "
                "`python3 automation/pin/pin.py freeze-oracle --from extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/providers/data`"
            )
        else:
            print(f"oracle: no frozen model data for {pin.commit}; hydrating from models.dev and freezing that first build's data in {frozen_path(pin)} (commit it with the pin)", file=sys.stderr)
            npm("run", "hydrate:model-data", cwd=source)
            write_snapshot(source / DATA_DIR, frozen_path(pin))
            digest = data_digest(source / DATA_DIR)
        npm("run", "build:offline", cwd=source)
        return digest
    npm("run", "build", cwd=source)
    return ""


def freeze(pin: Pin, data: Path | None = None) -> Path:
    """Snapshot model data into automation/pin/frozen: `data`, or the data of an existing build of the pinned commit."""
    if data is None:
        build = published(pin, "current")
        data = (build if build is not None else entry(pin)) / "src" / DATA_DIR
    if not data.is_dir():
        raise PinError(f"no model data at {data}; pass --from <dir> or run `pin.py install-oracle` first")
    if frozen_path(pin).exists():
        raise PinError(f"{frozen_path(pin)} already exists; a frozen snapshot is never rewritten")
    write_snapshot(data, frozen_path(pin))
    return frozen_path(pin)


def workspace_names(source: Path) -> dict[str, Path]:
    names = {}
    for manifest in sorted((source / "packages").glob("*/package.json")):
        names[json.loads(manifest.read_text())["name"]] = manifest.parent
    return names


def closure_workspaces(source: Path, root: str | None = None) -> list[str]:
    """The workspace packages `root` (default `@earendil-works/pi-coding-agent`) needs: itself and its transitive workspace dependencies."""
    workspaces = workspace_names(source)
    root = root or AGENT
    if root not in workspaces:
        raise PinError(f"the pinned source has no workspace package {root}")
    seen: list[str] = []
    todo = [root]
    while todo:
        name = todo.pop()
        if name in seen:
            continue
        seen.append(name)
        manifest = json.loads((workspaces[name] / "package.json").read_text())
        for field in ("dependencies", "optionalDependencies"):
            todo.extend(dep for dep in manifest.get(field, {}) if dep in workspaces)
    return sorted(seen)


def pack(source: Path, destination: Path, root: str | None = None) -> dict[str, Path]:
    """Pack each workspace package the oracle installs (those `root` needs).

    npm pack records each file's mode, and Windows has no execute bit: the build's chmod +x (coding-agent's dist/cli.js and
    bundle launchers) made the packs, and so the frozen integrity, differ by OS. Each package is packed from a copy whose
    files are all 0644, so every OS packs the same bytes; npm marks bin targets executable when it installs them."""
    workspaces = workspace_names(source)
    shutil.rmtree(destination, ignore_errors=True)
    destination.mkdir(parents=True)
    packed: dict[str, Path] = {}
    for name in closure_workspaces(source, root):
        before = set(destination.glob("*.tgz"))
        with tempfile.TemporaryDirectory() as staging:
            copy = Path(staging) / "package"
            shutil.copytree(workspaces[name], copy, symlinks=True, ignore=shutil.ignore_patterns("node_modules"))
            for path in copy.rglob("*"):
                if path.is_file() and not path.is_symlink():
                    path.chmod(0o644)
            npm("pack", *NPM_FLAGS, "--pack-destination", str(destination), cwd=copy)
        (new,) = set(destination.glob("*.tgz")) - before
        packed[name] = new
    return packed


# The dependency closure ---------------------------------------------------------

AGENT = "@earendil-works/pi-coding-agent"
DURABLE = "@earendil-works/pi-durable"
# The packages durable/interop imports directly (its package.json dependencies): they sit at the top of the durable oracle tree.
INTEROP = ["@earendil-works/chord", "@earendil-works/pi-ai", DURABLE, "@earendil-works/pi-telemetry"]
LOCK_KEY = "node_modules/" + AGENT
PACK_PREFIX = "pig-pack:"


def frozen_lock_path(pin: Pin, root: str | None = None) -> Path:
    return FROZEN / f"{pin.commit}.{'durable-' if root == DURABLE else ''}closure.json.gz"


def published_closure(lock: dict, version: str) -> dict:
    """The closure of a published install, from the lock of an npm install of the package: the package's own subtree
    (the install's third-party tree) and nothing of the project that installed it."""
    packages = {"": {"name": "pi-oracle", "version": "0.0.0", "devDependencies": {AGENT: version}}}
    for key, item in lock["packages"].items():
        if key == LOCK_KEY or key.startswith(LOCK_KEY + "/"):
            packages[key] = dict(item)
    if LOCK_KEY not in packages:
        raise PinError(f"the lock has no {AGENT}; it is not the lock of an install of the package")
    return {"name": "pi-oracle", "version": "0.0.0", "lockfileVersion": lock["lockfileVersion"], "requires": True, "packages": packages}


def workspace_key_name(key: str, workspaces) -> str | None:
    """The workspace package a lock key installs (`.../node_modules/@earendil-works/pi-ai`), or None."""
    tail = key.rsplit("node_modules/", 1)[-1]
    return tail if "node_modules/" in key and tail in workspaces else None


def tarball_integrity(path: Path) -> str:
    return "sha512-" + base64.b64encode(hashlib.sha512(path.read_bytes()).digest()).decode()


def abstract_packs(lock: dict, workspaces, integrity: dict[str, str]) -> dict:
    """The lock with each workspace package a registry entry resolved replaced by a pack token, so the frozen pair does not
    depend on where the packs live. Each entry gets its pack's integrity, so `npm ci` rejects a pack that is not the bytes
    the closure was frozen with. A release's registry integrity is not kept: a source pack of the tag never reproduces the
    published tarball (build output and the portable file modes of `pack` differ), so `npm ci` would refuse every build."""
    out = json.loads(json.dumps(lock))
    for key, item in out["packages"].items():
        name = workspace_key_name(key, workspaces)
        if name:
            item["resolved"] = PACK_PREFIX + name
            item["integrity"] = integrity[name]
    return out


def concrete_packs(lock: dict, packed: dict[str, Path]) -> dict:
    out = json.loads(json.dumps(lock))
    for key, item in out["packages"].items():
        resolved = item.get("resolved", "")
        if resolved.startswith(PACK_PREFIX):
            name = resolved[len(PACK_PREFIX):]
            if name not in packed:
                raise PinError(f"the frozen closure installs {name}, which the pinned source does not pack")
            item["resolved"] = "file:" + str(packed[name])
    return out


def closure_digest(lock: dict) -> str:
    return hashlib.sha256(json.dumps(lock, sort_keys=True).encode()).hexdigest()


def frozen_closure_digest(pin: Pin) -> str | None:
    lock = read_frozen_lock(pin)
    return closure_digest(lock) if lock is not None else None


def read_frozen_lock(pin: Pin, root: str | None = None) -> dict | None:
    path = frozen_lock_path(pin, root)
    if not path.exists():
        return None
    return json.loads(gzip.decompress(path.read_bytes()))


def write_frozen_lock(pin: Pin, lock: dict, root: str | None = None) -> Path:
    path = frozen_lock_path(pin, root)
    if path.exists():
        raise PinError(f"{path} already exists; a frozen closure is never rewritten")
    path.parent.mkdir(parents=True, exist_ok=True)
    buffer = io.BytesIO()
    with gzip.GzipFile(fileobj=buffer, mode="wb", mtime=0) as handle:
        handle.write((json.dumps(lock, indent=1, sort_keys=True) + "\n").encode())
    write_replacing(path, buffer.getvalue())
    return path


def commit_date(pin: Pin) -> str:
    """The commit's date (ISO 8601 UTC), the modification time `git archive` gives every file of a commit tarball; npm's --before."""
    import datetime

    with tarfile.open(pinlib.tarball(pin.commit, False)) as archive:
        member = next((m for m in archive if m.isreg()), None)
    if member is None:
        raise PinError("cannot read the commit date from the source tarball")
    return datetime.datetime.fromtimestamp(member.mtime, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def derive_closure(pin: Pin, source: Path, packed: dict[str, Path], work: Path, root: str | None = None) -> dict:
    """Resolve the closure of a published install of the pinned packages: `npm install` of the packed package with the other
    workspace packages overridden to their packs and the registry as of the commit date."""
    if work.exists():
        shutil.rmtree(work)
    work.mkdir(parents=True)
    root = root or AGENT
    roots = [root] + sorted(name for name in INTEROP if name != root) if root == DURABLE else [root]
    overrides = {name: "file:" + str(tgz) for name, tgz in packed.items() if name not in roots}
    # The third-party packages the pinned pi-durable imports (typebox, ...) also sit at the top, where Pi's source resolves them for the Go tests that import it.
    third_party = {}
    if root == DURABLE:
        workspaces = workspace_names(source)
        third_party = {dep: version for dep, version in json.loads((workspaces[DURABLE] / "package.json").read_text()).get("dependencies", {}).items() if dep not in workspaces}
    manifest = {"name": "pi-oracle", "version": "0.0.0", "private": True, "devDependencies": {**{name: "file:" + str(packed[name]) for name in roots}, **third_party}, "overrides": overrides}
    (work / "package.json").write_text(json.dumps(manifest, indent=1))
    npm("install", *NPM_FLAGS, "--package-lock-only", "--install-strategy=shallow", "--before", commit_date(pin), cwd=work)
    lock = json.loads((work / "package-lock.json").read_text())
    lock["packages"][""]["devDependencies"] = {**{name: pin.package_version for name in roots}, **third_party}
    lock["packages"][""].pop("overrides", None)
    return abstract_packs(lock, workspace_names(source), {name: tarball_integrity(tgz) for name, tgz in packed.items()})


def closure_for(pin: Pin, source: Path, packed: dict[str, Path], work: Path) -> dict:
    """The frozen closure of the pin; a commit without one is derived and frozen, a release without one is an error."""
    frozen = read_frozen_lock(pin)
    if frozen is not None:
        return frozen
    if pin.kind == "tag":
        raise PinError(
            f"no frozen dependency closure for release {pin.tag} ({frozen_lock_path(pin).name}): freeze the lock of the published install, "
            "`python3 automation/pin/pin.py freeze-closure --from-published-lock extensions/sdk-ts/package-lock.json`"
        )
    print(f"oracle: no frozen dependency closure for {pin.commit}; deriving it with npm and freezing it in {frozen_lock_path(pin)} (commit it with the pin)", file=sys.stderr)
    lock = derive_closure(pin, source, packed, work)
    write_frozen_lock(pin, lock)
    return lock


def tree_packages(modules: Path) -> dict[str, str]:
    """name@version of every installed package under modules, keyed like the lock: `node_modules/<name>` and, for a package
    installed inside another, `node_modules/<outer>/node_modules/<name>`."""
    found: dict[str, str] = {}

    def walk(directory: Path, prefix: str) -> None:
        for entry in sorted(directory.iterdir()):
            if entry.name == ".bin" or entry.name.startswith(".") or not entry.is_dir():
                continue
            names = [entry] if not entry.name.startswith("@") else sorted(p for p in entry.iterdir() if p.is_dir())
            for package in names:
                manifest = package / "package.json"
                if not manifest.is_file():
                    continue
                data = json.loads(manifest.read_text(encoding="utf-8"))
                key = prefix + package.relative_to(directory).as_posix()
                found[key] = f"{data['name']}@{data['version']}"
                if (package / "node_modules").is_dir():
                    walk(package / "node_modules", key + "/node_modules/")

    walk(modules, "node_modules/")
    return found


def lock_packages(lock: dict) -> dict[str, tuple[str, bool]]:
    """key -> (version, installed on every platform). An entry restricted by os/cpu/libc is installed only where it matches."""
    out = {}
    for key, item in lock["packages"].items():
        if key and "version" in item and not item.get("link"):
            out[key] = (item["version"], not any(field in item for field in ("os", "cpu", "libc")))
    return out


def tree_differences(modules: Path, lock: dict) -> list[str]:
    """Differences between the installed tree and the closure (empty when it is exactly the lock's tree, apart from typescript)."""
    installed = tree_packages(modules)
    expected = lock_packages(lock)
    problems = []
    for key, (version, everywhere) in sorted(expected.items()):
        got = installed.get(key)
        if got is None:
            if not everywhere:
                continue
            problems.append(f"{key} {version} is in the closure but not installed")
        elif not got.endswith("@" + version):
            problems.append(f"{key}: installed {got}, closure {version}")
    for key in sorted(set(installed) - set(expected)):
        if key == "node_modules/typescript":
            continue
        problems.append(f"{key} {installed[key]} is installed but not in the closure")
    return problems


def install_closure(closure: dict, packed: dict[str, Path], flat: Path) -> None:
    if flat.exists():
        shutil.rmtree(flat)
    flat.mkdir(parents=True)
    lock = concrete_packs(closure, packed)
    manifest = {"name": lock["name"], "version": lock["version"], "private": True, "devDependencies": lock["packages"][""]["devDependencies"]}
    (flat / "package.json").write_text(json.dumps(manifest, indent=1))
    (flat / "package-lock.json").write_text(json.dumps(lock, indent=1))
    npm("ci", *NPM_FLAGS, cwd=flat)


def lay_out(flat: Path, destination: Path, typescript: str) -> None:
    """Move the installed tree to `destination`, the layout of extensions/sdk-ts/node_modules, and add .bin and TypeScript."""
    if destination.exists():
        shutil.rmtree(destination)
    shutil.move(str(flat / "node_modules"), str(destination))
    for name in (".package-lock.json",):
        (destination / name).unlink(missing_ok=True)
    bin_dir = destination / ".bin"
    shutil.rmtree(bin_dir, ignore_errors=True)
    bin_dir.mkdir()
    agent = destination / "@earendil-works" / "pi-coding-agent"
    manifest = json.loads((agent / "package.json").read_text())
    binary = manifest["bin"]["pi"] if isinstance(manifest["bin"], dict) else manifest["bin"]
    (bin_dir / "pi").symlink_to(os.path.relpath(agent / binary, bin_dir))
    (agent / binary).chmod(0o755)
    if os.name == "nt":
        # Windows cannot execute the script a symlink names; npm's own install writes a .cmd shim beside it.
        target = os.path.relpath(agent / binary, bin_dir).replace("/", "\\")
        (bin_dir / "pi.cmd").write_text(f'@node "%~dp0\\{target}" %*\r\n', newline="")
    with_typescript(flat, destination, typescript)


def with_typescript(flat: Path, destination: Path, version: str) -> None:
    holder = flat / "ts"
    holder.mkdir()
    (holder / "package.json").write_text(json.dumps({"name": "ts", "version": "0.0.0", "private": True, "dependencies": {"typescript": version}}))
    npm("install", *NPM_FLAGS, cwd=holder)
    shutil.move(str(holder / "node_modules" / "typescript"), str(destination / "typescript"))
    (destination / ".bin" / "tsc").symlink_to("../typescript/bin/tsc")
    (destination / ".bin" / "tsserver").symlink_to("../typescript/bin/tsserver")


def typescript_version() -> str:
    return json.loads((SDK_TS / "package.json").read_text())["devDependencies"]["typescript"]


def reusable(pin: Pin, build: Path | None) -> bool:
    """Whether `build` is a stamped source build of the pin made with the frozen inputs of this tree (any, where none are frozen)."""
    if build is None or not (build / STAMP).is_file() or not (build / "node_modules").is_dir():
        return False
    data = json.loads((build / STAMP).read_text())
    return data.get("commit") == pin.commit and data.get("package_version") == pin.package_version and data.get("source") == "build" and data.get("provider_data_sha256", "") == (frozen_digest(pin) or data.get("provider_data_sha256", "")) and data.get("closure_sha256", "") == (frozen_closure_digest(pin) or data.get("closure_sha256", ""))


def ensure(pin: Pin, *, force: bool = False) -> Path:
    """Build (once per commit and frozen inputs) and return the oracle's node_modules directory. A build goes into a fresh
    directory and is published by an atomic ref write after its stamp, so a published oracle is never rewritten or removed:
    a forced rebuild publishes another directory, and a build that fails part-way publishes nothing."""
    home = entry(pin)
    key = identity(frozen_digest(pin) or "", frozen_closure_digest(pin) or "")
    candidates = (f"oracle-{key}", "current")
    if not force:
        for name in candidates:
            if reusable(pin, build := published(pin, name)):
                return build / "node_modules"
    home.mkdir(parents=True, exist_ok=True)
    with exclusive(home / f".lock-{key}"):
        if not force:  # another builder of the identity may have published while this one waited
            for name in candidates:
                if reusable(pin, build := published(pin, name)):
                    return build / "node_modules"
        build = Path(tempfile.mkdtemp(prefix="build-", dir=home))
        try:
            build.chmod(0o755)
            name = build_into(pin, build)
        except BaseException:
            shutil.rmtree(build, ignore_errors=True)
            raise
        for ref in (name, "current"):
            write_replacing(home / f"{ref}.ref", (build.name + "\n").encode())
        return build / "node_modules"


def build_into(pin: Pin, build: Path) -> str:
    """Build and stamp the oracle in the fresh directory `build`; return the name of its identity's ref."""
    source = build / "src"
    extract_source(pin, source)
    data_sha = build_source(pin, source)
    packed = pack(source, build / "packs")
    closure = closure_for(pin, source, packed, build / "closure")
    flat = build / "flat"
    install_closure(closure, packed, flat)
    modules = build / "node_modules"
    lay_out(flat, modules, typescript_version())
    problems = tree_differences(modules, closure)
    if problems:
        raise PinError("the installed tree differs from the frozen closure:\n  " + "\n  ".join(problems[:20]))
    for scratch in ("flat", "packs", "closure"):
        shutil.rmtree(build / scratch, ignore_errors=True)
    closure_sha = closure_digest(closure)
    write_replacing(build / STAMP, (json.dumps({"commit": pin.commit, "package_version": pin.package_version, "version": pin.version, "source": "build", "provider_data_sha256": data_sha, "closure_sha256": closure_sha}) + "\n").encode())
    return f"oracle-{identity(data_sha, closure_sha)}"


def durable_modules(pin: Pin) -> Path:
    return entry(pin) / "durable-oracle" / "node_modules"


def ensure_durable(pin: Pin, *, force: bool = False) -> Path:
    """Pack `@earendil-works/pi-durable` and its workspace dependencies from the built pinned source and install them with the
    frozen durable closure (derived with npm when the commit has none); return the node_modules directory. A release pin
    installs the published package instead (automation/ci/npm-locked.py durable/interop)."""
    if pin.kind == "tag":
        raise PinError(f"{pin.ref} is a release: durable/interop installs the published {DURABLE} (make durable-interop-deps)")
    home = entry(pin)
    modules = durable_modules(pin)
    stamp = home / "durable-oracle" / STAMP
    frozen = read_frozen_lock(pin, DURABLE)
    if not force and frozen is not None and stamp.exists() and modules.exists():
        data = json.loads(stamp.read_text())
        if data.get("commit") == pin.commit and data.get("closure") == closure_digest(frozen):
            return modules
    build = published(pin, "current")  # ensure() builds into build-<random>/src and publishes it, as freeze_closure resolves it
    source = (build if build is not None else home) / "src"
    if not (source / "packages" / "durable" / "dist").is_dir():
        raise PinError(f"the pinned source at {source} is not built: run `python3 automation/pin/pin.py install-oracle` first")
    stamp.unlink(missing_ok=True)
    shutil.rmtree(home / "durable-packs", ignore_errors=True)
    packed = pack(source, home / "durable-packs", DURABLE)
    closure = frozen
    if closure is None:
        print(f"oracle: no frozen durable closure for {pin.commit}; deriving it with npm (run `pin.py freeze-closure` and commit it)", file=sys.stderr)
        closure = derive_closure(pin, source, packed, home / "durable-closure", DURABLE)
    flat = home / "durable-flat"
    install_closure(closure, packed, flat)
    if modules.exists():
        shutil.rmtree(modules)
    modules.parent.mkdir(parents=True, exist_ok=True)
    shutil.move(str(flat / "node_modules"), str(modules))
    (modules / ".package-lock.json").unlink(missing_ok=True)
    problems = tree_differences(modules, closure)
    if problems:
        raise PinError("the installed durable tree differs from the frozen closure:\n  " + "\n  ".join(problems[:20]))
    shutil.rmtree(flat)
    stamp.write_text(json.dumps({"commit": pin.commit, "package_version": pin.package_version, "version": pin.version, "closure": closure_digest(closure)}))
    return modules


def check_durable(pin: Pin) -> None:
    """durable/interop runs the build of the pin's pi-durable: its link, stamp and tree are the frozen durable closure's. A stale link would
    run the published release and pass silently."""
    link = ROOT / "durable" / "interop" / "node_modules"
    modules = durable_modules(pin)
    stamp = modules.parent / STAMP
    if not link.is_symlink() or Path(os.path.realpath(link)) != modules.resolve():
        raise PinError(f"durable/interop/node_modules is not the durable oracle of commit {pin.commit} ({modules}); run `make durable-interop-deps`")
    data = json.loads(stamp.read_text()) if stamp.exists() else None
    frozen = read_frozen_lock(pin, DURABLE)
    if data is None or frozen is None or data.get("commit") != pin.commit or data.get("closure") != closure_digest(frozen):
        raise PinError(f"the durable oracle at {modules} is not the build of commit {pin.commit} with the frozen durable closure; run `python3 automation/pin/pin.py install-oracle`")
    problems = tree_differences(modules, frozen)
    if problems:
        raise PinError("the installed durable tree differs from the frozen durable closure:\n  " + "\n  ".join(problems[:20]))
    version = json.loads((modules / "@earendil-works" / "pi-durable" / "package.json").read_text())["version"]
    if version != pin.package_version:
        raise PinError(f"the installed pi-durable is {version}, the pin needs {pin.package_version}")


def link_interop(modules: Path) -> None:
    """Point durable/interop/node_modules at `modules` (a derived, untracked path)."""
    link = ROOT / "durable" / "interop" / "node_modules"
    if link.is_symlink() or link.is_file():
        if link.is_symlink() and Path(os.path.realpath(link)) == modules.resolve():
            return
        link.unlink()
    elif link.exists():
        shutil.rmtree(link)
    link.symlink_to(modules)


def top_level_packages(modules: Path) -> list[str]:
    """The package names at the top of an npm tree (`@scope/name` for a scoped one), without npm's dot entries."""
    names = []
    for entry in sorted(modules.iterdir()):
        if entry.name.startswith("."):
            continue
        if entry.name.startswith("@"):
            names.extend(f"{entry.name}/{child.name}" for child in sorted(entry.iterdir()))
        else:
            names.append(entry.name)
    return names


def own_directory(path: Path) -> None:
    """Make `path` a real directory whose entries are links to what it linked to: a locked tree installed through a shared
    store is a link to a directory other checkouts read, and editing an entry through it would change theirs."""
    if not path.is_symlink():
        return
    target = Path(os.path.realpath(path))
    path.unlink()
    path.mkdir()
    for entry in sorted(target.iterdir()):
        (path / entry.name).symlink_to(entry)


def replace_with_link(path: Path, target: Path) -> None:
    if path.is_symlink() or path.is_file():
        path.unlink()
    elif path.exists():
        shutil.rmtree(path)
    path.symlink_to(target)


def link_host(pin: Pin, host: Path) -> list[str]:
    """Make the packages the Durable Cloudflare host (`host`, a directory with its own locked npm tree) imports from Pi the pin's.

    The host's lock names the published release of the packages, so its tests would type-check and run against whatever npm
    holds for that version, not against the Pi the repository pins: a commit pin is newer than the release its package.json
    names (the facade ports Pi's `checkpointWhen`, which the published 1.3.0 predates). For a commit pin, every top-level package
    of the pinned durable oracle (`ensure_durable`, the tree durable/interop runs) replaces the same-named entry of the host's
    tree with a link to the oracle's, so the host and Pi's own packages share one copy of each (one `typebox`, one `chord`); every
    other package stays the host's locked one. A release pin needs no replacement: the lock is the published install of the pin,
    and only its version is checked. Returns the linked package names."""
    modules = host / "node_modules"
    if pin.kind == "tag":
        version = json.loads((modules / DURABLE / "package.json").read_text())["version"]
        if version != pin.package_version:
            raise PinError(f"{host} installs {DURABLE} {version}, the release pin needs {pin.package_version}; run `make pin-sync`")
        return []
    check_durable(pin)
    oracle = durable_modules(pin)
    own_directory(modules)
    linked = top_level_packages(oracle)
    for name in linked:
        scope, _, _ = name.partition("/")
        if scope.startswith("@"):
            (modules / scope).mkdir(exist_ok=True)
            own_directory(modules / scope)
        replace_with_link(modules / name, oracle / name)
    check_host(pin, host)
    return linked


def check_host(pin: Pin, host: Path) -> None:
    """The host's tree imports the pin's Pi packages: for a commit pin each package of the pinned durable oracle resolves to the
    oracle's copy. A stale tree would type-check against the published release and pass or fail for the wrong reason."""
    modules = host / "node_modules"
    if pin.kind == "tag":
        version = json.loads((modules / DURABLE / "package.json").read_text())["version"]
        if version != pin.package_version:
            raise PinError(f"{host} installs {DURABLE} {version}, the release pin needs {pin.package_version}; run `make pin-sync`")
        return
    oracle = durable_modules(pin)
    stale = [name for name in top_level_packages(oracle) if not (modules / name).exists() or Path(os.path.realpath(modules / name)) != (oracle / name).resolve()]
    if stale:
        raise PinError(f"{host}/node_modules does not use the pinned build of commit {pin.commit} for {', '.join(stale)}; run `make durable-core-host-deps`")
    version = json.loads((modules / DURABLE / "package.json").read_text())["version"]
    if version != pin.package_version:
        raise PinError(f"the host's {DURABLE} is {version}, the pin needs {pin.package_version}")


def pi_version(modules: Path) -> str:
    """`pi --version` of the installed oracle, run through node from the package's own bin entry, so it works for the built
    layout and npm's, on every platform (Windows cannot execute the script that .bin/pi names)."""
    env = {k: v for k, v in os.environ.items() if k.lower() not in {"https_proxy", "http_proxy", "all_proxy"}}
    agent = modules / "@earendil-works" / "pi-coding-agent"
    manifest = json.loads((agent / "package.json").read_text(encoding="utf-8"))
    binary = manifest["bin"]["pi"] if isinstance(manifest["bin"], dict) else manifest["bin"]
    node = shutil.which("node") or "node"
    return subprocess.run([node, str(agent / binary), "--version"], check=True, capture_output=True, text=True, env=env, timeout=120).stdout.strip()


def link_sdk(modules: Path) -> None:
    """Point extensions/sdk-ts/node_modules at `modules` (a derived, untracked path)."""
    link = SDK_TS / "node_modules"
    if link.is_symlink() or link.is_file():
        if link.is_symlink() and Path(os.path.realpath(link)) == modules.resolve():
            return
        link.unlink()
    elif link.exists():
        shutil.rmtree(link)
    link.symlink_to(modules)


def source_modules(pin: Pin) -> Path | None:
    """The node_modules of the pinned source build the oracle links (`npm ci` of the pinned monorepo: its third-party
    dependencies, such as typebox, and its workspace packages), or None when the installed oracle is not a source build of
    the pin (the published opt-in)."""
    build = Path(os.path.realpath(SDK_TS / "node_modules")).parent
    stamp = build / STAMP
    if not stamp.is_file():
        return None
    data = json.loads(stamp.read_text())
    modules = build / "src" / "node_modules"
    if data.get("commit") != pin.commit or data.get("source") != "build" or not modules.is_dir():
        return None
    return modules


def workspace_dirs(source: Path) -> dict[str, Path]:
    """Every workspace package of the pinned monorepo (the globs of its root package.json `workspaces`) by name."""
    names = {}
    for pattern in json.loads((source / "package.json").read_text()).get("workspaces", []):
        for manifest in sorted(source.glob(f"{pattern}/package.json")):
            names[json.loads(manifest.read_text())["name"]] = manifest.parent
    return names


def link_directory(link: Path, target: Path) -> None:
    if os.name == "nt":
        import _winapi  # a junction needs no symlink privilege and is what npm itself links workspaces with on Windows

        _winapi.CreateJunction(str(target.resolve()), str(link))
    else:
        link.symlink_to(os.path.relpath(target, link.parent), target_is_directory=True)


def workspace_link_state(link: Path, directory: Path) -> tuple[str, str]:
    """How `link` stands against the workspace package `directory` it must be a link to: (state, what the link names).
    States: `missing`; `linked` (a symlink or junction whose real path is `directory`); `stale` (a link to anywhere else,
    including a path that no longer exists); `copy` (a real directory, as a restore that follows links writes);
    `file` (a regular file, as an archiver that cannot make a link leaves)."""
    if not os.path.lexists(link):
        return "missing", ""
    if link.is_symlink() or os.path.isjunction(link):
        try:
            named = os.readlink(link)
        except OSError as error:
            named = f"unreadable: {error}"
        same = os.path.normcase(os.path.realpath(link)) == os.path.normcase(os.path.realpath(directory))
        return ("linked" if same else "stale"), named
    return ("copy" if link.is_dir() else "file"), ""


def remove_link(link: Path) -> None:
    """Remove whatever stands at `link` without following it: a junction, a symlink, a file or a copied directory."""
    if os.path.isjunction(link):
        os.rmdir(link)
    elif link.is_symlink() or not link.is_dir():
        link.unlink()
    else:
        shutil.rmtree(link)


def restore_workspace_links(source: Path) -> None:
    """Make `node_modules/<name>` of the source build a link to each workspace package, as `npm ci` linked them, and say
    what each one was and what was done.

    The links are the one part of the build tree that is not plain files: a build restored from the CI cache can come back
    with them missing, stale, copied or flattened to files, while real directories (typebox) survive, so the mirror
    resolved typebox but not the workspace packages. A mirrored package that imports a workspace package (chord imports
    @earendil-works/pi-telemetry) then fails with "Cannot find package". Only a link to the package's current directory is
    kept; every other state is replaced. The workspaces are read from the source tree's own package.json, so a package a
    pin adds needs no list here."""
    modules = source / "node_modules"
    for name, directory in workspace_dirs(source).items():
        link = modules / name
        state, named = workspace_link_state(link, directory)
        detail = f" -> {named}" if named else ""
        # A junction restored from the Windows CI cache can read as a link to the right directory while Node cannot
        # resolve through it, so on Windows every workspace link is recreated.
        if state == "linked" and os.name != "nt":
            print(f"link-mirror: workspace {name}: ok{detail} (expected {directory})", file=sys.stderr)
            continue
        if state != "missing":
            remove_link(link)
        link.parent.mkdir(parents=True, exist_ok=True)
        link_directory(link, directory)
        after, now = workspace_link_state(link, directory)
        print(f"link-mirror: workspace {name}: {state}{detail} (expected {directory}): relinked, now {after} -> {now}", file=sys.stderr)
        if after != "linked":
            raise PinError(f"could not link workspace package {name} at {link} to {directory}: {after} {now}")


def report_resolution(mirror: Path, source: Path) -> None:
    """Print what Node resolves each importable workspace package (one whose manifest names `exports` or `main`) to from the
    mirrored chord package, as the parity tests import them. Informational: it never fails the mirror."""
    node = shutil.which("node")
    where = mirror / "packages" / "chord"
    if node is None or not where.is_dir():
        return
    importable = sorted(name for name, directory in workspace_names(source).items() if {"exports", "main"} & json.loads((directory / "package.json").read_text()).keys())
    script = (
        "for (const name of JSON.parse(process.env.PIG_WORKSPACES)) {"
        " try { console.error(`link-mirror: node resolves ${name} -> ${import.meta.resolve(name)}`); }"
        " catch (error) { console.error(`link-mirror: node cannot resolve ${name}: ${error.message}`); } }"
    )
    subprocess.run([node, "--input-type=module", "-e", script], cwd=where, env={**os.environ, "PIG_WORKSPACES": json.dumps(importable)}, check=False, timeout=120)


def link_mirror(pin: Pin) -> Path | None:
    """Point .upstream/<mirror>/node_modules at the source build's node_modules, so Node resolves the imports of the mirrored
    source (typebox, the workspace packages) the way the pinned source resolves them, without installing anything into the
    mirror. Return the linked directory, or None when the mirror or a source build of the pin is missing."""
    mirror = ROOT / ".upstream" / pin.mirror_dir
    if not mirror.is_dir():
        return None
    modules = source_modules(pin)
    if modules is None:
        print(f"link-mirror: no source build of {pin.ref} is installed; imports of third-party packages from {mirror} stay unresolved (run `make parity-deps` without PIG_PARITY_PI_PUBLISHED=1)", file=sys.stderr)
        return None
    restore_workspace_links(modules.parent)
    link = mirror / "node_modules"
    if not (link.is_symlink() and Path(os.path.realpath(link)) == modules.resolve()):
        mode = mirror.stat().st_mode
        mirror.chmod(mode | 0o200)  # the mirror is read-only (mirror-upstream.sh); only this link is added
        try:
            if link.is_symlink() or link.is_file():
                link.unlink()
            elif link.exists():
                shutil.rmtree(link)
            link.symlink_to(modules)
        finally:
            mirror.chmod(mode)
    report_resolution(mirror, modules.parent)
    return modules


def freeze_closure(pin: Pin, published_lock: Path | None = None) -> Path:
    """Freeze the pin's dependency closure into automation/pin/frozen: a release's from the lock of the published install, a
    commit's derived from its packs (a build of the commit must exist)."""
    build = published(pin, "current")
    source = build / "src" if build is not None else entry(pin) / "src"
    if not (source / "packages").is_dir():
        extract_source(pin, source)
    if pin.kind == "tag":
        if published_lock is None:
            raise PinError("a release pin freezes the lock of the published install: pass --from-published-lock <package-lock.json>")
        packed = pack(source, entry(pin) / "packs")
        lock = abstract_packs(published_closure(json.loads(published_lock.read_text()), pin.package_version), workspace_names(source), {name: tarball_integrity(tgz) for name, tgz in packed.items()})
    else:
        if published_lock is not None:
            raise PinError("a commit has no published install; its closure is derived from its packs")
        if read_frozen_lock(pin, DURABLE) is None:
            durable_packed = pack(source, entry(pin) / "durable-packs", DURABLE)
            write_frozen_lock(pin, derive_closure(pin, source, durable_packed, entry(pin) / "durable-closure", DURABLE), DURABLE)
        if frozen_lock_path(pin).exists():
            return frozen_lock_path(pin)
        packed = pack(source, entry(pin) / "packs")
        lock = derive_closure(pin, source, packed, entry(pin) / "closure")
    return write_frozen_lock(pin, lock)
