#!/usr/bin/env python3
"""Build the current CJK dictionary index from ICU 78.3's exact source data."""
import hashlib
import pathlib
import sys

import dictionary_format

SOURCE_HASH = "e73fd72048981d0cc13e9dc436a7eaba07ffb6eff58c8a59dc75c1df746663a0"
source = pathlib.Path(sys.argv[1]).read_bytes()
if hashlib.sha256(source).hexdigest() != SOURCE_HASH:
    raise SystemExit("expected ICU release-78.3 cjdict.txt")
rows = []
license_lines = []
for line in source.decode("utf-8-sig").splitlines():
    line = line.strip()
    if not line or line.startswith("#"):
        if not rows:
            license_lines.append(line.removeprefix("#").lstrip())
        continue
    word, cost = line.split()
    rows.append((word.encode("utf-8"), int(cost)))
rows.sort()
if len({word for word, _ in rows}) != len(rows):
    raise SystemExit("duplicate dictionary word")
# One current unversioned shape, written by dictionary_format.py: the words in byte-lexical order, indexed by block, with
# one length byte and one weight byte per word.
encoded = dictionary_format.encode([word for word, _ in rows], [cost for _, cost in rows])
root = pathlib.Path(__file__).resolve().parent
(root / "cjk_dictionary.bin").write_bytes(encoded)
(root.parent.parent / "LICENSES" / "LicenseRef-ICU-CJK.txt").write_text("\n".join(license_lines).strip() + "\n", encoding="utf-8")
print(f"{len(rows)} dictionary entries, {len(encoded)} bytes, source sha256:{SOURCE_HASH}")
