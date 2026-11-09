#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Fetch the pinned pig-acp source for a PiG release.

pig-acp is built from the `@pi-in-go/pigpen-acp` Pigpen Package, the same
source `pig acp install` builds. automation/release/pig-acp.lock pins that
Package by version, tarball URL, exact byte size and npm sha512 integrity. This
script downloads the tarball (https only, never more than the pinned size),
verifies its size and digest, checks that package.json names the pinned package
and version, extracts it without following links outside the destination, and
prints the directory that holds pig-acp's go.mod.

    pig-acp-source.py [--lock FILE] DEST
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import io
import json
import pathlib
import sys
import tarfile
import urllib.request

SOURCE_REL = pathlib.PurePosixPath("extensions/acp/cmd/pig-acp")
LOCK_KEYS = ("package", "version", "tarball", "integrity", "size")


class SourceError(Exception):
    pass


def read_lock(path: pathlib.Path) -> dict:
    lock = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(lock, dict) or set(lock) != set(LOCK_KEYS):
        raise SourceError(f"{path}: expected exactly the keys {', '.join(LOCK_KEYS)}")
    for key in LOCK_KEYS:
        if key == "size":
            if type(lock[key]) is not int or lock[key] <= 0:
                raise SourceError(f"{path}: size must be a positive integer")
        elif not isinstance(lock[key], str) or not lock[key]:
            raise SourceError(f"{path}: {key} must be a non-empty string")
    if not lock["tarball"].startswith("https://"):
        raise SourceError(f"{path}: tarball must be an https URL")
    if not lock["integrity"].startswith("sha512-"):
        raise SourceError(f"{path}: integrity must be an npm sha512 digest")
    return lock


def download(url: str, size: int) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": "pig-release"})
    with urllib.request.urlopen(request, timeout=60) as response:
        declared = response.headers.get("Content-Length")
        if declared is not None and declared != str(size):
            raise SourceError(f"{url}: Content-Length {declared} is not the pinned size {size}")
        data = response.read(size + 1)
    return data


def verify(data: bytes, lock: dict) -> None:
    if len(data) != lock["size"]:
        raise SourceError(f"{lock['tarball']}: {len(data)} bytes, pinned size is {lock['size']}")
    want = lock["integrity"].removeprefix("sha512-")
    got = base64.b64encode(hashlib.sha512(data).digest()).decode()
    if got != want:
        raise SourceError(f"{lock['tarball']}: sha512 integrity mismatch (got sha512-{got})")


def extract(data: bytes, dest: pathlib.Path) -> None:
    dest.mkdir(parents=True, exist_ok=True)
    root = dest.resolve()
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        members = []
        for member in archive.getmembers():
            parts = pathlib.PurePosixPath(member.name).parts
            if not parts or parts[0] != "package" or ".." in parts or member.name.startswith("/"):
                raise SourceError(f"unsafe tar member {member.name!r}")
            if not (member.isfile() or member.isdir()):
                raise SourceError(f"tar member {member.name!r} is not a regular file or directory")
            if len(parts) == 1:
                continue
            member.name = pathlib.PurePosixPath(*parts[1:]).as_posix()
            target = (root / member.name).resolve()
            if root not in target.parents:
                raise SourceError(f"tar member {member.name!r} leaves the destination")
            members.append(member)
        archive.extractall(root, members=members, filter="data")


def fetch(lock: dict, dest: pathlib.Path) -> pathlib.Path:
    data = download(lock["tarball"], lock["size"])
    verify(data, lock)
    extract(data, dest)
    manifest = json.loads((dest / "package.json").read_text(encoding="utf-8"))
    if manifest.get("name") != lock["package"] or manifest.get("version") != lock["version"]:
        raise SourceError(f"package.json is {manifest.get('name')}@{manifest.get('version')}, pinned {lock['package']}@{lock['version']}")
    source = dest.joinpath(*SOURCE_REL.parts)
    if not (source / "go.mod").is_file():
        raise SourceError(f"{lock['package']} ships no {SOURCE_REL}/go.mod")
    return source


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--lock", type=pathlib.Path, default=pathlib.Path(__file__).with_name("pig-acp.lock"))
    parser.add_argument("dest", type=pathlib.Path, help="empty directory to extract the Package into")
    args = parser.parse_args()
    try:
        print(fetch(read_lock(args.lock), args.dest))
    except (SourceError, OSError, ValueError, tarfile.TarError) as error:
        print(f"pig-acp-source: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
