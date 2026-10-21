"""Host-backed reads of the active session, preserving missing values as None, and Pi's SessionManager class."""
from __future__ import annotations

import os
from datetime import datetime, timezone
from typing import Any

from ._graph import _GraphObject, _callback_id


class ReadonlySessionManager:
    """Pi's ``ReadonlySessionManager``: the reads of the running session (``ctx.session_manager()``)."""

    def __init__(self, context: Any) -> None:
        self._context = context

    def _read(self, method: str, **args: Any) -> Any:
        return self._context._call("sessionRead", {"method": method, "args": args}).get("result")

    def get_cwd(self) -> str:
        return self._read("getCwd")

    def get_session_dir(self) -> str:
        return self._read("getSessionDir")

    def get_session_id(self) -> str:
        return self._read("getSessionId")

    def get_session_file(self) -> str | None:
        return self._read("getSessionFile")

    def get_session_name(self) -> str | None:
        return self._read("getSessionName")

    def get_leaf_id(self) -> str | None:
        return self._read("getLeafId")

    def get_header(self) -> dict[str, Any] | None:
        return self._read("getHeader")

    def get_leaf_entry(self) -> dict[str, Any] | None:
        return self._read("getLeafEntry")

    def get_entry(self, entry_id: str) -> dict[str, Any] | None:
        return self._read("getEntry", id=entry_id)

    def get_label(self, entry_id: str) -> str | None:
        return self._read("getLabel", id=entry_id)

    def get_entries(self) -> list[dict[str, Any]]:
        return self._read("getEntries")

    def get_branch(self, from_id: str | None = None) -> list[dict[str, Any]]:
        return self._read("getBranch", fromId=from_id)

    def get_children(self, parent_id: str | None) -> list[dict[str, Any]]:
        return self._read("getChildren", parentId=parent_id)

    def get_tree(self) -> list[dict[str, Any]]:
        return self._read("getTree")

    def build_context_entries(self) -> list[dict[str, Any]]:
        return self._read("buildContextEntries")

    def build_session_projection(self) -> dict[str, Any]:
        return self._read("buildSessionProjection")

    def build_session_context(self) -> dict[str, Any]:
        return self._read("buildSessionContext")

    def is_persisted(self) -> bool:
        return self._read("isPersisted")

    def uses_default_session_dir(self) -> bool:
        return self._read("usesDefaultSessionDir")


class SetupSessionManager(ReadonlySessionManager):
    """The SessionManager of new_session's ``setup`` callback (types.ts:411, agent-session-runtime.ts:254-257): the reads of ReadonlySessionManager and the appends that seed the replacement Session. Each append acts on the replacement Session and returns the new entry's id."""

    def _write(self, method: str, **args: Any) -> str:
        return self._context._call("sessionWrite", {"method": method, "args": args}).get("result")

    def append_message(self, message: dict[str, Any]) -> str:
        return self._write("appendMessage", message=message)

    def append_custom_entry(self, custom_type: str, data: Any = None) -> str:
        return self._write("appendCustomEntry", customType=custom_type, data=data)

    def append_custom_message_entry(self, custom_type: str, content: Any, display: bool, details: Any = None) -> str:
        return self._write("appendCustomMessageEntry", customType=custom_type, content=content, display=display, details=details)

    def append_session_info(self, name: str) -> str:
        return self._write("appendSessionInfo", name=name)

    def append_model_change(self, provider: str, model_id: str) -> str:
        return self._write("appendModelChange", provider=provider, modelId=model_id)

    def append_thinking_level_change(self, thinking_level: str) -> str:
        return self._write("appendThinkingLevelChange", thinkingLevel=thinking_level)

    def append_label_change(self, target_id: str, label: str | None) -> str:
        return self._write("appendLabelChange", targetId=target_id, label=label)


def _parse_date(value: str) -> datetime:
    """Date.toISOString text as a timezone-aware datetime."""
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc)


def _session_info(raw: dict[str, Any]) -> dict[str, Any]:
    """Pi's SessionInfo: the JSON object with ``created`` and ``modified`` as datetimes."""
    info = dict(raw)
    info["created"] = _parse_date(raw["created"])
    info["modified"] = _parse_date(raw["modified"])
    return info


def _progress_id(context: Any, on_progress: Any) -> Any:
    if on_progress is None:
        return None
    return _callback_id(context, lambda loaded, total, partial: on_progress(loaded, total, None if partial is None else [_session_info(info) for info in partial]))


class SessionManager(_GraphObject):
    """Pi's ``SessionManager`` class (core/session-manager.ts): an append-only session log an extension constructs, reads and appends to. The host owns the log; a SessionManager passed to ``create_agent_session`` is the very log the child session appends to. Build one with ``create``, ``open``, ``continue_recent``, ``in_memory`` or ``fork_from``; every method is Pi's under its snake_case name."""

    @classmethod
    def create(cls, context: Any, cwd: str, session_dir: str | None = None, options: dict[str, Any] | None = None) -> "SessionManager":
        """``SessionManager.create(cwd, sessionDir?, options?)``: a new persisted session. ``options`` is Pi's NewSessionOptions (``id``, ``parentSession``)."""
        return cls(context, "SessionManager", "create", cwd, session_dir, options)

    @classmethod
    def open(cls, context: Any, path: str, session_dir: str | None = None, cwd_override: str | None = None) -> "SessionManager":
        """``SessionManager.open(path, sessionDir?, cwdOverride?)``: the session file at ``path``."""
        return cls(context, "SessionManager", "open", path, session_dir, cwd_override)

    @classmethod
    def continue_recent(cls, context: Any, cwd: str, session_dir: str | None = None) -> "SessionManager":
        """``SessionManager.continueRecent(cwd, sessionDir?)``: the most recent session of the directory, or a new one."""
        return cls(context, "SessionManager", "continueRecent", cwd, session_dir)

    @classmethod
    def in_memory(cls, context: Any, cwd: str | None = None, options: dict[str, Any] | None = None, entries: list[dict[str, Any]] | None = None) -> "SessionManager":
        """``SessionManager.inMemory(cwd = process.cwd(), options?, entries?)``: a session that is never written."""
        return cls(context, "SessionManager", "inMemory", cwd if cwd is not None else os.getcwd(), options, entries)

    @classmethod
    def fork_from(cls, context: Any, source_path: str, target_cwd: str, session_dir: str | None = None, options: dict[str, Any] | None = None) -> "SessionManager":
        """``SessionManager.forkFrom(sourcePath, targetCwd, sessionDir?, options?)``: a new session in ``target_cwd`` with the whole history of the source."""
        return cls(context, "SessionManager", "forkFrom", source_path, target_cwd, session_dir, options)

    @staticmethod
    def find_by_id(context: Any, cwd: str, session_id: str, session_dir: str | None = None) -> str | None:
        """``SessionManager.findById(cwd, id, sessionDir?)``: the path of the session file with that exact id."""
        return _GraphObject._static(context, "SessionManager", "findById", cwd, session_id, session_dir)

    @staticmethod
    def list(context: Any, cwd: str, session_dir: str | None = None, on_progress: Any = None) -> list[dict[str, Any]]:
        """``SessionManager.list(cwd, sessionDir?, onProgress?)``: the sessions of a directory, most recently modified first, as Pi's SessionInfo dicts (``created`` and ``modified`` are datetimes). ``on_progress(loaded, total, partial)`` is called as session files are read; ``partial`` is the sessions read so far on the updates that carry them and ``None`` on the others."""
        return [_session_info(info) for info in _GraphObject._static_async(context, "SessionManager", "list", cwd, session_dir, _progress_id(context, on_progress)) or []]

    @staticmethod
    def list_all(context: Any, session_dir: str | None = None, on_progress: Any = None) -> list[dict[str, Any]]:
        """``SessionManager.listAll(sessionDir?, onProgress?)``: the sessions of every project (or of ``session_dir``), most recently modified first."""
        return [_session_info(info) for info in _GraphObject._static_async(context, "SessionManager", "listAll", session_dir, _progress_id(context, on_progress)) or []]

    def get_cwd(self) -> str:
        return self._invoke("getCwd")

    def get_session_dir(self) -> str:
        return self._invoke("getSessionDir")

    def get_session_id(self) -> str:
        return self._invoke("getSessionId")

    def get_session_file(self) -> str | None:
        return self._invoke("getSessionFile")

    def get_session_name(self) -> str | None:
        return self._invoke("getSessionName")

    def get_leaf_id(self) -> str | None:
        return self._invoke("getLeafId")

    def get_header(self) -> dict[str, Any] | None:
        return self._invoke("getHeader")

    def get_leaf_entry(self) -> dict[str, Any] | None:
        return self._invoke("getLeafEntry")

    def get_entry(self, entry_id: str) -> dict[str, Any] | None:
        return self._invoke("getEntry", entry_id)

    def get_label(self, entry_id: str) -> str | None:
        return self._invoke("getLabel", entry_id)

    def get_entry_count(self) -> int:
        return self._invoke("getEntryCount")

    def get_entries(self) -> list[dict[str, Any]]:
        return self._invoke("getEntries")

    def get_branch(self, from_id: str | None = None) -> list[dict[str, Any]]:
        return self._invoke("getBranch", from_id)

    def get_children(self, parent_id: str) -> list[dict[str, Any]]:
        return self._invoke("getChildren", parent_id)

    def get_tree(self) -> list[dict[str, Any]]:
        return self._invoke("getTree")

    def build_context_entries(self) -> list[dict[str, Any]]:
        return self._invoke("buildContextEntries")

    def build_session_projection(self) -> dict[str, Any]:
        return self._invoke("buildSessionProjection")

    def build_session_context(self) -> dict[str, Any]:
        return self._invoke("buildSessionContext")

    def is_persisted(self) -> bool:
        return self._invoke("isPersisted")

    def uses_default_session_dir(self) -> bool:
        return self._invoke("usesDefaultSessionDir")

    def append_message(self, message: dict[str, Any]) -> str:
        """Append a message (a Pi Message, CustomMessage or BashExecutionMessage as a dict); returns the entry id."""
        return self._invoke("appendMessage", message)

    def append_thinking_level_change(self, thinking_level: str) -> str:
        return self._invoke("appendThinkingLevelChange", thinking_level)

    def append_model_change(self, provider: str, model_id: str) -> str:
        return self._invoke("appendModelChange", provider, model_id)

    def append_usage(self, kind: str, provider: str, model: str, usage: dict[str, Any], note: str | None = None) -> dict[str, Any]:
        """Record the usage of work outside the conversation; returns the entry."""
        return self._invoke("appendUsage", kind, provider, model, usage, note)

    def append_compaction(self, summary: str, first_kept_entry_id: str, tokens_before: int, details: Any = None, from_hook: bool | None = None, usage: dict[str, Any] | None = None) -> str:
        return self._invoke("appendCompaction", summary, first_kept_entry_id, tokens_before, details, from_hook, usage)

    def append_custom_entry(self, custom_type: str, data: Any = None) -> str:
        return self._invoke("appendCustomEntry", custom_type, data)

    def append_session_info(self, name: str) -> str:
        return self._invoke("appendSessionInfo", name)

    def append_custom_message_entry(self, custom_type: str, content: Any, display: bool, details: Any = None) -> str:
        return self._invoke("appendCustomMessageEntry", custom_type, content, display, details)

    def append_context_edit(self, target_id: str, replacement: dict[str, Any]) -> str:
        return self._invoke("appendContextEdit", target_id, replacement)

    def append_label_change(self, target_id: str, label: str | None) -> str:
        return self._invoke("appendLabelChange", target_id, label)

    def branch(self, branch_from_id: str) -> None:
        self._invoke("branch", branch_from_id)

    def reset_leaf(self) -> None:
        self._invoke("resetLeaf")

    def branch_with_summary(self, branch_from_id: str | None, summary: str, details: Any = None, from_hook: bool | None = None, usage: dict[str, Any] | None = None) -> str:
        return self._invoke("branchWithSummary", branch_from_id, summary, details, from_hook, usage)

    def create_branched_session(self, leaf_id: str) -> str | None:
        """Start a new session holding the path to ``leaf_id``; returns the new session file, None when not persisted."""
        return self._invoke("createBranchedSession", leaf_id)

    def new_session(self, options: dict[str, Any] | None = None) -> str | None:
        """Reset the manager to an empty session; returns the new session file, None when not persisted."""
        return self._invoke("newSession", options)

    def set_session_file(self, session_file: str) -> None:
        self._invoke("setSessionFile", session_file)
