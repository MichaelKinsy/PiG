#!/usr/bin/env python3
from __future__ import annotations

import dataclasses
import json
import pathlib
import struct
import sys
import threading
import time
import zlib

# kit-kinds' image, a 1×1 PNG every SDK fixture embeds byte for byte.
KIT_KINDS_PNG = "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c48900000010494441547801010500faff002a005fff026a01892888e8cd0000000049454e44ae426082"

ROOT = pathlib.Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / "extensions" / "sdk-py"))

import pig_sdk  # noqa: E402
from pig_sdk import kit  # noqa: E402


def kit_probe_view() -> kit.View:
    """The component kit's conformance view (D107,
    docs/plan/extension-component-kit.md §10)."""
    items = [kit.SelectItem(f"k{i}", f"Track {i}", f"Artist {i}") for i in range(5)]
    return kit.View(
        kit.Container([
            kit.DynamicBorder("accent"),
            kit.Text("Kit probe", 2, 0, bg="customMessageBg"),
            kit.Markdown("- one\n- **two**", 1, 0),
            kit.HStack(
                [kit.StackEntry(kit.TruncatedText("left side", 0, 0), grow=1), kit.StackEntry(kit.TruncatedText("right", 0, 0), grow=1)],
                gap=1,
            ),
            kit.Spacer(1),
            kit.SelectList("kit-tracks", items, 3, selected_index=2),
        ]),
        focus="kit-tracks",
        theme={"accent": "#d75f00"},
    )


class KitProbe:
    """kit-probe's component: logs every input and view event and closes on
    select with the log joined by ","."""

    def __init__(self) -> None:
        self.log: list[str] = []

    def view(self, _width: int) -> kit.View:
        return kit_probe_view()

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        self.log.append("input:" + data)
        return pig_sdk.RemoteComponentResult()

    def handle_view_event(self, event: kit.Event) -> pig_sdk.RemoteComponentResult:
        self.log.append(f"{event.type}:{event.index}:{event.item.value if event.item else ''}")
        if event.type == kit.SELECT:
            return pig_sdk.RemoteComponentResult(done=True, value=",".join(self.log))
        return pig_sdk.RemoteComponentResult()


class MouseProbe:
    """The extension mouse row's component: logs every mouse event it
    receives, all of its fields, and closes on a click with the log joined by
    ","."""

    def __init__(self) -> None:
        self.log: list[str] = []

    def render(self, _width: int) -> list[str]:
        return ["mouse probe", "row 1", "row 2", "row 3"]

    def handle_input(self, _data: str) -> pig_sdk.RemoteComponentResult:
        return pig_sdk.RemoteComponentResult()

    def handle_mouse(self, event: pig_sdk.MouseEvent) -> pig_sdk.RemoteComponentResult:
        mods = "".join(name for on, name in ((event.shift, "S"), (event.alt, "A"), (event.ctrl, "C")) if on)
        self.log.append(
            f"{event.type}/{event.button}/{event.x},{event.y}/{event.screen_x},{event.screen_y}/"
            f"{event.width}x{event.height}/w{event.wheel_delta}/c{event.click_count}/{mods}"
        )
        if event.type == "click":
            return pig_sdk.RemoteComponentResult(done=True, value=",".join(self.log))
        return pig_sdk.RemoteComponentResult()


def kit_image_png(n: int) -> bytes:
    """Image n of kit-images: a 1×1 RGBA PNG whose pixel encodes n, so each n
    has its own bytes and ref."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    pixel = bytes([0, n & 0xFF, (n >> 8) & 0xFF, 0x5F, 0xFF])
    header = struct.pack(">IIBBBBB", 1, 1, 8, 6, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(pixel)) + chunk(b"IEND", b"")


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


class ConformanceBashOperations:
    """The BashOperations every SDK fixture returns for the user_bash command "operations" (TestConformance_UserBashOperationsRunInTheExtension)."""

    def exec(self, command: str, cwd: str, options: pig_sdk.BashExecOptions) -> int | None:
        if command == "echo":
            options.on_data(f"cmd:{command}\n".encode())
            options.on_data(f"cwd:{cwd}\n".encode())
            if options.env is not None and not options.env:
                options.on_data(b"env-empty\n")
            for name in sorted(options.env or {}):
                options.on_data(f"env:{name}={options.env[name]}\n".encode())
            if options.timeout is not None:
                options.on_data(f"timeout:{options.timeout:g}\n".encode())
            return 3
        if command == "chunks":
            for chunk in (b"a", b"b", b"c"):
                options.on_data(chunk)
            return None
        if command == "binary":
            options.on_data(bytes([0xFF, 0x00, 0x80]))
            return 0
        if command == "wait":
            options.on_data(b"waiting")
            options.signal.wait()
            options.on_data(b"stopped")
            raise RuntimeError("aborted")
        raise RuntimeError(f"exec failed: {command}")


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


def conformance_sprite_definition() -> pig_sdk.SpriteDefinition:
    return pig_sdk.SpriteDefinition(
        id="conformance-pig",
        name="Conformance Pig",
        tagline="One canonical sprite across every SDK",
        mascot=["A" * 16 for _ in range(14)],
        palette={"A": "#123ABC"},
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
    class ConformanceEditor(pig_sdk.EditorComponent):
        """Swallows "q", rewrites "a" to "A", upper-cases the host's setText, and frames super.render."""

        def handle_input(self, data):
            if data == "q":
                return
            if data == "L":
                super().set_text("raw:lines=" + "|".join(self.base.get_lines()))
                return
            super().handle_input("A" if data == "a" else data)

        def set_text(self, text):
            super().set_text(text if text.startswith("raw:") else text.upper())

        def render(self, width):
            return ["[custom editor]"] + super().render(width)

    class EmbeddingEditor(ConformanceEditor):
        embed_working_status = True

    def eager_editor(ctx, _args):
        def factory(base):
            try:
                base.set_text("eager")
                ctx.notify("eager:" + base.get_text(), "info")
            except Exception as exc:  # noqa: BLE001
                ctx.notify("eager-error:" + str(exc), "info")
            return ConformanceEditor(base)

        ctx.set_editor_component(factory)

    ext.command("editor-install-eager", "Install a custom editor that calls super in its factory", eager_editor)
    ext.command("editor-install-embed", "Install a custom editor that embeds the working status", lambda ctx, _args: ctx.set_editor_component(EmbeddingEditor))
    ext.command("editor-install", "Install a custom editor component", lambda ctx, _args: ctx.set_editor_component(ConformanceEditor))
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
    def session_order(ctx, _args):
        s = ctx.session_manager
        out = {
            "getEntries": s.get_entries(), "getEntry": s.get_entry("a4"), "getLeafEntry": s.get_leaf_entry(),
            "getBranch": s.get_branch("a4"), "getChildren": s.get_children("a1"), "getTree": s.get_tree(),
            "buildContextEntries": s.build_context_entries(), "buildSessionProjection": s.build_session_projection(),
            "buildSessionContext": s.build_session_context(),
        }
        ctx.notify(json.dumps(out, separators=(",", ":"), ensure_ascii=False), "info")
    ext.command("session-order", "Read the session as Pi returns it", session_order)
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
    ext.command("thinking-model", "Read the thinking level and switch the model", lambda ctx, _args: (ctx.set_thinking_level("high"), ctx.notify(json.dumps([ctx.get_thinking_level(), ctx.set_model("probe/model")[0]], separators=(",", ":")), "info"))[-1])
    ext.command("active_tools_set", "Set the active tools to the comma-separated names", lambda ctx, args: ctx.set_active_tools(args.strip().split(",")))
    def session_actions_unbound(ctx, _args):
        parts = []
        for name, call in (("new", lambda: ctx.new_session()), ("fork", lambda: ctx.fork("entry")), ("navigate", lambda: ctx.navigate_tree("entry")), ("switch", lambda: ctx.switch_session("/s.jsonl"))):
            try:
                parts.append("%s=%s" % (name, str(call()["cancelled"]).lower()))
            except Exception as error:
                parts.append("%s=error:%s" % (name, error))
        try:
            ctx.reload()
            parts.append("reload=ok")
        except Exception as error:
            parts.append("reload=error:%s" % error)
        ctx.notify("unbound:" + ",".join(parts), "info")

    ext.command("session_actions_unbound", "Report what unbound session actions answer", session_actions_unbound)
    ext.command("active_tools_get", "Report the active tools", lambda ctx, _args: ctx.notify("active_tools:%s" % ",".join(ctx.get_active_tools()), "info"))
    # provider_probe_register and provider_probe_unregister register and unregister a plain provider after the extension connected.
    ext.command("provider_probe_register", "Register a provider with one model", lambda ctx, _args: ext.register_provider("conformance-probe", json.loads(r'''{"baseUrl":"https://probe.invalid/v1","api":"openai-completions","apiKey":"probe-key","models":[{"id":"probe-model","name":"Probe Model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}''')))
    ext.command("provider_probe_unregister", "Unregister the provider", lambda ctx, _args: ext.unregister_provider("conformance-probe"))
    ext.command("commands-probe", "Read the host's slash commands", lambda ctx, _args: ctx.notify(json.dumps([[c["name"], c["source"], c.get("description", "")] for c in ctx.get_commands() if c["name"] == "conformance-listed"], separators=(",", ":")), "info"))
    ext.command("session-identity", "Read context identity accessors", lambda ctx, _args: ctx.notify(json.dumps([ctx.get_session_id(), ctx.get_session_file(), ctx.get_leaf_id(), ctx.get_session_name()]), "info"))
    ext.command("registry-session", "Read registry and session facades", registry_session)
    ext.message_renderer(
        "conformance-message",
        lambda _ctx, message, options, width: [json.dumps(options)] if message.get("content") == "padding-options" else [
            "renderer:%s:expanded=%s:width=%d"
            % (message.get("content", ""), str(bool(options.get("expanded"))).lower(), width)
        ],
    )
    ext.message_view_renderer("kit-message", lambda _ctx, _message, _options, _width: kit_probe_view())
    def transform_markdown(markdown, context):
        # "trace:<dir>:<name>" records the body's entry in <dir>/trace; the body named "first" returns only once <dir>/release exists. newline="" keeps "\n" from becoming CRLF on Windows.
        if markdown.startswith("trace:"):
            # The name is the last field: a Windows <dir> contains a drive colon.
            directory, name = markdown[len("trace:"):].rsplit(":", 1)
            with open(directory + "/trace", "a", newline="") as trace:
                trace.write("entered %s\n" % name)
            while name == "first" and not pathlib.Path(directory + "/release").exists():
                time.sleep(0.01)
            if name == "first":
                with open(directory + "/trace", "a", newline="") as trace:
                    trace.write("returned first\n")
        return "md:%s:%s:streaming=%s:width=%d" % (markdown, context.get("messageType", ""), str(bool(context.get("isStreaming"))).lower(), int(context.get("availableWidth") or 0))

    ext.markdown_transformer(transform_markdown)
    def resolved_line(prefix: str, tool: str):
        return lambda _ctx, args, _render, _width: ["%s:%s:%s" % (prefix, tool, args.get("q"))]

    def tool_renderer(tool: str, next_renderers):
        if tool == "conformance_tool_renderer":
            return pig_sdk.ToolRenderers(render_call=resolved_line("resolved", tool))
        if tool == "conformance_no_renderer":
            return None
        if tool == "conformance_fill":
            return next_renderers() or pig_sdk.ToolRenderers(render_call=resolved_line("filled", tool))
        if tool == "conformance_wrap":
            return dataclasses.replace(next_renderers(), render_call=resolved_line("wrapped", tool))
        return next_renderers()

    ext.tool_renderer(tool_renderer)

    def late_tool_renderer(_ctx: pig_sdk.Context, _args: str) -> None:
        ext.tool_renderer(
            lambda tool, next_renderers: pig_sdk.ToolRenderers(render_call=resolved_line("late", tool))
            if tool == "conformance_late"
            else next_renderers()
        )

    ext.command("late_tool_renderer", "Register a tool renderer resolver after loading", late_tool_renderer)
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
            "toolrender:result:%s:%s:expanded=%s:calls=%s:width=%d:duration=%s"
            % (result["content"][0]["text"], result["details"]["k"], str(bool(options.get("expanded"))).lower(), render.state.get("calls"), width, "none" if render.duration_ms is None else render.duration_ms)
        ]

    ext.tool_renderers("render_probe", render_call=render_probe_call, render_result=render_probe_result, render_shell="self")
    abort_observed = [False]

    def update_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "step 1"}]})
        ctx.on_update({"content": [{"type": "text", "text": "step 2"}]})
        return {"content": [{"type": "text", "text": "done"}]}

    def ordered_details(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "partial"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}})
        return {"content": [{"type": "text", "text": "done"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}}

    def ordered_result(ctx, _params):
        ctx.on_update({"details": {"k": 1}, "content": [{"type": "text", "text": "partial"}]})
        return {"details": {"k": 1}, "is_error": True, "content": [{"type": "text", "text": "done"}]}

    def abort_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "waiting"}]})
        while not ctx.is_cancelled():
            time.sleep(0.01)
        abort_observed[0] = True
        return {"content": "aborted"}

    # hang_tool ignores its abort signal for longer than the host's abort grace period (D111).
    def hang_tool(ctx, _params):
        ctx.on_update({"content": [{"type": "text", "text": "waiting"}]})
        time.sleep(8)
        return {"content": "late"}

    ext.tool("update_tool", "Stream two partial results", {"type": "object", "properties": {}}, update_tool)
    ext.tool("ordered_details", "Return details whose members are not in alphabetical order", {"type": "object", "properties": {}}, ordered_details)
    ext.tool("ordered_result", "Return a result whose members are not in the declared order", {"type": "object", "properties": {}}, ordered_result)
    ext.tool("abort_tool", "Wait for the abort signal", {"type": "object", "properties": {}}, abort_tool)
    ext.tool("hang_tool", "Ignore the abort signal", {"type": "object", "properties": {}}, hang_tool)
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
    # Reports the order in which calls start: the number of calls that started before it, plus its own argument.
    started_calls = [0]
    started_lock = threading.Lock()

    def start_order(_ctx, params):
        with started_lock:
            started_calls[0] += 1
            number = started_calls[0]
        return {"content": f"start#{number} n={params.get('n')}"}

    ext.tool("start_order", "Report the order in which calls start", {"type": "object", "properties": {"n": {"type": "number"}}}, start_order)
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

    def model_stream_callback_probe(ctx: pig_sdk.Context, _args: str) -> None:
        model = {"provider": "conformance", "id": "declared", "modelId": "declared", "api": "openai-responses"}
        request = {"systemPrompt": "callbacks", "messages": [{"role": "user", "content": "hello", "timestamp": 1}]}
        seen: dict[str, Any] = {}

        def on_payload(payload: Any, callback_model: dict[str, Any]) -> Any:
            seen["payload"] = payload
            seen["model"] = callback_model.get("id")
            return {**payload, "mark": "on-payload"}

        def on_response(response: dict[str, Any], _callback_model: dict[str, Any]) -> None:
            seen["response"] = response

        def transform_headers(headers: dict[str, Any], _callback_model: dict[str, Any]) -> dict[str, Any]:
            return {**headers, "x-transformed": "yes"}

        stream = ctx.model_registry.stream(model, request, {"onPayload": on_payload, "onResponse": on_response, "transformHeaders": transform_headers})
        if stream.result()["stopReason"] != "stop":
            raise RuntimeError("callback stream did not stop")
        if (seen.get("payload") or {}).get("original") is not True or seen.get("model") != "declared":
            raise RuntimeError(f"onPayload saw {seen}")
        response = seen.get("response") or {}
        if response.get("status") != 201 or (response.get("headers") or {}).get("x-upstream") != "seen":
            raise RuntimeError(f"onResponse saw {response}")
        if ctx.model_registry.stream(model, request, {}).result()["stopReason"] != "stop":
            raise RuntimeError("plain stream did not stop")
        ctx.notify("model-callbacks=ok", "info")

    class HandleProbeComponent:
        def render(self, width: int) -> list[str]:
            return ["overlay"]

        def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
            return pig_sdk.RemoteComponentResult(done=data == "q", value="closed" if data == "q" else None)

    def overlay_handle_probe(ctx: pig_sdk.Context, _args: str) -> None:
        failures: list[str] = []

        def expect(label: str, actual: Any, expected: Any) -> None:
            if actual != expected:
                failures.append(f"{label}: {actual!r} != {expected!r}")

        def on_handle(handle: pig_sdk.OverlayHandle) -> None:
            expect("initial focused", handle.is_focused(), True)
            expect("initial hidden", handle.is_hidden(), False)
            expect("initial bounds", handle.get_bounds(), {"row": 3, "col": 4, "width": 20, "height": 5})
            handle.focus()
            expect("focus", handle.is_focused(), True)
            handle.set_hidden(True)
            expect("hidden", [handle.is_hidden(), handle.is_focused(), handle.get_bounds()], [True, False, None])
            handle.set_hidden(False)
            expect("shown", [handle.is_hidden(), handle.is_focused()], [False, False])
            handle.focus()
            expect("refocus", handle.is_focused(), True)
            handle.unfocus()
            expect("unfocus", handle.is_focused(), False)
            handle.unfocus(None)

        ctx.custom(HandleProbeComponent(), {"overlay": True, "onHandle": on_handle})
        if failures:
            raise RuntimeError("; ".join(failures))
        ctx.notify("overlay-handle=ok", "info")

    ext.command("overlay-handle-probe", "Exercise the overlay handle ui.custom hands to on_handle", overlay_handle_probe)

    def model_stream_fetch_probe(ctx: pig_sdk.Context, _args: str) -> None:
        model = {"provider": "conformance", "id": "declared", "modelId": "declared", "api": "openai-responses"}
        request = {"systemPrompt": "fetch", "messages": [{"role": "user", "content": "hello", "timestamp": 1}]}
        seen: dict[str, Any] = {}

        def fetch(call: dict[str, Any]) -> dict[str, Any]:
            seen["url"] = call["url"]
            seen["method"] = call["method"]
            seen["host"] = (call["headers"].get("X-Host") or [None])[0]
            seen["body"] = call["body"].decode()
            return {"status": 207, "statusText": "Answered", "headers": {"x-sdk-fetch": "answered"}, "body": bytes(index % 251 for index in range(70000))}

        stream = ctx.model_registry.stream(model, request, {"fetch": fetch})
        if stream.result()["stopReason"] != "stop":
            raise RuntimeError("fetch stream did not stop")
        if seen != {"url": "https://fetch.invalid/v1/chat?x=1", "method": "POST", "host": "1", "body": "ping-body"}:
            raise RuntimeError(f"fetch saw {seen}")
        if ctx.model_registry.stream(model, request, {}).result()["stopReason"] != "stop":
            raise RuntimeError("plain stream did not stop")
        ctx.notify("model-fetch=ok", "info")

    ext.command("model-stream-fetch-probe", "Exercise the fetch option of model_registry.stream", model_stream_fetch_probe)

    ext.command("model-stream-callback-probe", "Exercise the provider request callbacks of model_registry.stream", model_stream_callback_probe)

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
    event_probe_off: list = []

    def event_probe_subscribe(ctx, args):
        event_probe_off.append(
            ext.on_event("turn_end", lambda hctx, data: hctx.notify("event_probe:%s" % data.get("messageEntryId", ""), "info"))
        )

    def event_probe_unsubscribe(ctx, args):
        while event_probe_off:
            event_probe_off.pop()()

    ext.command("event_probe_subscribe", "Subscribe to turn_end after connecting", event_probe_subscribe)
    ext.command("event_probe_unsubscribe", "Remove the turn_end handler registered after connecting", event_probe_unsubscribe)
    ext.command(
        "report_geometry",
        "Report observed terminal geometry",
        lambda ctx, args: ctx.notify(f"geometry:{ctx.width}x{ctx.height}", "info"),
    )

    width_probe: list = []

    def arm_width_probe(ctx, args):
        # A width handler makes a host call the way any other handler does; the host's reply must reach it (conformance TestConformance_WidthHandlerHostCall).
        if not width_probe:
            def on_width(width):
                ctx.notify(f"width-probe:{width}", "info")
                ctx.notify(f"width-probe-returned:{width}", "info")

            width_probe.append(ctx.on_width_change(on_width))

    ext.command("arm_width_probe", "Notify from a width handler", arm_width_probe)

    ext.command("surface_footer", "Install a footer renderer", lambda ctx, args: ctx.set_footer_renderer(lambda width: [f"footer@{width}"]))
    ext.command("surface_header", "Install a header renderer", lambda ctx, args: ctx.set_header_renderer(lambda width: [f"header@{width}"]))
    ext.command("surface_static_footer", "Push static footer rows", lambda ctx, args: ctx.set_footer([f"static@{ctx.width}"]))
    ext.command("surface_widget", "Set a string list widget wider than the pane", lambda ctx, args: ctx.set_widget("wide", ["A" * 60 + " tail", "short"]))

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
    ext.command("settings_probe", "Report the effective settings", lambda ctx, args: ctx.notify("settings_probe:" + json.dumps(ctx.get_settings(), separators=(",", ":")), "info"))

    def model_set(ctx, args):
        ok, _error = ctx.set_model(args.strip())
        ctx.notify("model_set:%s" % str(ok).lower(), "info")

    ext.command("model_set", "Switch the model", model_set)
    ext.command("thinking_set", "Set the thinking level", lambda ctx, args: ctx.set_thinking_level(args.strip()))
    ext.command("thinking_get", "Report the thinking level", lambda ctx, args: ctx.notify("thinking_get:%s" % ctx.get_thinking_level(), "info"))
    ext.command("set_session_name", "Set the session name", lambda ctx, args: ctx.set_session_name("conformance-session"))

    def host_state_probe(ctx: pig_sdk.Context, _args: str) -> None:
        commands = ["|".join([c["name"], c.get("description", ""), c["source"], c["sourceInfo"]["path"], c["sourceInfo"]["scope"]]) for c in ctx.get_commands()]
        ctx.notify(f"getThinkingLevel={ctx.get_thinking_level()};getCommands={','.join(commands)}", "info")

    ext.command("host_state_probe", "Report the thinking level and the session commands", host_state_probe)

    def set_model(ctx: pig_sdk.Context, args: str) -> None:
        ok, error = ctx.set_model(args)
        if error:
            raise RuntimeError(error)
        ctx.notify(f"setModel={'true' if ok else 'false'}", "info")

    ext.command("set_model", "Switch to the model in the arguments", set_model)
    ext.command("append_entry", "Append a custom entry", lambda ctx, args: ctx.append_entry("conformance-entry", "hello-entry"))

    def event_field_probe(ctx, args):
        name, field = args.split()
        ext.on_event(name, lambda hctx, data: hctx.notify("event_field:%s.%s=%s" % (name, field, json.dumps(data[field], separators=(",", ":")) if field in data else "absent"), "info"))

    ext.command("event_field_probe", "Report a field of the events named by the argument `<event> <field>`", event_field_probe)
    event_probe_on_offs: dict = {}

    event_probe_results: dict = {}

    def event_probe_result(ctx, args):
        name, _, raw = args.strip().partition(" ")
        event_probe_results.setdefault(name, []).append(json.loads(raw))

    def event_probe_on(ctx, args):
        name = args.strip()

        def probe(hctx, data):
            hctx.notify("event_probe_on:%s" % name, "info")
            hctx.notify("event_payload:%s:%s" % (name, json.dumps(data)), "info")
            # The event as the host sent it, for the tests that compare a payload field with the Go reference's.
            hctx.notify("event_probe_data:%s:%s" % (name, json.dumps(data, separators=(",", ":"))), "info")
            # mcp_servers_change carries every registered server (types.ts:699-709): report their names, which only the host's registry knows.
            if isinstance(data, dict) and isinstance(data.get("servers"), list):
                hctx.notify("event_probe_servers:%s" % ",".join(str(s.get("name")) for s in data["servers"]), "info")
            queued = event_probe_results.get(name)
            return queued.pop(0) if queued else None

        off = ext.on_event(name, probe)
        event_probe_on_offs.setdefault(name, []).append(off)

    def event_probe_on_off(ctx, args):
        for off in event_probe_on_offs.pop(args.strip(), []):
            off()

    ext.command("event_probe_result", "Queue the JSON result the next event_probe_on handler call of an event returns", event_probe_result)
    ext.command("event_probe_on", "Subscribe to the event named by the argument", event_probe_on)

    def event_result_probe(ctx, args):
        # Subscribe to the event named first with a handler that returns the JSON that follows: the result an SDK extension gives the host.
        name, _, raw = args.strip().partition(" ")
        result = json.loads(raw)
        ext.on_event(name, lambda hctx, data: result)

    ext.command("event_result_probe", "Subscribe to an event with a handler that returns the given JSON", event_result_probe)
    ext.command("event_probe_off", "Unsubscribe the event_probe_on handlers of the event named by the argument", event_probe_on_off)
    def event_payload_probe(ctx, args):
        name = args.strip()
        ext.on_event(name, lambda hctx, data: hctx.notify("event_payload:%s:%s" % (name, json.dumps(data)), "info"))

    ext.command("event_payload_probe", "Subscribe and report the payload of the event named by the argument", event_payload_probe)
    ext.command("set_label", "Set an entry label", lambda ctx, args: ctx.set_label("label-entry", "conformance-label"))
    # label_probe labels the entry named first with the rest of the arguments: an entry the host's Session holds.
    def label_probe(ctx, args):
        parts = args.strip().split(" ", 1)
        ctx.set_label(parts[0], parts[1] if len(parts) > 1 else "")

    ext.command("label_probe", "Label the entry named first", label_probe)
    ext.shortcut("ctrl+alt+y", "Conformance shortcut", lambda ctx: None)

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
    def signal_probe(ctx: pig_sdk.Context, args: str) -> None:
        signal = ctx.signal
        ctx.notify("signal:" + ("none" if signal is None else "aborted" if signal.is_set() else "live"), "info")

    def signal_wait(ctx: pig_sdk.Context, args: str) -> None:
        signal = ctx.signal
        if signal is None:
            ctx.notify("wait:none", "info")
            return
        ctx.notify("wait:start", "info")
        ctx.notify("wait:" + ("aborted" if signal.wait(10) else "timeout"), "info")

    def signal_poll(ctx: pig_sdk.Context, args: str) -> None:
        ctx.notify("poll:start", "info")
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if (ctx.signal is not None) == (args == "live"):
                ctx.notify("poll:" + args, "info")
                return
            time.sleep(0.005)
        ctx.notify("poll:timeout", "info")

    ext.command("signal-poll", "Poll ctx.signal until it is live or none", signal_poll)
    ext.command("signal-probe", "Report ctx.signal", signal_probe)
    # Upstream ctx.cwd, ctx.mode, ctx.hasUI and ctx.model throw the stale message after invalidation (runner.ts:571-600).
    # The members are read before the report, and a failed report is ignored, so only the local members can fail the command: the Host also rejects the stale ui.notify call with the same message.
    def stale_probe(ctx: pig_sdk.Context, args: str) -> None:
        report = "stale:%s|%s|%s|%s" % (ctx.cwd, ctx.mode, str(ctx.has_ui()).lower(), ctx.model)
        try:
            ctx.notify(report, "info")
        except Exception:
            pass

    ext.command("stale-probe", "Report cwd, mode, hasUI and model", stale_probe)
    ext.command("signal-wait", "Wait for ctx.signal to abort", signal_wait)
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

    def sprite_probe(ctx: pig_sdk.Context, args: str) -> None:
        definition = conformance_sprite_definition()
        ctx.register_sprite(definition)
        definition.mascot[0] = definition.mascot[0][:-1]
        try:
            ctx.register_sprite(definition)
        except pig_sdk.HostCallError as err:
            ctx.notify(str(err), "error")
            return
        raise RuntimeError("invalid sprite definition was accepted")

    ext.command("sprite-probe", "Exercise sprite registration and host errors", sprite_probe)
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

    def kit_probe(ctx: pig_sdk.Context, _args: str) -> None:
        result = ctx.custom(KitProbe(), {"title": "Kit"})
        ctx.notify(f"kit={'' if result is None else result}", "info")

    ext.command("kit-probe", "Exercise the component kit (D107)", kit_probe)

    # mouse-probe opens the mouse row's component: the host hands it
    # fullscreen mouse events, and it closes on a click with what it got.
    def mouse_probe(ctx: pig_sdk.Context, _args: str) -> None:
        result = ctx.custom(MouseProbe(), {"overlay": True})
        ctx.notify(f"mouse={'' if result is None else result}", "info")

    ext.command("mouse-probe", "Exercise extension mouse input", mouse_probe)

    # kit-surfaces shows the kit probe's tree on every other view surface
    # (D107, spec §10): a pushed widget, the header, the footer and a tool
    # result. The message renderer "kit-message" draws it too.
    def kit_surfaces(ctx: pig_sdk.Context, _args: str) -> None:
        ctx.set_widget("kit-probe", kit_probe_view())
        ctx.set_header_view(kit_probe_view())
        ctx.set_footer_view(kit_probe_view())
        ctx.register_tool(pig_sdk.ToolDefinition(
            name="kit_view_tool",
            label="kit_view_tool",
            description="Render its result as the kit probe",
            parameters={"type": "object"},
            execute=lambda _ctx, _params: "kit",
            result_view=lambda _ctx, _result, _options, _render, _width: kit_probe_view(),
        ))

    ext.command("kit-surfaces", "Show the kit probe on every view surface (D107)", kit_surfaces)

    # kit-images drives the image transport (D107, spec §7): step "a<k>"
    # shows image 0 and step "b<n>" image n (1 ≤ n ≤ 64), each with its step
    # as the text, as one ui.setWidget call on the "kit-img" widget.
    def kit_images(ctx: pig_sdk.Context, args: str) -> None:
        n = int(args[1:]) if args[1:].isdecimal() else 0
        if args[:1] == "a" and n >= 1:
            image = 0
        elif args[:1] == "b" and 1 <= n <= 64:
            image = n
        else:
            raise ValueError(f"kit-images: unknown step {args!r}")
        view = kit.View(kit.Container([kit.Image(kit_image_png(image), "image/png"), kit.Text(args, 0, 0)]))
        ctx.set_widget("kit-img", view, {})

    ext.command("kit-images", "Exercise the component kit's image transport (D107)", kit_images)

    # kit-kinds sets the "kit-kinds" widget to the kinds kit-probe does not
    # draw (D107, spec §10): a box, a settings list, a loader, an image and a
    # lines node whose list annotation goes out only while a frontend draws.
    def kit_kinds(ctx: pig_sdk.Context, _args: str) -> None:
        settings = kit.SettingsList("kit-settings", [
            kit.SettingItem("theme", "Theme", "dark", description="Color theme", values=["dark", "light"]),
            kit.SettingItem("wrap", "Wrap", "on", description="Wrap lines", values=["on", "off"]),
        ], 3)
        loader = kit.Loader("Working", spinner_color="accent", message_color="muted", indicator=kit.LoaderIndicator(frames=["*"]))
        lines = kit.Lines(
            ["track one", "track two"],
            list=kit.List([kit.ListItem("track one", "A"), kit.ListItem("track two", "B")], selected=1),
        )
        stack = kit.VStack([
            kit.Box(1, 0, bg="customMessageBg", children=[kit.Text("boxed", 0, 0)]),
            settings,
            loader,
            kit.Image(bytes.fromhex(KIT_KINDS_PNG), "image/png"),
            lines,
        ], gap=1)
        ctx.set_widget("kit-kinds", kit.View(stack, theme={"accent": "#d75f00"}), {})

    ext.command("kit-kinds", "Show every other component kit kind (D107)", kit_kinds)

    # kit-conversation sets the "kit-conversation" widget to every
    # conversation kind (D107, spec §2.1, §10); "next" updates the nodes
    # with an id as a Pi author updates kept components.
    def kit_conversation(ctx: pig_sdk.Context, args: str) -> None:
        nxt = args == "next"
        user = kit.UserMessage("Fix **the** kit build\n\n- one\n- two")
        streaming = kit.AssistantMessage(id="kit-a1")
        reply = "Done. **Bold** reply\n\n1. a\n2. b"
        if nxt:
            reply += "\n\nThen more kit."
        streaming.update_content(kit.Message([kit.thinking_block("Reading the *kit* file"), kit.text_block(reply)]), not nxt)
        failed = kit.AssistantMessage(kit.Message([kit.thinking_block("secret"), kit.text_block("Visible kit")], stop_reason="error", error_message="kit-boom-7"))
        failed.set_hide_thinking_block(True)
        failed.set_hidden_thinking_label("Pondering kit...")
        failed.set_output_pad(0)

        def card(id: str, name: str, call_id: str, card_args: dict[str, Any], **options: Any) -> kit.ToolExecution:  # noqa: A002
            tool = kit.ToolExecution(name, call_id, card_args, "/work/kit", id=id, **options)
            tool.set_args_complete()
            tool.mark_execution_started()
            return tool

        ls = card("kit-t1", "ls", "call-1", {"path": "src"})
        ls.update_result(kit.ToolResult([kit.text_content("a.go\nb.go")]), False)
        grep = card("kit-t2", "grep", "call-2", {"pattern": "TODO"})
        if nxt:
            grep.update_result(kit.ToolResult([kit.text_content("x.go:1: TODO kit\ny.go:2: TODO kit")]), False)
        else:
            grep.update_result(kit.ToolResult([kit.text_content("x.go:1: TODO kit")]), True)
        read = card("", "read", "call-3", {"path": "missing.txt"})
        read.update_result(kit.ToolResult([kit.text_content("ENOENT: kit")], is_error=True), False)
        read.set_expanded(True)
        custom = card("", "kit_tool", "call-4", {"q": "x"}, tool_definition=kit.TOOL_DEFINITION_EMPTY)
        custom.update_result(kit.ToolResult([kit.text_content("answer 42")]), False)
        exited = kit.BashExecution("ls -la", id="kit-b1")
        exited.append_output("a.txt\n")
        exited.append_output("b.txt")
        exited.set_complete(2)
        exited.set_expanded(nxt)
        seq = kit.BashExecution("seq 25", True)
        seq.append_output("\n".join(str(i + 1) for i in range(25)))
        seq.set_complete(0)
        diff = kit.Diff(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail", file_path="kit.go")
        root = kit.Container([user, streaming, failed, ls, grep, read, custom, exited, seq, diff])
        ctx.set_widget("kit-conversation", kit.View(root), {})

    ext.command("kit-conversation", "Show Pi's conversation components (D107)", kit_conversation)

    def timer_focused_probe(ctx: pig_sdk.Context, args: str) -> None:
        component = TimerFocused()
        frame = ctx.custom(component, {"title": "Timer"})
        ctx.notify(
            "timer=%s disposed=%s detached=%s"
            % (frame or 0, str(component.disposed).lower(), str(component.detached).lower()),
            "info",
        )

    ext.command("timer-focused-probe", "Exercise timer-driven focused UI", timer_focused_probe)

    class OverlayWidthProbe:
        def __init__(self) -> None:
            self.width = 0

        def render(self, width: int) -> list[str]:
            self.width = width
            return [f"overlay width={width}"]

        def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
            if data == "\r":
                return pig_sdk.RemoteComponentResult(done=True, value=self.width)
            return pig_sdk.RemoteComponentResult()

    def overlay_width_probe(ctx: pig_sdk.Context, _args: str) -> None:
        default_width = ctx.custom(OverlayWidthProbe(), {"overlay": True})
        percent_width = ctx.custom(OverlayWidthProbe(), {"overlay": True, "overlayOptions": {"width": "50%"}})
        ctx.notify(f"overlay-width default={default_width} percent={percent_width}", "info")

    ext.command("overlay-width-probe", "Report the width overlay components render at", overlay_width_probe)

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
                % (data.get("toolName"), args.get("path"), args.get("nested", {}).get("depth"), partial.get("content", [{}])[0].get("text"), partial.get("details", {}).get("progress")),
                "info",
            )
            return
        ctx.notify(
            "tool-update=%s:%s:%s:%s"
            % (data.get("toolName"), json.dumps(data.get("args"), separators=(",", ":")), data.get("partialResult", {}).get("content", [{}])[0].get("text"), data.get("partialResult", {}).get("details", {}).get("progress")),
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
        elif command == "operations":
            return {"operations": ConformanceBashOperations()}
        elif command != "valid":
            return None
        return value

    ext.on_event("user_bash", user_bash)

    def tool_call(ctx: pig_sdk.Context, data: dict[str, Any]) -> dict[str, Any] | None:
        name = data.get("toolName")
        # Pi's handler mutates event.input in place and the runner reads it back (runner.ts emitToolCall), so the rewrite is the handler's own edit to the event.
        # Assigning a new dict to the event's input leaves the object the tool runs with (agent-loop.ts prepareToolCall).
        if name == "rewrite_reassign_probe":
            data["input"] = {**data["input"], "command": "git status --short"}
            return None
        # Moving a member to the end is an edit in Pi, and two added members follow in the order the handler added them.
        if name == "rewrite_order_probe":
            order = data["input"]
            if order.get("command") == "reorder":
                order["timeout"] = order.pop("timeout")
            else:
                order["zeta"] = 1
                order["alpha"] = 2
            return None
        if name in ("rewrite_probe", "rewrite_block_probe"):
            rewrite = data["input"]
            if rewrite.get("command") == "git status" or name == "rewrite_block_probe":
                rewrite["command"] = "git status --short"
                rewrite.pop("drop", None)
                rewrite["added"] = True
                rewrite["nested"] = {"depth": 2}
            if name == "rewrite_block_probe":
                return {"block": True, "reason": "blocked after rewrite"}
            return None
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
    ext.on_project_trust(lambda _ctx, data: {"trusted": "undecided"} if data.get("cwd") == "/probe" else {"trusted": "yes", "remember": True})
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
