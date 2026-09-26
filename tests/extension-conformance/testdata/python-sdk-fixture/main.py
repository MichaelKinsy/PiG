#!/usr/bin/env python3
from __future__ import annotations

import json
import pathlib
import sys
import threading
import time

ROOT = pathlib.Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "extensions" / "sdk-py"))

import pig_sdk  # noqa: E402


class FocusedList:
    def __init__(self) -> None:
        self.items = ["alpha", "beta", "gamma"]
        self.selected = 0
        self.disposed = False

    def render(self, width: int) -> list[str]:
        lines = [f"focused width={width}"]
        lines.extend(("> " if index == self.selected else "  ") + item for index, item in enumerate(self.items))
        return lines

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        if data == "\x1b[A":
            self.selected = (self.selected - 1) % len(self.items)
        elif data == "\x1b[B":
            self.selected = (self.selected + 1) % len(self.items)
        elif data == "\x1b[6~":
            self.selected = min(self.selected + 2, len(self.items) - 1)
        elif data in {"\r", "\n"}:
            return pig_sdk.RemoteComponentResult(done=True, value=self.items[self.selected])
        elif data == "\x1b":
            return pig_sdk.RemoteComponentResult(done=True)
        return pig_sdk.RemoteComponentResult()

    def dispose(self) -> None:
        self.disposed = True


class TimerFocused:
    def __init__(self) -> None:
        self.frame = 0
        self.disposed = False
        self.detached = False
        self._invalidate = None
        self._lock = threading.Lock()
        self._stop = threading.Event()
        self._worker = threading.Thread(target=self._tick, daemon=True)
        self._worker.start()

    def _tick(self) -> None:
        while not self._stop.wait(0.02):
            with self._lock:
                self.frame += 1
                invalidate = self._invalidate
            if invalidate is not None:
                invalidate()

    def render(self, width: int) -> list[str]:
        with self._lock:
            return [f"timer frame={self.frame} width={width}"]

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        with self._lock:
            if data == "\r":
                return pig_sdk.RemoteComponentResult(done=True, value=self.frame)
        return pig_sdk.RemoteComponentResult()

    def set_invalidate(self, invalidate) -> None:
        with self._lock:
            self._invalidate = invalidate
            self.detached = invalidate is None

    def dispose(self) -> None:
        self._stop.set()
        self._worker.join()
        self.disposed = True


def conformance_login_definition() -> pig_sdk.LoginDefinition:
    return pig_sdk.LoginDefinition(
        brand=["A" * 41 for _ in range(5)],
        hero=["A" * 32 for _ in range(14)],
        mascot=["A" * 16 for _ in range(14)],
        palette={"A": "#123ABC"},
        name="Conformance Pig",
        description="Cross-language login fixture",
        tagline="One canonical definition across every SDK",
    )


def new_extension() -> pig_sdk.Extension:
    prompt_lock = threading.Lock()
    prompt_sequence = 0

    def ui_prompt_event(ctx, data):
        nonlocal prompt_sequence
        with prompt_lock:
            sequence = prompt_sequence
            prompt_sequence += 1
        title = data["title"] if "title" in data else "(none)"
        if title.startswith("fifo:"):
            ctx.notify("fifo:%d:%s:%s" % (sequence, data["type"], title), "info")
            return
        ctx.notify("ui_prompt:%s:%s:%s:%s" % (data.get("type"), data.get("reason"), data.get("kind"), title), "info")

    ext = pig_sdk.Extension("python-sdk-fixture")
    ext.message_renderer(
        "conformance-message",
        lambda _ctx, message, options, width: [json.dumps(options)] if message.get("content") == "padding-options" else [
            "renderer:%s:expanded=%s:width=%d"
            % (message.get("content", ""), str(bool(options.get("expanded"))).lower(), width)
        ],
    )
    ext.entry_renderer(
        "conformance-entry",
        lambda _ctx, entry, options, width: [
            "entryrenderer:%s:expanded=%s:width=%d"
            % (entry.get("data", ""), str(bool(options.get("expanded"))).lower(), width)
        ],
    )

    def echo(ctx: pig_sdk.Context, params: dict) -> dict:
        return {"content": "echo: " + str(params.get("text", ""))}

    def tool_error(ctx: pig_sdk.Context, params: dict) -> dict:
        raise RuntimeError("tool exploded")

    def tool_is_error(ctx: pig_sdk.Context, params: dict) -> dict:
        return {"content": "soft tool error", "is_error": True}

    ext.tool("echo", "Echo input text", {"type": "object"}, echo)
    ext.tool("render_probe", "Render its own tool card", {"type": "object", "properties": {}}, lambda _ctx, _params: "render ok")

    def render_probe_call(_ctx, args, render, width):
        render.state["calls"] = render.state.get("calls", 0) + 1
        return ["toolrender:call:%s:partial=%s:calls=%d:width=%d" % (args.get("topic"), str(render.is_partial).lower(), render.state["calls"], width)]

    def render_probe_result(_ctx, result, options, render, width):
        return [
            "toolrender:result:%s:%s:expanded=%s:calls=%s:width=%d"
            % (result["content"][0]["text"], result["details"]["k"], str(bool(options.get("expanded"))).lower(), render.state.get("calls"), width)
        ]

    ext.tool_renderers("render_probe", render_call=render_probe_call, render_result=render_probe_result, render_shell="self")
    abort_observed = [False]

    def update_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "step 1"}]})
        ctx.on_update({"content": [{"type": "text", "text": "step 2"}]})
        return {"content": [{"type": "text", "text": "done"}]}

    def abort_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "waiting"}]})
        while not ctx.is_cancelled():
            time.sleep(0.01)
        abort_observed[0] = True
        return {"content": "aborted"}

    ext.tool("update_tool", "Stream two partial results", {"type": "object", "properties": {}}, update_tool)
    ext.tool("abort_tool", "Wait for the abort signal", {"type": "object", "properties": {}}, abort_tool)
    ext.command(
        "abort_probe",
        "Report whether abort_tool saw its abort signal",
        lambda ctx, args: ctx.notify("abort:" + ("true" if abort_observed[0] else "false"), "info"),
    )
    ext.tool(
        "rich_tool",
        "Return text, image, and terminate",
        {"type": "object", "properties": {}},
        lambda _ctx, _params: {
            "content": [
                {"type": "text", "text": "  padded  "},
                {"type": "image", "data": "aW1n", "mimeType": "image/png"},
                {"type": "text", "text": "tail\n"},
            ],
            "terminate": True,
        },
    )
    ext.tool(
        "prepared_tool",
        "Transform legacy arguments before execution",
        {"type": "object", "required": ["text"], "properties": {"text": {"type": "string"}}},
        lambda _ctx, params: {"content": "prepared:" + str(params.get("text", ""))},
        prepare_arguments=lambda params: {"text": params.get("legacy")},
    )
    ext.tool("tool_error", "Return a thrown tool error", {"type": "object"}, tool_error)
    ext.tool("tool_is_error", "Return a structured tool error result", {"type": "object"}, tool_is_error)
    ext.tool(
        "guided_tool", "Tool with prompt guidelines", {"type": "object"},
        lambda ctx, params: {"content": "guided"},
        prompt_guidelines=["Use guided_tool when the user asks for guided behavior."],
    )
    ext.tool(
        "sourced_tool", "Tool with explicit source", {"type": "object"},
        lambda ctx, params: {"content": "sourced"},
        prompt_guidelines=["Use sourced_tool to test per-tool source attribution."],
        source="mcp:test-server",
    )
    ext.tool(
        "grammar_tool", "Tool with a grammar constrained sampling request", {"type": "object"},
        lambda ctx, params: {"content": "grammar"},
        constrained_sampling={"type": "grammar", "variants": {"openai_lark": "start: NUMBER"}},
    )

    def complete_probe(prefix: str) -> list[dict[str, str]] | None:
        items = [{"value": "alpha", "label": "alpha — first"}, {"value": "apple", "description": "fruit"}, {"value": "beta"}]
        matched = [item for item in items if item["value"].startswith(prefix.strip())]
        return matched or None

    ext.command("complete_probe", "Complete its arguments", lambda ctx, args: None, get_argument_completions=complete_probe)
    ext.command("ping", "Respond with pong", lambda ctx, args: ctx.notify("pong", "info"))

    def model_stream_probe(ctx: pig_sdk.Context, _args: str) -> None:
        current = ctx.model_registry.find("conformance", "current")
        if current is None or current.get("id") != "current":
            raise RuntimeError(f"find current = {current}")
        found = ctx.model_registry.find("conformance", "declared")
        if found is None or found.get("id") != "declared" or found.get("provider") != "conformance":
            raise RuntimeError(f"find declared = {found}")
        slash = ctx.model_registry.find("conformance", "org/model/name")
        if slash is None or slash.get("id") != "org/model/name":
            raise RuntimeError(f"find slash = {slash}")
        expected_limits = {"maxRequestBytes":12345,"images":{"maxPerMessage":7,"maxPerRequest":11,"resize":{"maxWidth":321,"maxHeight":123,"maxBytes":45678,"jpegQuality":67}}}
        if slash.get("inputLimits") != expected_limits or ctx.get_model_info().get("inputLimits") != expected_limits:
            raise RuntimeError(f"model inputLimits = {slash}")
        required = {"baseUrl", "input", "cost", "thinkingLevelMap", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"}
        if not required.issubset(slash):
            raise RuntimeError(f"find slash missing {required - set(slash)}: {slash}")
        if slash["input"] != [] or slash["cost"]["input"] != 0 or len(slash["cost"]["tiers"]) != 1 or slash["compat"]["supportsStrictMode"] is not False:
            raise RuntimeError(f"find slash shape = {slash}")
        if ctx.model_registry.find("conformance", "missing") is not None:
            raise RuntimeError("find missing returned a model")
        if ctx.model_registry.find("conformance", "override-only") is not None:
            raise RuntimeError("find override-only returned a model")
        auth = ctx.model_registry.get_api_key_and_headers(found)
        expected_auth = {"ok": True, "apiKey": "conformance-key", "headers": {"X-Conformance-Auth": "yes"}, "baseUrl": "https://models.invalid/v1", "env": {"CONFORMANCE_AUTH": "yes"}}
        if auth != expected_auth:
            raise RuntimeError(f"auth = {auth}")
        model = {"provider": "conformance", "modelId": "declared", "api": "openai-responses"}
        request = {
            "systemPrompt": "conformance-system",
            "messages": [
                {"role": "system", "content": [{"type": "text", "text": "signed system", "textSignature": "system-signature"}], "sections": {"zeta": "last-first", "alpha": None, "middle": "middle"}, "timestamp": 41},
                {"role": "user", "content": "hello", "timestamp": 42},
                {"role": "assistant", "content": [{"type": "text", "text": "prior", "textSignature": "signed"}], "api": "openai-responses", "provider": "prior-provider", "model": "prior-model", "usage": {"input": 1, "output": 2, "cacheRead": 3, "cacheWrite": 4, "totalTokens": 10, "cost": {"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": 1.0}}, "stopReason": "stop", "timestamp": 43},
            ],
            "tools": [{"name": "lookup", "description": "lookup", "parameters": {"type": "object"}, "constrainedSampling": {"type": "grammar", "variants": {"openai_lark": "start: NUMBER"}}}],
        }
        options = {
            "maxTokens": 321, "temperature": 0.65, "samplingParams": {"topP": 0.8},
            "thinkingBudgets": {"minimal": 11, "low": 22, "medium": 33, "high": 44}, "thinking": "high", "isReasoning": True,
            "env": {"WIRE_ENV": "request-value", "SECOND_ENV": "distinct-value"}, "headers": {"X-Wire": "yes", "X-Remove": None}, "sessionId": "conformance-session", "transport": "sse",
        }
        stream = ctx.model_registry.stream(model, request, options)
        types = [event["type"] for event in stream.events()]
        if types != ["start", "text_start", "text_delta", "text_end", "done"]:
            raise RuntimeError(f"stream events = {types}")
        result = stream.result()
        if result["content"][0]["text"] != "streamed":
            raise RuntimeError(f"stream result = {result}")
        simple = ctx.model_registry.stream_simple(model, request, options)
        simple_types = [event["type"] for event in simple.events()]
        if simple_types != ["start", "text_start", "text_delta", "text_end", "done"]:
            raise RuntimeError(f"simple events = {simple_types}")
        if simple.result()["stopReason"] != "stop":
            raise RuntimeError("simple did not stop")
        if ctx.model_registry.complete(model, request, options)["stopReason"] != "stop":
            raise RuntimeError("complete did not stop")
        if ctx.model_registry.stream(model, request, options).result()["stopReason"] != "stop":
            raise RuntimeError("result without iteration did not stop")
        unknown = ctx.model_registry.complete({"provider": "conformance", "modelId": "unknown", "api": "openai-responses"}, request, options)
        if unknown["stopReason"] != "error" or "unknown model" not in unknown["errorMessage"]:
            raise RuntimeError(f"unknown result = {unknown}")
        transport_error = ctx.model_registry.complete({"provider": "conformance", "modelId": "protocol-error", "api": "openai-responses"}, request, options)
        timestamp = transport_error.pop("timestamp", None)
        if not isinstance(timestamp, int) or timestamp <= 0:
            raise RuntimeError(f"transport timestamp = {timestamp}")
        expected_transport = {
            "role": "assistant", "content": [], "api": "openai-responses", "provider": "conformance", "model": "protocol-error",
            "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
                      "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
            "stopReason": "error", "errorMessage": "transport boom",
        }
        if transport_error != expected_transport:
            raise RuntimeError(f"transport result = {transport_error}")
        ctx.notify("model-stream=ok", "info")

    ext.command("model-stream-probe", "Exercise model streaming", model_stream_probe)

    def command_error(ctx: pig_sdk.Context, args: str) -> None:
        raise RuntimeError("command exploded")

    ext.command("liveness_host_call", "Exercise an awaited host call", lambda ctx, args: ctx.wait_for_idle())
    ext.command("liveness_user_call", "Exercise an interactive host call", lambda ctx, args: ctx.input("Question", "Answer"))
    ext.command("liveness_fire_call", "Exercise a no-result UI host call", lambda ctx, args: ctx.set_title("Conformance title"))

    ext.command("command_error", "Return a command error", command_error)

    def command_awaited_error(ctx: pig_sdk.Context, args: str) -> None:
        time.sleep(0.15)
        raise RuntimeError("awaited command exploded")

    ext.command("command_awaited_error", "Return an error after awaited work", command_awaited_error)
    ext.command("status", "Set a status entry", lambda ctx, args: ctx.set_status("conformance", "ok"))

    def status_burst(ctx, args):
        for i in range(200):
            ctx.set_status("burst", str(i))

    ext.command("status_burst", "Set one status repeatedly without awaiting", status_burst)
    ext.command(
        "report_geometry",
        "Report observed terminal geometry",
        lambda ctx, args: ctx.notify(f"geometry:{ctx.width}x{ctx.height}", "info"),
    )

    term_unsub: list = []

    def term_verdict(data: str) -> pig_sdk.TerminalInputResult:
        if data == "\x1b[98~":
            return pig_sdk.TerminalInputResult(data="rewritten")
        if data == "\x1b[97~":
            time.sleep(0.2)
            return pig_sdk.TerminalInputResult(consume=True)
        return pig_sdk.TerminalInputResult(consume=data == "\x1b[99~")

    def term_subscribe(ctx: pig_sdk.Context, args: str) -> None:
        term_unsub.append(ctx.on_terminal_input(term_verdict))

    def term_unsubscribe(ctx: pig_sdk.Context, args: str) -> None:
        while term_unsub:
            term_unsub.pop()()

    ext.command("term_subscribe", "Subscribe to raw terminal input", term_subscribe)
    ext.command("term_unsubscribe", "Release the raw input subscription", term_unsubscribe)
    ext.command("send_message", "Send a custom message", lambda ctx, args: ctx.send_message("notice", "hello-custom", True, True, "steer"))
    ext.command("send_message_default", "Send a custom message with default options", lambda ctx, args: ctx.send_message("notice", "default"))
    ext.command("send_message_no_turn", "Send a custom message that never starts a turn", lambda ctx, args: ctx.send_message("notice", "no-turn", trigger_turn=False))
    ext.command("send_user_message", "Send a user message", lambda ctx, args: ctx.send_user_message(json.loads(args) if args else "hello-user", "followUp"))
    ext.command("set_session_name", "Set the session name", lambda ctx, args: ctx.set_session_name("conformance-session"))
    ext.command("append_entry", "Append a custom entry", lambda ctx, args: ctx.append_entry("conformance-entry", "hello-entry"))

    def context_probe(ctx: pig_sdk.Context, args: str) -> None:
        opts = ctx.get_system_prompt_options()
        ctx.notify(
            "mode=%s trusted=%s spo_prompt=%s spo_cwd=%s spo_tools=%s"
            % (
                ctx.mode,
                "true" if ctx.is_project_trusted() else "false",
                opts.get("customPrompt", ""),
                opts.get("cwd", ""),
                ",".join(opts.get("selectedTools") or []),
            ),
            "info",
        )

    def login_probe(ctx: pig_sdk.Context, args: str) -> None:
        definition = conformance_login_definition()
        ctx.set_login(definition)
        definition.brand[0] = definition.brand[0][:-1]
        try:
            ctx.set_login(definition)
        except pig_sdk.HostCallError as err:
            ctx.notify(str(err), "error")
            return
        raise RuntimeError("invalid login definition was accepted")

    ext.command("login-probe", "Exercise semantic login submission and host errors", login_probe)
    ext.command("context-probe", "Report ctx.mode + ctx.getSystemPromptOptions()", context_probe)

    def dialog_probe(ctx: pig_sdk.Context, args: str) -> None:
        selected, _ = ctx.select("Pick", ["first", "second"])
        input_value, _ = ctx.input("Input", "placeholder")
        edited, _ = ctx.editor("Editor", "prefill")
        confirmed = ctx.confirm("Confirm", "message")
        ctx.notify(
            "select=%s input=%s editor=%s confirm=%s"
            % (selected, input_value, edited, str(confirmed).lower()),
            "info",
        )

    ext.command(
        "session-log-probe",
        "Read a paged session log",
        lambda ctx, args: ctx.notify(
            f"session entries={len(ctx.get_entries())} branch={len(ctx.get_branch())}",
            "info",
        ),
    )
    ext.command("dialog-probe", "Exercise interactive dialog responses", dialog_probe)

    def focused_probe(ctx: pig_sdk.Context, args: str) -> None:
        component = FocusedList()
        selected = ctx.custom(
            component,
            {"title": "Focused", "widthFraction": 0.5, "heightFraction": 0.5},
        )
        ctx.notify(f"focused={selected or ''} disposed={str(component.disposed).lower()}", "info")

    ext.command("focused-probe", "Exercise focused subprocess UI", focused_probe)

    def timer_focused_probe(ctx: pig_sdk.Context, args: str) -> None:
        component = TimerFocused()
        frame = ctx.custom(component, {"title": "Timer"})
        ctx.notify(
            "timer=%s disposed=%s detached=%s"
            % (frame or 0, str(component.disposed).lower(), str(component.detached).lower()),
            "info",
        )

    ext.command("timer-focused-probe", "Exercise timer-driven focused UI", timer_focused_probe)

    def project_trust_error(_ctx: pig_sdk.Context, _data: dict[str, object]) -> dict[str, object]:
        raise RuntimeError("trust-boom")

    ext.on_event(
        "message_update",
        lambda ctx, data: ctx.notify(
            "message-update=%s:%s:%s:%s"
            % (
                data.get("assistantMessageEvent", {}).get("type"),
                data.get("assistantMessageEvent", {}).get("contentIndex"),
                data.get("assistantMessageEvent", {}).get("delta"),
                str("assistantMessageEvent" in data.get("assistantMessageEvent", {})).lower(),
            ),
            "info",
        ),
    )
    def tool_execution_update(ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        if data.get("toolName") == "production_tool":
            args = data.get("args", {})
            partial = data.get("partialResult", {})
            ctx.notify(
                "tool-update=%s:%s:%s:%s:%s"
                % (data.get("toolName"), args.get("path"), args.get("nested", {}).get("depth"), partial.get("content"), partial.get("details", {}).get("progress")),
                "info",
            )
            return
        ctx.notify(
            "tool-update=%s:%s:%s:%s"
            % (data.get("toolName"), json.dumps(data.get("args"), separators=(",", ":")), data.get("partialResult", {}).get("content"), data.get("partialResult", {}).get("details", {}).get("progress")),
            "info",
        )

    def tool_execution_end(ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        result = data.get("result", {})
        content = result.get("content", [])
        if data.get("toolName") == "production_tool":
            ctx.notify(
                "tool-end=%s:%s:%s:%s:%s:%s:%s"
                % (data.get("toolName"), content[0].get("text"), len(content), content[1].get("data"), content[1].get("mimeType"), result.get("details", {}).get("nested", {}).get("value"), str(data.get("isError")).lower()),
                "info",
            )
            return
        ctx.notify(
            "tool-end=%s:%s:%s:%s:%s"
            % (data.get("toolName"), len(content), content[1].get("data"), result.get("details", {}).get("nested", {}).get("value"), str(data.get("isError")).lower()),
            "info",
        )

    # Typed tool events (upstream PowerShellToolCallEvent/BashToolCallEvent and
    # their result variants): record the input and details, block the
    # conformance sentinel command, and replace a result's content.
    def tool_call(ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any] | None:
        name = data.get("toolName")
        if name not in ("powershell", "bash"):
            return None
        tool_input = data.get("input") or {}
        ctx.notify("tool-call=%s:%s:%s" % (name, tool_input.get("command"), tool_input.get("timeout")), "info")
        if tool_input.get("command") == "blocked-command":
            return {"block": True, "reason": "blocked %s" % name}
        return None

    def tool_result(ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any] | None:
        name = data.get("toolName")
        if name not in ("powershell", "bash"):
            return None
        details = data.get("details") or {}
        content = data.get("content") or [{}]
        ctx.notify(
            "tool-result=%s:%s:%s:%s"
            % (name, details.get("fullOutputPath"), (details.get("truncation") or {}).get("totalLines"), content[0].get("text")),
            "info",
        )
        return {"content": [{"type": "text", "text": "%s redacted" % name}]}

    ext.on_event("tool_call", tool_call)
    ext.on_event("tool_result", tool_result)
    ext.on_event("tool_execution_update", tool_execution_update)
    ext.on_event("tool_execution_end", tool_execution_end)
    ext.on_project_trust(project_trust_error)
    ext.on_project_trust(lambda _ctx, _data: {"trusted": "undecided"})
    ext.on_project_trust(lambda _ctx, _data: {"trusted": "yes", "remember": True})
    def agent_before_settle(ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        entries = data.get("entries") or []
        preview = data.get("context") or {}
        context_entries = preview.get("contextEntries") or []
        ctx.notify(
            "agent_before_settle:%s:%d:%s:%d:%s"
            % (
                data.get("outcome", ""),
                len(entries),
                str(bool(data.get("continue", False))).lower(),
                len(context_entries),
                str(bool(preview.get("canContinue", False))).lower(),
            ),
            "info",
        )
        data["entries"].append({"type": "custom", "customType": "kept"})

    def boundary_error(_ctx: pig_sdk.Context, data: dict[str, Any]) -> None:
        data["entries"].append({"type": "custom", "customType": "before-error"})
        raise RuntimeError("boundary failed")

    def boundary_result(_ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any]:
        assert [entry["customType"] for entry in data["entries"]] == ["kept", "before-error"]
        assert len(data["context"]["contextEntries"]) == 2
        return {"entries": [{"type": "custom", "customType": "conformance-boundary"}], "continue": True}

    ext.on_event("agent_before_settle", agent_before_settle)
    ext.on_event("agent_before_settle", boundary_error)
    ext.on_event("agent_before_settle", boundary_result)
    ext.on_event("session_start", lambda ctx, data: ctx.notify("session_start:" + str(data.get("reason", "")), "info"))
    ext.on_event("session_shutdown", lambda ctx, data: ctx.notify("session_shutdown:" + str(data.get("reason", "")), "info"))
    ext.on_event("session_info_changed", lambda ctx, data: ctx.notify("session_info_changed:" + str(data.get("name", "")), "info"))
    for name in ("ui_prompt_start", "ui_prompt_end"):
        ext.on_event(name, ui_prompt_event)
    ext.on_event(
        "session_before_compact",
        lambda ctx, data: ctx.notify(
            "session_before_compact:%s:%s"
            % (data.get("reason", ""), str(data.get("willRetry", False)).lower()),
            "info",
        ),
    )
    ext.on_event(
        "session_compact",
        lambda ctx, data: ctx.notify(
            "session_compact:%s:%s:%s"
            % (
                data.get("reason", ""),
                str(data.get("willRetry", False)).lower(),
                str(data.get("fromExtension", False)).lower(),
            ),
            "info",
        ),
    )
    ext.on_event(
        "session_compact_failed",
        lambda ctx, data: ctx.notify(
            "session_compact_failed:%s:%s:%s:%s:%s"
            % (
                data.get("reason", ""),
                data.get("errorMessage", ""),
                str(data.get("aborted", False)).lower(),
                str(data.get("willRetry", False)).lower(),
                str(data.get("fromExtension", False)).lower(),
            ),
            "info",
        ),
    )
    ext.on_event(
        "turn_end",
        lambda ctx, data: ctx.notify(
            "turn_end:%s:%s" % (data.get("messageEntryId", ""), data.get("toolResultEntryIds", [""])[0]),
            "info",
        ),
    )
    _register_conformance_oauth(ext)
    return ext


class _ConformanceStore:
    """Owns credentials for the conformance OAuth provider; values must match the
    Go and Rust fixtures so the recordings compare equal."""

    def credential_status(self) -> pig_sdk.OAuthCredentialStatus:
        return pig_sdk.OAuthCredentialStatus(present=True, auth_type="oauth", source="conformance")

    def store_credentials(self, creds: pig_sdk.OAuthCredentials) -> str:
        return "/conf/creds.json"

    def delete_credentials(self) -> bool:
        return True


def _conformance_api_key(creds: pig_sdk.OAuthCredentials) -> str:
    if creds.access == "boom":
        raise RuntimeError("getApiKey exploded")
    return "key:" + creds.access


def _register_conformance_oauth(ext: pig_sdk.Extension) -> None:
    """Contribute the canonical OAuth provider the cross-transport conformance
    suite drives. Behavior must match the Go and Rust fixtures byte-for-byte."""

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        cb.on_device_code(
            pig_sdk.OAuthDeviceCodeInfo(user_code="CONF-USER-CODE", verification_uri="https://conf.example/verify")
        )
        cb.on_progress("waiting")
        value = cb.on_prompt(pig_sdk.OAuthPrompt(message="paste the code"))
        return pig_sdk.OAuthCredentials(access="access-" + value, refresh="refresh-tok", expires=4242)

    ext.register_oauth_provider(
        "conformance-oauth",
        {"name": "Conformance OAuth"},
        pig_sdk.OAuthProvider(
            name="Conformance OAuth",
            is_subscription=True,
            login=login,
            refresh_token=lambda creds: pig_sdk.OAuthCredentials(
                access="refreshed-" + creds.refresh, refresh=creds.refresh, expires=9999
            ),
            get_api_key=_conformance_api_key,
            credential_store=_ConformanceStore(),
        ),
    )


if __name__ == "__main__":
    new_extension().run()
