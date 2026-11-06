"""The current dictionary index shape, shared by generate_dictionary.py and generate_sea_dictionary.py.

One current unversioned shape: a little-endian uint32 count; one uint32 per block of 16 words, the offset of the block's
first word in the word bytes; one length byte per word; one weight byte per word when weights are given; then the
concatenated words, in byte-lexical order. A word's start is its block offset plus the lengths of the words before it in
its block, so a lookup adds at most 15 bytes and the index carries a quarter byte of offset per word where one uint32
per word would carry four.
"""
import struct

BLOCK = 16


def encode(words, weights=None):
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
    if position > 0xFFFFFFFF or len(words) > 0xFFFFFFFF:
        raise SystemExit("dictionary offsets exceed uint32")
    encoded = struct.pack("<I", len(words)) + struct.pack("<" + "I" * len(blocks), *blocks)
    encoded += bytes(len(word) for word in words)
    if weights is not None:
        encoded += bytes(weights)
    return encoded + b"".join(words)


def decode(encoded, weighted):
    """Return (words, weights) of an index; weights is None when the index has none."""
    (count,) = struct.unpack_from("<I", encoded, 0)
    block_count = (count + BLOCK - 1) // BLOCK
    blocks = struct.unpack_from("<" + "I" * block_count, encoded, 4)
    lengths_at = 4 + 4 * block_count
    weights_at = lengths_at + count
    words_at = weights_at + (count if weighted else 0)
    words = []
    for index in range(count):
        if index % BLOCK == 0:
            start = blocks[index // BLOCK]
        length = encoded[lengths_at + index]
        words.append(encoded[words_at + start : words_at + start + length])
        start += length
    return words, (list(encoded[weights_at:words_at]) if weighted else None)
