"""The dictionary shapes, shared by generate_dictionary.py and generate_sea_dictionary.py.

The embedded shape is the trie of encode_trie, documented below. The flat shape (encode, decode) is the test oracle: the
sorted-array index the package embedded before the trie, written gzipped to testdata/ so word_trie_test.go can compare every
word and prefix of the trie with it. The flat shape is one current unversioned shape: a little-endian uint32 count; one uint32 per block of 16 words, the offset of the block's
first word in the word bytes; one length byte per word; one weight byte per word when weights are given; then the
concatenated words, in byte-lexical order. A word's start is its block offset plus the lengths of the words before it in
its block, so a lookup adds at most 15 bytes and the index carries a quarter byte of offset per word where one uint32
per word would carry four.
"""
import gzip
import io
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


# ---- trie shape --------------------------------------------------------------------------------------------------------
#
# The lookup index the Go package borrows in place: a radix DAG over the dictionary words, where one symbol is one label of
# `width` bytes (one byte for the Southeast Asian byte alphabets, one little-endian UTF-16 unit for CJK, whose words use only
# BMP characters). Identical subtrees (same weight, same edges) are stored once, so shared word endings cost nothing, and a
# run of single-child nodes is stored as one skip string. Walking a word of n symbols reads about n small records and never
# decodes or allocates.
#
# A node is a record at a byte offset; the root is at offset 0. Record layout:
#   flags byte: bit 7 terminal (a word ends at this node), bit 0 skip present, bits 1-3 the number of leaf edges (7 means a
#   LEB128 count follows) and bits 4-6 the number of inner edges (7 means a LEB128 count follows)
#   weight byte, when terminal and the dictionary is weighted
#   skip: one length byte, then that many labels, matched after entering the node and before its edges
#   leaf edges: sorted by label, each a label and, when weighted, a weight byte: a word ends after that label with no longer
#   word beyond it
#   inner edges: sorted by label, each a label and a 3-byte little-endian offset of the child record
# A word is a leaf edge's label, an inner node's terminal flag, or both ends of a path. The counts, offsets and widths are
# the whole format; lookup in word_trie.go reads exactly this.

POINTER = 3


def _leb(value):
    out = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            out.append(byte | 0x80)
        else:
            out.append(byte)
            return bytes(out)


def _label(value, width):
    return value.to_bytes(width, "little")


def encode_trie(entries, width, weighted):
    """entries: sorted, distinct (labels, weight) pairs, labels a tuple of ints below 256**width. Return the trie bytes."""
    if width not in (1, 2):
        raise SystemExit("unsupported label width")
    root = {}
    for labels, weight in entries:
        if not labels or any(not 0 <= label < 256**width for label in labels) or not 0 <= weight <= 255:
            raise SystemExit("dictionary entry is outside the trie's label or weight range")
        node = root
        for label in labels:
            node = node.setdefault(label, {})
        if None in node:
            raise SystemExit("duplicate dictionary word")
        node[None] = weight if weighted else 0
    nodes = []  # id -> (weight or -1, ((label, child id), ...))
    ids = {}

    def canon(node):
        edges = tuple((label, canon(child)) for label, child in sorted((k, v) for k, v in node.items() if k is not None))
        key = (node.get(None, -1), edges)
        found = ids.get(key)
        if found is None:
            found = ids[key] = len(nodes)
            nodes.append(key)
        return found

    top = canon(root)
    leaf = [not edges for _, edges in nodes]
    refs = [0] * len(nodes)
    for weight, edges in nodes:
        for _, child in edges:
            refs[child] += 1
    refs[top] += 2  # the root is a record of its own

    def record(x):
        weight, edges = nodes[x]
        skip, cur = [], x
        if weight < 0:
            while len(nodes[cur][1]) == 1 and nodes[cur][0] < 0:
                label, child = nodes[cur][1][0]
                if leaf[child] or refs[child] != 1 or nodes[child][0] >= 0:
                    break
                skip.append(label)
                cur = child
        leaves = [(label, nodes[child][0]) for label, child in nodes[cur][1] if leaf[child]]
        inner = [(label, child) for label, child in nodes[cur][1] if not leaf[child]]
        return weight, skip, leaves, inner

    def header(weight, skip, leaves, inner):
        out = bytearray([(0x80 if weight >= 0 else 0) | (1 if skip else 0) | min(len(leaves), 7) << 1 | min(len(inner), 7) << 4])
        if weight >= 0 and weighted:
            out.append(weight)
        if skip:
            if len(skip) > 255:
                raise SystemExit("skip string too long")
            out.append(len(skip))
            for label in skip:
                out += _label(label, width)
        if len(leaves) >= 7:
            out += _leb(len(leaves))
        if len(inner) >= 7:
            out += _leb(len(inner))
        return out

    order, records, offsets, position = [], {}, {}, 0
    stack = [top]
    while stack:
        x = stack.pop()
        if x in records:
            continue
        rec = record(x)
        records[x] = rec
        offsets[x] = position
        order.append(x)
        position += len(header(*rec)) + len(rec[2]) * (width + weighted) + len(rec[3]) * (width + POINTER)
        for _, child in reversed(rec[3]):
            stack.append(child)
    if position >= 1 << (8 * POINTER):
        raise SystemExit("trie offsets exceed the pointer width")
    out = bytearray()
    for x in order:
        weight, skip, leaves, inner = records[x]
        out += header(weight, skip, leaves, inner)
        for label, leaf_weight in leaves:
            out += _label(label, width) + (bytes([leaf_weight]) if weighted else b"")
        for label, child in inner:
            out += _label(label, width) + offsets[child].to_bytes(POINTER, "little")
    assert len(out) == position and offsets[top] == 0
    return bytes(out)


def write_flat(path, words, weights=None):
    """Write the flat sorted-array index of the words (the test oracle) gzipped, byte-for-byte reproducible."""
    path.parent.mkdir(exist_ok=True)
    # GzipFile writes the OS byte 0xff on every Python version; gzip.compress writes 0x03 through zlib on 3.12.
    buffer = io.BytesIO()
    with gzip.GzipFile(fileobj=buffer, mode="wb", compresslevel=9, mtime=0) as stream:
        stream.write(encode(words, weights))
    path.write_bytes(buffer.getvalue())
