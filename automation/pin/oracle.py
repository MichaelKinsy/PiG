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
`check-oracle` compares the installed tree with the pair. A Pi commit the release line carries ahead of the pin can move a
dependency a Pi package pins exactly; its lock entry rides the backports script, and the pair installed is the frozen one
with that entry in place (`carried_closure`). The result is cached per
commit and frozen inputs in a build directory that is never changed once published
(see `ensure`), and `extensions/sdk-ts/node_modules` links to it.
"""
from __future__ import annotations

import base64
import contextlib
import functools
import glob
import gzip
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import threading
import time
from pathlib import Path, PurePath
from urllib.parse import quote

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


def identity(data_sha: str, closure_sha: str, backports: str = "", catalog: str = "") -> str:
    # "bundle": the backports are in the bundle `pi` runs too (rebuild_bundle). A build made before the bundle was rebuilt has the key
    # without the word, and stays the oracle of the trees that still ask for that key.
    return hashlib.sha256((f"{data_sha}\n{closure_sha}" + (f"\n{backports}\nbundle" if backports else "") + (f"\ncatalog {catalog}" if catalog else "")).encode()).hexdigest()[:12]


# The release line's Pi is the pinned release plus the post-release Pi fixes its Go and vendored-Node ports carry
# (automation/gen/pi-post-release-backports.mjs, accounted for in the release's upstream-sync ledger). The parity tests compare the
# port with this oracle, so the oracle carries the same fixes: a pinned release lacks them, and every scenario they change would
# differ for a reason that is the pin's age, not a port defect.
BACKPORTS = ROOT / "automation" / "gen" / "pi-post-release-backports.mjs"
BACKPORT_PACKAGES = ("pi-agent-core", "pi-ai", "pi-coding-agent", "pi-mcp", "pi-tui")


def backport_ids(text: str | None = None) -> list[str]:
    """The Pi commits the backports script carries an entry (a `// BEGIN backport <commit>` block) for, in file order, without repeats."""
    if text is None:
        text = BACKPORTS.read_text(encoding="utf-8")
    return list(dict.fromkeys(re.findall(r"^// BEGIN backport (\w+)$", text, re.M)))


def backports_digest(pin: Pin) -> str:
    """The digest of the backports the oracle of `pin` carries, "" for a pin that carries none. Only the committed pin does: a run
    with PI_UPSTREAM_REF set builds another Pi (the upstream follower's), which the backports' exact before-strings do not describe.
    A script with no entry carries none either, so the oracle's key and stamp are those of a build that never heard of backports."""
    if pin != pinlib.committed() or not backport_ids():
        return ""
    return hashlib.sha256(BACKPORTS.read_bytes()).hexdigest()


# The catalog data the release line carries: Pi's own generator (packages/ai/catalog/generate.ts of the pinned build, with the catalog files of the
# later Pi commits the line carries, CATALOG_OVERLAY, in place of the pin's) run over the committed recording of the upstream responses
# (automation/gen/catalog-source), the recording `cmd/gen-models -replay-source` ports the generator over. A Pi build of the line's Pi main commit
# makes data like this when it builds (`generate-models` before `build`); the pinned build's frozen data is the day its release was built.
# The data replaces the installed pi-ai's dist/providers/data and the copy of it the oracle's bundle is made of.
CATALOG_SOURCE = ROOT / "automation" / "gen" / "catalog-source"
CATALOG_OVERLAY = CATALOG_SOURCE / "pi-overlay"
CATALOG_REPLAY = ROOT / "test" / "parity" / "testdata" / "catalog-generation" / "replay.mjs"


def catalog_digest(pin: Pin) -> str:
    """The digest of the inputs of the oracle's catalog data (the recording, its meta and the overlay files), "" for a pin that carries none."""
    if pin != pinlib.committed() or not (CATALOG_SOURCE / "meta.json").is_file():
        return ""
    digest = hashlib.sha256()
    files = [CATALOG_SOURCE / "meta.json", CATALOG_SOURCE / "responses.tar.gz"] + sorted(p for p in CATALOG_OVERLAY.rglob("*") if p.is_file())
    for path in files:
        digest.update(path.relative_to(CATALOG_SOURCE).as_posix().encode() + b"\0" + hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def catalog_fetched_at() -> str:
    return json.loads((CATALOG_SOURCE / "meta.json").read_text(encoding="utf-8"))["fetchedAt"]


def node_import_specifier(path: Path | PurePath) -> str:
    """Return a file:// URL specifier for Node `--import` / ESM `import()`. On Windows a bare absolute path (`D:\\...`) is rejected by Node's ESM loader as an unsupported URL scheme (`d:`)."""
    if isinstance(path, Path):
        return path.resolve().as_uri()
    if not path.is_absolute():
        raise ValueError(f"relative path cannot be a file URI: {path}")
    posix = path.as_posix()
    if not posix.startswith("/"):
        posix = "/" + posix
    return "file://" + quote(posix, safe="/:")


def generate_catalog_data(source: Path, work: Path) -> Path:
    """Run Pi's catalog generator of the source build over the committed recording and return the data directory it wrote. The generator runs in a
    copy of the build's packages/ai inside packages/ (so it resolves the build's node_modules); the build is left as built."""
    node = shutil.which("node")
    if node is None:
        raise PinError("node is required to generate the oracle's catalog data")
    meta = json.loads((CATALOG_SOURCE / "meta.json").read_text(encoding="utf-8"))
    fixtures = work / "fixtures"
    fixtures.mkdir(parents=True)
    with tarfile.open(CATALOG_SOURCE / "responses.tar.gz") as archive:
        archive.extractall(fixtures, filter="data")
    ai = source / "packages" / "ai"
    copy = source / "packages" / f".catalog-data-{os.getpid()}"
    try:
        copy.mkdir()
        for name in ("package.json", "catalog", "scripts", "src"):
            if (ai / name).is_dir():
                shutil.copytree(ai / name, copy / name)
            else:
                shutil.copy2(ai / name, copy / name)
        shutil.copytree(CATALOG_OVERLAY, copy, dirs_exist_ok=True)
        shutil.rmtree(copy / "src" / "providers" / "data")
        env = {**os.environ, "CATALOG_FIXTURES": str(fixtures), "CATALOG_FETCHED_AT": meta["fetchedAt"]}
        done = subprocess.run([node, "--import", node_import_specifier(CATALOG_REPLAY), "catalog/generate.ts", "--strict"], cwd=copy, env=env, capture_output=True, text=True, check=False, timeout=600)
        if done.returncode != 0:
            raise PinError(f"Pi's catalog generator failed over the committed recording:\n{(done.stderr or done.stdout).strip()[-2000:]}")
        data = work / "data"
        shutil.copytree(copy / "src" / "providers" / "data", data)
    finally:
        shutil.rmtree(copy, ignore_errors=True)
    return data


def install_catalog_data(package: Path, data: Path) -> None:
    """Make the pi-ai `package`'s dist/providers/data (installed, or in the source build for the bundler's run) the generated `data`."""
    target = package / "dist" / "providers" / "data"
    shutil.rmtree(target)
    shutil.copytree(data, target)


def backport_places(modules: Path) -> dict[str, Path]:
    """The one installed copy of each Pi package the backports patch, by short name (pi-ai), under the oracle's `modules`."""
    found: dict[str, list[Path]] = {name: [] for name in BACKPORT_PACKAGES}
    for key, installed in tree_packages(modules).items():
        package = installed.rsplit("@", 1)[0].removeprefix("@earendil-works/")
        if installed.startswith("@earendil-works/") and package in found:
            found[package].append(modules.parent / key)
    for package, places in found.items():
        if len(places) != 1:
            raise PinError(f"the installed oracle has {len(places)} copies of {package} ({', '.join(map(str, places))}); the post-release backports patch exactly one")
    return {package: places[0] for package, places in found.items()}


def backports_node(*arguments: str) -> subprocess.CompletedProcess:
    node = shutil.which("node")
    if node is None:
        raise PinError("node is required to apply the post-release backports to the oracle")
    return subprocess.run([node, str(BACKPORTS), *arguments], capture_output=True, text=True, check=False, timeout=120)


def patch_packages(places: dict[str, Path], what: str) -> None:
    done = backports_node("--packages", *(f"{name}={place}" for name, place in places.items()))
    if done.returncode != 0:
        raise PinError(f"the post-release backports do not apply to {what}:\n{done.stderr.strip()}")


@functools.lru_cache(maxsize=8)
def _dependencies_of(script: str, digest: str) -> tuple:
    done = backports_node("--dependencies")
    if done.returncode != 0:
        raise PinError(f"the post-release backports script does not list its carried dependencies:\n{done.stderr.strip()}")
    return tuple(json.loads(done.stdout))


def carried_dependencies(pin: Pin | None = None) -> list[dict]:
    """The dependency changes of the Pi commits the backports script carries (its `dependency(...)` entries). Each names the Pi commit
    (`id`), the Pi package that pins the dependency exactly (`package`), the dependency (`name`), the version the pin installs (`from`),
    the carried one (`to`) and the lock entry of the carried version as Pi's own package-lock.json holds it at that commit (`resolved`,
    `integrity`, `dependencies`). Empty for a script without entries and, with `pin`, for a pin whose oracle carries no backports."""
    if (pin is not None and not backports_digest(pin)) or not backport_ids():
        return []
    return [dict(dep) for dep in _dependencies_of(str(BACKPORTS), hashlib.sha256(BACKPORTS.read_bytes()).hexdigest())]


def peer_range_admits(range_: str, version: str) -> bool:
    """Whether `version` satisfies a peer range written as space-separated comparators (`>=5 <9`); any other form is not read, so it admits nothing."""
    def numbers(text: str) -> list[int] | None:
        parts = text.split(".")
        return [int(part) for part in parts] if len(parts) <= 3 and all(part.isdigit() for part in parts) else None
    have = numbers(version)
    comparators = range_.split()
    if have is None or not comparators:
        return False
    for comparator in comparators:
        operator = next((op for op in (">=", "<=", ">", "<") if comparator.startswith(op)), None)
        bound = numbers(comparator[len(operator):]) if operator else None
        if bound is None:
            return False
        bound += [0] * (3 - len(bound))
        have3 = have + [0] * (3 - len(have))
        if not {">=": have3 >= bound, "<=": have3 <= bound, ">": have3 > bound, "<": have3 < bound}[operator]:
            return False
    return True


def carried_closure(lock: dict, carried: list[dict]) -> dict:
    """The closure the oracle installs: `lock` (the frozen closure, left as it is) with each carried dependency in place of the version
    the pin installs. The dependency's entry takes the carried version, archive and integrity, and the entry of the Pi package that pins
    it names the carried version: the two changes Pi's lock has at the carried commit. A peer range of another package that admits both versions stays as it is. `npm ci` of the result verifies the archive
    against the integrity. Everything else stays the frozen closure's, so the carried version must stand where the pinned one stood,
    with the same dependencies of its own; any other shape stops the build and names the entry (a pin that already holds the commit
    has no pinned version left to replace: `make pin-bump` deletes the entry with the commit's blocks)."""
    if not carried:
        return lock
    out = json.loads(json.dumps(lock))
    packages = out["packages"]
    for dep in carried:
        name, owner = dep["name"], "@earendil-works/" + dep["package"]
        what = f"the carried dependency {name} {dep['from']} -> {dep['to']} of Pi {dep['id']} (automation/gen/pi-post-release-backports.mjs)"
        archive = f"https://registry.npmjs.org/{name}/-/{name.rsplit('/', 1)[-1]}-{dep['to']}.tgz"
        if dep["resolved"] != archive or not dep["integrity"].startswith("sha512-"):
            raise PinError(f"{what} must name the registry archive of that exact version ({archive}) and its sha512 integrity, as Pi's package-lock.json does; it names {dep['resolved']} {dep['integrity'][:16]}")
        places = [key for key in packages if key.endswith("node_modules/" + name)]
        if not places:
            raise PinError(f"{what}: the frozen closure installs no {name}")
        for key in places:
            item = packages[key]
            if item.get("version") != dep["from"]:
                raise PinError(f"{what}: the frozen closure installs {name} {item.get('version')} at {key}, not {dep['from']}. A pin that already holds the Pi commit needs no carried dependency: delete the entry with the commit's blocks (`make pin-bump` does)")
            if item.get("dependencies", {}) != dep.get("dependencies", {}):
                raise PinError(f"{what}: Pi's lock gives {dep['to']} the dependencies {dep.get('dependencies', {})}, the frozen closure resolved {item.get('dependencies', {})} for {dep['from']}: its own dependencies changed, and the closure has no entries for them")
            item.update(version=dep["to"], resolved=dep["resolved"], integrity=dep["integrity"])
        pinned = 0
        for key, item in packages.items():
            for field in ("dependencies", "optionalDependencies", "peerDependencies"):
                if name not in item.get(field, {}):
                    continue
                if field == "peerDependencies" and peer_range_admits(item[field][name], dep["from"]) and peer_range_admits(item[field][name], dep["to"]):
                    continue
                if not key.endswith("node_modules/" + owner) or field != "dependencies" or item[field][name] != dep["from"]:
                    raise PinError(f"{what}: {key or 'the root'} needs {name} {item[field][name]} ({field}); the entry replaces the exact pin {dep['from']} of {owner} and nothing else")
                item[field][name] = dep["to"]
                pinned += 1
        if not pinned:
            raise PinError(f"{what}: no {owner} of the frozen closure pins {name}")
    return out


def carried_place(modules: Path, dep: dict) -> Path:
    """The one installed copy of a carried dependency under the oracle's `modules`."""
    places = [modules.parent / key for key, installed in tree_packages(modules).items() if installed == f"{dep['name']}@{dep['to']}"]
    if len(places) != 1:
        raise PinError(f"the installed oracle has {len(places)} copies of {dep['name']} {dep['to']}, the dependency carried for Pi {dep['id']}; it installs exactly one")
    return places[0]


def carried_problems(modules: Path, carried: list[dict]) -> list[str]:
    """What the installed tree lacks of the carried dependencies: each is installed once at the carried version (its archive passed
    `npm ci`'s integrity check) and declares the dependencies of its own that the entry records from Pi's lock. The manifest of the Pi
    package that pins it is not looked at: it stays the pin's (published ranges follow the pin)."""
    problems = []
    for dep in carried:
        try:
            place = carried_place(modules, dep)
        except PinError as error:
            problems.append(str(error))
            continue
        own = json.loads((place / "package.json").read_text(encoding="utf-8")).get("dependencies", {})
        if own != dep.get("dependencies", {}):
            problems.append(f"{dep['name']} {dep['to']} declares the dependencies {own}, the entry carried for Pi {dep['id']} records {dep.get('dependencies', {})} from Pi's lock")
    return problems


def source_dependency(source: Path, dep: dict) -> Path:
    """Where the source build resolves a carried dependency: the one copy `npm ci` of Pi's lock hoisted to the root, at the pinned version."""
    place = source / "node_modules" / dep["name"]
    try:
        version = json.loads((place / "package.json").read_text(encoding="utf-8"))["version"]
    except (OSError, ValueError, KeyError):
        version = None
    nested = sorted(path.relative_to(source).as_posix() for path in source.glob(f"packages/*/node_modules/{dep['name']}"))
    if version != dep["from"] or place.is_symlink() or nested:
        raise PinError(f"the source build under {source} does not resolve {dep['name']} to one copy of {dep['from']} at node_modules/{dep['name']} (found {version}{', also ' + ', '.join(nested) if nested else ''}); the dependency carried for Pi {dep['id']} replaces exactly that copy for the bundler")
    return place


def apply_backports(modules: Path) -> None:
    """Apply the post-release backports to the installed Pi packages under `modules` in place. The packs and the source tree stay
    as built: the frozen closure pins each pack's integrity, and the Durable oracle packs the source tree again."""
    patch_packages(backport_places(modules), "the installed oracle")


# The backports patch compiled output, and `pi` does not run that: the package's bin is dist/bundle/cli.js, the bundle Pi's build makes of
# the packages' compiled output (scripts/build-coding-agent-bundle.mjs: esbuild, minified, chunks named by content). It is what a user of the
# published package runs and what every CLI parity scenario runs. An oracle patched in its compiled output only was the release plus its fixes
# for a Node import and the bare release for `pi`, and both at once where the bundle reads a patched file from disk (the HTML export's
# template.js beside the bundled ansi-to-html). So the build makes the bundle again from the patched compiled output, with Pi's bundler and the
# esbuild of Pi's lock. Two checks keep the result Pi's program plus the backports and nothing else: the bundler must first reproduce the
# installed bundle, byte for byte, from the compiled output as built, and the source build must be as it was afterwards.
BUNDLER = Path("scripts") / "build-coding-agent-bundle.mjs"


def tree_digest(directory: Path) -> str:
    """The digest of the files under `directory`: the sha256 of their `<sha256 of the file>  <path>` lines in path order
    (internal/pioracle computes the same digest of the bundle)."""
    files = sorted((path.relative_to(directory).as_posix(), path) for path in directory.rglob("*") if path.is_file())
    return hashlib.sha256("".join(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {name}\n" for name, path in files).encode()).hexdigest()


def bundle_of(modules: Path) -> Path | None:
    """The directory of the bundle the installed package's `pi` bin runs (dist/bundle), None for a Pi whose bin is compiled output."""
    agent = modules / "@earendil-works" / "pi-coding-agent"
    manifest = json.loads((agent / "package.json").read_text(encoding="utf-8"))
    binary = manifest["bin"]["pi"] if isinstance(manifest["bin"], dict) else manifest["bin"]
    directory = (agent / binary).parent
    return directory if directory.name == "bundle" else None


def run_bundler(source: Path) -> None:
    node = shutil.which("node")
    if node is None:
        raise PinError("node is required to rebuild the bundle of the oracle")
    done = subprocess.run([node, str(source / BUNDLER)], cwd=source, capture_output=True, text=True, check=False, timeout=900)
    if done.returncode != 0:
        raise PinError(f"Pi's bundler ({BUNDLER.as_posix()}) failed:\n{(done.stderr or done.stdout).strip()}")


def rebuild_bundle(source: Path, modules: Path, catalog: Path | None = None) -> str:
    """Replace the installed bundle with the one Pi's bundler makes of the compiled output plus the backports, and return its digest
    ("" for a Pi without a bundle). The bundler reads the source build's packages, so the backports are applied there for the run and
    taken out again: the packs, the mirror and the Durable oracle need the source build as built. A carried dependency is bundled too:
    for that run the source build's copy of it is replaced by a copy of the installed one (the archive `npm ci` verified), whose own
    dependencies resolve from Pi's workspace as in Pi's build of the carried commit, and put back."""
    installed = bundle_of(modules)
    if installed is None:
        return ""
    workspaces = workspace_dirs(source)
    places = {name: workspaces[f"@earendil-works/{name}"] for name in BACKPORT_PACKAGES}
    built = places["pi-coding-agent"] / installed.relative_to(modules / "@earendil-works" / "pi-coding-agent")
    parts = [place / part for place in places.values() for part in ("dist", "docs", "schemas") if (place / part).is_dir()]
    carried = carried_dependencies()
    parts += [source_dependency(source, dep) for dep in carried]
    as_built = [tree_digest(part) for part in parts]
    shelf = source.parent / "dependencies-as-built"
    shelved: list[tuple[Path, Path]] = []
    listed = backports_node("--package-files", *(f"{name}={place}" for name, place in places.items()))
    if listed.returncode != 0:
        raise PinError(f"the post-release backports script does not name the files it patches:\n{listed.stderr.strip()}")
    saved = {Path(path): Path(path).read_bytes() for path in json.loads(listed.stdout)}
    kept = source.parent / "bundle-as-built"
    shutil.move(str(built), str(kept))
    try:
        run_bundler(source)
        if tree_digest(built) != tree_digest(installed):
            raise PinError(f"Pi's bundler does not reproduce the installed bundle from the compiled output as built ({built} differs from {installed}): a bundle rebuilt with the backports would differ from Pi's by more than the backports")
        patch_packages(places, "the source build")
        if catalog is not None:
            data_place = places["pi-ai"] / "dist" / "providers" / "data"
            shutil.move(str(data_place), str(source.parent / "catalog-data-as-built"))
            shutil.copytree(catalog, data_place)
        for dep in carried:
            place = source_dependency(source, dep)
            shelf.mkdir(exist_ok=True)
            shelved.append((place, shelf / str(len(shelved))))
            shutil.move(str(place), str(shelved[-1][1]))
            shutil.copytree(carried_place(modules, dep), place, symlinks=True)
        run_bundler(source)
        shutil.rmtree(installed)
        shutil.copytree(built, installed)
    finally:
        for path, data in saved.items():
            path.write_bytes(data)
        for place, held in reversed(shelved):
            if held.exists():
                shutil.rmtree(place, ignore_errors=True)
                shutil.move(str(held), str(place))
        shutil.rmtree(shelf, ignore_errors=True)
        held_data = source.parent / "catalog-data-as-built"
        if held_data.exists():
            data_place = places["pi-ai"] / "dist" / "providers" / "data"
            shutil.rmtree(data_place, ignore_errors=True)
            shutil.move(str(held_data), str(data_place))
        shutil.rmtree(built, ignore_errors=True)
        shutil.move(str(kept), str(built))
    if [tree_digest(part) for part in parts] != as_built:
        raise PinError(f"the source build under {source} is not as built after the bundle was rebuilt")
    return tree_digest(installed)


def bundle_problem(modules: Path, stamp: dict) -> str:
    """Why the bundle the installed `pi` bin runs is not the one the build of `stamp` made from the patched compiled output, "" when it
    is (or when the oracle carries no backports, or its bin is compiled output)."""
    if not stamp.get("backports") and not stamp.get("catalog"):
        return ""
    bundle = bundle_of(modules)
    if bundle is None:
        return ""
    if not stamp.get("bundle_sha256"):
        return "the installed Pi carries the post-release backports and the catalog data in its compiled output only: the bundle its `pi` bin runs is the pinned release's"
    got = tree_digest(bundle)
    return "" if got == stamp["bundle_sha256"] else f"the bundle the installed `pi` bin runs has the digest {got}, its build stamped {stamp['bundle_sha256']}"


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
# What a pack of pi-ai ships: the build copies DATA_DIR here.
DIST_DATA_DIR = Path("packages/ai/dist/providers/data")
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


def built_with_frozen_data(pin: Pin, source: Path) -> bool:
    """Whether the built tree `source` ships the model data the pin froze (a pin that froze none accepts any build)."""
    frozen = frozen_digest(pin)
    shipped = source / DIST_DATA_DIR
    return frozen is None or (shipped.is_dir() and data_digest(shipped) == frozen)


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


def mirror_data(commit: str, target: Path) -> str:
    """Make `target` (the provider data directory of a source mirror of Pi `commit`) the snapshot frozen for that commit, and
    return its digest. The mirror is the pinned release's bytes: its data never comes from an installed package, which carries the
    backports. Data that already has the snapshot's digest is left alone; anything else is replaced; no snapshot is an error."""
    archive_path = FROZEN / f"{commit}.provider-data.tgz"
    marker = FROZEN / f"{commit}.provider-data.tgz.sha256"
    if not archive_path.exists() or not marker.exists():
        raise PinError(f"no frozen provider data for Pi {commit or '(unknown commit)'} ({archive_path.name}): the mirror's provider data is the frozen snapshot of its commit, never an installed package's")
    recorded = marker.read_text().strip()
    if target.is_dir() and data_digest(target) == recorded:
        return recorded
    target.mkdir(parents=True, exist_ok=True)
    for old in target.iterdir():
        old.unlink()
    with tarfile.open(archive_path) as archive:
        archive.extractall(target, filter="data")
    got = data_digest(target)
    if got != recorded:
        raise PinError(f"{archive_path} unpacks to digest {got}, its recorded digest is {recorded}")
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
LOCK_KEY = "node_modules/" + AGENT
PACK_PREFIX = "pig-pack:"


def frozen_lock_path(pin: Pin) -> Path:
    return FROZEN / f"{pin.commit}.closure.json.gz"


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


def oracle_closure(pin: Pin) -> dict | None:
    """The closure the oracle of `pin` installs: the frozen one, with the carried dependencies of the pin whose oracle carries backports."""
    lock = read_frozen_lock(pin)
    return carried_closure(lock, carried_dependencies(pin)) if lock is not None else None


def oracle_closure_digest(pin: Pin) -> str | None:
    """The digest of `oracle_closure`: what a build's stamp records as closure_sha256 and what the oracle's key is made of. There is no
    separate digest of the carried dependencies: they are in the lock this digest covers."""
    if not carried_dependencies(pin):
        return frozen_closure_digest(pin)
    lock = oracle_closure(pin)
    return closure_digest(lock) if lock is not None else None


def read_frozen_lock(pin: Pin) -> dict | None:
    path = frozen_lock_path(pin)
    if not path.exists():
        return None
    return json.loads(gzip.decompress(path.read_bytes()))


def write_frozen_lock(pin: Pin, lock: dict) -> Path:
    path = frozen_lock_path(pin)
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


def derive_closure(pin: Pin, source: Path, packed: dict[str, Path], work: Path) -> dict:
    """Resolve the closure of a published install of the pinned packages: `npm install` of the packed package with the other
    workspace packages overridden to their packs and the registry as of the commit date."""
    if work.exists():
        shutil.rmtree(work)
    work.mkdir(parents=True)
    overrides = {name: "file:" + str(tgz) for name, tgz in packed.items() if name != AGENT}
    manifest = {"name": "pi-oracle", "version": "0.0.0", "private": True, "devDependencies": {AGENT: "file:" + str(packed[AGENT])}, "overrides": overrides}
    (work / "package.json").write_text(json.dumps(manifest, indent=1))
    npm("install", *NPM_FLAGS, "--package-lock-only", "--install-strategy=shallow", "--before", commit_date(pin), cwd=work)
    lock = json.loads((work / "package-lock.json").read_text())
    lock["packages"][""]["devDependencies"] = {AGENT: pin.package_version}
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


def installed(modules: Path, pin: Pin) -> bool:
    """Whether `modules` holds the oracle package of the pin: its manifest is readable, names the pin's version and its `pi` bin
    file exists, so `pi_version` can run it. An empty or partial node_modules (an interrupted or pruned build) is not an oracle."""
    agent = modules / "@earendil-works" / "pi-coding-agent"
    try:
        manifest = json.loads((agent / "package.json").read_text(encoding="utf-8"))
        binary = manifest["bin"]["pi"] if isinstance(manifest["bin"], dict) else manifest["bin"]
        return manifest.get("version") == pin.package_version and (agent / binary).is_file()
    except (OSError, ValueError, KeyError, TypeError):
        return False


def protect(modules: Path) -> None:
    """Make every directory of a published node_modules unwritable. The trees that link it (`extensions/sdk-ts/node_modules`) are
    one `npm ci` away from emptying it for all of them: npm ci removes the contents of node_modules, through a link too, so a
    hint to "run npm ci in extensions/sdk-ts" destroyed the shared build and every later `make parity-deps` reused the empty
    directory. With no write bit the npm ci fails (EACCES) and the build stays whole. `thaw` undoes it, to delete a build."""
    if os.name == "nt":  # a directory's mode is not enforced there
        return
    for directory, _names, _files in os.walk(modules, topdown=False):  # children first, so the walk never needs a write bit
        os.chmod(directory, os.stat(directory).st_mode & ~0o222)


def thaw(tree: Path) -> None:
    """Give the owner write access to every directory under `tree` again (the inverse of `protect`), so `shutil.rmtree` can delete it."""
    for directory, _names, _files in os.walk(tree):
        with contextlib.suppress(OSError):
            os.chmod(directory, os.stat(directory).st_mode | 0o700)


def reusable(pin: Pin, build: Path | None) -> bool:
    """Whether `build` is a stamped source build of the pin made with the frozen inputs of this tree (any, where none are frozen)."""
    if build is None or not (build / STAMP).is_file() or not installed(build / "node_modules", pin):
        return False
    data = json.loads((build / STAMP).read_text())
    return data.get("commit") == pin.commit and data.get("package_version") == pin.package_version and data.get("source") == "build" and data.get("provider_data_sha256", "") == (frozen_digest(pin) or data.get("provider_data_sha256", "")) and data.get("closure_sha256", "") == (oracle_closure_digest(pin) or data.get("closure_sha256", "")) and data.get("backports", "") == backports_digest(pin) and data.get("catalog", "") == catalog_digest(pin) and not bundle_problem(build / "node_modules", data)


def ensure(pin: Pin, *, force: bool = False) -> Path:
    """Build (once per commit and frozen inputs) and return the oracle's node_modules directory. A build goes into a fresh
    directory and is published by an atomic ref write after its stamp, so a published oracle is never rewritten or removed:
    a forced rebuild publishes another directory, and a build that fails part-way publishes nothing."""
    home = entry(pin)
    key = identity(frozen_digest(pin) or "", oracle_closure_digest(pin) or "", backports_digest(pin), catalog_digest(pin))
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
            thaw(build)
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
    carried = carried_dependencies(pin)
    closure = carried_closure(closure_for(pin, source, packed, build / "closure"), carried)
    flat = build / "flat"
    install_closure(closure, packed, flat)
    modules = build / "node_modules"
    lay_out(flat, modules, typescript_version())
    problems = tree_differences(modules, closure) + carried_problems(modules, carried)
    if problems:
        raise PinError("the installed tree differs from the frozen closure:\n  " + "\n  ".join(problems[:20]))
    backports = backports_digest(pin)
    catalog = catalog_digest(pin)
    catalog_data = None
    if catalog:
        recorded_for = json.loads((CATALOG_SOURCE / "meta.json").read_text(encoding="utf-8")).get("piCommit")
        if recorded_for != pin.commit:
            raise PinError(f"the recording in {CATALOG_SOURCE} names the Pi generator of commit {recorded_for}, the pin is {pin.commit}: re-record with `make model-catalogs-live` and review the overlay")
        catalog_data = generate_catalog_data(source, build / "catalog")
        install_catalog_data(backport_places(modules)["pi-ai"], catalog_data)
    bundle = ""
    if backports or catalog:
        if backports:
            apply_backports(modules)
        bundle = rebuild_bundle(source, modules, catalog_data)
    for scratch in ("flat", "packs", "closure"):
        shutil.rmtree(build / scratch, ignore_errors=True)
    closure_sha = closure_digest(closure)
    stamp = {"commit": pin.commit, "package_version": pin.package_version, "version": pin.version, "source": "build", "provider_data_sha256": data_sha, "closure_sha256": closure_sha}
    if backports:
        stamp["backports"] = backports
    if catalog:
        stamp["catalog"] = catalog
        stamp["catalog_data_sha256"] = data_digest(catalog_data)
    if bundle:
        stamp["bundle_sha256"] = bundle
    write_replacing(build / STAMP, (json.dumps(stamp) + "\n").encode())
    protect(modules)
    return f"oracle-{identity(data_sha, closure_sha, backports, catalog)}"


def pi_version(modules: Path) -> str:
    """`pi --version` of the installed oracle, run through node from the package's own bin entry, so it works for the built
    layout and npm's, on every platform (Windows cannot execute the script that .bin/pi names)."""
    env = {k: v for k, v in os.environ.items() if k.lower() not in {"https_proxy", "http_proxy", "all_proxy"}}
    agent = modules / "@earendil-works" / "pi-coding-agent"
    try:
        manifest = json.loads((agent / "package.json").read_text(encoding="utf-8"))
    except FileNotFoundError as error:
        raise PinError(f"no {AGENT} at {agent} (resolves to {os.path.realpath(agent)}): the oracle build behind {modules} is empty or was never installed. An `npm ci` in extensions/sdk-ts empties a shared build through its link; run `make parity-deps` to rebuild it") from error
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
    commit's derived from its packs. The packs come from a build of the commit, made here when none exists."""
    build = published(pin, "current")
    source = build / "src" if build is not None else entry(pin) / "src"
    if (source / "packages" / "coding-agent" / "dist").is_dir() and not built_with_frozen_data(pin, source):
        # The packs ship dist/providers/data. A build made before the pin froze its model data, or from another snapshot, packs other bytes than
        # the build of every later oracle, and a closure frozen from it fails EINTEGRITY at `npm ci` in a clean cache (a warm cache keeps the old build).
        source = entry(pin) / "src"
        extract_source(pin, source)
    if not (source / "packages").is_dir():
        extract_source(pin, source)
    if not (source / "packages" / "coding-agent" / "dist").is_dir():
        # The closure records the integrity of each pack. A pack of unbuilt source has no dist, so its integrity is not the one the
        # oracle's build produces, and the later `npm ci` of the closure fails EINTEGRITY. Build with the pin's frozen model data first.
        build_source(pin, source)
    if pin.kind == "tag":
        if published_lock is None:
            raise PinError("a release pin freezes the lock of the published install: pass --from-published-lock <package-lock.json>")
        packed = pack(source, entry(pin) / "packs")
        lock = abstract_packs(published_closure(json.loads(published_lock.read_text()), pin.package_version), workspace_names(source), {name: tarball_integrity(tgz) for name, tgz in packed.items()})
    else:
        if published_lock is not None:
            raise PinError("a commit has no published install; its closure is derived from its packs")
        if frozen_lock_path(pin).exists():
            return frozen_lock_path(pin)
        packed = pack(source, entry(pin) / "packs")
        lock = derive_closure(pin, source, packed, entry(pin) / "closure")
    return write_frozen_lock(pin, lock)
