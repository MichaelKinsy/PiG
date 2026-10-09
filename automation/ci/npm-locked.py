#!/usr/bin/env python3
"""Install a locked npm tree only when its manifest content changes.

With PIG_NODE_MODULES_STORE set to a directory, the tree is installed once per
manifest content under that directory and the checkout's node_modules becomes a
symbolic link to it, so every checkout on a host with the same package.json and
package-lock.json shares one tree instead of installing its own copy.
"""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def manifest_digest(directory: Path) -> str:
    digest = hashlib.sha256()
    for name in ("package.json", "package-lock.json"):
        content = (directory / name).read_bytes()
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
    return digest.hexdigest() + "\n"


def npm_ci(directory: Path) -> None:
    # Resolve through PATHEXT: Windows installs npm as npm.cmd, which
    # CreateProcess does not find by its bare name.
    npm = shutil.which("npm") or "npm"
    subprocess.run([npm, "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=directory, check=True)


def ensure_shared(directory: Path, store: Path, expected: str) -> None:
    """Install into store/<name>-<digest> once, under a lock, and link directory/node_modules to it."""
    store.mkdir(parents=True, exist_ok=True)
    entry = store / f"{directory.name}-{expected.strip()[:24]}"
    shared = entry / "node_modules"
    stamp = shared / ".pig-deps-ready"
    if not (stamp.is_file() and stamp.read_text() == expected):
        import fcntl  # POSIX only; the store is a host cache for lane machines

        with open(store / f".{entry.name}.lock", "w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            if not (stamp.is_file() and stamp.read_text() == expected):
                staging = Path(tempfile.mkdtemp(prefix=f".{entry.name}.", dir=store))
                try:
                    for name in ("package.json", "package-lock.json"):
                        shutil.copyfile(directory / name, staging / name)
                    npm_ci(staging)
                    if manifest_digest(directory) != expected or manifest_digest(staging) != expected:
                        raise ValueError("npm manifests changed during install; no dependency stamp written")
                    (staging / "node_modules" / ".pig-deps-ready").write_text(expected)
                    if entry.exists():
                        shutil.rmtree(entry)
                    staging.rename(entry)
                finally:
                    if staging.exists():
                        shutil.rmtree(staging)
    modules = directory / "node_modules"
    if modules.is_symlink() or modules.is_file():
        modules.unlink()
    elif modules.exists():
        shutil.rmtree(modules)
    modules.symlink_to(shared, target_is_directory=True)


def ensure(directory: Path) -> None:
    expected = manifest_digest(directory)
    modules = directory / "node_modules"
    stamp = modules / ".pig-deps-ready"
    if stamp.is_file() and stamp.read_text() == expected:
        return
    store = os.environ.get("PIG_NODE_MODULES_STORE")
    if store:
        ensure_shared(directory, Path(store), expected)
        return
    if modules.absolute() != modules.resolve():
        raise ValueError(f"refusing to install through shared path {modules}; run in its owning checkout {modules.resolve().parent}")
    npm_ci(directory)
    # Never certify content that changed while npm was running.
    if manifest_digest(directory) != expected:
        raise ValueError("npm manifests changed during install; no dependency stamp written")
    stamp.write_text(expected)


if __name__ == "__main__":
    try:
        ensure(Path(sys.argv[1]))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
