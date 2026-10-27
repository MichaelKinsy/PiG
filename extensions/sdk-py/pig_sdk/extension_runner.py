"""Pi's ``ExtensionRunner`` class (core/extensions/runner.ts): the dispatcher and registry over the extensions a ``DefaultResourceLoader`` loaded. The host owns the runner and the extensions stay in their own processes, as a loader's do; the runner dispatches events to them in extension order with Pi's chaining and error isolation, and answers the registries (tools, commands, flags, shortcuts, handlers) from them.

Pi's runner is built over ``Extension`` objects that hold the extensions' functions. Here an extension is named by a ``LoadedExtension``, the data ``DefaultResourceLoader.get_extensions`` returns, and an extension built in this process (an inline factory) cannot take part: it would run here while the runner runs in the host. Load it through ``additional_extension_paths``.

Events and their results cross as Pi's JSON values (dicts and lists). The functions of ``bind_core``, ``bind_command_context`` and ``set_ui_context`` run in this process, called by the host when an extension asks for the action; they may call the runner. Each call blocks until Pi's would settle; the reads are calls on the host's state, so a read after a write sees the write. The members of Pi's ``ExtensionUIContext`` that take or return TUI components (``setFooter``, ``setHeader``, ``custom``, ``setEditorComponent``, ``addAutocompleteProvider``, ``theme`` and the theme functions, ``onTerminalInput``) are not served here: they belong to the TUI handle."""
from __future__ import annotations

from typing import Any, Callable

from ._graph import _GraphObject, _callback_id
from .extension_runtime import ExtensionRuntime
from .provider import ProviderSignal

_ACTIONS = (
    "send_message", "send_user_message", "append_entry", "set_session_name", "get_session_name", "set_label", "get_active_tools", "get_all_tools", "get_settings",
    "set_active_tools", "refresh_tools", "get_commands", "set_model", "get_thinking_level", "set_thinking_level",
)
_CONTEXT_ACTIONS = (
    "get_model", "get_scoped_models", "is_idle", "is_project_trusted", "get_signal", "abort", "has_pending_messages", "shutdown", "get_context_usage", "compact",
    "get_system_prompt", "get_system_prompt_options", "execute_tool", "get_callable_tools",
)
_PROVIDER_ACTIONS = ("register_provider", "unregister_provider")
_COMMAND_ACTIONS = ("wait_for_idle", "new_session", "fork", "navigate_tree", "switch_session", "reload")
_UI_MEMBERS = (
    "select", "confirm", "input", "editor", "notify", "set_status", "set_working_message", "set_working_visible", "set_hidden_thinking_label", "set_widget", "set_title",
    "paste_to_editor", "set_editor_text", "get_editor_text", "get_tools_expanded", "set_tools_expanded",
)


def _camel(name: str) -> str:
    head, *rest = name.split("_")
    return head + "".join(part.capitalize() for part in rest)


def _table(context: Any, functions: dict[str, Callable[..., Any]] | None, allowed: tuple[str, ...], what: str, adapt: dict[str, Callable[[Callable[..., Any]], Callable[..., Any]]] | None = None) -> dict[str, Any]:
    """Name each function of ``functions`` for the host under Pi's member name. ``None`` values are Pi's undefined and are not bound."""
    wire: dict[str, Any] = {}
    for name, function in (functions or {}).items():
        if name not in allowed:
            raise TypeError(f"{what} has no member {name}")
        if function is None:
            continue
        wire[_camel(name)] = _callback_id(context, (adapt or {}).get(name, lambda f: f)(function))
    return wire


def _signal_wire(function: Callable[[], ProviderSignal | None]) -> Callable[[], Any]:
    def answer() -> Any:
        signal = function()
        return None if signal is None else {"aborted": signal.is_set()}

    return answer


class ExtensionRunner(_GraphObject):
    def __init__(
        self,
        context: Any,
        extensions: list[Any],
        runtime: ExtensionRuntime | None = None,
        cwd: str = "",
        session_manager: Any = None,
        model_registry: Any = None,
    ) -> None:
        """``new ExtensionRunner(extensions, runtime, cwd, sessionManager, modelRegistry)``. ``extensions`` are the ``LoadedExtension`` values of a ``get_extensions`` result, ``runtime`` its ``runtime``. ``session_manager`` is the log ``ctx.sessionManager`` exposes and ``model_registry`` the registry ``ctx.modelRegistry`` reads and provider registration applies to; Pi's ModelRegistry class is served as ``ModelRuntime``. A ``None`` runtime is the runtime the extensions share; a ``None`` session manager or model registry is not bound."""
        refs = []
        for extension in extensions:
            loader = getattr(extension, "_loader", "")
            if not loader:
                raise RuntimeError(
                    f"extension {extension.get('path') if isinstance(extension, dict) else extension!r} was not read from a DefaultResourceLoader: an extension built in this process cannot join the runner, load it with additional_extension_paths"
                )
            refs.append({"loader": loader, "resolvedPath": extension["resolvedPath"]})
        wire: dict[str, Any] = {"extensions": refs, "cwd": cwd}
        if runtime is not None:
            wire["runtime"] = runtime.handle
        if session_manager is not None:
            wire["sessionManager"] = session_manager.handle
        if model_registry is not None:
            wire["modelRegistry"] = model_registry.handle
        super().__init__(context, "ExtensionRunner", "new", wire)
        self._session_manager = session_manager
        self._model_registry = model_registry
        self._context_actions: dict[str, Callable[..., Any]] = {}
        self._command_actions: dict[str, Callable[..., Any]] = {}

    # Binding.

    def bind_core(self, actions: dict[str, Callable[..., Any]], context_actions: dict[str, Callable[..., Any]], provider_actions: dict[str, Callable[..., Any]] | None = None) -> None:
        """``bindCore(actions, contextActions, providerActions?)``: it installs the functions the extensions' actions and contexts call, and applies the provider registrations the extensions queued while loading (to ``provider_actions``, or to the model registry the runner was built with). Each table maps Pi's member name in snake_case (``send_message``, ``get_active_tools``, ``is_idle``, ``register_provider``, ...) to a function that takes Pi's arguments as JSON values and returns Pi's result; ``None`` is Pi's undefined (not bound). ``get_signal`` answers a ``ProviderSignal`` or ``None``."""
        context = self._context
        wire = [
            _table(context, actions, _ACTIONS, "ExtensionActions"),
            _table(context, context_actions, _CONTEXT_ACTIONS, "ExtensionContextActions", {"get_signal": _signal_wire}),
        ]
        if provider_actions is not None:
            wire.append(_table(context, provider_actions, _PROVIDER_ACTIONS, "ProviderActions"))
        self._invoke("bindCore", *wire)
        self._context_actions = {name: function for name, function in context_actions.items() if function is not None}

    def bind_command_context(self, actions: dict[str, Callable[..., Any]] | None = None) -> None:
        """``bindCommandContext(actions?)``. ``None`` is Pi's undefined: it restores the defaults (waitForIdle and reload resolve, a session replacement is not cancelled). ``new_session``, ``fork`` and ``switch_session`` return ``{"cancelled": bool}``; a request an extension of the runner makes carries only the data options, so ``setup`` and ``with_session`` come from calls on this process's own command context."""
        if actions is None:
            self._invoke("bindCommandContext")
            self._command_actions = {}
            return
        self._invoke("bindCommandContext", _table(self._context, actions, _COMMAND_ACTIONS, "ExtensionCommandContextActions"))
        self._command_actions = {name: function for name, function in actions.items() if function is not None}

    def on_error(self, listener: Callable[[dict[str, Any]], None]) -> Callable[[], None]:
        """``onError(listener)``: the listener runs, in this process, for each error the runner reports (``{extensionPath, event, error, stack?}``). The returned function removes it."""

        def deliver(report: Any = None) -> None:
            listener(report)

        handle = self._invoke("onError", _callback_id(self._context, deliver))

        def off() -> None:
            self._invoke("offError", handle)

        return off

    def emit_error(self, error: dict[str, Any]) -> None:
        """``emitError(error)``: it reports ``error`` (``{extensionPath, event, error, stack?}``) to the runner's listeners."""
        self._invoke("emitError", error)

    # Registries and reads.

    def get_extension_paths(self) -> list[str]:
        """``getExtensionPaths()``: the path of each extension, in load order."""
        return list(self._invoke("getExtensionPaths") or [])

    def get_all_registered_tools(self) -> list[dict[str, Any]]:
        """``getAllRegisteredTools()``: each extension's tools in load order, the first registration of a name winning, as ``[{definition, sourceInfo}]``."""
        return self._invoke("getAllRegisteredTools")

    def get_tool_definition(self, tool_name: str) -> dict[str, Any] | None:
        """``getToolDefinition(toolName)``: the definition's data members, ``None`` for Pi's undefined."""
        return self._invoke("getToolDefinition", tool_name)

    def get_flags(self) -> dict[str, Any]:
        """``getFlags()``: the flags the extensions registered as a dict by name, the first registration of a name winning."""
        return self._invoke("getFlags")

    def set_flag_value(self, name: str, value: bool | str) -> None:
        """``setFlagValue(name, value)``."""
        self._invoke("setFlagValue", name, value)

    def get_flag_values(self) -> dict[str, Any]:
        """``getFlagValues()``: a copy of the runtime's flag values as a dict, listed by name."""
        return self._invoke("getFlagValues")

    def get_shortcuts(self, resolved_keybindings: dict[str, Any]) -> list[list[Any]]:
        """``getShortcuts(resolvedKeybindings)``: the extensions' shortcuts under their normalized keys, as ``[[key, {shortcut, description?, extensionPath}]]`` in Pi's Map order. ``resolved_keybindings`` maps each action to a key or a list of keys. A conflict with a built-in or another extension's shortcut is reported by ``get_shortcut_diagnostics``."""
        return self._invoke("getShortcuts", resolved_keybindings)

    def get_shortcut_diagnostics(self) -> list[dict[str, Any]]:
        """``getShortcutDiagnostics()``: the conflicts the last ``get_shortcuts`` found."""
        return self._invoke("getShortcutDiagnostics")

    def has_handlers(self, event_type: str) -> bool:
        """``hasHandlers(eventType)``."""
        return bool(self._invoke("hasHandlers", event_type))

    def get_message_renderer(self, custom_type: str) -> bool:
        """``getMessageRenderer(customType)``: whether an extension registered one. The function runs in the extension; Pi's lookup returns a function or undefined."""
        return bool(self._invoke("getMessageRenderer", custom_type))

    def get_entry_renderer(self, custom_type: str) -> bool:
        """``getEntryRenderer(customType)``: whether an extension registered one."""
        return bool(self._invoke("getEntryRenderer", custom_type))

    def get_markdown_transformers(self) -> int:
        """``getMarkdownTransformers()``: how many Markdown transformers extensions registered, one per extension, in load order."""
        return int(self._invoke("getMarkdownTransformers") or 0)

    def resolve_tool_renderers(self, tool_name: str, base: Callable[[], Any] | None = None) -> dict[str, Any] | None:
        """``resolveToolRenderers(toolName, base)``: the renderers of calls to ``tool_name``, as ``{renderCall, renderResult, renderShell?}`` (which renderers exist), the extensions' resolvers in load order and then ``base``, a function of this process that answers the renderers to use when no extension chose. ``None`` when no renderers apply. An extension process answers a resolution on a later turn (D89), so the first call answers what ``base`` gives."""
        return self._invoke("resolveToolRenderers", tool_name, _callback_id(self._context, None if base is None else (lambda *_: base())))

    def get_model_registry(self) -> Any:
        """``getModelRegistry()``: the registry the runner was built with."""
        return self._model_registry

    def get_registered_commands(self) -> list[dict[str, Any]]:
        """``getRegisteredCommands()``: each extension's commands in load order, a name two extensions registered invoked as ``name:1`` and ``name:2``."""
        return self._invoke("getRegisteredCommands")

    def get_command_diagnostics(self) -> list[dict[str, Any]]:
        """``getCommandDiagnostics()``."""
        return self._invoke("getCommandDiagnostics")

    def get_command(self, name: str) -> dict[str, Any] | None:
        """``getCommand(name)``: the command invoked under ``name``, ``None`` for Pi's undefined."""
        return self._invoke("getCommand", name)

    def shutdown(self) -> None:
        """``shutdown()``: it calls the shutdown function bound by ``bind_core``."""
        self._invoke("shutdown")

    def get_active_tools(self) -> list[str]:
        """``getActiveTools()``: the active tool names the bound action answers. It raises before the runner is bound, and after it is invalidated."""
        return list(self._invoke("getActiveTools") or [])

    def invalidate(self, message: str | None = None) -> None:
        """``invalidate(message?)``: later uses of the runner's contexts fail with ``message``, or Pi's default one. A second call keeps the first message."""
        self._invoke("invalidate", message)

    def report_unhandled_mcp_servers(self) -> None:
        """``reportUnhandledMcpServers()``: it reports through the error listeners each registered MCP server that no loaded extension handles."""
        self._invoke("reportUnhandledMcpServers")

    # The UI context.

    def has_ui(self) -> bool:
        """``hasUI()``: whether a UI context is set."""
        return bool(self._invoke("hasUI"))

    def set_ui_context(self, ui: dict[str, Callable[..., Any]] | None = None, mode: str = "print") -> None:
        """``setUIContext(uiContext?, mode = "print")``. ``None`` is Pi's undefined: no UI. ``mode`` is ``"tui"``, ``"rpc"``, ``"json"`` or ``"print"``; empty is ``"print"``. ``ui`` maps the data members of Pi's ``ExtensionUIContext`` in snake_case (``select``, ``confirm``, ``input``, ``editor``, ``notify``, ``set_status``, ``set_working_message``, ``set_working_visible``, ``set_hidden_thinking_label``, ``set_widget``, ``set_title``, ``paste_to_editor``, ``set_editor_text``, ``get_editor_text``, ``get_tools_expanded``, ``set_tools_expanded``) to functions of this process; a member left out is Pi's no-op one. The runner wraps the dialogs so each reports ui_prompt_start and ui_prompt_end to the extensions."""
        wire = None if ui is None else _table(self._context, ui, _UI_MEMBERS, "ExtensionUIContext (the members that take or return TUI components belong to the TUI handle)")
        self._invoke("setUIContext", wire, mode)

    def get_ui_context(self) -> "RunnerUIContext":
        """``getUIContext()``: the runner's UI context. The calls go through the runner, which wraps the dialogs of the context ``set_ui_context`` installed (or Pi's no-op context) so they report ui_prompt_start and ui_prompt_end."""
        return RunnerUIContext(self)

    # Contexts.

    def create_context(self) -> "ExtensionContext":
        """``createContext()``."""
        return ExtensionContext(self, {"kind": "context"})

    def create_tool_context(self, tool_call_id: str, signal: ProviderSignal | None = None) -> "ExtensionToolContext":
        """``createToolContext(toolCallId, signal)``: ``signal`` is the default cancellation of nested calls; ``None`` is Pi's undefined."""
        return ExtensionToolContext(self, {"kind": "tool", "toolCallId": tool_call_id}, tool_call_id, signal)

    def create_command_context(self) -> "ExtensionCommandContext":
        """``createCommandContext()``."""
        return ExtensionCommandContext(self, {"kind": "command"})

    # Emits. Each blocks until Pi's promise would settle.

    def emit(self, event: dict[str, Any]) -> Any:
        """``emit(event)``: ``event`` is Pi's event object (a RunnerEmitEvent: session_start, agent_start, message_update and the other events that have no emit method of their own). The result is what the chain answered, which is not ``None`` for the session_before_* events only."""
        return self._invoke_async("emit", event)

    def emit_tool_call(self, event: dict[str, Any]) -> dict[str, Any] | None:
        """``emitToolCall(event)``: a tool_call event, chained so each handler sees the input the earlier ones edited; the result is ``{block, reason}`` when a handler blocked, ``None`` otherwise."""
        return self._invoke_async("emitToolCall", event)

    def emit_tool_result(self, event: dict[str, Any]) -> dict[str, Any] | None:
        """``emitToolResult(event)``: a tool_result event whose handlers' edits chain; the result is the edits (``{content, details, isError, ...}``), ``None`` when none."""
        return self._invoke_async("emitToolResult", event)

    def emit_input(self, text: str, images: list[dict[str, Any]] | None, source: str, streaming_behavior: str | None = None) -> dict[str, Any]:
        """``emitInput(text, images, source, streamingBehavior?)``: the result is Pi's InputEventResult, ``{action: "continue"}``, ``{action: "transform", text, images?}`` or ``{action: "handled"}``."""
        return self._invoke_async("emitInput", text, images, source, streaming_behavior)

    def emit_context(self, messages: list[dict[str, Any]]) -> list[dict[str, Any]]:
        """``emitContext(messages)``: the messages the ``context`` handlers left."""
        return self._invoke_async("emitContext", messages)

    def emit_before_agent_start(self, trigger: dict[str, Any], system_prompt_options: dict[str, Any]) -> dict[str, Any] | None:
        """``emitBeforeAgentStart(trigger, systemPromptOptions)``: ``trigger`` is what starts the run, ``{messageType: "user", prompt, images?}`` or ``{messageType: "custom", message}``. The result is ``{messages, systemPromptOptions}``: the handlers' custom messages and the options their system prompt edits left, with the prompt a handler set as forceSystemPrompt."""
        return self._invoke_async("emitBeforeAgentStart", trigger, system_prompt_options)

    def emit_before_provider_request(self, payload: Any) -> Any:
        """``emitBeforeProviderRequest(payload)``: the payload the handlers left."""
        return self._invoke_async("emitBeforeProviderRequest", payload)

    def emit_before_provider_headers(self, headers: dict[str, Any]) -> dict[str, Any]:
        """``emitBeforeProviderHeaders(headers)``: the headers the handlers left; a ``None`` value deletes a header."""
        return self._invoke_async("emitBeforeProviderHeaders", headers)

    def emit_user_bash(self, event: dict[str, Any]) -> dict[str, Any] | None:
        """``emitUserBash(event)``: the result is a handler's ``{result}``, ``None`` when none."""
        return self._invoke_async("emitUserBash", event)

    def emit_message_end(self, event: dict[str, Any]) -> dict[str, Any] | None:
        """``emitMessageEnd(event)``: the message a handler replaced the finalized one with, ``None`` when none did."""
        return self._invoke_async("emitMessageEnd", event)

    def emit_resources_discover(self, cwd: str, reason: str) -> dict[str, Any]:
        """``emitResourcesDiscover(cwd, reason)``: the result is ``{skillPaths, promptPaths, themePaths}``, each path with the extension that gave it."""
        return self._invoke_async("emitResourcesDiscover", cwd, reason)

    def emit_cache_warming_decision(self, event: dict[str, Any]) -> str:
        """``emitCacheWarmingDecision(event)``: the event's own action, ``"warm"`` or ``"stop"``, unless a handler overrides it; the last override wins."""
        return self._invoke_async("emitCacheWarmingDecision", event)

    def emit_boundary(self, base_event: dict[str, Any], build_context: Callable[[list[dict[str, Any]]], dict[str, Any]]) -> dict[str, Any]:
        """``emitBoundary(baseEvent, buildContext)``: ``base_event`` is a turn_end or agent_before_settle event without entries, continue and context; ``build_context`` is a function of this process that answers the context preview for a list of drafted entries. The result is ``{entries, continue, context, valid}``."""
        return self._invoke_async("emitBoundary", base_event, _callback_id(self._context, lambda entries=None: build_context(entries)))


class RunnerUIContext:
    """Pi's ``runner.getUIContext()``: the calls go through the runner."""

    def __init__(self, runner: ExtensionRunner) -> None:
        self._runner = runner

    def _member(self, member: str, *args: Any) -> Any:
        return self._runner._invoke_async("ui", member, *args)

    def select(self, title: str, options: list[str], opts: dict[str, Any] | None = None) -> str | None:
        """``ui.select(title, options, opts?)``; ``None`` is the dialog's undefined (cancelled)."""
        return self._member("select", title, options, opts)

    def confirm(self, title: str, message: str, opts: dict[str, Any] | None = None) -> bool:
        """``ui.confirm(title, message, opts?)``."""
        return bool(self._member("confirm", title, message, opts))

    def input(self, title: str, placeholder: str | None = None, opts: dict[str, Any] | None = None) -> str | None:
        """``ui.input(title, placeholder?, opts?)``; ``None`` is the dialog's undefined."""
        return self._member("input", title, placeholder, opts)

    def editor(self, title: str, prefill: str | None = None) -> str | None:
        """``ui.editor(title, prefill?)``; ``None`` is the dialog's undefined."""
        return self._member("editor", title, prefill)

    def notify(self, message: str, kind: str | None = None) -> None:
        """``ui.notify(message, type?)``."""
        self._member("notify", message, kind)

    def set_status(self, key: str, text: str | None) -> None:
        """``ui.setStatus(key, text)``."""
        self._member("setStatus", key, text)

    def set_working_message(self, message: str | None = None) -> None:
        """``ui.setWorkingMessage(message?)``."""
        self._member("setWorkingMessage", message)

    def set_working_visible(self, visible: bool) -> None:
        """``ui.setWorkingVisible(visible)``."""
        self._member("setWorkingVisible", visible)

    def set_hidden_thinking_label(self, label: str | None = None) -> None:
        """``ui.setHiddenThinkingLabel(label?)``."""
        self._member("setHiddenThinkingLabel", label)

    def set_widget(self, key: str, content: list[str] | None, options: dict[str, Any] | None = None) -> None:
        """``ui.setWidget(key, content, options?)`` for lines of text."""
        self._member("setWidget", key, content, options)

    def set_title(self, title: str) -> None:
        """``ui.setTitle(title)``."""
        self._member("setTitle", title)

    def paste_to_editor(self, text: str) -> None:
        """``ui.pasteToEditor(text)``."""
        self._member("pasteToEditor", text)

    def set_editor_text(self, text: str) -> None:
        """``ui.setEditorText(text)``."""
        self._member("setEditorText", text)

    def get_editor_text(self) -> str:
        """``ui.getEditorText()``."""
        return self._member("getEditorText") or ""

    def get_tools_expanded(self) -> bool:
        """``ui.getToolsExpanded()``."""
        return bool(self._member("getToolsExpanded"))

    def set_tools_expanded(self, expanded: bool) -> None:
        """``ui.setToolsExpanded(expanded)``."""
        self._member("setToolsExpanded", expanded)


class ExtensionContext:
    """Pi's ``ExtensionContext`` as the runner's ``create_context`` returns it: its getters read the runner at call time, so a change bound functions answer is seen, and a runner that was invalidated fails each of them with its message."""

    def __init__(self, runner: ExtensionRunner, spec: dict[str, Any]) -> None:
        self._runner = runner
        self._spec = spec

    def _get(self, member: str, *args: Any) -> Any:
        return self._runner._invoke("context", self._spec, member, *args)

    def _assert_active(self) -> None:
        self._get("assertActive")

    def ui(self) -> RunnerUIContext:
        """``ctx.ui``."""
        self._assert_active()
        return RunnerUIContext(self._runner)

    def mode(self) -> str:
        """``ctx.mode``: ``"tui"``, ``"rpc"``, ``"json"`` or ``"print"``."""
        return self._get("mode") or ""

    def has_ui(self) -> bool:
        """``ctx.hasUI``."""
        return bool(self._get("hasUI"))

    def cwd(self) -> str:
        """``ctx.cwd``."""
        return self._get("cwd") or ""

    def session_manager(self) -> Any:
        """``ctx.sessionManager``: the log the runner was built with."""
        self._assert_active()
        return self._runner._session_manager

    def model_registry(self) -> Any:
        """``ctx.modelRegistry``: the registry the runner was built with."""
        self._assert_active()
        return self._runner._model_registry

    def model(self) -> dict[str, Any] | None:
        """``ctx.model``: the model the bound getModel answers; ``None`` is Pi's undefined."""
        return self._get("model")

    def scoped_models(self) -> list[dict[str, Any]]:
        """``ctx.scopedModels``: ``[{model, thinkingLevel?}]``."""
        return self._get("scopedModels") or []

    def thinking_level(self) -> str:
        """``ctx.thinkingLevel``: what the bound getThinkingLevel answers."""
        return self._get("thinkingLevel") or ""

    def is_idle(self) -> bool:
        """``ctx.isIdle()``."""
        return bool(self._get("isIdle"))

    def is_project_trusted(self) -> bool:
        """``ctx.isProjectTrusted()``."""
        return bool(self._get("isProjectTrusted"))

    def signal(self) -> ProviderSignal | None:
        """``ctx.signal``: ``None`` is Pi's undefined (no run); otherwise a signal that is set when the host's was aborted at the time of the read."""
        wire = self._get("signal")
        if wire is None:
            return None
        signal = ProviderSignal()
        if wire.get("aborted"):
            signal.set()
        return signal

    def abort(self) -> None:
        """``ctx.abort()``."""
        self._get("abort")

    def has_pending_messages(self) -> bool:
        """``ctx.hasPendingMessages()``."""
        return bool(self._get("hasPendingMessages"))

    def shutdown(self) -> None:
        """``ctx.shutdown()``."""
        self._get("shutdown")

    def get_context_usage(self) -> dict[str, Any] | None:
        """``ctx.getContextUsage()``; ``None`` is Pi's undefined."""
        return self._get("getContextUsage")

    def compact(self, options: dict[str, Any] | None = None) -> None:
        """``ctx.compact(options?)``: it calls the bound compact function with ``options``, here, so its callbacks are this process's."""
        self._assert_active()
        compact = self._runner._context_actions.get("compact")
        if compact is not None:
            compact(options or {})

    def get_system_prompt(self) -> str:
        """``ctx.getSystemPrompt()``."""
        return self._get("getSystemPrompt") or ""


class ExtensionToolContext(ExtensionContext):
    """Pi's ``ExtensionToolContext``: the context of one tool call, with the callable tools and nested execution."""

    def __init__(self, runner: ExtensionRunner, spec: dict[str, Any], tool_call_id: str, signal: ProviderSignal | None) -> None:
        super().__init__(runner, spec)
        self._tool_call_id = tool_call_id
        self._signal = signal

    def tools(self) -> list[dict[str, Any]]:
        """``ctx.tools``: the tools a nested call can run."""
        return self._get("tools") or []

    def execute_tool(self, name: str, args: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any]:
        """``ctx.executeTool(name, args, options?)``: it calls the bound executeTool function, here, with ``options`` whose ``signal`` defaults to the one the context was created with. Without a bound function the outcome is Pi's error outcome ("Nested tool calls are not available in this context")."""
        self._assert_active()
        execute = self._runner._context_actions.get("execute_tool")
        merged = dict(options or {})
        if merged.get("signal") is None and self._signal is not None:
            merged["signal"] = self._signal
        if execute is not None:
            return execute(self._tool_call_id, name, args, merged)
        return {
            "toolCall": {"type": "toolCall", "id": f"{self._tool_call_id}/0", "name": name, "arguments": {}},
            "result": {"content": [{"type": "text", "text": "Nested tool calls are not available in this context"}], "details": {}},
            "isError": True,
        }


class ExtensionCommandContext(ExtensionContext):
    """Pi's ``ExtensionCommandContext``: the context of a command, which can wait for the agent and replace the session."""

    def get_system_prompt_options(self) -> dict[str, Any]:
        """``ctx.getSystemPromptOptions()``."""
        return self._get("getSystemPromptOptions")

    def _action(self, name: str) -> Callable[..., Any] | None:
        self._assert_active()
        return self._runner._command_actions.get(name)

    def wait_for_idle(self) -> None:
        """``ctx.waitForIdle()``."""
        action = self._action("wait_for_idle")
        if action is not None:
            action()

    def new_session(self, options: dict[str, Any] | None = None) -> dict[str, Any]:
        """``ctx.newSession(options?)``: the bound function receives ``options`` as given, including a ``setup`` or ``withSession`` function of this process."""
        action = self._action("new_session")
        return action(options or {}) if action is not None else {"cancelled": False}

    def fork(self, entry_id: str, options: dict[str, Any] | None = None) -> dict[str, Any]:
        """``ctx.fork(entryId, options?)``."""
        action = self._action("fork")
        return action(entry_id, options or {}) if action is not None else {"cancelled": False}

    def navigate_tree(self, target_id: str, options: dict[str, Any] | None = None) -> dict[str, Any]:
        """``ctx.navigateTree(targetId, options?)``."""
        action = self._action("navigate_tree")
        return action(target_id, options or {}) if action is not None else {"cancelled": False}

    def switch_session(self, session_path: str, options: dict[str, Any] | None = None) -> dict[str, Any]:
        """``ctx.switchSession(sessionPath, options?)``."""
        action = self._action("switch_session")
        return action(session_path, options or {}) if action is not None else {"cancelled": False}

    def reload(self) -> None:
        """``ctx.reload()``."""
        action = self._action("reload")
        if action is not None:
            action()
