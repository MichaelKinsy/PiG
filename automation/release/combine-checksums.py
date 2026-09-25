#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Combine per-archive SHA-256 digests into one release SHA256SUMS.

release-candidate.yml builds one archive per target in its own matrix job and
writes a separate SHA256SUMS beside it (evidence for that job only).
`install.sh` and the hosting Worker's installer route need one combined
SHA256SUMS that lists every release archive exactly once, in the exact format
`sha256sum` writes and `sha256sum -c` and `install.sh`'s `expected_sha256`
awk parser both accept: a lowercase 64-hex digest, two spaces, then the bare
file name, one line per archive, sorted by name.

This script does not trust any digest it did not compute itself: it hashes
every archive in DIRECTORY directly, writes DIRECTORY/SHA256SUMS, then reads
that file back and re-verifies each digest against the file on disk.
"""

from __future__ import annotations

import argparse
import hashlib
import pathlib
import sys

ARCHIVE_SUFFIXES = (".tar.gz", ".zip")


def sha256_of(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def find_archives(directory: pathlib.Path) -> list[pathlib.Path]:
    archives = sorted(
        (path for path in directory.iterdir() if path.is_file() and path.name.endswith(ARCHIVE_SUFFIXES)),
        key=lambda path: path.name,
    )
    if not archives:
        raise SystemExit(f"combine-checksums: no archives ({' or '.join(ARCHIVE_SUFFIXES)}) in {directory}")
    return archives


def write_sums(sums_path: pathlib.Path, archives: list[pathlib.Path]) -> None:
    lines = [f"{sha256_of(path)}  {path.name}\n" for path in archives]
    # Bytes, not text mode: text mode writes CRLF on Windows, and
    # sha256sum -c and install.sh read the file name up to the line end.
    sums_path.write_bytes("".join(lines).encode("utf-8"))


def verify(directory: pathlib.Path, sums_path: pathlib.Path) -> list[str]:
    problems = []
    seen: set[str] = set()
    for number, line in enumerate(sums_path.read_text(encoding="utf-8").splitlines(), 1):
        parts = line.split(maxsplit=1)
        if len(parts) != 2:
            problems.append(f"SHA256SUMS:{number}: malformed line {line!r}")
            continue
        digest, name = parts
        if name in seen:
            problems.append(f"SHA256SUMS:{number}: {name} is listed more than once")
        seen.add(name)
        path = directory / name
        if not path.is_file():
            problems.append(f"{name}: listed in SHA256SUMS but missing from {directory}")
            continue
        actual = sha256_of(path)
        if actual != digest:
            problems.append(f"{name}: checksum mismatch: SHA256SUMS says {digest}, disk has {actual}")
    return problems


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("directory", type=pathlib.Path, help="directory holding the release archives")
    args = parser.parse_args()
    directory = args.directory.resolve()
    if not directory.is_dir():
        print(f"combine-checksums: {directory} is not a directory", file=sys.stderr)
        return 2
    archives = find_archives(directory)
    sums_path = directory / "SHA256SUMS"
    write_sums(sums_path, archives)
    problems = verify(directory, sums_path)
    if problems:
        for problem in problems:
            print(f"combine-checksums: {problem}", file=sys.stderr)
        return 1
    for path in archives:
        print(f"combine-checksums: verified {path.name}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
