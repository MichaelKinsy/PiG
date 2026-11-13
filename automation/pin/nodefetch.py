# SPDX-License-Identifier: MIT
"""The oracle Node: the release the observation oracles are recorded under (ai/testdata/node-microtasks/oracle-node-version).

The goldens record the microtask order of one Node release line (automation/gen/regenerate-observation-oracles.sh names the releases that record identical cases and the first that differs), and the
repository's qualified Node (.node-version) is a later one, so the recording step needs the exact oracle release. No image or host is guaranteed to
have it: this module downloads nodejs.org's tarball for the platform, checks its sha256 against the release's SHASUMS256.txt, unpacks it into the
user cache and checks `node --version`. A cached copy is reused without a request.
"""
from __future__ import annotations

import hashlib
import os
import platform
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
from pathlib import Path
from typing import Callable

sys.path.insert(0, str(Path(__file__).resolve().parent))
from pinlib import ROOT, PinError  # noqa: E402

VERSION_FILE = ROOT / "ai" / "testdata" / "node-microtasks" / "oracle-node-version"
DIST = "https://nodejs.org/dist"


def oracle_version() -> str:
    return VERSION_FILE.read_text(encoding="utf-8").strip()


def platform_name() -> str:
    machine = {"x86_64": "x64", "amd64": "x64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine().lower())
    system = {"linux": "linux", "darwin": "darwin"}.get(sys.platform)
    if machine is None or system is None:
        raise PinError(f"no Node release is fetched for {sys.platform}/{platform.machine()}: install Node v{oracle_version()} and put it first on PATH")
    return f"{system}-{machine}"


CACHE_ENV = "PIG_ORACLE_NODE_CACHE"


def cache_root() -> Path:
    """$PIG_ORACLE_NODE_CACHE, else $XDG_CACHE_HOME/pig/node, else ~/.cache/pig/node."""
    if os.environ.get(CACHE_ENV):
        return Path(os.environ[CACHE_ENV])
    return Path(os.environ.get("XDG_CACHE_HOME") or Path.home() / ".cache") / "pig" / "node"


def download(url: str) -> bytes:
    try:
        with urllib.request.urlopen(url, timeout=300) as response:
            return response.read()
    except OSError as error:
        raise PinError(f"cannot download {url}: {error}; install Node v{oracle_version()} and put it first on PATH") from error


def node_version(binary: Path) -> str:
    try:
        return subprocess.run([str(binary), "--version"], check=True, capture_output=True, text=True, timeout=60).stdout.strip()
    except (OSError, subprocess.SubprocessError):
        return ""


def ensure(version: str | None = None, plat: str | None = None, fetch: Callable[[str], bytes] = download, cache: Path | None = None) -> Path:
    """The bin directory of Node v<version> (default: the oracle Node), fetched and verified when the cache has none."""
    version = version or oracle_version()
    plat = plat or platform_name()
    name = f"node-v{version}-{plat}"
    dest = (cache or cache_root()) / name
    if node_version(dest / "bin" / "node") == f"v{version}":
        return dest / "bin"
    archive = f"{name}.tar.xz"
    sums = fetch(f"{DIST}/v{version}/SHASUMS256.txt").decode("utf-8", "replace")
    want = next((line.split()[0] for line in sums.splitlines() if line.split()[-1:] == [archive]), None)
    if want is None:
        raise PinError(f"{DIST}/v{version}/SHASUMS256.txt lists no {archive}")
    data = fetch(f"{DIST}/v{version}/{archive}")
    got = hashlib.sha256(data).hexdigest()
    if got != want:
        raise PinError(f"{archive}: sha256 {got} is not the {want} that nodejs.org's SHASUMS256.txt lists; nothing was unpacked")
    dest.parent.mkdir(parents=True, exist_ok=True)
    staging = Path(tempfile.mkdtemp(prefix=name + ".", dir=dest.parent))
    try:
        tgz = staging / archive
        tgz.write_bytes(data)
        with tarfile.open(tgz) as tar:
            members = tar.getmembers()
            for member in members:
                if member.name.startswith(("/", "..")) or ".." in Path(member.name).parts:
                    raise PinError(f"{archive} holds an unsafe path {member.name!r}")
            tar.extractall(staging / "unpacked", **({"filter": "data"} if hasattr(tarfile, "data_filter") else {}))
        unpacked = staging / "unpacked" / name
        if node_version(unpacked / "bin" / "node") != f"v{version}":
            raise PinError(f"{archive} unpacks to Node {node_version(unpacked / 'bin' / 'node') or 'that does not run'}, not v{version}")
        if dest.exists():
            shutil.rmtree(dest)
        unpacked.rename(dest)
    finally:
        shutil.rmtree(staging, ignore_errors=True)
    return dest / "bin"
