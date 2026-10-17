import pytest

import pig_sdk
from pig_sdk.compaction_estimate import _js_number_string

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
