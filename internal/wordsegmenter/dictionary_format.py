"""The current dictionary index shape, shared by generate_dictionary.py and generate_sea_dictionary.py.

One current unversioned shape: a little-endian uint32 count; for a byte-keyed index (the Southeast Asian dictionaries),
257 little-endian uint24 first-byte entries, entry b being the index of the first word whose first byte is at least b
(entry 256 is the count); one uint24 per block of 16 words, the offset of the block's first word in the word bytes; one
length byte per word; one weight byte per word when weights are given; then the concatenated words, in byte-lexical
order. A word's start is its block offset plus the lengths of the words before it in its block, so a lookup adds at most
15 bytes and the index carries about a fifth of a byte of offset per word where one uint32 per word would carry four.
The first-byte table lets the byte walk of a Southeast Asian lookup start in the words of one first byte, not in the
whole dictionary. A uint24 holds every offset and index: the largest dictionary has 3.3 million word bytes and 316
thousand words, and the encoder refuses a value that does not fit.
"""
import struct

BLOCK = 16
FIRST_BYTES = 257
UINT24 = 1 << 24


def pack24(values):
    if any(not 0 <= value < UINT24 for value in values):
        raise SystemExit("dictionary offset exceeds uint24")
    return b"".join(value.to_bytes(3, "little") for value in values)


def unpack24(encoded, at, count):
    return [int.from_bytes(encoded[at + 3 * i : at + 3 * i + 3], "little") for i in range(count)]


def first_byte_table(words):
    table = []
    index = 0
    for byte in range(FIRST_BYTES):
        while index < len(words) and words[index][0] < byte:
            index += 1
        table.append(index)
    return table


def encode(words, weights=None, keyed=False):
    if weights is not None and len(weights) != len(words):
        raise SystemExit("every word needs one weight")
    if any(not 0 < len(word) <= 255 for word in words):
        raise SystemExit("dictionary word length is outside the one-byte range")
    if any(not 0 <= cost <= 255 for cost in weights or ()):
        raise SystemExit("dictionary weight is outside ICU byte cost range")
    blocks = []
    position = 0
    for index, word in enumerate(words):
        if index % BLOCK == 0:
            blocks.append(position)
        position += len(word)
    if len(words) >= UINT24 or position >= UINT24:
        raise SystemExit("dictionary offsets exceed uint24")
    encoded = struct.pack("<I", len(words))
    if keyed:
        encoded += pack24(first_byte_table(words))
    encoded += pack24(blocks)
    encoded += bytes(len(word) for word in words)
    if weights is not None:
        encoded += bytes(weights)
    return encoded + b"".join(words)


def decode(encoded, weighted, keyed=False):
    """Return (words, weights) of an index; weights is None when the index has none."""
    (count,) = struct.unpack_from("<I", encoded, 0)
    blocks_at = 4
    if keyed:
        table = unpack24(encoded, 4, FIRST_BYTES)
        blocks_at += 3 * FIRST_BYTES
    block_count = (count + BLOCK - 1) // BLOCK
    blocks = unpack24(encoded, blocks_at, block_count)
    lengths_at = blocks_at + 3 * block_count
    weights_at = lengths_at + count
    words_at = weights_at + (count if weighted else 0)
    words = []
    for index in range(count):
        if index % BLOCK == 0:
            start = blocks[index // BLOCK]
        length = encoded[lengths_at + index]
        words.append(encoded[words_at + start : words_at + start + length])
        start += length
    if keyed and table != first_byte_table(words):
        raise SystemExit("first-byte table does not match the words")
    return words, (list(encoded[weights_at:words_at]) if weighted else None)
