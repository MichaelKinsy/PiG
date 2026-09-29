#!/usr/bin/env python3
from __future__ import annotations

import dataclasses
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
    schema_rejected = False
    try:
        ext.tool("schema-invalid", "Must not register", None, lambda ctx, args: {"content": "bad"})
    except ValueError as error:
        schema_rejected = str(error) == 'Tool "schema-invalid" registered by extension "python-sdk-fixture" must define an object parameter schema.'
    ext.command("schema-probe", "Report schema rejection", lambda ctx, _args: ctx.notify("schema-rejected:" + str(schema_rejected).lower(), "info"))
    for name, kind, value in [("flag-true", "boolean", True), ("flag-false", "boolean", False), ("flag-string", "string", "default"), ("flag-empty", "string", ""), ("flag-unset", "string", None)]:
        ext.flag(name, flag_type=kind, default=value)
    ext.command("flag-probe", "Report registered flag values", lambda ctx, _args: ctx.notify(json.dumps([ctx.get_flag(name) for name in ["flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"]]), "info"))
    def registry_session(ctx, _args):
        s, r = ctx.session_manager, ctx.model_registry
        out = {
            "cwd": s.get_cwd(), "dir": s.get_session_dir(), "id": s.get_session_id(),
            "name": s.get_session_name(), "leaf": s.get_leaf_id(),
            "entry": s.get_entry("one"), "missing": s.get_entry("missing"), "label": s.get_label("one"),
            "entries": s.get_entries(), "branch": s.get_branch("one"), "tree": s.get_tree(),
            "contextEntries": s.build_context_entries(), "projection": s.build_session_projection(),
            "models": r.get_all(), "available": r.get_available(), "status": r.get_provider_auth_status("registry-probe"),
            "display": r.get_provider_display_name("registry-probe"), "error": r.get_error(),
            "config": r.get_registered_provider_config("registry-probe"), "ids": r.get_registered_provider_ids(),
            "auth": r.get_provider_auth("registry-probe"), "apiKey": r.get_api_key_for_provider("registry-probe"),
            "missingKey": r.get_api_key_for_provider("missing"), "refresh": r.refresh({"allowNetwork": False}),
        }
        ctx.notify(json.dumps(out), "info")
    def timeout_probe(ctx, _args):
        for timeout in (0.5, 4294967296.5, 1e21):
            ctx.exec("timeout-command", [], timeout=timeout)
        ctx.select("timeout", ["a"], timeout=1500.5)

    ext.command("timeout-probe", "Send JavaScript-number timeouts", timeout_probe)

    def exec_reject_probe(ctx, args):
        for command in json.loads(args):
            try:
                ctx.notify(f"resolved:{ctx.exec(command, []).exit_code}", "info")
            except RuntimeError as error:
                ctx.notify(f"rejected:{error}", "info")

    ext.command("exec-reject-probe", "Report exec outcomes", exec_reject_probe)
    ext.command("session-identity", "Read context identity accessors", lambda ctx, _args: ctx.notify(json.dumps([ctx.get_session_id(), ctx.get_session_file(), ctx.get_leaf_id(), ctx.get_session_name()]), "info"))
    ext.command("registry-session", "Read registry and session facades", registry_session)
    ext.message_renderer(
        "conformance-message",
        lambda _ctx, message, options, width: [json.dumps(options)] if message.get("content") == "padding-options" else [
            "renderer:%s:expanded=%s:width=%d"
            % (message.get("content", ""), str(bool(options.get("expanded"))).lower(), width)
        ],
    )
    ext.markdown_transformer(
        lambda markdown, context: "md:%s:%s:streaming=%s:width=%d"
        % (markdown, context.get("messageType", ""), str(bool(context.get("isStreaming"))).lower(), int(context.get("availableWidth") or 0))
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

    ext.tool("echo", "Echo input text", {"type": "object", "required": ["text"], "properties": {"text": {"type": "string", "description": "Text to echo"}, "offset": {"type": "number"}}}, echo)
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
        prompt_snippet=" \ufeffGuided\r\n tool\t summary ",
        prompt_guidelines=["Use guided_tool when the user asks for guided behavior."],
    )
    ext.tool(
        "sourced_tool", "Tool with explicit source", {"type": "object"},
        lambda ctx, params: {"content": "sourced"},
        prompt_guidelines=["Use sourced_tool to test per-tool source attribution."],
        source="mcp:test-server",
    )
    ext.tool("sampling_disabled", "Disable constrained sampling", {"type":"object"}, lambda ctx, params: "disabled", constrained_sampling=False)
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
            "timeoutMs":0,"websocketConnectTimeoutMs":1234,"maxRetries":2,"maxRetryDelayMs":3000,
            "maxTokens": 321, "temperature": 0.65, "samplingParams": {"topP": 0.8},
            "thinkingBudgets": {"minimal": 11, "low": 22, "medium": 33, "high": 44}, "reasoning": "high", "isReasoning": True,
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

    def term_verdict(ctx: pig_sdk.Context, data: str) -> pig_sdk.TerminalInputResult:
        if data in ("\ud83d", "\ude00", "😀"):
            return pig_sdk.TerminalInputResult(data="seen:" + data)
        if data == "\x1b[96~":
            return pig_sdk.TerminalInputResult(data=json.dumps([ctx.get_editor_text(), ctx.get_tools_expanded()], separators=(",", ":")))
        if data == "\x1b[98~":
            return pig_sdk.TerminalInputResult(data="rewritten")
        if data == "\x1b[97~":
            time.sleep(0.2)
            return pig_sdk.TerminalInputResult(consume=True)
        return pig_sdk.TerminalInputResult(consume=data == "\x1b[99~")

    def term_subscribe(ctx: pig_sdk.Context, args: str) -> None:
        term_unsub.append(ctx.on_terminal_input(lambda data: term_verdict(ctx, data)))

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

    def ui_availability(ctx: pig_sdk.Context, args: str) -> None:
        selected, _ = ctx.select("Pick", ["first", "second"])
        ctx.notify("availability-notify", "info")
        if not ctx.has_ui():
            assert ctx.input("Input", "placeholder") == ("", False)
            assert ctx.editor("Editor", "prefill") == ("", False)
            assert ctx.confirm("Confirm", "message") is False
            class NoUIComponent:
                def render(self, width):
                    raise AssertionError("headless render")
                def handle_input(self, data):
                    raise AssertionError("headless input")
                def set_invalidate(self, callback):
                    raise AssertionError("headless invalidation")
                def dispose(self):
                    raise AssertionError("headless disposal")
            assert ctx.custom(NoUIComponent()) is None
            off = ctx.on_terminal_input(lambda data: None)
            off(); off()
            ctx.add_autocomplete_provider()
            ctx.set_editor_text("ignored")
            ctx.set_tools_expanded(True)
            assert ctx.get_editor_text() == ""
            assert ctx.get_tools_expanded() is False
            assert ctx.get_all_themes() == []
            assert ctx.get_theme("dark") is None
            assert ctx.set_theme("dark") == (False, "UI not available")
        ctx.append_entry("ui-availability", f"hasUI={str(ctx.has_ui()).lower()} selected={selected}")

    ext.command("ui-availability", "Probe bound UI and headless defaults", ui_availability)

    def ui_state_barrier(ctx, args):
        selected, _ = ctx.select("expand", ["chosen"])
        named = ctx.get_theme("light")
        missing = ctx.get_theme("missing")
        success, message = ctx.set_theme("missing")
        ctx.notify(f"selected={selected} expanded={str(ctx.get_tools_expanded()).lower()} named={named['name']} missing={str(missing is None).lower()} success={str(success).lower()} error={message}", "info")

    ext.command("ui-state-barrier", "Read UI state after a dialog", ui_state_barrier)
    def autocomplete_register(ctx, args):
        for tag in ["A", "B"]:
            def factory(ctx, current, tag=tag):
                ctx.notify("factory:" + tag, "info")
                class Wrapped:
                    trigger_characters = ["$"] if tag == "A" else ["#", "$"]
                    calls = 0
                    def get_suggestions(self, ctx, lines, line, col, force=False):
                        self.calls += 1
                        result = current.get_suggestions(ctx, lines, line, col, force)
                        if result is None:
                            return None
                        items = ([dict(item, label=f"{item['value']}:{self.calls}") for item in result["items"] if item["value"] != "drop"] if tag == "A" else result["items"] + [{"value":"tail", "label":f"tail:{self.calls}"}])
                        return dict(result, items=items)
                    def apply_completion(self, ctx, lines, line, col, item, prefix):
                        result = current.apply_completion(ctx, lines, line, col, item, prefix)
                        result["lines"][result["cursorLine"]] += "-" + tag
                        result["cursorCol"] += 2
                        return result
                    def should_trigger_file_completion(self, ctx, lines, line, col):
                        return current.should_trigger_file_completion(ctx, lines, line, col)
                return Wrapped()
            ctx.add_autocomplete_provider(factory)
            ctx.notify("registered:" + tag, "info")

    ext.command("autocomplete-register", "Register retained provider wrappers", autocomplete_register)
    ext.command("usage-probe", "Report context usage", lambda ctx, args: ctx.notify(json.dumps(ctx.get_context_usage()), "info"))

    def context_probe(ctx: pig_sdk.Context, args: str) -> None:
        opts = ctx.get_system_prompt_options()

        def shape(value: object) -> str:
            if value is None:
                return "absent"
            if isinstance(value, list):
                return "array:%d" % len(value)
            if isinstance(value, dict):
                return "object:%d" % len(value)
            return "%s:%d" % ("string" if isinstance(value, str) else type(value).__name__, len(str(value)))

        shapes = ",".join(
            "%s:%s" % (key, shape(opts.get(key)))
            for key in ("selectedTools", "toolSnippets", "toolGuidelines", "promptGuidelines", "appendSystemPrompt", "sections", "contextFiles", "skills")
        )
        ctx.notify(
            "mode=%s trusted=%s spo_prompt=%s spo_cwd=%s spo_tools=%s spo_shape=%s spo_guidelines=%s spo_skill_scope=%s spo_force_empty=%s spo_custom_present=%s"
            % (
                ctx.mode,
                "true" if ctx.is_project_trusted() else "false",
                opts.get("customPrompt", ""),
                opts.get("cwd", ""),
                ",".join(opts.get("selectedTools") or []),
                shapes,
                ",".join(opts.get("toolGuidelines", {}).get("read", [])),
                (opts.get("skills") or [{}])[0].get("sourceInfo", {}).get("scope", ""),
                str(opts.get("forceSystemPrompt") == "").lower(),
                str("customPrompt" in opts).lower(),
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
    ext.command("scoped-models-probe", "Report the model scope", lambda ctx, _args: ctx.notify(json.dumps(ctx.scoped_models()), "info"))
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

    def provider_response(ctx, data):
        ctx.notify("provider-response=%s:%s:%s" % (data["type"], data["status"], data["headers"]["x-probe"]), "info")
        return {"cancel": True}

    ext.on_event("after_provider_response", provider_response)
    ext.on_event("after_provider_response", lambda ctx, data: ctx.notify("provider-response=second", "info"))
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
    def user_bash(_ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any] | None:
        result: dict[str, Any] = {"output": "handled", "exitCode": 7, "cancelled": False, "truncated": False}
        value: dict[str, Any] = {"result": result}
        command = data.get("command")
        if command == "undefined":
            result["exitCode"] = None
        elif command == "undefined-path":
            result["fullOutputPath"] = None
        elif command == "missing":
            del result["exitCode"]
        elif command == "invalid":
            result["exitCode"] = "invalid"
        elif command == "null-operations":
            value["operations"] = None
        elif command != "valid":
            return None
        return value

    ext.on_event("user_bash", user_bash)

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
    def cache_warming_decision(_ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any]:
        if data.get("warmCost") != 0.05 or data.get("missCost") != 0.5 or data.get("continuationProbability") != 0.15 or data.get("action") != "warm":
            raise ValueError("unexpected cache decision")
        return {"action": "stop"}

    ext.on_event("cache_warming_decision", cache_warming_decision)
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
    def session_start(ctx, data):
        ctx.notify("session_start:" + str(data.get("reason", "")), "info")
        if data.get("previousSessionFile"):
            ctx.notify("previous:" + data["previousSessionFile"], "info")

    def session_shutdown(ctx, data):
        ctx.notify("session_shutdown:" + str(data.get("reason", "")), "info")
        if data.get("targetSessionFile"):
            ctx.notify("target:" + data["targetSessionFile"], "info")

    ext.on_event("session_start", session_start)
    ext.on_event("session_shutdown", session_shutdown)
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
    def mutate_turn_boundary(_ctx, data):
        if data.get("messageEntryId") != "boundary-assistant":
            return
        data["entries"].append({"type": "custom", "customType": "mutated"})
        raise RuntimeError("turn-boundary-failure")

    def snapshot_turn_boundary(_ctx, data):
        if data.get("messageEntryId") == "boundary-assistant":
            return {"entries": [{"type": "custom", "customType": "turn-boundary", "data": data}], "continue": True}

    ext.on_event("turn_end", mutate_turn_boundary)
    ext.on_event("turn_end", snapshot_turn_boundary)
    ext.on_event(
        "turn_end",
        lambda ctx, data: ctx.notify(
            "turn_end:%s:%s" % (data.get("messageEntryId", ""), data.get("toolResultEntryIds", [""])[0]),
            "info",
        ),
    )
    _register_conformance_oauth(ext)
    _register_conformance_oauth_object(ext)
    _register_conformance_oauth_large(ext)
    return ext


class _ConformanceStore:
    """Owns credentials for the conformance OAuth provider; values must match the
    Go and Rust fixtures so the recordings compare equal."""

    def credential_status(self) -> pig_sdk.OAuthCredentialStatus:
        return pig_sdk.OAuthCredentialStatus(present=True, auth_type="oauth", source="conformance")

    def store_credentials(self, creds: pig_sdk.OAuthCredentials) -> str:
        if creds.account_id != "account-store" or creds.scope != "scope-store":
            raise RuntimeError("credential metadata lost")
        return "/conf/creds.json"

    def delete_credentials(self) -> bool:
        return True


def _conformance_api_key(creds: pig_sdk.OAuthCredentials) -> str:
    if creds.access == "boom":
        raise RuntimeError("getApiKey exploded")
    return "key:" + creds.access


def _register_conformance_oauth_object(ext: pig_sdk.Extension) -> None:
    """Contribute a provider whose callbacks treat credentials as Pi's complete token object: login
    returns a fractional expiry and provider-owned keys, and refresh spreads its input as
    ``{ ...creds, access, expires }`` does. Behavior must match the Go, Rust and Node fixtures."""

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        return pig_sdk.OAuthCredentials(
            access="object-access", refresh="object-refresh", expires=1700000000000.25, extra={"meta": {"k": [1, None, ""]}, "projectId": ""}
        )

    def refresh(creds: pig_sdk.OAuthCredentials) -> pig_sdk.OAuthCredentials:
        return dataclasses.replace(creds, access="refreshed-" + creds.refresh, expires=creds.expires + 0.5, extra=dict(creds.extra))

    def api_key(creds: pig_sdk.OAuthCredentials) -> str:
        typed = "typed" if creds.extra.get("type") == "oauth" else "untyped"
        meta = "meta" if "meta" in creds.extra else "nometa"
        return f"key:{typed}:{meta}"

    ext.register_oauth_provider(
        "conformance-oauth-object",
        {"name": "Conformance OAuth Object"},
        pig_sdk.OAuthProvider(name="Conformance OAuth Object", login=login, refresh_token=refresh, get_api_key=api_key),
    )


def _register_conformance_oauth_large(ext: pig_sdk.Extension) -> None:
    """Contribute a provider whose expiry is the double 2**60: JSON.parse reads the wire digits 1152921504606847000 as that double, so getApiKey reports a distance of 0 from 2**60. Behavior must match the Go, Rust and Node fixtures."""

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        return pig_sdk.OAuthCredentials(access="large-access", refresh="large-refresh", expires=2**60)

    ext.register_oauth_provider(
        "conformance-oauth-large",
        {"name": "Conformance OAuth Large"},
        pig_sdk.OAuthProvider(name="Conformance OAuth Large", login=login, get_api_key=lambda creds: f"key:{int(creds.expires - 2**60)}:{int(creds.expires) - 2**60}"),
    )


def _register_conformance_oauth(ext: pig_sdk.Extension) -> None:
    """Contribute the canonical OAuth provider the cross-transport conformance
    suite drives. Behavior must match the Go and Rust fixtures byte-for-byte."""

    def login(cb: pig_sdk.OAuthLoginCallbacks) -> pig_sdk.OAuthCredentials:
        cb.on_device_code(
            pig_sdk.OAuthDeviceCodeInfo(user_code="CONF-USER-CODE", verification_uri="https://conf.example/verify")
        )
        cb.on_progress("waiting")
        value = cb.on_prompt(pig_sdk.OAuthPrompt(message="paste the code"))
        return pig_sdk.OAuthCredentials(access="access-" + value, refresh="refresh-tok", expires=4242, account_id="account-login", scope="scope-login")

    ext.register_oauth_provider(
        "conformance-oauth",
        {"name": "Conformance OAuth"},
        pig_sdk.OAuthProvider(
            name="Conformance OAuth",
            is_subscription=True,
            login=login,
            refresh_token=lambda creds: pig_sdk.OAuthCredentials(
                access="refreshed-" + creds.refresh, refresh=creds.refresh, expires=9999, account_id=creds.account_id, scope=creds.scope
            ),
            get_api_key=_conformance_api_key,
            credential_store=_ConformanceStore(),
        ),
    )


if __name__ == "__main__":
    new_extension().run()
