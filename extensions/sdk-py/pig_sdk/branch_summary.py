"""Pi's branch summarization helpers (core/compaction/branch-summarization.ts): the entries a tree navigation leaves behind and the messages and file operations
that make of them. The model call that writes the summary is not here."""
import math
from typing import Any, Mapping, Optional

from .compaction_estimate import estimate_tokens
from .host_helpers import parse_date


class FileOperations:
    """Pi's FileOperations: the files tool calls read, wrote and edited. Each is a JavaScript Set kept as a list in insertion order: a value is added once
    (SameValueZero), and an element is not always a string, because a branch summary's recorded details can hold any JSON value."""

    def __init__(self) -> None:
        self.read: list[Any] = []
        self.written: list[Any] = []
        self.edited: list[Any] = []


class BranchPreparation:
    """Pi's BranchPreparation: the messages to summarize (oldest first), their file operations and their estimated size."""

    def __init__(self, messages: list[dict[str, Any]], file_ops: FileOperations, total_tokens: int) -> None:
        self.messages = messages
        self.file_ops = file_ops
        self.total_tokens = total_tokens


class CollectEntriesResult:
    """Pi's CollectEntriesResult: the entries to summarize (oldest first) and the deepest entry on both the old and the new path, or None."""

    def __init__(self, entries: list[dict[str, Any]], common_ancestor_id: Optional[str]) -> None:
        self.entries = entries
        self.common_ancestor_id = common_ancestor_id


def collect_entries_for_branch_summary(session: Any, old_leaf_id: Optional[str], target_id: str) -> CollectEntriesResult:
    """Pi's collectEntriesForBranchSummary: the entries from ``old_leaf_id`` back to its common ancestor with ``target_id``, oldest first. A compaction on the
    way is included, not a boundary. ``session`` has ``get_entry(id)`` and ``get_branch(from_id)``, as a SessionManager does. With no old position there is
    nothing to summarize."""
    if not old_leaf_id:
        return CollectEntriesResult([], None)
    old_path = {entry.get("id") for entry in session.get_branch(old_leaf_id)}
    target_path = session.get_branch(target_id)
    common_ancestor_id = None
    for entry in reversed(target_path):
        if entry.get("id") in old_path:
            common_ancestor_id = entry.get("id")
            break
    entries: list[dict[str, Any]] = []
    current = old_leaf_id
    while current and current != common_ancestor_id:
        entry = session.get_entry(current)
        if not entry:
            break
        entries.append(entry)
        current = entry.get("parentId")
    entries.reverse()
    return CollectEntriesResult(entries, common_ancestor_id)


def prepare_branch_entries(entries: list[Mapping[str, Any]], token_budget: int = 0) -> BranchPreparation:
    """Pi's prepareBranchEntries: entries (oldest first) become the messages to summarize, newest first until ``token_budget`` (0 is no limit) is reached, with
    the file operations of every entry, including those that did not fit. A branch summary Pi wrote (not one an extension did) contributes the files it
    recorded. An entry the budget stops at is dropped, unless it is a compaction or a branch summary and the messages so far use under 90% of the budget.
    Timestamps are ``new Date(timestamp).getTime()``, which the host's date parser answers (``parse_date``)."""
    newest_first: list[dict[str, Any]] = []
    file_ops = FileOperations()
    total_tokens = 0
    for entry in entries:
        if entry.get("type") == "branch_summary" and not _truthy(entry.get("fromHook")) and _truthy(entry.get("details")):
            details = entry["details"]
            read = details.get("readFiles") if isinstance(details, Mapping) else None
            if isinstance(read, list):
                for file in read:
                    _add(file_ops.read, file)
            modified = details.get("modifiedFiles") if isinstance(details, Mapping) else None
            if isinstance(modified, list):
                for file in modified:
                    _add(file_ops.edited, file)
    for entry in reversed(entries):
        message = _message_from_entry(entry)
        if message is None:
            continue
        _extract_file_ops(message, file_ops)
        tokens = estimate_tokens(message)
        if token_budget > 0 and total_tokens + tokens > token_budget:
            if entry.get("type") in ("compaction", "branch_summary") and total_tokens < token_budget * 0.9:
                newest_first.append(message)
                total_tokens += tokens
            break
        newest_first.append(message)
        total_tokens += tokens
    newest_first.reverse()
    return BranchPreparation(newest_first, file_ops, total_tokens)


def _message_from_entry(entry: Mapping[str, Any]) -> Optional[dict[str, Any]]:
    """Pi's getMessageFromEntry: a tool result adds nothing, because its context is in the assistant's call, and neither do the entry types that carry no
    conversation content."""
    kind = entry.get("type")
    if kind == "message":
        message = entry.get("message")
        if not isinstance(message, Mapping):
            raise TypeError("Cannot read properties of " + ("null" if "message" in entry else "undefined") + " (reading 'role')")
        return None if message.get("role") == "toolResult" else dict(message)
    if kind == "custom_message":
        return _created("custom", entry, ("customType", "content", "display", "details"))
    if kind == "branch_summary":
        return _created("branchSummary", entry, ("summary", "fromId"))
    if kind == "compaction":
        return _created("compactionSummary", entry, ("summary", "tokensBefore"))
    return None


def _created(role: str, entry: Mapping[str, Any], names: tuple[str, ...]) -> dict[str, Any]:
    """Pi's create*Message: an absent member is ``undefined``, which JSON drops."""
    message: dict[str, Any] = {"role": role}
    for name in names:
        if name in entry:
            message[name] = entry[name]
    message["timestamp"] = _date_time(entry)
    return message


def _date_time(entry: Mapping[str, Any]) -> Any:
    """``new Date(entry.timestamp).getTime()``: a string is parsed by the host, a number is clipped to the time range, ``null`` is the epoch and anything else,
    including an absent timestamp, is NaN."""
    if "timestamp" not in entry:
        return math.nan
    timestamp = entry["timestamp"]
    if isinstance(timestamp, bool):
        return 1 if timestamp else 0
    if timestamp is None:
        return 0
    if isinstance(timestamp, str):
        ms = parse_date(timestamp)
        return math.nan if ms is None else _integral(ms)
    if isinstance(timestamp, (int, float)):
        if math.isnan(timestamp) or abs(timestamp) > 8.64e15:
            return math.nan
        return _integral(math.trunc(timestamp))
    return math.nan


def _integral(value: float) -> Any:
    return int(value) if isinstance(value, float) and math.isfinite(value) and value.is_integer() else value


def _extract_file_ops(message: Mapping[str, Any], file_ops: FileOperations) -> None:
    """Pi's extractFileOpsFromMessage: the tool calls of an assistant message. Pi also reads the calls a codemode script made from a tool result, but prepareBranchEntries never passes one."""
    role = message.get("role")
    if role == "assistant":
        blocks = message.get("content")
        for block in blocks if isinstance(blocks, list) else []:
            if isinstance(block, Mapping) and block.get("type") == "toolCall" and "arguments" in block and "name" in block:
                _add_file_op(block["name"], block["arguments"], file_ops)


def _add_file_op(tool_name: Any, arguments: Any, file_ops: FileOperations) -> None:
    path = arguments.get("path") if isinstance(arguments, Mapping) else None
    if not isinstance(path, str) or path == "":
        return
    if tool_name == "read":
        _add(file_ops.read, path)
    elif tool_name == "write":
        _add(file_ops.written, path)
    elif tool_name == "edit":
        _add(file_ops.edited, path)


def _truthy(value: Any) -> bool:
    if value is None:
        return False
    if isinstance(value, (bool, str, int, float)):
        return bool(value) and not (isinstance(value, float) and math.isnan(value))
    return True


def _add(values: list[Any], value: Any) -> None:
    """``Set.prototype.add``: the value joins at the end unless an equal one (SameValueZero) is in. Objects and arrays decoded from JSON are all distinct."""
    if isinstance(value, (list, dict)):
        values.append(value)
        return
    for existing in values:
        if isinstance(existing, (list, dict)) or isinstance(existing, bool) != isinstance(value, bool):
            continue
        if isinstance(existing, (int, float)) and isinstance(value, (int, float)) and not isinstance(value, bool):
            if existing == value or (math.isnan(existing) and math.isnan(value)):
                return
        elif existing == value and type(existing) is type(value):
            return
    values.append(value)
