"""Pi's estimateTokens (core/compaction/compaction.ts): a chars/4 heuristic over one agent message as the wire carries it, rounded up.

Lengths are JavaScript string lengths, UTF-16 code units, and a tool call's arguments count as the length of their JSON.stringify text.
"""
import math
from typing import Any, Mapping

_ESTIMATED_IMAGE_CHARS = 4800


def _utf16_length(text: Any) -> int:
    if not isinstance(text, str):
        return 0
    return len(text.encode("utf-16-le", "surrogatepass")) // 2


def _content_chars(content: Any) -> int:
    if isinstance(content, str):
        return _utf16_length(content)
    if isinstance(content, list):
        chars = 0
        for block in content:
            kind = block.get("type") if isinstance(block, Mapping) else None
            if kind == "text":
                chars += _utf16_length(block.get("text"))
            elif kind == "image":
                chars += _ESTIMATED_IMAGE_CHARS
        return chars
    return 0


def _js_truthy(value: Any) -> bool:
    if value is None:
        return False
    if isinstance(value, (bool, str, int, float)):
        return bool(value) and not (isinstance(value, float) and math.isnan(value))
    return True


def estimate_tokens(message: Mapping[str, Any]) -> int:
    """Pi's estimateTokens for one agent message (a dict with a ``role``). A role Pi does not size is 0."""
    role = message.get("role")
    if role == "system":
        chars = _content_chars(message.get("content"))
        sections = message.get("sections")
        if isinstance(sections, Mapping):
            chars += sum(_utf16_length(section) for section in sections.values() if isinstance(section, str))
        added = message.get("toolsAdded")
        if _js_truthy(added):
            chars += _json_stringify_length(added)
    elif role in ("user", "custom", "toolResult"):
        chars = _content_chars(message.get("content"))
    elif role == "assistant":
        chars = 0
        blocks = message.get("content")
        for block in blocks if isinstance(blocks, list) else []:
            kind = block.get("type") if isinstance(block, Mapping) else None
            if kind == "text":
                chars += _utf16_length(block.get("text"))
            elif kind == "thinking":
                chars += _utf16_length(block.get("thinking"))
            elif kind == "toolCall":
                chars += _utf16_length(block.get("name")) + _json_stringify_length(block.get("arguments"))
    elif role == "bashExecution":
        chars = _utf16_length(message.get("command")) + _utf16_length(message.get("output"))
    elif role in ("branchSummary", "compactionSummary"):
        chars = _utf16_length(message.get("summary"))
    else:
        return 0
    return -(-chars // 4)


def _json_stringify_length(value: Any) -> int:
    """The UTF-16 length of JSON.stringify(value) for a decoded JSON value, without building the text."""
    if value is None:
        return 4
    if isinstance(value, bool):
        return 4 if value else 5
    if isinstance(value, (int, float)):
        return len(_js_number_string(value))
    if isinstance(value, str):
        return _json_quoted_length(value)
    if isinstance(value, (list, tuple)):
        return 2 + sum(_json_stringify_length(element) for element in value) + max(len(value) - 1, 0)
    if isinstance(value, Mapping):
        return 2 + sum(_json_quoted_length(str(key)) + 1 + _json_stringify_length(element) for key, element in value.items()) + max(len(value) - 1, 0)
    return 0


def _json_quoted_length(text: str) -> int:
    """The UTF-16 length of JSON.stringify(text): quotes, the short escapes, \\u00XX for the other control characters and for an unpaired surrogate (a Python string holds
    one as a code point of its own), every other character as it is."""
    length = 2
    for char in text:
        code = ord(char)
        if char in '"\\\b\f\n\r\t':
            length += 2
        elif code < 0x20 or 0xD800 <= code < 0xE000:
            length += 6
        elif code >= 0x10000:
            length += 2
        else:
            length += 1
    return length


def _js_number_string(value: Any) -> str:
    """JavaScript's Number::toString for the number JSON.stringify writes: the shortest digits that round-trip, positional for exponents from -7 to 20
    and exponential beyond; -0 is "0" and a non-finite number is "null"."""
    try:
        number = float(value)
    except OverflowError:
        return "null"
    if math.isnan(number) or math.isinf(number):
        return "null"
    if number == 0:
        return "0"
    sign = "-" if number < 0 else ""
    # repr gives the shortest round-trip digits.
    shortest = repr(abs(number))
    if "e" in shortest:
        mantissa, _, exponent_text = shortest.partition("e")
        digits = mantissa.replace(".", "")
        exponent = int(exponent_text)
        integer_digits = len(mantissa.partition(".")[0])
        exponent += integer_digits - 1
    else:
        integer, _, fraction = shortest.partition(".")
        if fraction == "0":
            fraction = ""
        stripped = (integer + fraction).lstrip("0")
        digits = stripped.rstrip("0") or "0"
        if integer.strip("0"):
            exponent = len(integer.lstrip("0")) - 1
        else:
            exponent = -(len(fraction) - len(fraction.lstrip("0")) + 1)
    n, k = exponent + 1, len(digits)
    if k <= n <= 21:
        return sign + digits + "0" * (n - k)
    if 0 < n <= 21:
        return sign + digits[:n] + "." + digits[n:]
    if -6 < n <= 0:
        return sign + "0." + "0" * (-n) + digits
    exponent_sign = "-" if n - 1 < 0 else "+"
    magnitude = abs(n - 1)
    if k == 1:
        return f"{sign}{digits}e{exponent_sign}{magnitude}"
    return f"{sign}{digits[0]}.{digits[1:]}e{exponent_sign}{magnitude}"
