"""Host-backed reads of the active session, preserving missing values as None."""
from typing import Any


class SessionManager:
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
