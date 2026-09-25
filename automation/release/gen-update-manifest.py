#!/usr/bin/env python3
r"""Generate a Pig self-update manifest from a release directory.

Host the output on any static site (GitHub Pages, S3, a marketplace) and point
Pig at it with PIG_UPDATE_URL.

The exact bytes this emits must be signed before publication. The manifest
matches what pig's self-update reads (internal/codingagent D39):

    {"version": "<v>", "packageName": "pig", "notes": "...",
     "binaries": {"<goos>/<goarch>": {"url": "<base>/<file>", "sha256": "<hex>"}}}

Binaries are matched by the release naming pig uses: pig-<goos>-<goarch>
(and pig-<goos>-<goarch>.exe on Windows).

Usage:
    gen-update-manifest.py --version 0.2.0 --base-url https://ORG.github.io/pig \\
        --dir ./release-binaries [--notes "See the changelog."] > update.json
"""
from __future__ import annotations

import argparse
import hashlib
import ipaddress
import json
import re
import sys
from pathlib import Path
from urllib.parse import urlparse

# GOOS/GOARCH releases produced for stock Pig.
PLATFORMS = [
    ("linux", "amd64"),
    ("linux", "arm64"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
    ("windows", "amd64"),
]

_VERSION_RE = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$"
)
_PACKAGE_RE = re.compile(r"^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$")


def _valid_version(version: str) -> bool:
    match = _VERSION_RE.fullmatch(version)
    if match is None:
        return False
    prerelease = match.group(4)
    if prerelease is None:
        return True
    return all(
        not (identifier.isdigit() and len(identifier) > 1 and identifier.startswith("0"))
        for identifier in prerelease.split(".")
    )


def _valid_base_url(raw: str, *, allow_loopback_http: bool) -> bool:
    parsed = urlparse(raw)
    if not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        return False
    if parsed.scheme == "https":
        return True
    if parsed.scheme != "http" or not allow_loopback_http:
        return False
    if parsed.hostname.lower() == "localhost":
        return True
    try:
        return ipaddress.ip_address(parsed.hostname).is_loopback
    except ValueError:
        return False


def _binary_name(goos: str, goarch: str) -> str:
    name = f"pig-{goos}-{goarch}"
    return f"{name}.exe" if goos == "windows" else name


def main() -> int:
    """Validate release inputs and write the current manifest shape to stdout."""
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--version", required=True, help="release version, e.g. 0.2.0")
    ap.add_argument("--base-url", required=True, help="URL the binaries are served from (no trailing slash)")
    ap.add_argument("--dir", required=True, type=Path, help="directory containing the pig-<os>-<arch> binaries")
    ap.add_argument("--package-name", default="pig", help="signed package-manager release identity")
    ap.add_argument(
        "--allow-loopback-http",
        action="store_true",
        help="allow HTTP binary URLs only when the base host is loopback",
    )
    ap.add_argument("--notes", default="", help="optional release note shown in the update banner")
    args = ap.parse_args()

    if not _valid_version(args.version):
        print(f"invalid version: {args.version!r}", file=sys.stderr)
        return 2
    if not _PACKAGE_RE.fullmatch(args.package_name):
        print(f"invalid package name: {args.package_name!r}", file=sys.stderr)
        return 2
    if not _valid_base_url(
        args.base_url,
        allow_loopback_http=args.allow_loopback_http,
    ):
        print("base URL must use HTTPS; loopback HTTP requires --allow-loopback-http", file=sys.stderr)
        return 2
    base = args.base_url.rstrip("/")

    binaries: dict[str, dict[str, str]] = {}
    for goos, goarch in PLATFORMS:
        path = args.dir / _binary_name(goos, goarch)
        if not path.is_file():
            continue
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        binaries[f"{goos}/{goarch}"] = {"url": f"{base}/{path.name}", "sha256": digest}

    if not binaries:
        print(f"no pig-<os>-<arch> binaries found in {args.dir}", file=sys.stderr)
        return 1

    manifest: dict[str, object] = {
        "version": args.version,
        "packageName": args.package_name,
        "binaries": binaries,
    }
    if args.notes.strip():
        manifest["notes"] = args.notes.strip()
    json.dump(manifest, sys.stdout, indent=2, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
