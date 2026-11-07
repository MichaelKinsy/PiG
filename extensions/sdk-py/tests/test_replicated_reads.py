"""get_model_info and the session id are answered from the replica every state snapshot refreshes, with no host call.

Pi reads ctx.model and sessionManager.getSessionId() locally; the Go and Rust SDKs answer the same reads from the same snapshots (test/extension-conformance/replica_reads_test.go).
A context that exists before the first snapshot, and a withSession replacement context, still ask the host. getContextUsage stays a host call.
"""

from __future__ import annotations

import dataclasses
from typing import Any

import pytest

import pig_sdk

MODEL_A = {"id": "m-a", "name": "Model A", "provider": "prov", "contextWindow": 1000, "maxTokens": 100, "cost": {"input": 1, "output": 2}}
MODEL_B = {"id": "m-b", "name": "Model B", "provider": {"id": "prov-b"}, "contextWindow": 2000}
STALE = "This extension ctx is stale after session replacement or reload."


class Recorder:
    """Stands in for the host: records every call and answers with the canned reply of its method."""

    def __init__(self, replies: dict[str, Any]) -> None:
        self.replies = replies
        self.calls: list[tuple[str, Any]] = []

    def __call__(self, method: str, args: Any = None, *_: Any, **__: Any) -> dict[str, Any]:
        self.calls.append((method, args))
        return {"result": self.replies[method]}


def make(replies: dict[str, Any] | None = None) -> tuple[pig_sdk.Extension, pig_sdk.Context, Recorder]:
    ext = pig_sdk.Extension("replicated-reads")
    host = Recorder(
        replies
        or {
            "getModelInfo": {"id": "host-model", "provider": "host-prov"},
            "getSessionID": {"sessionId": "host-session"},
            "sessionRead": "host-manager-session",
        }
    )
    ext._call = host  # type: ignore[method-assign]
    return ext, pig_sdk.Context(ext), host


def apply(ext: pig_sdk.Extension, state: dict[str, Any]) -> None:
    with ext._state_lock:
        ext._apply_ui_state(state)


def replacement_of(ctx: pig_sdk.Context) -> pig_sdk.Context:
    replaced = pig_sdk.ReplacedSessionContext(**{item.name: getattr(ctx, item.name) for item in dataclasses.fields(pig_sdk.Context)})
    replaced._replacement = pig_sdk._ReplacementState()
    return replaced


def test_snapshot_answers_the_three_reads_without_a_host_call():
    ext, ctx, host = make()
    apply(ext, {"model": MODEL_A, "session": {"sessionId": "s-1", "sessionName": "n"}})
    assert ctx.get_model_info() == MODEL_A
    assert ctx.get_session_id() == "s-1"
    assert ctx.session_manager.get_session_id() == "s-1"
    assert host.calls == []


def test_later_snapshots_replace_the_replica_and_absent_members_keep_it():
    ext, ctx, host = make()
    apply(ext, {"model": MODEL_A, "session": {"sessionId": "s-1"}})
    apply(ext, {"isIdle": True})
    assert (ctx.get_model_info(), ctx.get_session_id()) == (MODEL_A, "s-1")
    apply(ext, {"model": MODEL_B, "session": {"sessionId": "s-2"}})
    assert (ctx.get_model_info(), ctx.get_session_id(), ctx.session_manager.get_session_id()) == (MODEL_B, "s-2", "s-2")
    apply(ext, {"session": {"sessionId": "s-2", "sessionName": "named"}})
    assert ctx.get_session_id() == "s-2"
    apply(ext, {"model": None})
    assert ctx.get_model_info() is None
    apply(ext, {"isIdle": False})
    assert ctx.get_model_info() is None
    apply(ext, {"session": {}})
    assert ctx.get_session_id() == ""
    assert host.calls == []


def test_a_snapshot_that_repeats_the_model_keeps_the_replica_and_the_identity():
    ext, ctx, host = make()
    apply(ext, {"model": MODEL_A})
    apply(ext, {"model": dict(MODEL_A, cost=dict(MODEL_A["cost"]))})
    assert (ctx.model, ctx.model_provider, ctx.get_model_info()) == ("m-a", "prov", MODEL_A)
    apply(ext, {"model": MODEL_B})
    assert (ctx.model, ctx.model_provider, ctx.get_model_info()) == ("m-b", "prov-b", MODEL_B)
    assert host.calls == []


def test_a_model_without_an_id_is_no_model():
    ext, ctx, host = make()
    apply(ext, {"model": {"id": "", "name": "Only Name"}})
    assert ctx.get_model_info() is None
    assert ctx.model == "Only Name"
    assert host.calls == []


def test_before_the_first_snapshot_the_reads_ask_the_host():
    ext, ctx, host = make()
    assert ctx.get_model_info() == {"id": "host-model", "provider": "host-prov"}
    assert ctx.get_session_id() == "host-session"
    assert ctx.session_manager.get_session_id() == "host-manager-session"
    assert [call[0] for call in host.calls] == ["getModelInfo", "getSessionID", "sessionRead"]
    assert host.calls[2][1] == {"method": "getSessionId", "args": {}}


def test_each_value_is_known_once_its_member_was_seen():
    ext, ctx, host = make()
    apply(ext, {"model": None})
    assert ctx.get_model_info() is None
    assert host.calls == []
    assert ctx.get_session_id() == "host-session"
    assert [call[0] for call in host.calls] == ["getSessionID"]
    apply(ext, {"session": {"sessionId": "s-1"}})
    assert ctx.get_session_id() == "s-1"
    assert len(host.calls) == 1


def test_the_model_a_caller_gets_is_its_own_copy():
    ext, ctx, host = make()
    state = {"model": {"id": "m-a", "cost": {"input": 1}, "tags": ["x"]}}
    apply(ext, state)
    state["model"]["cost"]["input"] = 99
    first = ctx.get_model_info()
    assert first == {"id": "m-a", "cost": {"input": 1}, "tags": ["x"]}
    first["cost"]["input"] = 7
    first["tags"].append("y")
    first["id"] = "mutated"
    assert ctx.get_model_info() == {"id": "m-a", "cost": {"input": 1}, "tags": ["x"]}
    assert host.calls == []


def test_a_model_that_is_not_an_object_is_no_model_as_the_host_reply_was():
    ext, ctx, host = make({"getModelInfo": "not-an-object"})
    assert ctx.get_model_info() is None
    apply(ext, {"model": "not-an-object"})
    assert ctx.get_model_info() is None
    assert host.calls == [("getModelInfo", None)]


def test_a_replacement_context_asks_the_host():
    ext, ctx, host = make()
    apply(ext, {"model": MODEL_A, "session": {"sessionId": "s-1"}})
    replaced = replacement_of(ctx)
    assert replaced.get_model_info() == {"id": "host-model", "provider": "host-prov"}
    assert replaced.get_session_id() == "host-session"
    assert replaced.session_manager.get_session_id() == "host-manager-session"
    assert [call[0] for call in host.calls] == ["getModelInfo", "getSessionID", "sessionRead"]


def test_a_stale_context_raises_the_stale_message_from_the_local_reads():
    ext, ctx, host = make()
    apply(ext, {"model": MODEL_A, "session": {"sessionId": "s-1"}})
    ext._stale_message = STALE
    for read in (ctx.get_model_info, ctx.get_session_id, ctx.session_manager.get_session_id):
        with pytest.raises(RuntimeError, match="stale after session replacement or reload"):
            read()
    assert host.calls == []


def named_session(ext: pig_sdk.Extension, name: str, file: str, session_id: str = "s-1") -> None:
    apply(ext, {"session": {"sessionId": session_id, "sessionName": name, "sessionFile": file}})


def read_name_and_file(ctx: pig_sdk.Context) -> tuple[Any, Any, Any, Any]:
    manager = ctx.session_manager
    return ctx.get_session_name(), ctx.get_session_file(), manager.get_session_name(), manager.get_session_file()


def test_snapshot_answers_the_session_name_and_file_without_a_host_call():
    ext, ctx, host = make()
    named_session(ext, "my session", "/s/a.jsonl")
    assert read_name_and_file(ctx) == ("my session", "/s/a.jsonl", "my session", "/s/a.jsonl")
    assert ctx.session_name == "my session"
    assert host.calls == []


def test_later_snapshots_replace_the_name_and_file_and_empty_values_clear_them():
    ext, ctx, host = make()
    named_session(ext, "one", "/s/a.jsonl")
    apply(ext, {"isIdle": True})
    assert read_name_and_file(ctx) == ("one", "/s/a.jsonl", "one", "/s/a.jsonl")
    named_session(ext, "two", "/s/b.jsonl", "s-2")
    assert read_name_and_file(ctx) == ("two", "/s/b.jsonl", "two", "/s/b.jsonl")
    named_session(ext, "", "")
    assert read_name_and_file(ctx) == (None, None, None, None)
    assert (ctx.session_name, ext._session_file) == ("", "")
    apply(ext, {"session": {"sessionId": "s-3"}})
    assert read_name_and_file(ctx) == (None, None, None, None)
    assert host.calls == []


def test_before_the_first_snapshot_the_name_and_file_ask_the_host():
    ext, ctx, host = make({"getSessionName": {"name": "host-name"}, "getSessionFile": {"sessionFile": ""}, "sessionRead": "host-manager"})
    assert (ctx.get_session_name(), ctx.get_session_file()) == ("host-name", None)
    assert ctx.session_manager.get_session_name() == "host-manager"
    assert ctx.session_manager.get_session_file() == "host-manager"
    assert [call[0] for call in host.calls] == ["getSessionName", "getSessionFile", "sessionRead", "sessionRead"]
    assert [call[1]["method"] for call in host.calls[2:]] == ["getSessionName", "getSessionFile"]


def test_a_replacement_context_asks_the_host_for_the_name_and_file():
    ext, ctx, host = make({"getSessionName": {"name": "host-name"}, "getSessionFile": {"sessionFile": "/h/f.jsonl"}, "sessionRead": "host-manager"})
    named_session(ext, "mine", "/s/a.jsonl")
    replaced = replacement_of(ctx)
    assert (replaced.get_session_name(), replaced.get_session_file()) == ("host-name", "/h/f.jsonl")
    assert replaced.session_manager.get_session_name() == "host-manager"
    assert replaced.session_manager.get_session_file() == "host-manager"
    assert [call[0] for call in host.calls] == ["getSessionName", "getSessionFile", "sessionRead", "sessionRead"]


def test_a_stale_context_raises_the_stale_message_from_the_name_and_file_reads():
    ext, ctx, host = make()
    named_session(ext, "mine", "/s/a.jsonl")
    ext._stale_message = STALE
    manager = ctx.session_manager
    for read in (ctx.get_session_name, ctx.get_session_file, manager.get_session_name, manager.get_session_file):
        with pytest.raises(RuntimeError, match="stale after session replacement or reload"):
            read()
    assert host.calls == []


def test_get_context_usage_stays_a_host_call():
    ext, ctx, host = make({"getContextUsage": {"tokens": 5}})
    apply(ext, {"model": MODEL_A, "session": {"sessionId": "s-1"}})
    assert ctx.get_context_usage() == {"tokens": 5}
    assert [call[0] for call in host.calls] == ["getContextUsage"]
