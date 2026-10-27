"""Pi's buildSessionContext (core/session-manager.ts, exported from index.ts)."""
from typing import Any, Dict, List, Mapping, Optional, Sequence

from .session_entry_messages import _truthy, session_entry_to_context_messages


class _Last:
    def __repr__(self) -> str:
        return "SESSION_LEAF_LAST"


#: An omitted leafId: the context ends at the last entry. ``None`` is Pi's null: no entry is on the path, so the context is empty.
SESSION_LEAF_LAST = _Last()


def _key(value: Any) -> Any:
    """A Map key for an entry id as JavaScript compares it: a string, number or boolean (told apart, as ``1`` and ``true`` are two keys), anything else is no key."""
    if isinstance(value, (str, bool)):
        return (type(value), value)
    if isinstance(value, (int, float)):
        return (float, float(value))
    return None


def _same(a: Mapping[str, Any], a_key: str, b: Mapping[str, Any], b_key: str) -> bool:
    """``a[a_key] === b[b_key]`` for the values a decoded entry holds: undefined (a missing member) equals only undefined."""
    if (a_key in a) != (b_key in b):
        return False
    if a_key not in a:
        return True
    left, right = a[a_key], b[b_key]
    if left is None and right is None:
        return True
    left_key, right_key = _key(left), _key(right)
    return left_key is not None and left_key == right_key


def _entry_path(entries: Sequence[Mapping[str, Any]], leaf_id: Any, by_id: Optional[Mapping[Any, Mapping[str, Any]]]) -> List[Mapping[str, Any]]:
    if leaf_id is None:
        return []
    if by_id is not None:
        def lookup(value: Any) -> Optional[Mapping[str, Any]]:
            return by_id.get(value) if _key(value) is not None else None
    else:
        index: Dict[Any, Mapping[str, Any]] = {}
        for entry in entries:
            key = _key(entry.get("id"))
            if key is not None:
                index[key] = entry

        def lookup(value: Any) -> Optional[Mapping[str, Any]]:
            key = _key(value)
            return index.get(key) if key is not None else None
    current = lookup(leaf_id) if leaf_id is not SESSION_LEAF_LAST and _truthy(leaf_id) else None
    if current is None:
        if not entries:
            return []
        current = entries[-1]
    reversed_path: List[Mapping[str, Any]] = []
    limit = len(entries) + (len(by_id) if by_id is not None else 0)
    # A parent chain longer than every entry there is has looped, where Pi would never return.
    while current is not None and len(reversed_path) <= limit:
        reversed_path.append(current)
        parent = current.get("parentId")
        current = lookup(parent) if _truthy(parent) else None
    reversed_path.reverse()
    return reversed_path


def _role(entry: Mapping[str, Any]) -> Optional[str]:
    """``entry.message.role``: Pi's TypeError for a null or missing message, no role for one that is not an object."""
    if "message" not in entry:
        raise TypeError("Cannot read properties of undefined (reading 'role')")
    message = entry["message"]
    if message is None:
        raise TypeError("Cannot read properties of null (reading 'role')")
    role = message.get("role") if isinstance(message, dict) else None
    return role if isinstance(role, str) else None


def _members(source: Mapping[str, Any], names: Mapping[str, str]) -> Dict[str, Any]:
    """{out: source[in]} for the members source has: JSON.stringify drops the ones Pi reads as undefined."""
    return {out: source[name] for out, name in names.items() if name in source}


def _context_entries(path: List[Mapping[str, Any]]) -> List[Mapping[str, Any]]:
    compaction = None
    for entry in path:
        if entry.get("type") == "compaction":
            compaction = entry
    if compaction is None:
        return path
    compaction_index = next((i for i, entry in enumerate(path) if _same(entry, "id", compaction, "id")), -1)
    if compaction_index < 0:
        return path
    context: List[Mapping[str, Any]] = [compaction]
    found_first_kept = False
    for entry in path[:compaction_index]:
        if _same(entry, "id", compaction, "firstKeptEntryId"):
            found_first_kept = True
        if not found_first_kept:
            continue
        message = entry.get("message")
        if entry.get("type") == "message" and isinstance(message, dict) and message.get("role") == "system":
            continue
        context.append(entry)
    context.extend(path[compaction_index + 1:])
    return context


def _apply_edit(messages: List[Any], edit: Mapping[str, Any]) -> List[Any]:
    present = "replacement" in edit
    replacement = edit.get("replacement")
    if present and replacement is None:
        return []
    out: List[Any] = []
    for message in messages:
        role = message.get("role") if isinstance(message, dict) else None
        if role not in ("user", "assistant", "toolResult", "custom"):
            out.append(message)
            continue
        if not present:
            raise TypeError("Cannot read properties of undefined (reading 'content')")
        has_content = isinstance(replacement, dict) and "content" in replacement
        content = replacement["content"] if has_content else None
        copied = dict(message)
        if isinstance(content, str) and role in ("assistant", "toolResult"):
            copied["content"] = [{"type": "text", "text": content}]
        elif has_content:
            copied["content"] = content
        else:
            copied.pop("content", None)
        out.append(copied)
    return out


def build_session_context(
    entries: Sequence[Mapping[str, Any]],
    leaf_id: Any = SESSION_LEAF_LAST,
    by_id: Optional[Mapping[Any, Mapping[str, Any]]] = None,
) -> Dict[str, Any]:
    """Pi's buildSessionContext: the model context of a session as the entries on the path from the leaf to the root, with compaction, branch summaries and context edits
    applied (Pi's buildSessionProjection without its provenance). The result has ``messages`` (the context's messages in order), ``thinkingLevel`` (the last
    thinking_level_change on the path, "off" without one) and ``model`` (the last model_change or assistant message's ``{provider, modelId}``, None without one).
    ``leaf_id`` is Pi's leafId: omitted (:data:`SESSION_LEAF_LAST`) the last entry, ``None`` no path (an empty context), an id the entry to walk back from (an id no entry has,
    or the empty string, falls back to the last entry). ``by_id`` is an index of the entries by id that the walk reads instead of building one from ``entries``. A message
    entry with a null or missing message raises TypeError, as Pi throws."""
    path = _entry_path(entries, leaf_id, by_id)
    thinking: Any = "off"
    thinking_defined = True
    model: Optional[Dict[str, Any]] = None
    for entry in path:
        kind = entry.get("type")
        if kind == "thinking_level_change":
            thinking_defined = "thinkingLevel" in entry
            thinking = entry.get("thinkingLevel")
        elif kind == "model_change":
            model = _members(entry, {"provider": "provider", "modelId": "modelId"})
        elif kind == "message" and _role(entry) == "assistant":
            model = _members(entry["message"], {"provider": "provider", "modelId": "model"})
    context_entries = _context_entries(path)
    edits: Dict[Any, Mapping[str, Any]] = {}
    for entry in context_entries:
        if entry.get("type") == "context_edit" and _key(entry.get("targetId")) is not None:
            edits[_key(entry.get("targetId"))] = entry
    messages: List[Any] = []
    for index, entry in enumerate(context_entries):
        if entry.get("type") == "compaction" and index > 0:
            continue
        projected = session_entry_to_context_messages(entry)
        key = _key(entry.get("id"))
        if key is not None and key in edits:
            projected = _apply_edit(projected, edits[key])
        messages.extend(projected)
    result: Dict[str, Any] = {"messages": messages, "model": model}
    if thinking_defined:
        result["thinkingLevel"] = thinking
    return result
