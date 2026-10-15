#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Verify published nested-module tags and checksums without workspace/cache assistance."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def command(args, cwd, env):
    result = subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True, timeout=60)
    if result.returncode:
        raise ValueError(f"{' '.join(args)} failed:\n{result.stdout}{result.stderr}")
    return result.stdout


def check(root: Path, remote: str):
    # Stock disposition: required substrate; verify public Go dependency publication independently of workspace builds.
    env = dict(os.environ, GOWORK="off", GOFLAGS="", GIT_TERMINAL_PROMPT="0")
    manifest = json.loads(command(["go", "mod", "edit", "-json"], root, env))
    if manifest.get("Replace") or manifest.get("Exclude"):
        raise ValueError("root go.mod has replace or exclude directives; go install pkg@version refuses them")
    prefix = manifest["Module"]["Path"] + "/"
    requirements = [r for r in manifest.get("Require") or [] if r["Path"].startswith(prefix)]
    if not requirements:
        return
    tags = [r["Path"][len(prefix):] + "/" + r["Version"] for r in requirements]
    refs = command(["git", "ls-remote", "--refs", remote, *["refs/tags/" + t for t in tags]], root, env)
    published = {line.split()[1] for line in refs.splitlines()}
    missing = [tag for tag in tags if "refs/tags/" + tag not in published]
    if missing:
        raise ValueError("unpublished nested-module tag(s): " + ", ".join(missing) +
                         "; publish the approved SDK release before merging its requirement to main")

    sums = set((root / "go.sum").read_text().splitlines())
    # Downloads read a copy of the consumer's checksums and cannot repair the checkout or reuse a cached SDK.
    with tempfile.TemporaryDirectory(prefix="pig-module-publication-") as directory:
        scratch = Path(directory)
        for name in ("go.mod", "go.sum"):
            shutil.copyfile(root / name, scratch / name)
        env["GOMODCACHE"] = str(scratch / "cache")
        for requirement, tag in zip(requirements, tags):
            path, version = requirement["Path"], requirement["Version"]
            result = json.loads(command(["go", "mod", "download", "-json", path + "@" + version], scratch, env))
            for suffix, key in (("", "Sum"), ("/go.mod", "GoModSum")):
                expected = f"{path} {version}{suffix} {result[key]}"
                if expected not in sums:
                    raise ValueError("go.sum does not pin the published module: " + expected)
            print(f"published: {tag}; Go download and committed checksums verified")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--remote", default="https://github.com/MichaelKinsy/PiG.git")
    args = parser.parse_args()
    try:
        check(args.root.resolve(), args.remote)
    except (OSError, ValueError, KeyError, subprocess.TimeoutExpired) as error:
        sys.exit(f"module-publication: {error}")
