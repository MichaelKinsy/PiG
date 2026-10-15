"""Pi's findCutPoint and findTurnStartIndex (core/compaction/compaction.ts) over the session entries an extension gets (for example from
``SessionManager.get_entries()``), each a dict with a ``type``."""
from dataclasses import dataclass
from typing import Any, List, Mapping, Sequence

from .compaction_estimate import estimate_tokens


@dataclass(frozen=True)
class CutPointResult:
    """Pi's CutPointResult, with Python names."""

    first_kept_entry_index: int
    """The index of the first entry to keep."""
    turn_start_index: int
    """The index of the message that starts the turn being split, or -1 when no turn is split."""
    is_split_turn: bool
    """The cut point is not a turn start."""


def _context_messages(entry: Any) -> List[Mapping[str, Any]]:
    """Pi's sessionEntryToContextMessages (core/session-manager.ts) for the entries the cut point logic reads. Session files are parsed without
    validation, so a message with null or missing content reads as an empty one, and a message that is not an object (a number, string, list or bool) is
    still one context message: it has no role, so it is neither a cut point nor a turn start and weighs nothing, but it stops the cut's move back over
    metadata entries. A null or missing message, on which Pi throws, has none."""
    if not isinstance(entry, Mapping):
        return []
    kind = entry.get("type")
    if kind == "message":
        message = entry.get("message")
        if message is None:
            return []
        if not isinstance(message, Mapping):
            return [{}]
        if message.get("content") is None:
            role = message.get("role")
            if role == "system":
                return [{**message, "content": ""}]
            if role in ("user", "assistant", "toolResult"):
                return [{**message, "content": []}]
        return [message]
    if kind == "custom_message":
        content = entry.get("content")
        return [{"role": "custom", "content": [] if content is None else content}]
    if kind == "branch_summary":
        summary = entry.get("summary")
        return [{"role": "branchSummary", "summary": summary}] if isinstance(summary, str) and summary else []
    if kind == "compaction":
        summary = entry.get("summary")
        messages: List[Mapping[str, Any]] = []
        system = entry.get("systemMessage")
        if isinstance(system, Mapping):
            messages.append(system)
        messages.append({"role": "compactionSummary", "summary": summary if isinstance(summary, str) else ""})
        return messages
    return []


def _is_compaction(entry: Any) -> bool:
    return isinstance(entry, Mapping) and entry.get("type") == "compaction"


_CUT_POINT_ROLES = ("user", "assistant", "bashExecution", "custom", "branchSummary", "compactionSummary")
_TURN_START_ROLES = ("user", "bashExecution", "custom", "branchSummary", "compactionSummary")


def _is_turn_start_entry(entry: Any) -> bool:
    return not _is_compaction(entry) and any(message.get("role") in _TURN_START_ROLES for message in _context_messages(entry))


def find_turn_start_index(entries: Sequence[Mapping[str, Any]], entry_index: int, start_index: int) -> int:
    """Pi's findTurnStartIndex: the index of the context-visible message that starts the turn holding ``entry_index``, or -1 when none starts it at or
    after ``start_index``."""
    for i in range(min(entry_index, len(entries) - 1), start_index - 1, -1):
        if _is_turn_start_entry(entries[i]):
            return i
    return -1


def find_cut_point(entries: Sequence[Mapping[str, Any]], start_index: int, end_index: int, keep_recent_tokens: float) -> CutPointResult:
    """Pi's findCutPoint: it walks back from the newest entry before ``end_index``, adding estimated message sizes until ``keep_recent_tokens`` is
    reached, and cuts at the closest valid cut point at or after that entry (never at a tool result), then moves the cut back over adjacent entries that
    do not affect the context."""
    end_index = min(end_index, len(entries))
    cut_points = [
        i
        for i in range(start_index, end_index)
        if not _is_compaction(entries[i]) and any(message.get("role") in _CUT_POINT_ROLES for message in _context_messages(entries[i]))
    ]
    if not cut_points:
        return CutPointResult(start_index, -1, False)
    accumulated_tokens = 0
    cut_index = cut_points[0]
    for i in range(end_index - 1, start_index - 1, -1):
        message_tokens = sum(estimate_tokens(message) for message in _context_messages(entries[i]))
        if message_tokens == 0:
            continue
        accumulated_tokens += message_tokens
        if accumulated_tokens >= keep_recent_tokens:
            cut_index = next((candidate for candidate in cut_points if candidate >= i), cut_points[-1])
            break
    while cut_index > start_index:
        previous = entries[cut_index - 1]
        if _is_compaction(previous) or len(_context_messages(previous)) > 0:
            break
        cut_index -= 1
    starts_turn = _is_turn_start_entry(entries[cut_index])
    turn_start_index = -1 if starts_turn else find_turn_start_index(entries, cut_index, start_index)
    return CutPointResult(cut_index, turn_start_index, not starts_turn and turn_start_index != -1)
