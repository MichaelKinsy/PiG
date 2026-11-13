# SPDX-License-Identifier: MIT
"""The backports script's own code with a fixed set of entries, for the tests.

`make pin-bump` retires the entries the target Pi already holds, so the committed script can have any number of entries, none after a bump.
The tests of the script's machinery (classifying, applying, retiring blocks) therefore never read the committed entries: they run the committed
code of the script with the entries of testdata/backport-entries.mjs.txt (Pi's own text at the release they were written for; the fixture does not follow the pin) spliced in place of the committed ones.
"""
from __future__ import annotations

import atexit
import re
import shutil
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import carried  # noqa: E402
import oracle  # noqa: E402

ENTRIES = Path(__file__).with_name("testdata") / "backport-entries.mjs.txt"
_script: Path | None = None


def text(entries: str | None = None) -> str:
    """The committed script's machinery with `entries` (default: the fixture's) in place of the committed ones."""
    committed = oracle.BACKPORTS.read_text(encoding="utf-8")
    bare = carried.retire_backport_blocks(committed, list(dict.fromkeys(oracle.backport_ids(committed))))
    anchor = re.search(r"^const packageFile = .*\n", bare, re.M)
    if not anchor:
        raise AssertionError("the backports script has no `const packageFile = ...` helper line to splice the fixture entries after")
    return bare[:anchor.end()] + "\n" + (ENTRIES.read_text(encoding="utf-8") if entries is None else entries) + bare[anchor.end():]


def script() -> Path:
    """A file with the committed script's machinery and the fixture entries (one per process)."""
    global _script
    if _script is None:
        directory = Path(tempfile.mkdtemp())
        atexit.register(shutil.rmtree, directory, True)
        _script = directory / "pi-post-release-backports.mjs"
        _script.write_text(text(), encoding="utf-8")
    return _script
