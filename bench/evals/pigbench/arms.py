# SPDX-License-Identifier: MIT
"""Arms: the harness builds every bench compares. Each is installed from arms.lock into a throwaway directory.

A Workspace creates one temporary directory per bench invocation, installs every selected arm into it, points HOME,
config and cache directories into it, and deletes it on exit. Installation fails when a checksum or version does not
match the lock. PIGBENCH_BIN_<ARM> overrides a path for development; the report marks such an arm as unpinned.
"""
import json
import os
import platform
import shutil
import subprocess
import tarfile
import zipfile
import tempfile
import tomllib

from . import lock

BENCH = os.path.dirname(lock.EVALS)
REPO = os.path.dirname(BENCH)
ARMS_TOML = os.path.join(lock.EVALS, "arms.toml")


def extract_member(archive, member_name, dest, arm_id, asset_name):
    """Extract the one executable from a tar.gz, tar.xz or zip release archive into dest; a release asset that is not an archive is the executable itself."""
    if not archive.endswith((".zip", ".tar.gz", ".tgz", ".tar.xz")):
        shutil.copyfile(archive, os.path.join(dest, member_name))
        return
    if archive.endswith(".zip"):
        with zipfile.ZipFile(archive) as z:
            info = next((i for i in z.infolist() if not i.is_dir() and os.path.basename(i.filename) == member_name), None)
            if info is None:
                raise SystemExit(f"{arm_id}: {asset_name} has no {member_name}")
            with z.open(info) as src, open(os.path.join(dest, member_name), "wb") as out:
                shutil.copyfileobj(src, out)
        return
    with tarfile.open(archive) as tar:
        member = next((m for m in tar.getmembers() if m.isfile() and os.path.basename(m.name) == member_name), None)
        if member is None:
            raise SystemExit(f"{arm_id}: {asset_name} has no {member_name}")
        member.name = member_name
        tar.extract(member, dest, filter="data")


def host_platform():
    system = {"Linux": "linux", "Darwin": "darwin"}.get(platform.system(), platform.system().lower())
    machine = {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine(), platform.machine())
    return system, machine


class Arm:
    def __init__(self, id, spec, pin):
        self.id, self.spec, self.pin = id, spec, pin
        self.name = spec["name"]
        self.family = spec["family"]
        self.no_extensions = spec.get("no_extensions", False)
        self.piglet = spec.get("piglet")
        self.source = pin["source"]
        self.version = pin["version"]
        self.binary = None
        self.skip = None
        self.unpinned = False
        self.install_dir = None
        self.record = {}


def load(path=ARMS_TOML):
    with open(path, "rb") as f:
        spec = tomllib.load(f)["arms"]
    pins = lock.read()["arms"]
    arms = {}
    for key, s in spec.items():
        aid = key.replace("_", ".")
        if aid not in pins:
            raise SystemExit(f"arm {aid} is in arms.toml but not pinned in arms.lock")
        arms[aid] = Arm(aid, s, pins[aid])
    return arms


def select(arms, ids):
    if not ids or ids == ["all"]:
        return list(arms.values())
    out = []
    for i in ids:
        if i not in arms:
            raise SystemExit(f"unknown arm {i!r}; known: {', '.join(arms)}")
        out.append(arms[i])
    return out


def build_pig(out, pgo=None):
    """Build this checkout the way `make install` does: stripped, trimpath, CGO off. Profile-guided only when a profile is named; otherwise `-pgo=off`, so a stray default.pgo cannot change what is measured."""
    sha = subprocess.run(["git", "rev-parse", "--short", "HEAD"], cwd=REPO, capture_output=True, text=True, timeout=30).stdout.strip() or "dev"
    env = dict(os.environ, CGO_ENABLED="0")
    lock.run(["go", "build", "-buildvcs=false", "-trimpath", f"-pgo={pgo or 'off'}", "-ldflags", f"-s -w -X github.com/MichaelKinsy/PiG/coding/cli.Build={sha}", "-o", out, "./cmd/pig"], cwd=REPO, timeout=3600, env=env)
    return out


def check_version(arm, env=None, cwd=None):
    """Run the binary's version command and require the pinned version in its output."""
    done = subprocess.run([arm.binary, "--version"], capture_output=True, text=True, timeout=120, env=env, cwd=cwd)
    text = (done.stdout.strip() or done.stderr.strip())
    line = next((ln for ln in text.splitlines() if arm.version in ln), None)
    if done.returncode != 0 or line is None:
        raise SystemExit(f"{arm.id}: `--version` printed {text[:200]!r}; the lock pins {arm.version}")
    return line.strip()


class Workspace:
    """A throwaway directory holding installed arms and the isolated homes of one bench invocation."""

    def __init__(self, keep=False):
        self.root = tempfile.mkdtemp(prefix="pigbench-")
        self.keep = keep or bool(os.environ.get("PIGBENCH_KEEP"))
        self.work = os.path.join(self.root, "work")
        os.makedirs(self.work)
        self.env = dict(os.environ, npm_config_cache=os.path.join(self.root, "npm-cache"), npm_config_update_notifier="false", npm_config_fund="false", npm_config_audit="false")
        self.installed = {}

    def __enter__(self):
        return self

    def __exit__(self, *_):
        if not self.keep:
            shutil.rmtree(self.root, ignore_errors=True)

    def install(self, arm):
        """Install one arm; sets arm.binary or arm.skip. Raises SystemExit on a checksum or version mismatch."""
        override = arm.spec.get("binary") or os.environ.get("PIGBENCH_BIN_" + arm.id.upper().replace(".", "_").replace("-", "_"))
        if override:
            arm.binary, arm.unpinned = override, True
            if not os.access(override, os.X_OK):
                arm.binary, arm.skip = None, f"{override} is not executable"
            return arm
        system, machine = host_platform()
        key = f"{system}-{machine}"
        present = [p for p in lock.read().get("absent_paths", {}).get(arm.family, []) if os.path.exists(p)]
        if present:
            raise SystemExit(f"{arm.id}: machine-level configuration exists that no flag bypasses ({', '.join(present)}); remove it or run the bench on another host")
        dest = os.path.join(self.root, "arms", arm.id)
        os.makedirs(dest, exist_ok=True)
        arm.install_dir = dest
        pin = arm.pin
        if arm.source == "npm":
            self._install_npm(arm, dest, key)
        elif arm.source == "release":
            self._install_release(arm, dest, key)
        elif arm.source == "checkout":
            if arm.piglet:
                self._install_piglet(arm, dest)
            else:
                build = arm.pin.get("build", {})
                pgo = os.path.join(lock.EVALS, build["pgo"]) if build.get("pgo") else None
                if pgo and lock.sha256_file(pgo) != build.get("pgo_sha256"):
                    raise SystemExit(f"{arm.id}: {build['pgo']} does not match the locked sha256")
                arm.binary = build_pig(os.path.join(dest, "pig"), pgo)
                arm.record = {"install_method": "go build" + (f" -pgo={build['pgo']}" if pgo else " -pgo=off"), "pgo_sha256": build.get("pgo_sha256")}
        else:
            raise SystemExit(f"{arm.id}: unknown source {pin['source']}")
        return arm

    def _install_npm(self, arm, dest, key):
        pin = arm.pin
        src = os.path.join(lock.EVALS, pin["package_lock"])
        if lock.sha256_file(src) != pin["package_lock_sha256"]:
            raise SystemExit(f"{arm.id}: {pin['package_lock']} does not match package_lock_sha256 in arms.lock")
        with open(src) as f:
            plock = json.load(f)
        main = plock["packages"][f"node_modules/{pin['package']}"]
        # A vendor lock may leave out the primary package's own integrity; npm ci then checks it against the integrity pinned here.
        if main["version"] != pin["version"] or main.setdefault("integrity", pin["integrity"]) != pin["integrity"]:
            raise SystemExit(f"{arm.id}: package-lock disagrees with arms.lock about {pin['package']}")
        if pin.get("package_json_file"):
            # The vendor's own install pair (Pi publishes it with each release): its root package.json carries the overrides its lock was built with.
            pj = os.path.join(lock.EVALS, pin["package_json_file"])
            if lock.sha256_file(pj) != pin["package_json_sha256"]:
                raise SystemExit(f"{arm.id}: {pin['package_json_file']} does not match package_json_sha256 in arms.lock")
            shutil.copy(pj, os.path.join(dest, "package.json"))
        else:
            with open(os.path.join(dest, "package.json"), "w") as f:
                json.dump({"name": f"pigbench-arm-{arm.id}", "version": "0.0.0", "private": True, "dependencies": {pin["package"]: pin["version"]}}, f)
        with open(os.path.join(dest, "package-lock.json"), "w") as f:
            json.dump(plock, f, indent=2)
        done = subprocess.run(["npm", "ci", "--ignore-scripts"], cwd=dest, env=self.env, capture_output=True, text=True, timeout=1200)
        if done.returncode:
            raise SystemExit(f"{arm.id}: npm ci failed (integrity mismatch or network):\n{done.stderr[-1500:]}")
        rel = pin.get("bin_by_platform", {}).get(key, pin["bin"])
        arm.binary = os.path.join(dest, rel)
        if not os.path.exists(arm.binary):
            arm.binary, arm.skip = None, f"no executable for {key} in {pin['package']}"
            return
        with open(os.path.join(dest, "node_modules", *pin["package"].split("/"), "package.json")) as f:
            installed = json.load(f)["version"]
        if installed != pin["version"]:
            raise SystemExit(f"{arm.id}: installed {installed}, the lock pins {pin['version']}")
        want = pin.get("bin_sha256", {}).get(key)
        if want:
            got = lock.sha256_file(os.path.realpath(arm.binary))
            if got != want:
                raise SystemExit(f"{arm.id}: executable sha256 {got} does not match the lock ({want})")
            size = pin.get("bin_size", {}).get(key)
            if size is not None and os.path.getsize(os.path.realpath(arm.binary)) != size:
                raise SystemExit(f"{arm.id}: executable size does not match the lock ({size})")
        arm.record = {"install_method": "npm ci --ignore-scripts", "source_url": pin["tarball"], "artifact_sha256": pin["sha256"], "integrity": pin["integrity"], "platform": key}

    def _install_release(self, arm, dest, key):
        pin = arm.pin
        asset = pin["assets"].get(key)
        if not asset:
            arm.binary, arm.skip = None, f"no pinned release archive for {key}"
            return
        url = pin["release"]["base"].format(version=pin["version"]) + asset["name"]
        archive = os.path.join(dest, asset["name"])
        lock.fetch(url, archive)
        got = lock.sha256_file(archive)
        if got != asset["sha256"] or os.path.getsize(archive) != asset["size"]:
            raise SystemExit(f"{arm.id}: {url} has sha256 {got}, the lock pins {asset['sha256']}")
        member_name = pin["release"]["member"] + (".exe" if key.startswith("windows") else "")
        extract_member(archive, member_name, dest, arm.id, asset["name"])
        os.unlink(archive)
        arm.binary = os.path.join(dest, member_name)
        os.chmod(arm.binary, 0o755)
        arm.record = {"install_method": "release archive", "source_url": url, "artifact_sha256": got, "platform": key}

    def _install_piglet(self, arm, dest):
        builder = self.installed.get(arm.pin["builder"])
        if builder is None or not builder.binary:
            raise SystemExit(f"{arm.id}: the builder arm {arm.pin['builder']} must be installed first")
        path = os.path.join(dest, "pig")
        home = os.path.join(self.root, "build-home")
        os.makedirs(home, exist_ok=True)
        env = dict(os.environ, PIG_HOME=home, PIG_SOURCE_ROOT=REPO)
        lock.run([builder.binary, "piglet", "build", os.path.join(lock.EVALS, arm.piglet), "--format", "binary", "--builder", "native", "--out", path], cwd=REPO, timeout=3600, env=env)
        arm.binary = path

    def install_all(self, arms):
        """Install every arm (builders first). Arms that cannot run on this host get arm.skip."""
        ordered = sorted(arms, key=lambda a: bool(a.piglet))
        for arm in ordered:
            self.installed[arm.id] = arm
            if arm.piglet and arm.pin["builder"] not in self.installed:
                builder = load()[arm.pin["builder"]]
                self.installed[builder.id] = self.install(builder)
            self.install(arm)
            if arm.binary and not arm.unpinned:
                arm.version_line = check_version(arm, env=self._plain_env())
        return arms

    def _plain_env(self):
        home = os.path.join(self.root, "version-home")
        os.makedirs(home, exist_ok=True)
        return dict(os.environ, HOME=home, PIG_HOME=os.path.join(home, ".pig"), CODEX_HOME=os.path.join(home, ".codex"), CLAUDE_CONFIG_DIR=os.path.join(home, ".claude"),
                    XDG_CONFIG_HOME=os.path.join(home, ".config"), PI_OFFLINE="1", PIG_OFFLINE="1", PI_SKIP_VERSION_CHECK="1")


def footprint(arm):
    """Bytes a user installs for this arm: the executable, or the installed package tree with its dependencies."""
    if not arm.binary:
        return None
    if arm.source == "npm" and arm.install_dir:
        total = 0
        for base, _, files in os.walk(os.path.join(arm.install_dir, "node_modules")):
            for name in files:
                p = os.path.join(base, name)
                if not os.path.islink(p):
                    total += os.path.getsize(p)
        runtime = subprocess.run(["node", "-p", "process.execPath"], capture_output=True, text=True, timeout=30).stdout.strip() or shutil.which("node")
        native = os.path.realpath(arm.binary)
        is_native = not native.endswith((".js", ".cjs", ".mjs"))
        return {"bytes": total, "runtime": None if is_native else "node", "runtime_bytes": None if is_native else (os.path.getsize(os.path.realpath(runtime)) if runtime else None)}
    real = os.path.realpath(arm.binary)
    return {"bytes": os.path.getsize(real), "runtime": None, "runtime_bytes": 0}


def pin_binaries(only=None):
    """Install each npm or release arm and record the SHA-256 of its executable for this platform in arms.lock."""
    system, machine = host_platform()
    key = f"{system}-{machine}"
    data = lock.read()
    with Workspace() as ws:
        for arm in select(load(), [only] if only else ["all"]):
            if arm.source not in ("npm",):
                continue
            ws.install(arm)
            if arm.binary:
                data["arms"][arm.id].setdefault("bin_sha256", {})[key] = lock.sha256_file(os.path.realpath(arm.binary))
                print(f"{arm.id}: {key} {data['arms'][arm.id]['bin_sha256'][key]}")
    lock.write(data)
    return 0


# Directories where a third-party CLI keeps state under the real home. A bench run must leave them as it found them.
OUTSIDE_PATHS = ("~/.claude", "~/.claude.json", "~/.codex", "~/.copilot", "~/.cache/copilot", "~/.config/github-copilot")


def outside_snapshot():
    """Existence, size and newest modification time of the real-home state directories of the third-party CLIs."""
    snap = {}
    for raw in OUTSIDE_PATHS:
        path = os.path.expanduser(raw)
        if not os.path.exists(path):
            snap[raw] = None
            continue
        newest, count = os.path.getmtime(path), 1
        for base, dirs, files in os.walk(path):
            for name in dirs + files:
                try:
                    newest = max(newest, os.lstat(os.path.join(base, name)).st_mtime)
                except OSError:
                    continue
                count += 1
        snap[raw] = [count, newest]
    return snap


def outside_changes(before, after):
    """(created, modified): a path that did not exist before the run and does now is ours; a modified one may belong to another process on the host."""
    created = sorted(p for p in before if before[p] is None and after.get(p) is not None)
    modified = sorted(p for p in before if before[p] is not None and before[p] != after.get(p))
    return created, modified
