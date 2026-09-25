#!/usr/bin/env python3
"""Install a locked npm tree only when its manifest content changes."""
import hashlib
from pathlib import Path
import shutil
import subprocess
import sys


def manifest_digest(directory: Path) -> str:
    digest = hashlib.sha256()
    for name in ("package.json", "package-lock.json"):
        content = (directory / name).read_bytes()
        digest.update(len(content).to_bytes(8, "big"))
        digest.update(content)
    return digest.hexdigest() + "\n"


def ensure(directory: Path) -> None:
    expected = manifest_digest(directory)
    modules = directory / "node_modules"
    stamp = modules / ".pig-deps-ready"
    if stamp.is_file() and stamp.read_text() == expected:
        return
    if modules.absolute() != modules.resolve():
        raise ValueError(f"refusing to install through shared path {modules}; run in its owning checkout {modules.resolve().parent}")
    # Resolve through PATHEXT: Windows installs npm as npm.cmd, which
    # CreateProcess does not find by its bare name.
    npm = shutil.which("npm") or "npm"
    subprocess.run([npm, "ci", "--ignore-scripts", "--no-audit", "--no-fund"], cwd=directory, check=True)
    # Never certify content that changed while npm was running.
    if manifest_digest(directory) != expected:
        raise ValueError("npm manifests changed during install; no dependency stamp written")
    stamp.write_text(expected)


if __name__ == "__main__":
    try:
        ensure(Path(sys.argv[1]))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
