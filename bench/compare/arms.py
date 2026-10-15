#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Fetch, verify, build and wrap the comparison arms listed in arms.lock.

  arms.py install --root DIR [--arm NAME ...] [--cache DIR]   download (sha256-verified), unpack into DIR, build source arms
  arms.py specs   --root DIR [--arm NAME ...]                 print `name=command` build specs for the Pi-Bolt harness
  arms.py record  --root DIR                                  print the run record (version, artifact sha256, source URL, commit)
  arms.py check                                               fail when a PiG source arm is not a full commit of this repository, or an
                                                              artifact pinned here is pinned with another sha256 in bench/evals/arms.lock

Rules: no curl|sh installers; every artifact is verified against arms.lock (and the project's SHA256SUMS when it has one);
each arm unpacks into a fresh directory; the harness runs every arm through bin/<arm>, a launcher that only sets the arm's
environment and execs the binary (adapters/). A launcher never starts a second process.
"""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tomllib
import urllib.request
import zipfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
LOCK = tomllib.loads((HERE / "arms.lock").read_text())
EVALS_LOCK = HERE.parent / "evals" / "arms.lock"


def evals_mismatches(lock: dict, evals: dict) -> list[str]:
    """Artifacts both lock files pin (same release URL) must carry the same sha256: two pins of one artifact may not disagree."""
    pinned = {}
    for arm in evals.get("arms", {}).values():
        rel = arm.get("release") or {}
        for asset in (arm.get("assets") or {}).values():
            if rel.get("base") and asset.get("name"):
                pinned[rel["base"].format(version=arm.get("version")) + asset["name"]] = asset.get("sha256")
    return [f"{name}: {a['url']} is {a['sha256']} here, {pinned[a['url']]} in bench/evals/arms.lock"
            for name, a in lock.get("arm", {}).items() if a.get("url") in pinned and pinned[a["url"]] != a.get("sha256")]


def self_arm_problems(lock: dict) -> list[str]:
    """A PiG source arm builds a commit of this repository (repo = "self"): a full 40-hex sha, since `git fetch` of an abbreviated sha
    fails, and no ref (a branch name moves and may be private)."""
    bad = []
    for name, a in lock.get("arm", {}).items():
        if a.get("kind") != "go-source" or a.get("adapter") != "pig":
            continue
        if a.get("repo") != "self":
            bad.append(f"{name}: repo must be \"self\", is {a.get('repo')!r}")
        if not re.fullmatch(r"[0-9a-f]{40}", a.get("commit", "")):
            bad.append(f"{name}: commit must be a full 40-hex sha, is {a.get('commit')!r}")
        if "ref" in a:
            bad.append(f"{name}: no ref for a self arm, is {a['ref']!r}")
    return bad


def check_evals_lock():
    bad = self_arm_problems(LOCK)
    if bad:
        sys.exit("arms.lock:\n  " + "\n  ".join(bad))
    if EVALS_LOCK.exists():
        bad = evals_mismatches(LOCK, json.loads(EVALS_LOCK.read_text()))
        if bad:
            sys.exit("arms.lock disagrees with bench/evals/arms.lock:\n  " + "\n  ".join(bad))


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while chunk := f.read(1 << 22):
            h.update(chunk)
    return h.hexdigest()


def fetch(url: str, dest: Path, timeout=600) -> Path:
    dest.parent.mkdir(parents=True, exist_ok=True)
    if not dest.exists():
        tmp = dest.with_suffix(dest.suffix + ".part")
        with urllib.request.urlopen(url, timeout=timeout) as r, open(tmp, "wb") as f:
            shutil.copyfileobj(r, f)
        tmp.rename(dest)
    return dest


def verified(url: str, want: str, cache: Path, checksums: str | None = None) -> Path:
    path = fetch(url, cache / want[:12] / Path(url).name)
    got = sha256(path)
    if got != want:
        sys.exit(f"CHECKSUM MISMATCH {url}: lock {want}, got {got}")
    if checksums:  # the project's own SHA256SUMS must list the same hash for this file
        sums = fetch(checksums, cache / "sums" / hashlib.sha1(checksums.encode()).hexdigest() / Path(checksums).name).read_text()
        if not any(line.split()[0] == want and line.split()[-1].lstrip("*") == Path(url).name for line in sums.splitlines() if line.strip()):
            sys.exit(f"{url}: {checksums} does not list {want} for {Path(url).name}")
    return path


def run(cmd, **kw):
    print("+", " ".join(map(str, cmd)), flush=True)
    subprocess.run(cmd, check=True, timeout=kw.pop("timeout", 3600), **kw)


def unpack(archive: Path, into: Path):
    if into.exists():
        shutil.rmtree(into)
    into.mkdir(parents=True)
    if archive.suffix == ".zip":
        with zipfile.ZipFile(archive) as z:
            z.extractall(into)
        for p in into.rglob("*"):
            if p.is_file() and p.name in ("bun",):
                p.chmod(0o755)
    else:
        with tarfile.open(archive) as t:
            t.extractall(into, filter="data")


def bun_exe(root: Path) -> Path:
    return root / "tool" / "bun" / LOCK["tool"]["bun"]["exe"]


def install_tool(root: Path, cache: Path):
    t = LOCK["tool"]["bun"]
    unpack(verified(t["url"], t["sha256"], cache, t["checksums"]), root / "tool" / "bun")
    print("bun", subprocess.run([bun_exe(root), "--version"], capture_output=True, text=True).stdout.strip())


def install_arm(name: str, root: Path, cache: Path):
    a = LOCK["arm"][name]
    d = root / "arms" / name
    kind = a["kind"]
    if kind in ("tar", "zip"):
        unpack(verified(a["url"], a["sha256"], cache, a.get("checksums")), d)
    elif kind == "file":
        d.mkdir(parents=True, exist_ok=True)
        shutil.copy(verified(a["url"], a["sha256"], cache, a.get("checksums")), d / a["exe"])
        (d / a["exe"]).chmod(0o755)
    elif kind == "npm-lock":
        if d.exists():
            shutil.rmtree(d)
        d.mkdir(parents=True)
        for src, key in (("pi-coding-agent-install-package.json", "package_json_sha256"), ("pi-coding-agent-install-package-lock.json", "lock_sha256")):
            p = verified(a["base"] + src, a[key], cache, a["checksums"])
            shutil.copy(p, d / ("package.json" if "lock" not in src else "package-lock.json"))
        env = {**os.environ, "npm_config_cache": str(cache / "npm")}
        run(["npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=d, env=env, timeout=900)
    elif kind == "bun-compile":
        pkg = root / "arms" / a["from"] / "node_modules/@earendil-works/pi-coding-agent"
        tmpl = root / "arms" / a["asset_template"]
        if not pkg.exists() or not tmpl.exists():
            sys.exit(f"{name} needs {a['from']} and {a['asset_template']} installed first")
        if d.exists():
            shutil.rmtree(d)
        shutil.copytree(tmpl / "pi", d, ignore=shutil.ignore_patterns("pi"))
        run([bun_exe(root), "build", "--compile", "--no-compile-autoload-bunfig", "--no-compile-autoload-dotenv", "--target=bun-linux-x64",
             "--bytecode", "--format=esm", "./dist/bun/cli.js", "./dist/utils/image-resize-worker.js", "./dist/extensions/codemode/worker.js",
             "--outfile", str(d / "pi")], cwd=pkg)
    elif kind == "go-source":
        src = root / "src" / name
        repo = a["repo"]
        if repo == "self":
            repo = subprocess.run(["git", "-C", str(HERE), "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=True, timeout=60).stdout.strip()
        if not src.exists():
            run(["git", "clone", "--quiet", repo, str(src)])
        run(["git", "fetch", "--quiet", repo, a.get("ref", a["commit"])], cwd=src)
        run(["git", "checkout", "--quiet", "--detach", a["commit"]], cwd=src)
        d.mkdir(parents=True, exist_ok=True)
        if a.get("make"):
            run(["git", "submodule", "update", "--init", "--depth", "1"], cwd=src)
            tmp = root / "tmp" / name
            tmp.mkdir(parents=True, exist_ok=True)
            run(["make", a["make"], "GOTOOLCHAIN=local", f"PROJECT_TMP_BASE={tmp}", "CPU_SET=0-15", "CPU_PROCS=8", "CPU_NICE=5", f"BIN_DIR={d}"],
                cwd=src, env={**os.environ, "PATH": f"{bun_exe(root).parent}:{os.environ['PATH']}"}, timeout=3600)
        else:
            ld = f'{a.get("ldflags", "")} -X main.Build={a["commit"][:10]}'.strip()
            run(["go", "build", "-buildvcs=false", "-trimpath", "-ldflags", ld, "-o", str(d / "pig"), a["package"]], cwd=src,
                env={**os.environ, "CGO_ENABLED": "0"}, timeout=3600)
    else:
        sys.exit(f"{name}: unknown kind {kind}")
    (d / ".lock-sha").write_text(json.dumps(a, sort_keys=True))


def exe_path(name: str, root: Path) -> Path:
    a = LOCK["arm"][name]
    d = root / "arms" / name
    if a["kind"] == "go-source":
        return d / ("gi" if name == "gi" else "pig")
    if a["kind"] == "bun-compile":
        return d / "pi"
    return d / a.get("exe", "")


def write_launcher(name: str, root: Path):
    a = LOCK["arm"][name]
    (root / "bin").mkdir(parents=True, exist_ok=True)
    ad = a["adapter"]
    if ad == "none":
        return None
    out = root / "bin" / name
    out.write_text((HERE / "adapters" / ad).read_text().replace("@EXE@", str(exe_path(name, root))))
    out.chmod(0o755)
    return out


def command(name: str, root: Path) -> str:
    a = LOCK["arm"][name]
    if a["kind"] == "npm-lock":
        return f"node {root}/arms/{name}/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"
    return str(write_launcher(name, root) or exe_path(name, root))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cmd", choices=["install", "specs", "record", "check"])
    ap.add_argument("--root", type=Path)
    ap.add_argument("--arm", action="append")
    ap.add_argument("--cache", type=Path)
    a = ap.parse_args()
    if a.cmd == "check":
        check_evals_lock()
        print("arms.lock agrees with bench/evals/arms.lock" if EVALS_LOCK.exists() else "no bench/evals/arms.lock to check against")
        return
    if a.root is None:
        ap.error("--root is required")
    root = a.root.resolve()
    arms = a.arm or [n for n in LOCK["arm"]]
    cache = (a.cache or root / "dl").resolve()
    if a.cmd == "install":
        check_evals_lock()
        if not bun_exe(root).exists():
            install_tool(root, cache)
        order = sorted(arms, key=lambda n: LOCK["arm"][n]["kind"] == "bun-compile")  # bun-compile needs its inputs first
        for n in order:
            install_arm(n, root, cache)
            write_launcher(n, root)
    elif a.cmd == "specs":
        for n in arms:
            print(f"{n}={command(n, root)}")
    else:
        rec = {}
        for n in arms:
            m = LOCK["arm"][n]
            e = exe_path(n, root) if m["kind"] != "npm-lock" else None
            rec[n] = {"version": m.get("version"), "label": m["label"], "kind": m["kind"], "sha256_artifact": m.get("sha256") or m.get("lock_sha256"),
                      "source": m.get("url") or m.get("repo") or m.get("base") or (f"bun build --compile of {m['from']} with Bun {LOCK['tool']['bun']['version']}" if m.get("from") else None), "commit": m.get("commit"),
                      "binary_sha256": sha256(e) if e and e.exists() else None}
        rec["suite"] = LOCK["suite"]
        rec["bun"] = LOCK["tool"]["bun"]["version"]
        print(json.dumps(rec, indent=1))


if __name__ == "__main__":
    main()
