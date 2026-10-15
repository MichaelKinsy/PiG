"""Compaction usage helpers, mirroring Pi's exports of core/compaction/compaction.ts.

``CompactionSettings``, ``DEFAULT_COMPACTION_SETTINGS``, ``calculate_context_tokens``, ``should_compact`` and ``get_last_assistant_usage`` are pure:
they read values the caller already holds. A usage is the dict Pi's ``Usage`` has on the wire. As in JavaScript's arithmetic, a null token count is 0
and a missing or non-numeric one is NaN, so it never makes a usage valid.
"""
import math
from dataclasses import dataclass
from typing import Any, Mapping, Optional, Sequence


@dataclass(frozen=True)
class CompactionSettings:
    """Pi's CompactionSettings, with Python names."""

    enabled: bool = True
    reserve_tokens: int = 16384
    keep_recent_tokens: int = 20000


DEFAULT_COMPACTION_SETTINGS = CompactionSettings(enabled=True, reserve_tokens=16384, keep_recent_tokens=20000)


def _js_number(value: Any) -> float:
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return float(value)
    return math.nan


def calculate_context_tokens(usage: Mapping[str, Any]) -> float:
    """Pi's calculateContextTokens: the native total when it is truthy, else the sum of the components."""
    total = _js_number(usage.get("totalTokens"))
    if total != 0 and not math.isnan(total):
        return total
    return sum(_addend(usage, key) for key in ("input", "output", "cacheRead", "cacheWrite"))


def _addend(usage: Mapping[str, Any], key: str) -> float:
    """A token count as JavaScript's + reads it: null is 0, and a missing (undefined) or non-numeric count is NaN."""
    if key in usage and usage[key] is None:
        return 0.0
    return _js_number(usage.get(key))


def should_compact(context_tokens: float, context_window: float, settings: CompactionSettings) -> bool:
    """Pi's shouldCompact: compaction triggers when the context exceeds the window less the reserve."""
    if not settings.enabled:
        return False
    return context_tokens > context_window - settings.reserve_tokens


def get_last_assistant_usage(entries: Sequence[Mapping[str, Any]]) -> Optional[Mapping[str, Any]]:
    """Pi's getLastAssistantUsage over the session entries Pi hands an extension (for example ``SessionManager.get_entries()``).

    Returns the usage of the last assistant message that did not abort or fail and whose usage is not all zero, or None.
    """
    for entry in reversed(entries):
        if not isinstance(entry, Mapping) or entry.get("type") != "message":
            continue
        message = entry.get("message")
        if not isinstance(message, Mapping) or message.get("role") != "assistant":
            continue
        if message.get("stopReason") in ("aborted", "error"):
            continue
        usage = message.get("usage")
        if not isinstance(usage, Mapping):
            continue
        if calculate_context_tokens(usage) > 0:
            return usage
    return None
