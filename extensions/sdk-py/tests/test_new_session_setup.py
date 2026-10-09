"""newSession's ``setup`` callback (packages/coding-agent/src/core/extensions/types.ts:411, agent-session-runtime.ts:254-257).

The callback stays in the extension: the call names it by handle, the host sends a ``setup`` request, and the callback seeds the
replacement Session through ``sessionWrite`` host calls that name that request.
"""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

import pytest

import pig_sdk
from pig_sdk import _WithSessionRegistry
from pig_sdk.session_manager import SetupSessionManager


class _Ctx:
    def __init__(self) -> None:
        self.calls: list[tuple[str, dict[str, Any]]] = []

    def _call(self, method: str, args: dict[str, Any] | None = None) -> dict[str, Any]:
        self.calls.append((method, args or {}))
        return {"result": f"id-{len(self.calls)}"}


def test_setup_manager_appends_reach_the_host_as_session_write() -> None:
    ctx = _Ctx()
    manager = SetupSessionManager(ctx)
    ids = [
        manager.append_custom_entry("note", {"a": 1}),
        manager.append_custom_message_entry("c", "hi", True, {"d": 2}),
        manager.append_session_info("seeded"),
        manager.append_model_change("p", "m"),
        manager.append_thinking_level_change("high"),
        manager.append_label_change("e1", None),
        manager.append_message({"role": "user", "content": "x"}),
    ]
    assert ids == [f"id-{i}" for i in range(1, 8)]
    assert ctx.calls == [
        ("sessionWrite", {"method": "appendCustomEntry", "args": {"customType": "note", "data": {"a": 1}}}),
        ("sessionWrite", {"method": "appendCustomMessageEntry", "args": {"customType": "c", "content": "hi", "display": True, "details": {"d": 2}}}),
        ("sessionWrite", {"method": "appendSessionInfo", "args": {"name": "seeded"}}),
        ("sessionWrite", {"method": "appendModelChange", "args": {"provider": "p", "modelId": "m"}}),
        ("sessionWrite", {"method": "appendThinkingLevelChange", "args": {"thinkingLevel": "high"}}),
        ("sessionWrite", {"method": "appendLabelChange", "args": {"targetId": "e1", "label": None}}),
        ("sessionWrite", {"method": "appendMessage", "args": {"message": {"role": "user", "content": "x"}}}),
    ]


def test_dispatch_setup_runs_the_registered_callback_and_records_its_error() -> None:
    registry = _WithSessionRegistry()
    seen: list[Any] = []
    handle, _ = registry.add("ext:setup", lambda manager: seen.append(manager))
    ctx = _Ctx()
    registry.dispatch_setup(ctx, {"handle": handle})  # type: ignore[arg-type]
    assert len(seen) == 1 and isinstance(seen[0], SetupSessionManager)

    boom = RuntimeError("seed failed")

    def fail(manager: SetupSessionManager) -> None:
        raise boom

    failing, entry = registry.add("ext:setup", fail)
    with pytest.raises(RuntimeError, match="seed failed"):
        registry.dispatch_setup(ctx, {"handle": failing})  # type: ignore[arg-type]
    assert entry.error is boom
    registry.remove(failing)
    with pytest.raises(RuntimeError, match="unknown setup callback"):
        registry.dispatch_setup(ctx, {"handle": failing})  # type: ignore[arg-type]


def test_new_session_names_the_setup_callback_by_handle() -> None:
    sent: list[dict[str, Any]] = []
    setups = _WithSessionRegistry()
    holder: dict[str, Any] = {}

    def call_replacement(method: str, args: dict[str, Any]) -> Any:
        sent.append({"method": method, **args})
        holder["registered"] = args["setup"] in setups._entries  # noqa: SLF001
        return {"cancelled": False}

    self_ = SimpleNamespace(
        extension=SimpleNamespace(name="py-ext", _setups=setups),
        _call_replacement=call_replacement,
    )
    result = pig_sdk.Context.new_session(self_, {"parentSession": "/p.jsonl", "setup": lambda manager: None})  # type: ignore[arg-type]
    assert result == {"cancelled": False}
    assert len(sent) == 1 and sent[0]["method"] == "newSession" and sent[0]["parentSession"] == "/p.jsonl"
    assert isinstance(sent[0]["setup"], str) and holder["registered"] is True
    assert setups._entries == {}  # noqa: SLF001  the handle is released once the call returns
    with pytest.raises(TypeError, match="setup must be callable"):
        pig_sdk.Context.new_session(self_, {"setup": "nope"})  # type: ignore[arg-type]
