#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Verify published nested-module tags and checksums without workspace/cache assistance."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


def command(args, cwd, env, timeout=60):
    result = subprocess.run(args, cwd=cwd, env=env, text=True, capture_output=True, timeout=timeout)
    if result.returncode:
        raise ValueError(f"{' '.join(args)} failed:\n{result.stdout}{result.stderr}")
    return result.stdout


# A go mod download of a pin the public proxy has not served yet is 404 after about a minute and then a direct fetch of every branch and
# tag of the repository: 55 s with the machine to itself and 154 s under load, measured against github.com/MichaelKinsy/PiG on 2026-11-12.
# The limit covers both with a wide margin; the other commands stay at 60 s.
DOWNLOAD_TIMEOUT = 600


def proxy_info_url(path, version):
    escaped = "".join("!" + c.lower() if c.isupper() else c for c in path)
    return f"https://proxy.golang.org/{escaped}/@v/{version}.info"


# The shape of a Go pseudo-version (golang.org/x/mod/module.IsPseudoVersion): the commit a requirement names instead of a tag.
PSEUDO_VERSION = re.compile(r"^v[0-9]+\.(?:0\.0-|[0-9]+\.[0-9]+-(?:[^+]*\.)?0\.)[0-9]{14}-[0-9a-f]{12}$")


def check(root: Path, remote: str, download_timeout: int = DOWNLOAD_TIMEOUT):
    # Stock disposition: required substrate; verify public Go dependency publication independently of workspace builds.
    env = dict(os.environ, GOWORK="off", GOFLAGS="", GIT_TERMINAL_PROMPT="0")
    manifest = json.loads(command(["go", "mod", "edit", "-json"], root, env))
    if manifest.get("Replace") or manifest.get("Exclude"):
        raise ValueError("root go.mod has replace or exclude directives; go install pkg@version refuses them")
    prefix = manifest["Module"]["Path"] + "/"
    requirements = [r for r in manifest.get("Require") or [] if r["Path"].startswith(prefix)]
    if not requirements:
        return
    # Between releases main requires a nested module at the pseudo-version of the main commit that holds the API the root uses; the
    # release bumps it to the tag. A pseudo-version has no tag to look up: the download below proves the commit is public.
    tagged = [r for r in requirements if not PSEUDO_VERSION.match(r["Version"])]
    tags = [r["Path"][len(prefix):] + "/" + r["Version"] for r in tagged]
    published = set()
    if tags:
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
        for requirement in requirements:
            path, version = requirement["Path"], requirement["Version"]
            tag = version if PSEUDO_VERSION.match(version) else path[len(prefix):] + "/" + version
            try:
                result = json.loads(command(["go", "mod", "download", "-json", path + "@" + version], scratch, env, download_timeout))
            except subprocess.TimeoutExpired:
                raise ValueError(f"go mod download {path}@{version} did not finish within {download_timeout} s. proxy.golang.org answers a "
                                 "pin it has not fetched yet with 404 after about a minute, and the go command then clones the whole "
                                 "repository directly; the proxy takes up to 30 minutes to serve a new pseudo-version after its commit "
                                 f"is public. Wait and rerun, or poll {proxy_info_url(path, version)} until it answers 200") from None
            for suffix, key in (("", "Sum"), ("/go.mod", "GoModSum")):
                expected = f"{path} {version}{suffix} {result[key]}"
                if expected not in sums:
                    raise ValueError("go.sum does not pin the published module: " + expected)
            print(f"published: {tag}; Go download and committed checksums verified")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--remote", default="https://github.com/MichaelKinsy/PiG.git")
    parser.add_argument("--download-timeout", type=int, default=DOWNLOAD_TIMEOUT, help="seconds allowed to each go mod download")
    args = parser.parse_args()
    try:
        check(args.root.resolve(), args.remote, args.download_timeout)
    except (OSError, ValueError, KeyError, subprocess.TimeoutExpired) as error:
        sys.exit(f"module-publication: {error}")
