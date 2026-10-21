#!/usr/bin/env python3
"""Build the current CJK dictionary index from ICU 78.3's exact source data."""
import hashlib
import pathlib
import struct
import sys

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
offsets = [0]
for word, cost in rows:
    if not 0 <= cost <= 255:
        raise SystemExit("dictionary weight is outside ICU byte cost range")
    offsets.append(offsets[-1] + len(word))
if offsets[-1] > 0xffffffff or len(rows) > 0xffffffff:
    raise SystemExit("dictionary offsets exceed uint32")
# One current unversioned shape: uint32 count, count+1 uint32 offsets,
# count byte weights, then concatenated UTF-8 words, in byte-lexical order.
encoded = struct.pack("<I", len(rows))
encoded += struct.pack("<" + "I" * len(offsets), *offsets)
encoded += bytes(cost for _, cost in rows)
encoded += b"".join(word for word, _ in rows)
root = pathlib.Path(__file__).resolve().parent
(root / "cjk_dictionary.bin").write_bytes(encoded)
(root.parent.parent / "LICENSES" / "LicenseRef-ICU-CJK.txt").write_text("\n".join(license_lines).strip() + "\n", encoding="utf-8")
print(f"{len(rows)} dictionary entries, {len(encoded)} bytes, source sha256:{SOURCE_HASH}")
