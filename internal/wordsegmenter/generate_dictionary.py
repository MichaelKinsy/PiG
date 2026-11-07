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
# The embedded trie of dictionary_format.py: one UTF-16 unit per label. Every word is BMP text without U+FFFD, so a lookup
# that reads the text as UTF-16 units misses no word and invents none (invalid UTF-8 and supplementary characters match nothing).
entries = []
for word, cost in rows:
    text = word.decode("utf-8")
    if any(ord(c) > 0xFFFF or c == "\ufffd" for c in text):
        raise SystemExit("dictionary word outside the BMP or holding U+FFFD")
    entries.append((tuple(ord(c) for c in text), cost))
encoded = dictionary_format.encode_trie(entries, 2, True)
root = pathlib.Path(__file__).resolve().parent
dictionary_format.write_flat(root / "testdata" / "cjk_flat.bin.gz", [word for word, _ in rows], [cost for _, cost in rows])
(root / "cjk_dictionary.bin").write_bytes(encoded)
(root.parent.parent / "LICENSES" / "LicenseRef-ICU-CJK.txt").write_text("\n".join(license_lines).strip() + "\n", encoding="utf-8")
print(f"{len(rows)} dictionary entries, {len(encoded)} bytes, source sha256:{SOURCE_HASH}")
