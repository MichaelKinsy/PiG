"""Pi's convertToLlm (core/messages.ts:148, exported from index.ts): the coding agent's custom message roles become user messages for the model."""
import math
from typing import Any, Mapping, Sequence

from .compaction_estimate import _js_number_string, _js_truthy

_COMPACTION_SUMMARY_PREFIX = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
_COMPACTION_SUMMARY_SUFFIX = "\n</summary>"
_BRANCH_SUMMARY_PREFIX = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
_BRANCH_SUMMARY_SUFFIX = "</summary>"
_ABSENT = object()


def convert_to_llm(messages: Sequence[Any]) -> list:
    """Pi's convertToLlm: bashExecution, custom, branchSummary and compactionSummary messages become user messages, system, user, assistant and
    toolResult messages are returned as they are, and a bashExecution message with excludeFromContext set, or a message with any other role, is
    dropped. A message is the flat role-discriminated JSON object the wire carries. A message that is not an object has no role and is dropped;
    Pi throws a TypeError on null there."""
    converted = []
    for message in messages:
        result = _convert_message(message)
        if result is not None:
            converted.append(result)
    return converted


def _convert_message(message: Any) -> Any:
    if not isinstance(message, Mapping):
        return None
    role = message.get("role")
    if role == "bashExecution":
        if _js_truthy(message.get("excludeFromContext")):
            return None
        return _user_text_message(message, _bash_execution_to_text(message))
    if role == "custom":
        converted = {"role": "user"}
        content = message.get("content", _ABSENT)
        if isinstance(content, str):
            converted["content"] = [{"type": "text", "text": content}]
        elif content is not _ABSENT:
            converted["content"] = content
        _copy_timestamp(converted, message)
        return converted
    if role == "branchSummary":
        return _user_text_message(message, _BRANCH_SUMMARY_PREFIX + _js_to_string(message, "summary") + _BRANCH_SUMMARY_SUFFIX)
    if role == "compactionSummary":
        return _user_text_message(message, _COMPACTION_SUMMARY_PREFIX + _js_to_string(message, "summary") + _COMPACTION_SUMMARY_SUFFIX)
    if role in ("system", "user", "assistant", "toolResult"):
        return message
    return None


def _user_text_message(source: Mapping[str, Any], text: str) -> dict:
    converted = {"role": "user", "content": [{"type": "text", "text": text}]}
    _copy_timestamp(converted, source)
    return converted


def _copy_timestamp(converted: dict, source: Mapping[str, Any]) -> None:
    """Carries m.timestamp over; an undefined timestamp is absent from the JSON a message travels as."""
    if "timestamp" in source:
        converted["timestamp"] = source["timestamp"]


def _is_number_zero(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool) and value == 0


def _bash_execution_to_text(message: Mapping[str, Any]) -> str:
    """Pi's bashExecutionToText (core/messages.ts:76-92)."""
    text = "Ran `" + _js_to_string(message, "command") + "`\n"
    if _js_truthy(message.get("output")):
        text += "```\n" + _js_to_string(message, "output") + "\n```"
    else:
        text += "(no output)"
    if _js_truthy(message.get("cancelled")):
        text += "\n\n(command cancelled)"
    elif "exitCode" in message and message["exitCode"] is not None and not _is_number_zero(message["exitCode"]):
        text += "\n\nCommand exited with code " + _js_to_string(message, "exitCode")
    if _js_truthy(message.get("truncated")) and _js_truthy(message.get("fullOutputPath")):
        text += "\n\n[Output truncated. Full output: " + _js_to_string(message, "fullOutputPath") + "]"
    return text


def _js_to_string(message: Mapping[str, Any], key: str) -> str:
    """The string a template literal or + makes of message[key]: JavaScript's ToString of a value decoded from JSON, or "undefined" for an absent key."""
    if key not in message:
        return "undefined"
    return _js_value_string(message[key])


def _js_value_string(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, str):
        return value
    if isinstance(value, (int, float)):
        return _js_number_to_string(value)
    if isinstance(value, (list, tuple)):
        return ",".join("" if element is None else _js_value_string(element) for element in value)
    return "[object Object]"


def _js_number_to_string(value: Any) -> str:
    """Number::toString, which spells a non-finite number as NaN, Infinity or -Infinity where JSON.stringify writes null."""
    try:
        number = float(value)
    except OverflowError:
        number = math.inf if value > 0 else -math.inf
    if math.isnan(number):
        return "NaN"
    if math.isinf(number):
        return "Infinity" if number > 0 else "-Infinity"
    return _js_number_string(number)
