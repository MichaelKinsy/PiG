#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT

import json
import sys
from pathlib import Path


def check(path: Path):
    document = json.loads(path.read_text(encoding="utf-8"))
    failures = []
    for package_path, package in document.get("packages", {}).items():
        if not package_path or package.get("link"):
            continue
        if not package.get("version"):
            failures.append(f"{path}: {package_path} has no version")
        if not package.get("resolved"):
            failures.append(f"{path}: {package_path} has no resolved URL")
        if not package.get("integrity"):
            failures.append(f"{path}: {package_path} has no integrity digest")
    return failures


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: check-npm-lock-integrity.py <package-lock.json> [...]", file=sys.stderr)
        return 2
    failures = [failure for name in sys.argv[1:] for failure in check(Path(name))]
    if failures:
        print("\n".join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
