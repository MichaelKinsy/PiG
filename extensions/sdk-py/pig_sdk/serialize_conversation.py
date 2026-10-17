"""Pi's serializeConversation (core/compaction/utils.ts:120-155, exported from index.ts): the text a summarization request carries for a conversation."""
from typing import Any, Mapping, Sequence

from .compaction_estimate import _js_number_string
from .llm_convert import _js_value_string

_TOOL_RESULT_MAX_CHARS = 2000


def serialize_conversation(messages: Sequence[Any]) -> str:
    """Pi's serializeConversation: messages are LLM messages as JSON objects (the result of convert_to_llm). A tool call prints its arguments in property
    order, which a dict keeps. A message with another role than user, assistant or toolResult adds nothing, and thinking blocks are omitted (Pi 1.2.0). Pi
    throws a TypeError when a message it reads has content that is not a string or an array, and this raises TypeError too."""
    parts: list = []
    for message in messages:
        if message is None:
            raise TypeError("Cannot read properties of null (reading 'role')")
        role = message.get("role") if isinstance(message, Mapping) else None
        if role == "user":
            content = _content_text(message.get("content", _ABSENT), "")
            if content:
                parts.append("[User]: " + content)
        elif role == "assistant":
            _serialize_assistant(message, parts)
        elif role == "toolResult":
            content = _content_text(message.get("content", _ABSENT), "")
            if content:
                parts.append("[Tool result]: " + _truncate_for_summary(content, _TOOL_RESULT_MAX_CHARS))
    return "\n\n".join(parts)


_ABSENT = object()


def _serialize_assistant(message: Mapping[str, Any], parts: list) -> None:
    blocks = message.get("content")
    if not isinstance(blocks, list):
        raise TypeError("msg.content is not iterable")
    tool_calls: list = []
    has_text = False
    for block in blocks:
        if block is None:
            raise TypeError("Cannot read properties of null (reading 'type')")
        kind = block.get("type") if isinstance(block, Mapping) else None
        if kind == "toolCall":
            name = _js_value_string(block["name"]) if "name" in block else "undefined"
            tool_calls.append(name + "(" + _entries_string(block.get("arguments")) + ")")
        elif kind == "text":
            has_text = True
    if has_text:
        parts.append("[Assistant]: " + _content_text(blocks, "\n"))
    if tool_calls:
        parts.append("[Assistant tool calls]: " + "; ".join(tool_calls))


def _string_or_empty(block: Mapping[str, Any], key: str) -> str:
    """What Array.prototype.join makes of a member: absent and null are the empty string, any other value its ToString."""
    value = block.get(key)
    return "" if value is None else _js_value_string(value)


def _entries_string(arguments: Any) -> str:
    """Object.entries(args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(", "). Object.entries throws for null and undefined, reads the indexes of a
    string or array as keys, and yields nothing for a number or boolean."""
    if arguments is None:
        raise TypeError("Cannot convert undefined or null to object")
    if isinstance(arguments, Mapping):
        entries = [key + "=" + _json_stringify(arguments[key]) for key in _ordered_keys(arguments)]
    elif isinstance(arguments, list):
        entries = [str(i) + "=" + _json_stringify(element) for i, element in enumerate(arguments)]
    elif isinstance(arguments, str):
        units = arguments.encode("utf-16-le", "surrogatepass")
        entries = [str(i) + "=" + _unit_json(int.from_bytes(units[2 * i:2 * i + 2], "little")) for i in range(len(units) // 2)]
    else:
        entries = []
    return ", ".join(entries)


def _unit_json(unit: int) -> str:
    """JSON.stringify of a one-unit string: an unpaired surrogate is written as the lowercase \\uXXXX escape QuoteJSONString gives it."""
    if 0xD800 <= unit <= 0xDFFF:
        return '"\\u%04x"' % unit
    return _json_string(chr(unit))


def _content_text(content: Any, separator: str) -> str:
    """pi-ai's contentText (utils/text.ts:6-12): a string is its own text and an array of blocks is the text of its text blocks joined with separator."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        texts = []
        for block in content:
            if block is None:
                raise TypeError("Cannot read properties of null (reading 'type')")
            if isinstance(block, Mapping) and block.get("type") == "text":
                texts.append(_string_or_empty(block, "text"))
        return separator.join(texts)
    raise TypeError("content.filter is not a function")


def _truncate_for_summary(text: str, max_chars: int) -> str:
    """Pi's truncateForSummary: text.length counts UTF-16 units, and the cut keeps the first max_chars of them."""
    units = text.encode("utf-16-le", "surrogatepass")
    length = len(units) // 2
    if length <= max_chars:
        return text
    return units[: 2 * max_chars].decode("utf-16-le", "surrogatepass") + "\n\n[... " + str(length - max_chars) + " more characters truncated]"


def _is_array_index(key: str) -> bool:
    return key == "0" or (bool(key) and len(key) <= 10 and key.isascii() and key.isdigit() and key[0] != "0" and int(key) <= 4294967294)


def _ordered_keys(obj: Mapping[str, Any]) -> list:
    """The order of an object's own string keys that Object.keys and JSON.stringify use: the array-index keys ascending, then every other key in insertion order."""
    keys = list(obj.keys())
    indexes = sorted((key for key in keys if _is_array_index(key)), key=lambda key: (len(key), key))
    return indexes + [key for key in keys if not _is_array_index(key)]


def _json_stringify(value: Any) -> str:
    """JSON.stringify for a decoded JSON value."""
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return _js_number_string(value)
    if isinstance(value, str):
        return _json_string(value)
    if isinstance(value, (list, tuple)):
        return "[" + ",".join(_json_stringify(element) for element in value) + "]"
    if isinstance(value, Mapping):
        return "{" + ",".join(_json_string(key) + ":" + _json_stringify(value[key]) for key in _ordered_keys(value)) + "}"
    return "null"


_ESCAPES = {'"': '\\"', "\\": "\\\\", "\b": "\\b", "\f": "\\f", "\n": "\\n", "\r": "\\r", "\t": "\\t"}


def _json_string(text: str) -> str:
    """QuoteJSONString: a quote, a backslash and the control characters are escaped (\\b \\f \\n \\r \\t short, the rest as lowercase \\u00xx) and every
    other character is written as it is, except an unpaired surrogate, which is its lowercase \\uXXXX escape."""
    out = ['"']
    for char in text:
        if char in _ESCAPES:
            out.append(_ESCAPES[char])
        elif ord(char) < 0x20 or 0xD800 <= ord(char) <= 0xDFFF:
            out.append("\\u%04x" % ord(char))
        else:
            out.append(char)
    out.append('"')
    return "".join(out)
