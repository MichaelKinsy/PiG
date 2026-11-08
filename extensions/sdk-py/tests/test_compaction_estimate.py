import random
import struct

import pytest

import pig_sdk
from pig_sdk.compaction_estimate import _js_number_string, _utf16_length

# Pi's estimateTokens (core/compaction/compaction.ts) counts UTF-16 units and the JSON.stringify length of a tool call's arguments; the cases
# below are ECMA-262 7.1.12.1 Number::toString and V8 output.


@pytest.mark.parametrize(
    "value,want",
    [
        (0, "0"), (1, "1"), (-1, "-1"), (1.5, "1.5"), (0.1, "0.1"), (100, "100"), (123456789012345680000.0, "123456789012345680000"), (1e21, "1e+21"),
        (1.5e21, "1.5e+21"), (1e-6, "0.000001"), (1e-7, "1e-7"), (1.5e-7, "1.5e-7"), (123e-20, "1.23e-18"), (0.000001234, "0.000001234"), (5e-324, "5e-324"),
        (1.7976931348623157e308, "1.7976931348623157e+308"), (-0.5, "-0.5"), (4294967296.5, "4294967296.5"), (1e20, "100000000000000000000"),
        (12345.678, "12345.678"), (-0.0, "0"), (10**30, "1e+30"), (2**60, "1152921504606847000"), (1.0, "1"), (1e16, "10000000000000000"),
    ],
)
def test_number_to_string_matches_javascript(value, want):
    assert _js_number_string(value) == want


@pytest.mark.parametrize(
    "message,want",
    [
        ({"role": "user", "content": "abcde"}, 2),
        ({"role": "user", "content": "\U0001F600\U0001F600"}, 1),
        ({"role": "user", "content": [{"type": "image"}, {"type": "text", "text": "abcd"}]}, 1201),
        ({"role": "assistant", "content": [{"type": "toolCall", "name": "read", "arguments": {"path": 'a"b'}}]}, 5),
        ({"role": "x", "content": "abc"}, 0),
    ],
)
def test_estimate_tokens_counts_utf16_units_and_stringify_length(message, want):
    assert pig_sdk.estimate_tokens(message) == want


def test_utf16_length_is_the_sum_of_the_utf16_lengths_of_the_characters():
    # ASCII runs of every length around a word, characters of every encoded size, and an unpaired surrogate (a Python string holds one as a code point of its own,
    # one unit in JavaScript).
    pieces = ["a", "abcdefgh", "é", "日", "\U0001F600", "\U0010FFFF", "\uffff", "\ud7ff", "\ue000", "\ud800", "\udfff", "\x00", "\n"]
    rng = random.Random(11)
    for _ in range(20000):
        text = "".join(rng.choice(pieces) for _ in range(rng.randrange(8)))
        want = sum(2 if ord(char) >= 0x10000 else 1 for char in text)
        assert _utf16_length(text) == want, repr(text)
    assert _utf16_length(1) == 0
    assert [_utf16_length(text) for text in ("", "abcdefgh", "abcdefg\U0001F600", "\ud800")] == [0, 8, 9, 1]


# Strings with the numbers Pi 1.5.0's own estimateTokens gives them (run by node): (name, UTF-16 units of the text, tokens of the user message of the text four
# times, which is the string's JavaScript length, tokens of the user message of the text once, tokens of an assistant tool call named "" with the arguments
# {"k": text}, which counts JSON.stringify's length: an unpaired surrogate is its six-character escape). A Python string holds an unpaired surrogate as a code
# point of its own, as the JSON decoder gives it.
PI_ESTIMATE_CASES = [
        ("empty", [], 0, 0, 2),
        ("ascii", [0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68], 8, 2, 4),
        ("lone high surrogate", [0xd800], 1, 1, 4),
        ("lone low surrogate", [0xdc00], 1, 1, 4),
        ("eight lone high surrogates", [0xd800, 0xd800, 0xd800, 0xd800, 0xd800, 0xd800, 0xd800, 0xd800], 8, 2, 14),
        ("low then high", [0xdc00, 0xd800], 2, 1, 5),
        ("lone high between ASCII", [0x61, 0xd83d, 0x62], 3, 1, 4),
        ("astral", [0xd83d, 0xde00], 2, 1, 3),
        ("eight astral", [0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00, 0xd83d, 0xde00], 16, 4, 6),
        ("high half of a pair", [0xd83d], 1, 1, 4),
        ("low half of a pair", [0xde00], 1, 1, 4),
        ("combining acute", [0x65, 0x301], 2, 1, 3),
        ("combining run", [0x61, 0x300, 0x301, 0x302, 0x303], 5, 2, 4),
        ("zwj sequence", [0xd83d, 0xdc68, 0x200d, 0xd83d, 0xdc69, 0x200d, 0xd83d, 0xdc67], 8, 2, 4),
        ("regional indicators", [0xd83c, 0xddfa, 0xd83c, 0xddf8], 4, 1, 3),
        ("hangul jamo", [0x1100, 0x1161, 0x11a8], 3, 1, 3),
        ("bmp edges", [0xd7ff, 0xe000, 0xffff], 3, 1, 3),
        ("noncharacters", [0xfdd0, 0xfffe], 2, 1, 3),
        ("bom", [0xfeff], 1, 1, 3),
        ("line separators", [0x2028, 0x2029], 2, 1, 3),
        ("nul", [0x0], 1, 1, 4),
        ("ascii then lone high", [0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0xd800], 9, 3, 6),
        ("two highs then a pair", [0xd83d, 0xd83d, 0xd83d, 0xde00], 4, 1, 6),
        ("lone surrogates beside multibyte", [0xe9, 0xd800, 0x65e5, 0xdfff, 0xd83d, 0xde00], 6, 2, 6),
        ("control characters", [0x1, 0x1f, 0x7f], 3, 1, 6),
]


def _from_units(units):
    # A pair of units is one character, an unpaired surrogate stays a code point of its own.
    return struct.pack(f"<{len(units)}H", *units).decode("utf-16-le", "surrogatepass")


@pytest.mark.parametrize("name,units,length,tokens,call_tokens", PI_ESTIMATE_CASES)
def test_estimate_tokens_matches_pi_on_lone_surrogates_astral_and_combining_marks(name, units, length, tokens, call_tokens):
    text = _from_units(units)
    assert _utf16_length(text) == len(units)
    assert pig_sdk.estimate_tokens({"role": "user", "content": text * 4}) == length
    assert pig_sdk.estimate_tokens({"role": "user", "content": text}) == tokens
    call = {"role": "assistant", "content": [{"type": "toolCall", "id": "c", "name": "", "arguments": {"k": text}}]}
    assert pig_sdk.estimate_tokens(call) == call_tokens
