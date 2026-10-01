"""Wire-shape smoke tests for the Pi extension API surfaces of pig-sdk.

Each test runs an extension against a fake host socket and asserts the exact
frames it sends and how it decodes the host's answers.
"""

from __future__ import annotations

import os
import tempfile
import threading
from collections.abc import Callable
from typing import Any

from test_sdk import _read_frame, _start_fake_host, _write_frame

import pig_sdk


class _Host:
    """A connected fake host: register read, ready sent."""

    def __init__(self, ext: pig_sdk.Extension, ready: dict[str, Any] | None = None) -> None:
        tmp = tempfile.mkdtemp()
        sock_path = os.path.join(tmp, "ext.sock")
        self.listener = _start_fake_host(sock_path)
        self.thread = threading.Thread(target=ext.run_with_socket, args=(sock_path,), daemon=True)
        self.thread.start()
        self.conn, _ = self.listener.accept()
        self.register = _read_frame(self.conn)["register"]
        _write_frame(self.conn, {"type": "ready", "ready": {"cwd": tmp, "width": 80, **(ready or {})}})

    def request(self, req_id: str, request: dict[str, Any]) -> None:
        _write_frame(self.conn, {"type": "request", "id": req_id, "request": request})

    def read(self) -> dict[str, Any]:
        return _read_frame(self.conn)

    def answer(self, call: dict[str, Any], result: Any = None, error: dict[str, Any] | None = None) -> None:
        payload: dict[str, Any] = {"result": result}
        if error is not None:
            payload = {"error": error}
        _write_frame(self.conn, {"type": "call_result", "id": call["id"], "call_result": payload})

    def close(self) -> None:
        _write_frame(self.conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
        self.conn.close()
        self.listener.close()
        self.thread.join(timeout=2)


def _run_command(handler: Callable[[pig_sdk.Context], Any], answers: dict[str, Any], ready: dict[str, Any] | None = None) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Run handler as a command, answering each host call from answers.

    Returns the call frames the extension sent and its response frame.
    """
    ext = pig_sdk.Extension("py-surface")
    ext.command("go", "run", lambda ctx, _args: handler(ctx))
    host = _Host(ext, ready)
    calls: list[dict[str, Any]] = []
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        while True:
            env = host.read()
            if env["type"] == "response":
                return calls, env
            if env["type"] == "call":
                calls.append(env)
                host.answer(env, answers.get(env["call"]["method"]))
    finally:
        host.close()


def test_event_constants_match_upstream_names() -> None:
    names = [
        "project_trust", "resources_discover", "session_start", "session_info_changed",
        "session_before_switch", "session_before_fork", "session_before_compact", "session_compact",
        "session_compact_failed", "session_shutdown", "session_before_tree", "session_tree", "context",
        "context_with_system", "cache_warming_decision", "before_provider_request",
        "before_provider_headers", "after_provider_response", "before_agent_start", "agent_start",
        "agent_end", "agent_before_settle", "agent_settled", "ui_prompt_start", "ui_prompt_end",
        "turn_start", "turn_end", "message_start", "message_update", "message_end",
        "tool_execution_start", "tool_execution_update", "tool_execution_end", "model_select",
        "thinking_level_select", "tool_call", "tool_result", "user_bash", "input",
    ]
    for name in names:
        assert getattr(pig_sdk, "EVENT_" + name.upper()) == name


def test_register_tool_declares_definition_and_runs_execute() -> None:
    ext = pig_sdk.Extension("py-register-tool")
    ext.tool("other", "other tool", {"type": "object"}, lambda ctx, args: {"content": "other"})
    ext.register_tool(pig_sdk.ToolDefinition(
        name="greet",
        label="Greet",
        description="first",
        parameters={"type": "object"},
        execute=lambda ctx, args: {"content": "stale"},
    ))
    # A second registration of the name replaces the first in place.
    ext.register_tool(pig_sdk.ToolDefinition(
        name="greet",
        label="Greet",
        description="Say hello",
        prompt_snippet="Greet someone by name",
        prompt_guidelines=["Greet politely"],
        parameters={"type": "object", "properties": {"who": {"type": "string"}}},
        execution_mode="sequential",
        render_shell="self",
        prepare_arguments=lambda args: {"who": str(args.get("who", "")).strip()},
        execute=lambda ctx, args: {"content": f"hello {args['who']} ({ctx.tool_call_id})"},
        render_result=lambda ctx, result, options, render, width: ["done"],
    ))
    host = _Host(ext)
    try:
        assert host.register["tools"] == [
            {"name": "other", "description": "other tool", "parameters": {"type": "object"}},
            {
                "name": "greet",
                "description": "Say hello",
                "parameters": {"type": "object", "properties": {"who": {"type": "string"}}},
                "prompt_guidelines": ["Greet politely"],
                "label": "Greet",
                "prompt_snippet": "Greet someone by name",
                "execution_mode": "sequential",
                "render_shell": "self",
                "renders_call": False,
                "renders_result": True,
            },
        ]
        host.request("r1", {"method": "tool_call", "tool": "greet", "tool_call_id": "tc1", "args": {"who": " pi "}})
        response = host.read()
        assert response["response"] == {"result": {"content": "hello pi (tc1)"}, "error": None}
    finally:
        host.close()


def test_send_message_sends_details() -> None:
    calls, _ = _run_command(
        lambda ctx: ctx.send_message("note", [{"type": "text", "text": "hi"}], trigger_turn=False, details={"k": 1}),
        {},
    )
    assert [c["call"]["args"] for c in calls] == [{
        "message": {"customType": "note", "content": [{"type": "text", "text": "hi"}], "display": True, "details": {"k": 1}},
        "options": {"triggerTurn": False},
    }]


def test_exec_sends_options_and_decodes_result() -> None:
    results: list[pig_sdk.ExecResult] = []
    calls, _ = _run_command(
        lambda ctx: results.append(ctx.exec("sleep", ["5"], timeout=250, cwd="/tmp")),
        {"exec": {"stdout": "out", "stderr": "err", "code": 137, "killed": True}},
    )
    assert calls[0]["call"]["args"] == {"command": "sleep", "args": ["5"], "options": {"timeout": 250, "cwd": "/tmp"}}
    assert results == [pig_sdk.ExecResult(stdout="out", stderr="err", exit_code=137, killed=True)]


def test_dialog_timeout_sends_opts() -> None:
    seen: list[Any] = []

    def handler(ctx: pig_sdk.Context) -> None:
        seen.append(ctx.select("Pick", ["a", "b"], timeout=1000))
        seen.append(ctx.confirm("Sure?", "really", timeout=2000))
        seen.append(ctx.input("Name", "who", timeout=3000))
        seen.append(ctx.input("Plain"))

    calls, _ = _run_command(handler, {
        "ui.select": {"selected": "b", "ok": True},
        "ui.confirm": {"confirmed": True},
        "ui.input": {"text": "pi", "ok": True},
    })
    assert [(c["call"]["method"], c["call"]["args"]) for c in calls] == [
        ("ui.select", {"title": "Pick", "options": ["a", "b"], "opts": {"timeout": 1000}}),
        ("ui.confirm", {"title": "Sure?", "message": "really", "opts": {"timeout": 2000}}),
        ("ui.input", {"title": "Name", "placeholder": "who", "opts": {"timeout": 3000}}),
        ("ui.input", {"title": "Plain", "placeholder": ""}),
    ]
    assert seen == [("b", True), True, ("pi", True), ("pi", True)]


def test_set_footer_and_header_send_lines() -> None:
    def handler(ctx: pig_sdk.Context) -> None:
        ctx.set_footer(["f1", "f2"])
        ctx.set_header(["h1"])
        ctx.set_footer(None)
        ctx.set_header(None)

    calls, _ = _run_command(handler, {})
    assert [(c["call"]["method"], c["call"]["args"]) for c in calls] == [
        # Rows carry the width the SDK held (the ready width, 80): the host paints
        # them only at that width (public issue #104).
        ("ui.setFooter", {"lines": ["f1", "f2"], "width": 80}),
        ("ui.setHeader", {"lines": ["h1"], "width": 80}),
        ("ui.setFooter", {"clear": True}),
        ("ui.setHeader", {"clear": True}),
    ]


_PALETTE = {
    "name": "night",
    "sourcePath": "/themes/night.json",
    "foregrounds": {"accent": "\x1b[31m", "thinkingHigh": "\x1b[35m", "bashMode": "\x1b[36m"},
    "backgrounds": {"panel": "\x1b[44m"},
    "modifiers": True,
    "mode": "256color",
}


def test_has_ui_and_theme_follow_host_state() -> None:
    ext = pig_sdk.Extension("py-theme")
    observed: list[dict[str, Any]] = []

    def probe(ctx: pig_sdk.Context, _args: str) -> None:
        theme = ctx.theme

        def ansi(lookup: Callable[[str], str], token: str) -> str:
            try:
                return lookup(token)
            except ValueError as exc:
                return f"ValueError: {exc}"

        observed.append({
            "has_ui": ctx.has_ui(),
            "name": theme.name,
            "source_path": theme.source_path,
            "fg": theme.fg("accent", "x"),
            "fg_unknown": theme.fg("missing", "x"),
            "bg": theme.bg("panel", "x"),
            "bold": theme.bold("x"),
            "italic": theme.italic("x"),
            "underline": theme.underline("x"),
            "inverse": theme.inverse("x"),
            "strikethrough": theme.strikethrough("x"),
            "fg_ansi": ansi(theme.get_fg_ansi, "accent"),
            "bg_ansi": ansi(theme.get_bg_ansi, "panel"),
            "mode": theme.get_color_mode(),
            "thinking": theme.get_thinking_border_color("high")("x"),
            "thinking_unknown": theme.get_thinking_border_color("bogus")("x"),
            "bash": theme.get_bash_mode_border_color()("x"),
            "errors": [ansi(theme.get_fg_ansi, "missing"), ansi(theme.get_bg_ansi, "missing")],
        })

    ext.command("probe", "probe", probe)
    host = _Host(ext, {"state": {"hasUI": False, "theme": _PALETTE}})
    try:
        host.request("r1", {"method": "command", "tool": "probe"})
        assert host.read()["type"] == "response"
        _write_frame(host.conn, {"type": "notify", "notify": {"method": "theme_change", "args": {"name": "plain", "modifiers": False}}})
        _write_frame(host.conn, {"type": "notify", "notify": {"method": "state_update", "args": {"state": {"hasUI": True}}}})
        host.request("r2", {"method": "command", "tool": "probe"})
        assert host.read()["type"] == "response"
    finally:
        host.close()

    assert observed[0] == {
        "has_ui": False,
        "name": "night",
        "source_path": "/themes/night.json",
        "fg": "\x1b[31mx\x1b[39m",
        "fg_unknown": "x",
        "bg": "\x1b[44mx\x1b[49m",
        "bold": "\x1b[1mx\x1b[22m",
        "italic": "\x1b[3mx\x1b[23m",
        "underline": "\x1b[4mx\x1b[24m",
        "inverse": "\x1b[7mx\x1b[27m",
        "strikethrough": "\x1b[9mx\x1b[29m",
        "fg_ansi": "\x1b[31m",
        "bg_ansi": "\x1b[44m",
        "mode": "256color",
        "thinking": "\x1b[35mx\x1b[39m",
        "thinking_unknown": "x",
        "bash": "\x1b[36mx\x1b[39m",
        "errors": ["ValueError: Unknown theme color: missing", "ValueError: Unknown theme background color: missing"],
    }
    after = observed[1]
    assert after["has_ui"] is True
    assert (after["name"], after["source_path"], after["mode"]) == ("plain", None, "truecolor")
    assert (after["fg"], after["bold"]) == ("x", "x")
    assert after["fg_ansi"] == "ValueError: Unknown theme color: accent"


def _run_compact(answer: dict[str, Any]) -> tuple[dict[str, Any], list[Any]]:
    """Start a callback compaction from a command, then answer it after the
    command has returned. Returns the compact call frame and callback values."""
    ext = pig_sdk.Extension("py-compact")
    outcome: list[Any] = []
    done = threading.Event()

    def start(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.compact(
            custom_instructions="keep the plan",
            on_complete=lambda result: (outcome.append(("complete", result)), done.set()),
            on_error=lambda exc: (outcome.append(("error", str(exc))), done.set()),
        )

    ext.command("compact", "compact", start)
    host = _Host(ext)
    try:
        host.request("r1", {"method": "command", "tool": "compact"})
        call = response = None
        while call is None or response is None:
            env = host.read()
            if env["type"] == "call":
                call = env
            elif env["type"] == "response":
                response = env
        # The handler has finished; the compaction wait outlives it.
        assert response["response"]["error"] is None
        assert not done.is_set()
        _write_frame(host.conn, {"type": "call_result", "id": call["id"], "call_result": answer})
        assert done.wait(2)
    finally:
        host.close()
    return call, outcome


def test_compact_callbacks_on_success() -> None:
    result = {"summary": "s", "firstKeptEntryId": "e1", "tokensBefore": 1200}
    call, outcome = _run_compact({"result": result})
    assert call["call"] == {"method": "compact", "args": {"customInstructions": "keep the plan", "awaitCompletion": True}}
    assert "parent_request_id" not in call["call"]
    assert outcome == [("complete", result)]


def test_compact_callbacks_on_error() -> None:
    call, outcome = _run_compact({"error": {"message": "Nothing to compact"}})
    assert call["call"]["args"]["awaitCompletion"] is True
    assert "parent_request_id" not in call["call"]
    assert outcome == [("error", "Nothing to compact")]


def _probe_getter(call: Callable[[pig_sdk.Context], Any], answer: dict[str, Any] | None, error: dict[str, Any] | None = None) -> Any:
    """Run one getter against a fake host answering its call, returning the value or the raised exception."""
    outcome: list[Any] = []

    def handler(ctx: pig_sdk.Context) -> None:
        try:
            outcome.append(call(ctx))
        except Exception as exc:  # noqa: BLE001 - the exception is the observation
            outcome.append(exc)

    ext = pig_sdk.Extension("py-getters")
    ext.command("go", "run", lambda ctx, _args: handler(ctx))
    host = _Host(ext)
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        while True:
            env = host.read()
            if env["type"] == "call":
                host.answer(env, answer, error)
            elif env["type"] == "response":
                return outcome[0]
    finally:
        host.close()


def test_getters_distinguish_absent_empty_and_failure() -> None:
    """Pi's getters return undefined for absent state and throw for a failed host call."""
    assert _probe_getter(lambda c: c.get_session_name(), {"name": "named"}) == "named"
    assert _probe_getter(lambda c: c.get_session_name(), {"name": ""}) is None
    assert _probe_getter(lambda c: c.get_session_file(), {"sessionFile": ""}) is None
    assert _probe_getter(lambda c: c.get_leaf_id(), {"leafId": None}) is None
    assert _probe_getter(lambda c: c.get_context_usage(), None) is None
    assert _probe_getter(lambda c: c.get_model_info(), {}) is None
    assert _probe_getter(lambda c: c.get_editor_text(), {"text": ""}) == ""
    assert _probe_getter(lambda c: c.get_flag("f"), {"value": False}) is False
    assert _probe_getter(lambda c: c.is_idle(), {"idle": False}) is False
    failure = {"code": "host_failed", "message": "boom"}
    for name in ("get_session_name", "get_editor_text", "get_active_tools", "get_context_usage", "is_idle", "is_project_trusted", "get_leaf_id"):
        raised = _probe_getter(lambda c, name=name: getattr(c, name)(), None, failure)
        assert isinstance(raised, pig_sdk.HostCallError) and "boom" in str(raised), name
    raised = _probe_getter(lambda c: c.get_flag("f"), None, failure)
    assert isinstance(raised, pig_sdk.HostCallError)
    missing = _probe_getter(lambda c: c.get_thinking_level(), {"unrelated": 1})
    assert isinstance(missing, pig_sdk.HostCallError)


def test_session_log_getters_report_subscription_failure() -> None:
    """A failed watchSessionLog call raises from every mirror read instead of returning a partial mirror."""
    failure = {"code": "host_failed", "message": "log unavailable"}
    for name in ("get_branch", "get_entries"):
        raised = _probe_getter(lambda c, name=name: getattr(c, name)(), None, failure)
        assert isinstance(raised, pig_sdk.HostCallError) and "log unavailable" in str(raised), name
