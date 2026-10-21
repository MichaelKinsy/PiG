"""Pi's sessionEntryToContextMessages (core/session-manager.ts, exported from index.ts)."""
import math
from typing import Any, Dict, List, Mapping, Optional

from .host_helpers import parse_date


def _timestamp(entry: Mapping[str, Any]) -> Optional[float]:
    """``new Date(entry.timestamp).getTime()``: Date.parse of a string (read by the host), a number's own time value (clipped as TimeClip does), and None, the null
    JSON.stringify writes for the NaN Pi holds, when it is neither."""
    value = entry.get("timestamp")
    if isinstance(value, str):
        return parse_date(value)
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if (isinstance(value, float) and math.isnan(value)) or abs(value) > 8.64e15:
            return None
        return float(math.trunc(value)) + 0.0
    return None


def _truthy(value: Any) -> bool:
    if value is None or value is False or value == 0 or value == "":
        return False
    return not (isinstance(value, float) and math.isnan(value))


def session_entry_to_context_messages(entry: Mapping[str, Any]) -> List[Any]:
    """Pi's sessionEntryToContextMessages: the LLM and runtime messages a session entry contributes to the context. A message entry is its message, where a null or missing
    content reads as the empty one Pi supplies (an empty string for a system message, an empty list for a user, assistant or toolResult one). A custom_message entry is a
    custom message, a branch_summary entry with a summary a branchSummary message, and a compaction entry a compactionSummary message after the system message it carries,
    if any. Every other entry (a label, a model change, a plain custom entry) is no message. The entry's timestamp string becomes the epoch milliseconds of Date.parse, which
    the host reads, and a timestamp Date.parse cannot read is None in the message, as JSON.stringify writes the NaN Pi holds. A message entry with a null or missing message
    raises TypeError, as Pi throws."""
    kind = entry.get("type")
    if kind == "message":
        message = entry.get("message")
        if message is None:
            raise TypeError("Cannot read properties of undefined (reading 'role')")
        if not isinstance(message, dict):
            return [message]
        role = message.get("role")
        if message.get("content") is None:
            if role == "system":
                return [{**message, "content": ""}]
            if role in ("user", "assistant", "toolResult"):
                return [{**message, "content": []}]
        return [message]
    if kind == "custom_message":
        content = entry.get("content")
        message: Dict[str, Any] = {"role": "custom"}
        if "customType" in entry:
            message["customType"] = entry["customType"]
        message["content"] = [] if content is None else content
        for key in ("display", "details"):
            if key in entry:
                message[key] = entry[key]
        message["timestamp"] = _timestamp(entry)
        return [message]
    if kind == "branch_summary":
        summary = entry.get("summary")
        if not isinstance(summary, str) or summary == "":
            return []
        message = {"role": "branchSummary", "summary": summary}
        if "fromId" in entry:
            message["fromId"] = entry["fromId"]
        message["timestamp"] = _timestamp(entry)
        return [message]
    if kind == "compaction":
        summary = {"role": "compactionSummary", "summary": entry.get("summary")}
        if "tokensBefore" in entry:
            summary["tokensBefore"] = entry["tokensBefore"]
        summary["timestamp"] = _timestamp(entry)
        system = entry.get("systemMessage")
        return [system, summary] if _truthy(system) or isinstance(system, (dict, list)) else [summary]
    return []
