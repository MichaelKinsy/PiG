# SPDX-License-Identifier: MIT
"""PiG's npm package identity, read from the one place that defines it.

`PackageName` in internal/codingagent/paths.go is Pi's PACKAGE_NAME: the npm
package a global install owns and the package the signed update manifest names.
The manifest generator and the npm packer read it from there, so the manifest
cannot direct `npm install -g` at another package.
"""

from __future__ import annotations

import pathlib
import re

PATHS_GO = pathlib.Path(__file__).resolve().parents[2] / "internal" / "codingagent" / "paths.go"


def package_name(path: pathlib.Path = PATHS_GO) -> str:
    match = re.search(r'^const PackageName = "([^"]+)"$', path.read_text(encoding="utf-8"), re.M)
    if match is None:
        raise SystemExit(f"{path} does not define const PackageName")
    return match.group(1)
