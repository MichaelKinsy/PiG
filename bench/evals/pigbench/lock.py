# SPDX-License-Identifier: MIT
"""arms.lock: the pinned identity of every arm, and the commands that verify and bump it.

The lock is JSON. Each arm has an exact version, a source, and checksums:

  npm       package, version, integrity (SHA-512 SRI of the primary tarball, from the registry), sha256 (of that tarball),
            package_lock (a committed package-lock.json with the integrity of every dependency, platform variants included),
            package_lock_sha256, bin (the path under node_modules that runs), bin_sha256 (per platform, of the executable)
            size (bytes of that tarball); optional install_lock (Pi: the package.json and package-lock.json pair its release publishes,
            checked against the release's SHA256SUMS) and vendor_manifest (Claude Code: the vendor's per-platform checksum and size)
  release   a GitHub release: release (base URL, asset name per platform, checksum source, executable name), assets (name, sha256, size per platform), version
  checkout  built from this repository: version prefix `pig --version` must print

Nothing in this file names a person, host or private path. Pinned CLIs are installed only into a throwaway directory
that is deleted after the run (see arms.Workspace); they are never committed.
"""
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import urllib.error
import urllib.request

EVALS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOCK = os.path.join(EVALS, "arms.lock")


def sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def read():
    with open(LOCK) as f:
        return json.load(f)


def write(data):
    with open(LOCK, "w", newline="\n") as f:  # LF on every host, or a lock written on Windows changes arms_lock_sha256
        json.dump(data, f, indent=2, sort_keys=True)
        f.write("\n")


def lock_hash():
    return sha256_file(LOCK)


def run(argv, cwd=None, timeout=900, env=None):
    done = subprocess.run(argv, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    if done.returncode:
        raise SystemExit(f"{' '.join(argv)} failed:\n{(done.stdout + done.stderr)[-2000:]}")
    return done.stdout


def fetch(url, dest=None):
    with urllib.request.urlopen(url, timeout=300) as response:  # noqa: S310 - https registry and release URLs from the lock
        data = response.read()
    if dest:
        with open(dest, "wb") as f:
            f.write(data)
    return data


def npm_env(tmp):
    return dict(os.environ, npm_config_cache=os.path.join(tmp, "npm-cache"), npm_config_update_notifier="false", npm_config_fund="false", npm_config_audit="false")


MANIFEST_KEY = {"linux-amd64": "linux-x64", "linux-arm64": "linux-arm64", "darwin-amd64": "darwin-x64", "darwin-arm64": "darwin-arm64", "windows-amd64": "win32-x64"}


def release_sums(base_url, name="SHA256SUMS"):
    """The checksum file of a GitHub release (SHA256SUMS unless the project names it otherwise) as {asset name: sha256}."""
    out = {}
    for line in fetch(base_url + name).decode().splitlines():
        digest, _, name = line.strip().partition("  ")
        if name:
            out[name.lstrip("*")] = digest
    return out


def pin_install_lock(arm_id, entry, version):
    """Fetch the package.json and package-lock.json pair a release publishes, check both against its SHA256SUMS, and commit them as the arm's lock."""
    spec = entry["install_lock"]
    base = spec["base_url"].format(version=version)
    sums = release_sums(base)
    for key, dest_name in (("package_json", f"{arm_id}.package.json"), ("package_lock", f"{arm_id}.package-lock.json")):
        data = fetch(base + spec[key])
        if sha256_bytes(data) != sums.get(spec[key]):
            raise SystemExit(f"{base}{spec[key]} does not match the release's SHA256SUMS")
        dest = os.path.join(EVALS, "locks", dest_name)
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        with open(dest, "wb") as f:
            f.write(data)
        entry[f"{key}_file"] = f"locks/{dest_name}"
        entry[f"{key}_sha256"] = sums[spec[key]]
    entry["package_lock"] = entry["package_lock_file"]
    return entry


def pin_vendor_manifest(entry, version):
    """Record the vendor's own checksum and size of the executable for every platform, from its release manifest."""
    url = entry["vendor_manifest"]["url"].format(version=version)
    data = fetch(url)
    manifest = json.loads(data)
    if manifest.get("version") != version:
        raise SystemExit(f"{url} describes {manifest.get('version')}, not {version}")
    entry["vendor_manifest"] = dict(entry["vendor_manifest"], sha256=sha256_bytes(data), url=entry["vendor_manifest"]["url"])
    entry["bin_sha256"] = {plat: manifest["platforms"][key]["checksum"] for plat, key in MANIFEST_KEY.items()}
    entry["bin_size"] = {plat: manifest["platforms"][key]["size"] for plat, key in MANIFEST_KEY.items()}
    return entry


def bump_npm(arm_id, entry, version):
    """Pin `version` of the arm's npm package: registry integrity, tarball sha256 and size, and the package-lock that npm ci installs from."""
    package = entry["package"]
    with tempfile.TemporaryDirectory(prefix="pigbench-lock-") as tmp:
        env = npm_env(tmp)
        meta = json.loads(run(["npm", "view", f"{package}@{version}", "dist.integrity", "dist.tarball", "version", "--json"], env=env))
        if meta["version"] != version:
            raise SystemExit(f"{package}@{version} resolved to {meta['version']}")
        data = fetch(meta["dist.tarball"])
        import base64
        sri = "sha512-" + base64.b64encode(hashlib.sha512(data).digest()).decode()
        if sri != meta["dist.integrity"]:
            raise SystemExit(f"{package}@{version}: tarball does not match the registry integrity")
        if "install_lock" in entry:
            pin_install_lock(arm_id, entry, version)
        else:
            project = os.path.join(tmp, "p")
            os.makedirs(project)
            with open(os.path.join(project, "package.json"), "w") as f:
                json.dump({"name": f"pigbench-arm-{arm_id}", "version": "0.0.0", "private": True, "dependencies": {package: version}}, f, indent=2)
            run(["npm", "install", "--package-lock-only", "--ignore-scripts"], cwd=project, env=env)
            dest = os.path.join(EVALS, entry["package_lock"])
            os.makedirs(os.path.dirname(dest), exist_ok=True)
            shutil.copy(os.path.join(project, "package-lock.json"), dest)
            entry["package_lock_sha256"] = sha256_file(dest)
    entry.update({"version": version, "integrity": sri, "sha256": sha256_bytes(data), "size": len(data), "tarball": meta["dist.tarball"]})
    entry["bin_sha256"] = {}  # recomputed by `lock pin-binaries` on each platform it is run on
    entry.pop("bin_size", None)
    if "vendor_manifest" in entry:
        pin_vendor_manifest(entry, version)
    return entry


def bump_release(arm_id, entry, version):
    """Pin a GitHub release: for each platform the asset name, its sha256 (from the release's own checksum file, checked against the downloaded bytes) and size.

    entry["release"] = {"base": url template with {version}, "assets": {platform: asset-name template}, "sums": "SHA256SUMS" or "sidecar" (a <asset>.sha256 file), "member": executable name}
    """
    spec = entry["release"]
    base = spec["base"].format(version=version)
    mode = spec["sums"]
    sums = release_sums(base, mode) if mode not in ("sidecar", "first-use") else {}
    assets = {}
    for plat, template in spec["assets"].items():
        name = template.format(version=version)
        try:
            data = fetch(base + name)
        except urllib.error.HTTPError as error:
            if error.code == 404:
                continue  # a platform the release does not publish
            raise SystemExit(f"{base}{name}: HTTP {error.code}; the lock was not changed") from error
        except OSError as error:
            raise SystemExit(f"{base}{name}: {error}; the lock was not changed") from error
        if mode == "first-use":  # the project publishes no checksum: trust on first use, recorded as such
            digest = sha256_bytes(data)
        else:
            digest = sums.get(name) if sums else fetch(base + name + ".sha256").decode().split()[0]
        if sha256_bytes(data) != digest:
            raise SystemExit(f"{name} does not match the release's published checksum")
        assets[plat] = {"name": name, "sha256": digest, "size": len(data)}
    if not assets:
        raise SystemExit(f"release v{version} publishes none of the listed assets")
    entry["assets"], entry["version"] = assets, version
    if mode == "first-use":
        entry["trust"] = "The project publishes no checksum for these assets: each sha256 is the hash of the bytes first downloaded when the lock was written (trust on first use)."
    for old in ("sha256", "size", "url"):
        entry.pop(old, None)
    return entry


def bump(arm_id, version):
    """Rewrite arms.lock for one arm at `version`."""
    data = read()
    entry = data["arms"][arm_id]
    if entry["source"] == "npm":
        bump_npm(arm_id, entry, version)
    elif entry["source"] == "release":
        bump_release(arm_id, entry, version)
    elif entry["source"] == "checkout":
        entry["version"] = version
    write(data)
    return entry


def verify(online=False):
    """Check the lock against the committed package-lock files, and, online, against the registry. Returns a list of problems."""
    problems = []
    data = read()
    for arm_id, e in sorted(data["arms"].items()):
        if e["source"] == "npm":
            path = os.path.join(EVALS, e["package_lock"])
            if not os.path.exists(path):
                problems.append(f"{arm_id}: {e['package_lock']} is missing")
                continue
            if sha256_file(path) != e["package_lock_sha256"]:
                problems.append(f"{arm_id}: {e['package_lock']} does not match package_lock_sha256")
            with open(path) as f:
                packages = json.load(f)["packages"]
            for fkey in ("package_json",):
                if e.get(f"{fkey}_file"):
                    fp = os.path.join(EVALS, e[f"{fkey}_file"])
                    if not os.path.exists(fp) or sha256_file(fp) != e[f"{fkey}_sha256"]:
                        problems.append(f"{arm_id}: {e[f'{fkey}_file']} does not match {fkey}_sha256")
            if "install_lock" in e and online:
                sums = release_sums(e["install_lock"]["base_url"].format(version=e["version"]))
                if sums.get(e["install_lock"]["package_lock"]) != e["package_lock_sha256"] or sums.get(e["install_lock"]["package_json"]) != e["package_json_sha256"]:
                    problems.append(f"{arm_id}: the release SHA256SUMS no longer lists the locked install pair")
            if "vendor_manifest" in e and online:
                got = json.loads(fetch(e["vendor_manifest"]["url"].format(version=e["version"])))
                for plat, key in MANIFEST_KEY.items():
                    if got["platforms"][key]["checksum"] != e["bin_sha256"].get(plat):
                        problems.append(f"{arm_id}: the vendor manifest now lists a different {plat} checksum")
            main = packages.get(f"node_modules/{e['package']}", {})
            if main.get("version") != e["version"]:
                problems.append(f"{arm_id}: package-lock pins {main.get('version')}, arms.lock says {e['version']}")
            if main.get("integrity", e["integrity"]) != e["integrity"]:
                problems.append(f"{arm_id}: package-lock integrity differs from arms.lock")
            if online and sha256_bytes(fetch(e["tarball"])) != e["sha256"]:
                problems.append(f"{arm_id}: the registry tarball no longer has sha256 {e['sha256']}")
        elif e["source"] == "release":
            if not e.get("assets") or any(len(a.get("sha256", "")) != 64 for a in e["assets"].values()):
                problems.append(f"{arm_id}: release assets are missing or lack a sha256")
        elif e["source"] == "checkout":
            build = e.get("build", {})
            if build.get("pgo") and (not os.path.exists(os.path.join(EVALS, build["pgo"])) or sha256_file(os.path.join(EVALS, build["pgo"])) != build.get("pgo_sha256")):
                problems.append(f"{arm_id}: the PGO profile {build['pgo']} is missing or does not match its locked sha256")
        elif e["source"] != "checkout":
            problems.append(f"{arm_id}: unknown source {e['source']}")
    for tool_id, e in sorted(data.get("tools", {}).items()):
        assets = e.get("assets") or {}
        if not assets or not e.get("release", {}).get("base") or any(len(a.get("sha256", "")) != 64 or not isinstance(a.get("size"), int) or a["size"] <= 0 for a in assets.values()):
            problems.append(f"tool {tool_id}: release assets are missing or lack a sha256 and size")
        if online:
            for plat, a in sorted(assets.items()):
                blob = fetch(e["release"]["base"] + a["name"])
                if sha256_bytes(blob) != a["sha256"] or len(blob) != a["size"]:
                    problems.append(f"tool {tool_id}: the {plat} release asset no longer has sha256 {a['sha256']} and {a['size']} bytes")
    return problems
