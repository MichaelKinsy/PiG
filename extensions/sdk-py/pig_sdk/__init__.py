"""Pig subprocess extension SDK for Python.

Factory-style Python extensions should expose a function such as::

    def new_extension() -> pig_sdk.Extension: ...

Generated standalone and packed runners call that factory and then run the
returned extension over a host-provided subprocess socket.
"""

from __future__ import annotations

import copy
import decimal
import json
import math
import os
import queue
import socket
import struct
import sys
import threading
import time
import traceback
import weakref
from collections import deque
from contextvars import ContextVar
from dataclasses import dataclass, field
from typing import Any, Callable, Literal, NotRequired, Protocol, TypeAlias, TypedDict
from .autocomplete import AutocompleteProvider as AutocompleteProvider, AutocompleteProviderFactory, _AutocompleteRegistry
from .session_manager import SessionManager
from ._color import color_ansi, parse_color, style_text_with_ansi
from .event_bus import EventBus as EventBus, EventBusHandler as EventBusHandler, _EventBusHub
from .provider import (
    Provider, ProviderSignal,
    ProviderAuth as ProviderAuth, APIKeyAuth as APIKeyAuth, OAuthAuth as OAuthAuth,
    AuthContext as AuthContext, APIKeyAuthInput as APIKeyAuthInput, AuthResult as AuthResult,
    AuthCheck as AuthCheck, AuthInteraction as AuthInteraction,
    ModelsPublication as ModelsPublication, RefreshModelsContext as RefreshModelsContext,
    ProviderStreamOptions as ProviderStreamOptions,
    ProviderOperationOptions as ProviderOperationOptions,
    register_native as _register_native, remote_provider as _remote_provider,
    dispatch_provider as _dispatch_provider, dispatch_callback as _dispatch_provider_callback,
    dispatch_operation as _dispatch_provider_operation,
)

# Maximum frame size (128 MB). Bounds a single length-prefixed frame to guard
# against unbounded allocation while allowing large host responses such as
# getBranch on a long session. Must match the host and other-language SDK
# MaxFrameSize constants.
MAX_FRAME_SIZE = 128 * 1024 * 1024

# Winsock's AF_UNIX family and sockaddr_un path capacity (afunix.h).
_WINSOCK_AF_UNIX = 1
_WINSOCK_UNIX_PATH_MAX = 108


def _connect_unix(sock_path: str) -> socket.socket:
    """Connect a stream socket to the host's AF_UNIX socket at sock_path."""
    if hasattr(socket, "AF_UNIX"):
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            sock.connect(sock_path)
        except BaseException:
            sock.close()
            raise
        return sock
    return _connect_winsock_unix(sock_path)


def _connect_winsock_unix(sock_path: str) -> socket.socket:
    """Connect through Winsock's AF_UNIX support, which CPython does not expose.

    Windows 10 1803 and later provide AF_UNIX stream sockets in ws2_32. The
    connected handle is wrapped in an ordinary socket object, which owns and
    closes it.
    """
    import ctypes

    class _SockaddrUn(ctypes.Structure):
        _fields_ = [("sun_family", ctypes.c_ushort), ("sun_path", ctypes.c_char * _WINSOCK_UNIX_PATH_MAX)]

    path = os.fsencode(sock_path)
    if len(path) >= _WINSOCK_UNIX_PATH_MAX:
        raise OSError(f"socket path is {len(path)} bytes; AF_UNIX allows at most {_WINSOCK_UNIX_PATH_MAX - 1}: {sock_path}")
    ws2 = ctypes.WinDLL("ws2_32", use_last_error=True)
    ws2.socket.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_int]
    ws2.socket.restype = ctypes.c_size_t
    ws2.connect.argtypes = [ctypes.c_size_t, ctypes.c_void_p, ctypes.c_int]
    ws2.connect.restype = ctypes.c_int
    ws2.closesocket.argtypes = [ctypes.c_size_t]
    ws2.closesocket.restype = ctypes.c_int
    # Importing socket has already initialized Winsock (WSAStartup).
    handle = ws2.socket(_WINSOCK_AF_UNIX, socket.SOCK_STREAM, 0)
    if handle == ctypes.c_size_t(-1).value:
        raise ctypes.WinError(ctypes.get_last_error())
    address = _SockaddrUn(_WINSOCK_AF_UNIX, path)
    if ws2.connect(handle, ctypes.byref(address), ctypes.sizeof(address)) != 0:
        error = ctypes.get_last_error()
        ws2.closesocket(handle)
        raise ctypes.WinError(error)
    try:
        return socket.socket(_WINSOCK_AF_UNIX, socket.SOCK_STREAM, 0, fileno=handle)
    except BaseException:
        ws2.closesocket(handle)
        raise

# Upstream extension event names, for Extension.on_event.
EVENT_PROJECT_TRUST = "project_trust"
EVENT_RESOURCES_DISCOVER = "resources_discover"
EVENT_SESSION_START = "session_start"
EVENT_SESSION_INFO_CHANGED = "session_info_changed"
EVENT_SESSION_BEFORE_SWITCH = "session_before_switch"
EVENT_SESSION_BEFORE_FORK = "session_before_fork"
EVENT_SESSION_BEFORE_COMPACT = "session_before_compact"
EVENT_SESSION_COMPACT = "session_compact"
EVENT_SESSION_COMPACT_FAILED = "session_compact_failed"
EVENT_SESSION_SHUTDOWN = "session_shutdown"
EVENT_SESSION_BEFORE_TREE = "session_before_tree"
EVENT_SESSION_TREE = "session_tree"
EVENT_CONTEXT = "context"
EVENT_CONTEXT_WITH_SYSTEM = "context_with_system"
EVENT_CACHE_WARMING_DECISION = "cache_warming_decision"
EVENT_BEFORE_PROVIDER_REQUEST = "before_provider_request"
EVENT_BEFORE_PROVIDER_HEADERS = "before_provider_headers"
EVENT_AFTER_PROVIDER_RESPONSE = "after_provider_response"
EVENT_BEFORE_AGENT_START = "before_agent_start"
EVENT_AGENT_START = "agent_start"
EVENT_AGENT_END = "agent_end"
EVENT_AGENT_BEFORE_SETTLE = "agent_before_settle"
EVENT_AGENT_SETTLED = "agent_settled"
EVENT_UI_PROMPT_START = "ui_prompt_start"
EVENT_UI_PROMPT_END = "ui_prompt_end"
EVENT_TURN_START = "turn_start"
EVENT_TURN_END = "turn_end"
EVENT_MESSAGE_START = "message_start"
EVENT_MESSAGE_UPDATE = "message_update"
EVENT_MESSAGE_END = "message_end"
EVENT_TOOL_EXECUTION_START = "tool_execution_start"
EVENT_TOOL_EXECUTION_UPDATE = "tool_execution_update"
EVENT_TOOL_EXECUTION_END = "tool_execution_end"
EVENT_MODEL_SELECT = "model_select"
EVENT_THINKING_LEVEL_SELECT = "thinking_level_select"
EVENT_TOOL_CALL = "tool_call"
EVENT_TOOL_RESULT = "tool_result"
EVENT_USER_BASH = "user_bash"
EVENT_INPUT = "input"
EVENT_PROVIDER_STREAM_EVENT = "provider_stream_event"
EVENT_MCP_SERVERS_CHANGE = "mcp_servers_change"


Schema: TypeAlias = dict[str, Any]
ToolPrepareArguments: TypeAlias = Callable[[dict[str, Any]], dict[str, Any]]
ToolHandler: TypeAlias = Callable[["Context", dict[str, Any]], Any]
CommandHandler: TypeAlias = Callable[["Context", str], None]
# Upstream RegisteredCommand.getArgumentCompletions: the pi-tui AutocompleteItem
# dicts ({"value", "label"?, "description"?}) for the text after
# "/<command> ", or None for none.
ArgumentCompletionsHandler: TypeAlias = Callable[[str], "list[dict[str, Any]] | None"]
EventHandler: TypeAlias = Callable[["Context", dict[str, Any]], Any]


class ProjectTrustResult(TypedDict):
    trusted: Literal["yes", "no", "undecided"]
    remember: NotRequired[bool]


ProjectTrustHandler: TypeAlias = Callable[["Context", dict[str, Any]], ProjectTrustResult]
ShortcutHandler: TypeAlias = Callable[["Context"], None]
RendererHandler: TypeAlias = Callable[["Context", dict[str, Any], dict[str, Any], int], list[str]]


# Upstream 0.99.1 extension API additions (types.ts:509-607, 1708-1855, 2161-2169).
ToolExposure: TypeAlias = Literal["direct", "model-only", "codemode", "deferred", "hidden"]
_TOOL_EXPOSURES = ("direct", "model-only", "codemode", "deferred", "hidden")


class ToolAnnotations(TypedDict, total=False):
    """Upstream ToolAnnotations: hints with the meaning of MCP tool annotations."""

    readOnlyHint: bool
    destructiveHint: bool
    idempotentHint: bool
    openWorldHint: bool


class ToolNamespace(TypedDict):
    """Upstream ToolNamespace: a group of related tools, such as one MCP server's tools."""

    name: str
    description: NotRequired[str]
    instructions: NotRequired[str]


class ToolLoadoutChanges(TypedDict, total=False):
    """Upstream ToolLoadoutChanges."""

    descriptions: dict[str, str]
    hiddenDeclarations: list[str]


@dataclass(frozen=True)
class ToolLoadout:
    """Upstream ToolLoadout: the tools of a session as ``prepare_loadout`` sees them.

    ``declared``, ``callable`` and ``registered`` are AgentTool dicts.
    """

    declared: list[dict[str, Any]]
    callable: list[dict[str, Any]]
    registered: list[dict[str, Any]]
    exposures: dict[str, str] = field(default_factory=dict)
    namespaces: dict[str, ToolNamespace] = field(default_factory=dict)

    def get_exposure(self, name: str) -> ToolExposure:
        """The tool's exposure; a tool without a definition is ``"direct"`` (agent-session.ts:1480-1482)."""
        return self.exposures.get(name, "direct")  # type: ignore[return-value]

    def get_namespace(self, name: str) -> ToolNamespace | None:
        """The tool's namespace, or None (agent-session.ts:1520)."""
        return self.namespaces.get(name)


ToolPrepareLoadoutHandler: TypeAlias = Callable[[ToolLoadout], "ToolLoadoutChanges | None"]


@dataclass
class ToolRenderContext:
    """Upstream ToolRenderContext for a renderer that returns lines.

    ``state`` is the tool card's renderer state: it starts empty and is shared
    by the call and result renderers of one card. ``invalidate()`` asks the
    host to run both renderers again, as upstream ``context.invalidate()``
    does.
    """

    args: dict[str, Any]
    tool_call_id: str
    cwd: str
    execution_started: bool
    args_complete: bool
    is_partial: bool
    expanded: bool
    show_images: bool
    is_error: bool
    state: dict[str, Any]
    _invalidate: Callable[[], None] = field(repr=False, default=lambda: None)

    def invalidate(self) -> None:
        self._invalidate()


# A call renderer: (ctx, args, render context, width) -> lines.
ToolRenderCallHandler: TypeAlias = Callable[["Context", dict[str, Any], ToolRenderContext, int], list[str]]
# A result renderer: (ctx, {content, details}, {expanded, isPartial}, render context, width) -> lines.
ToolRenderResultHandler: TypeAlias = Callable[["Context", dict[str, Any], dict[str, Any], ToolRenderContext, int], list[str]]
Factory: TypeAlias = Callable[[], "Extension"]


@dataclass(kw_only=True)
class ToolDefinition:
    """Upstream ToolDefinition, the argument of :meth:`Extension.register_tool`.

    Fields are keyword-only, as upstream's are object-literal keys.
    ``execute`` is this SDK's tool handler ``(ctx, params) -> result``: the
    tool call id, cancellation and ``onUpdate`` of upstream's
    ``execute(toolCallId, params, signal, onUpdate, ctx)`` are
    ``ctx.tool_call_id``, ``ctx.is_cancelled()`` and ``ctx.on_update()``.
    ``render_call`` and ``render_result`` are the line renderers
    :meth:`Extension.tool_renderers` takes.
    """

    name: str
    label: str
    description: str
    prompt_snippet: str | None = None
    prompt_guidelines: list[str] | None = None
    parameters: Schema
    constrained_sampling: dict[str, Any] | Literal[False] | None = None
    render_shell: Literal["default", "self"] = "default"
    prepare_arguments: ToolPrepareArguments | None = None
    execution_mode: Literal["sequential", "parallel"] | None = None
    execute: ToolHandler
    render_call: ToolRenderCallHandler | None = None
    render_result: ToolRenderResultHandler | None = None
    output_schema: Schema | None = None
    exposure: ToolExposure | None = None
    namespace: ToolNamespace | None = None
    annotations: ToolAnnotations | None = None
    default_active: bool | None = None
    prepare_loadout: ToolPrepareLoadoutHandler | None = None


# pig additive (D60): Python extensions provide typed data for Pig's native
# login template instead of Pi's in-process TUI component factory.
@dataclass(frozen=True)
class LoginDefinition:
    """Semantic pixel art and labels for Pig's native login template."""

    brand: list[str]
    hero: list[str]
    mascot: list[str]
    palette: dict[str, str]
    name: str
    description: str
    tagline: str

    def _to_wire(self) -> dict[str, Any]:
        return {
            "brand": list(self.brand),
            "hero": list(self.hero),
            "mascot": list(self.mascot),
            "palette": dict(self.palette),
            "name": self.name,
            "description": self.description,
            "tagline": self.tagline,
        }


@dataclass(frozen=True)
class ExecResult:
    """Outcome of a host-executed command."""

    stdout: str = ""
    stderr: str = ""
    exit_code: int = 0
    # Upstream ExecResult.killed: the process was killed (timeout or abort).
    killed: bool = False


@dataclass(kw_only=True)
class ExecuteToolOptions:
    """Upstream ExecuteToolOptions, the options of :meth:`Context.execute_tool`.

    ``signal`` defaults to the calling tool's own cancellation; a signal given
    here replaces it, so cancelling the calling tool then leaves the nested
    call running (runner.ts:980). ``on_update``
    receives each partial result of the nested tool as an AgentToolResult dict.
    """

    signal: ProviderSignal | None = None
    on_update: Callable[[dict[str, Any]], None] | None = None


VirtualModelRouteHandler: TypeAlias = Callable[["Context", dict[str, Any]], dict[str, Any]]


@dataclass(kw_only=True)
class VirtualModel:
    """Upstream ExtensionVirtualModel, the argument of :meth:`Extension.register_virtual_model`.

    ``route`` is ``(ctx, request) -> route``. ``request`` is upstream's
    ModelRouteRequest as a dict (``model``, ``thinkingLevel``, ``reason``,
    ``previous``, ``failed``, ``state``, ``messages``) plus ``signal``, the
    cancellation of the request. The route is a dict with the physical
    ``model``, ``thinkingLevel`` and optionally ``state``. A ``state`` that is
    absent or None keeps the current state, as upstream's undefined does, so
    returning ``request.get("state")`` never stores a null state.
    """

    provider: str
    id: str
    name: str
    route: VirtualModelRouteHandler
    thinking_levels: list[str] | None = None
    context_window: int | None = None
    max_tokens: int | None = None
    input: list[Literal["text", "image"]] | None = None


def message_role(data: dict[str, Any]) -> str:
    """Role of an event's message payload, or "" when the event carries none.

    Message-shaped events carry upstream's flat role-discriminated union:
    ``{"type": "message_end", "message": {"role": "assistant", "content": [...]}}``
    where content is a block array, never a bare string.
    """
    message = data.get("message")
    if not isinstance(message, dict):
        return ""
    role = message.get("role")
    return role if isinstance(role, str) else ""


def message_text(data: dict[str, Any]) -> str:
    """Concatenated text blocks of an event's message payload.

    Non-text blocks (tool calls, images, thinking) are skipped. Returns "" when
    the event carries no message or the message has no text.
    """
    message = data.get("message")
    if not isinstance(message, dict):
        return ""
    content = message.get("content")
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return ""
    parts: list[str] = []
    for block in content:
        if not isinstance(block, dict) or block.get("type") != "text":
            continue
        text = block.get("text")
        if isinstance(text, str):
            parts.append(text)
    return "".join(parts)


class SessionMirror:
    """Local session log kept in sync by incremental appends from state_update.

    Eliminates the full-session IPC fetch that was causing 640 MB RSS in
    extensions that call get_branch() on large sessions.
    """

    def __init__(self) -> None:
        # The host sends no entries until this extension asks for them, so that
        # the majority which never inspect the session do not each hold a full
        # copy of it resident. Set before the subscribe call, since the host
        # starts sending the log as soon as it registers the subscription.
        self.subscribed: bool = False
        self._session_id: str = ""
        self._entries: list[dict[str, Any]] = []
        self._leaf_id: str = ""
        self._index: dict[str, tuple[int, str]] = {}  # id -> (pos, parent_id)
        self._branch_cache: list[dict[str, Any]] | None = None
        self._branch_cache_for: str = ""

    def apply_update(self, session: dict[str, Any]) -> bool:
        leaf_id = session.get("leafId", "")
        appended = session.get("entriesAppended") or []
        entry_count = session.get("entryCount", 0)
        expected_base = entry_count - len(appended)
        changed = False
        session_id = session.get("sessionId", "")
        if session_id and session_id != self._session_id:
            self._session_id = session_id
            self._entries = []
            self._index = {}
            self._leaf_id = ""
            self._branch_cache = None
            self._branch_cache_for = ""
            changed = True

        # The leaf is small and always tracked. The log itself is applied only
        # once subscribed, and never from a push carrying no entries and a zero
        # count: that is the shape sent to an unsubscribed extension, and
        # reading it as an empty session would discard a mirror a concurrent
        # subscribe had just filled.
        if not self.subscribed or (entry_count == 0 and not appended):
            if leaf_id and leaf_id != self._leaf_id:
                self._leaf_id = leaf_id
                self._branch_cache = None
                self._branch_cache_for = ""
                return True
            return changed

        if expected_base != len(self._entries):
            self._entries = []
            self._index = {}
            changed = True

        for entry in appended:
            pos = len(self._entries)
            self._entries.append(entry)
            eid = entry.get("id", "")
            if eid:
                self._index[eid] = (pos, entry.get("parentId", ""))
            changed = True

        if leaf_id and leaf_id != self._leaf_id:
            self._leaf_id = leaf_id
            changed = True

        if changed:
            self._branch_cache = None
            self._branch_cache_for = ""
        return changed

    def seed(self, entries: list[dict[str, Any]], leaf_id: str) -> None:
        """Install the log returned by the host at subscribe time.

        A no-op once the push stream has delivered anything, which keeps the
        two paths from fighting over a mirror they both fill.
        """
        if leaf_id:
            self._leaf_id = leaf_id
        if self._entries:
            return
        self._index = {}
        for pos, entry in enumerate(entries):
            eid = entry.get("id", "")
            if eid:
                self._index[eid] = (pos, entry.get("parentId", ""))
        self._entries = list(entries)
        self._branch_cache = None
        self._branch_cache_for = ""

    def get_entries(self) -> list[dict[str, Any]]:
        return list(self._entries)

    def get_branch(self) -> list[dict[str, Any]]:
        if self._branch_cache is not None and self._branch_cache_for == self._leaf_id:
            return list(self._branch_cache)

        if not self._leaf_id or not self._index:
            branch = list(self._entries)
        else:
            path: list[dict[str, Any]] = []
            seen: set[str] = set()
            current = self._leaf_id
            while current and current not in seen:
                seen.add(current)
                meta = self._index.get(current)
                if meta is None:
                    break
                pos, parent_id = meta
                path.append(self._entries[pos])
                current = parent_id
            path.reverse()
            branch = path

        self._branch_cache = branch
        self._branch_cache_for = self._leaf_id
        return list(branch)


@dataclass
class RemoteComponentResult:
    """Result of one focused component input event."""

    done: bool = False
    value: Any = None


class RemoteComponent(Protocol):
    """Subprocess component rendered locally and focused by the host overlay."""

    def render(self, width: int) -> list[str]: ...

    def handle_input(self, data: str) -> RemoteComponentResult: ...


class RemoteComponentInvalidator(Protocol):
    """Optional timer-driven render callback owned by an active overlay."""

    def set_invalidate(self, callback: Callable[[], None] | None) -> None: ...


@dataclass
class _RemoteOverlayState:
    component: RemoteComponent
    last_lines: list[str] = field(default_factory=list)
    seq: int = 0
    events: queue.Queue[tuple[str, str | None]] = field(default_factory=lambda: queue.Queue(maxsize=64))
    active: threading.Event = field(default_factory=threading.Event)
    render_pending: bool = False
    event_lock: threading.Lock = field(default_factory=threading.Lock)
    worker: threading.Thread | None = None
    last_render: float = float("-inf")

    def __post_init__(self) -> None:
        self.active.set()

    def request_render(self) -> None:
        with self.event_lock:
            if not self.active.is_set() or self.render_pending:
                return
            self.render_pending = True
        try:
            self.events.put_nowait(("render", None))
        except queue.Full:
            with self.event_lock:
                self.render_pending = False

    def enqueue_input(self, data: str) -> bool:
        if not self.active.is_set():
            return True
        try:
            self.events.put_nowait(("input", data))
            return True
        except queue.Full:
            self.active.clear()
            return False

    def stop(self) -> None:
        self.active.clear()
        try:
            self.events.put_nowait(("stop", None))
        except queue.Full:
            pass

    def next_event(self) -> tuple[str, str | None]:
        event = self.events.get()
        if event[0] == "render":
            with self.event_lock:
                self.render_pending = False
        return event


def _dispose_remote_component(component: RemoteComponent) -> None:
    set_invalidate = getattr(component, "set_invalidate", None)
    if callable(set_invalidate):
        try:
            set_invalidate(None)
        except Exception:  # noqa: BLE001 - upstream ignores disposer errors  # nosec B110
            pass
    dispose = getattr(component, "dispose", None)
    if callable(dispose):
        try:
            dispose()
        except Exception:  # noqa: BLE001 - upstream ignores disposer errors  # nosec B110
            pass


def _ensure_jsonable(value: Any, what: str) -> None:
    try:
        json.dumps(value)
    except (TypeError, ValueError) as exc:
        raise TypeError(f"{what} must be JSON-serializable") from exc


class HostCallError(RuntimeError):
    """Raised when the host returns an error for an extension→host call."""

    def __init__(self, message: str, code: str | None = None):
        super().__init__(message if not code else f"{code}: {message}")
        self.code = code
        self.message = message


@dataclass(frozen=True)
class TerminalInputResult:
    """A raw-input handler's verdict on one chunk.

    It mirrors upstream's ``{consume?: boolean; data?: string}``. When
    ``data`` is not None it replaces the chunk for later handlers and for
    normal handling; an empty replacement drops the chunk.
    """

    consume: bool = False
    data: str | None = None


def _terminal_input_verdict(verdict: Any) -> tuple[bool, str | None]:
    """Read (consume, data) from a TerminalInputResult or a plain mapping."""
    if isinstance(verdict, TerminalInputResult):
        return verdict.consume, verdict.data
    if isinstance(verdict, dict):
        data = verdict.get("data")
        return bool(verdict.get("consume")), None if data is None else str(data)
    return False, None


_model_stream_transport = ContextVar("model_stream_transport", default=None)


def _forward_model_stream(stream, stopped, emit):
    # pig additive (D19): only SDK-owned waits observe transport shutdown; user iterator overrides remain callable.
    token = _model_stream_transport.set((stream, stopped))
    try:
        for event in stream.events():
            emit(event)
    finally:
        _model_stream_transport.reset(token)


class ModelEventStream:
    def __init__(self) -> None:
        self._condition = threading.Condition()
        self._events: deque[dict[str, Any]] = deque()
        self._terminal = False
        self._result: dict[str, Any] | None = None
        self._started = threading.Event()
        self._start_error = None

    def mark_started(self, error=None):
        with self._condition:
            if not self._started.is_set():
                self._start_error = error
                self._started.set()

    def push(self, event: dict[str, Any]) -> None:
        with self._condition:
            if self._terminal:
                return
            self._events.append(event)
            if event.get("type") in {"done", "error"}:
                self._terminal = True
                self._result = event.get("message") if event.get("type") == "done" else event.get("error")
            self._condition.notify_all()

    def end(self, result: dict[str, Any] | None) -> None:
        with self._condition:
            if self._terminal:
                return
            self._terminal = True
            self._result = result
            self._condition.notify_all()

    def events(self):
        """Yield ordered events; transport shutdown releases only the SDK forwarding wait."""
        scope = _model_stream_transport.get()
        stopped = scope[1] if scope is not None and scope[0] is self else None
        return self._events_until(stopped)

    def _events_until(self, stopped):
        unsubscribe = stopped.subscribe(self._wake_transport) if stopped is not None else lambda: None
        try:
            while True:
                with self._condition:
                    while not self._events and not self._terminal:
                        self._check_transport(stopped)
                        self._condition.wait()
                    self._check_transport(stopped)
                    if self._events:
                        event = self._events.popleft()
                    else:
                        return
                yield event
        finally:
            unsubscribe()

    @staticmethod
    def _check_transport(stopped):
        if stopped is not None and stopped.is_set():
            raise RuntimeError("Provider connection closed")

    def _wake_transport(self):
        with self._condition:
            self._condition.notify_all()

    def result(self) -> dict[str, Any] | None:
        with self._condition:
            while not self._terminal:
                self._condition.wait()
            return self._result


def _model_stream_error_event(error: Exception, model: dict[str, Any]) -> dict[str, Any]:
    provider = model.get("provider", "")
    if isinstance(provider, dict):
        provider = provider.get("id", "")
    model_id = model.get("modelId") or model.get("id", "")
    return {
        "type": "error", "reason": "error",
        "error": {
            "role": "assistant", "content": [], "api": model.get("api", ""),
            "provider": provider, "model": model_id,
            "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
                      "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
            "stopReason": "error", "errorMessage": str(error), "timestamp": int(time.time() * 1000),
        },
    }


class ModelRegistry:
    """Session model discovery, request authentication, and model operations."""
    def __init__(self, context: "Context") -> None:
        self._context = context

    def _state(self) -> dict[str, Any]:
        return self._context._call("getModelRegistryState").get("result")

    def get_all(self) -> list[dict[str, Any]]:
        return self._state()["models"]

    def get_available(self) -> list[dict[str, Any]]:
        state = self._state()
        return [model for model in state["models"] if state["providers"].get(model["provider"], {}).get("configured", False) and ("availableModelIds" not in state["providers"][model["provider"]] or model["id"] in state["providers"][model["provider"]]["availableModelIds"])]

    def get_error(self) -> str | None:
        return self._state().get("error")

    def get_models_of_type(self, model_type: str, provider: str | None = None) -> list[dict[str, Any]]:
        """Every known model of a type (``chat``, ``image`` or ``classifier``), optionally for one provider (``getModelsOfType``).

        Chat models come from the registry state's ``models`` and the others from its ``typedModels``, in the host's order.
        """
        state = self._state()
        return [
            model for model in [*state["models"], *state.get("typedModels", [])]
            if (model.get("type") or "chat") == model_type and (provider is None or model.get("provider") == provider)
        ]

    def get_model_of_type(self, model_type: str, provider: str, model_id: str) -> dict[str, Any] | None:
        """One model of a type by provider and id, or None (``getModelOfType``)."""
        return next((model for model in self.get_models_of_type(model_type, provider) if model.get("id") == model_id), None)

    def find_of_type(self, model_type: str, provider: str, model_id: str) -> dict[str, Any] | None:
        """One model of a type by provider and id, or None (``findOfType``)."""
        return self.get_model_of_type(model_type, provider, model_id)

    def get_available_of_type(self, model_type: str, provider: str | None = None) -> list[dict[str, Any]]:
        """The models of a type whose provider has working credentials. It awaits the host (``getAvailableOfType``)."""
        args: dict[str, Any] = {"type": model_type}
        if provider is not None:
            args["provider"] = provider
        return self._context._call("getAvailableOfType", args).get("result")

    def classify(self, model: dict[str, Any], context: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any]:
        """Classify structured state with request-time authentication (``classify``).

        It never raises: a failure, and a cancelled request, are a result with ``stopReason`` ``error`` or ``aborted`` that names the model.
        ``context`` is ``{"state", "questions"}``; the questions and the answers keep their order.
        """
        args: dict[str, Any] = {"model": model, "context": context}
        if options is not None:
            args["options"] = options
        try:
            return self._context._call("classify", args).get("result")
        except Exception as error:  # noqa: BLE001 - classify reports every failure as a result
            return {
                "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"), "answers": {},
                "stopReason": "aborted" if self._context._cancelled.is_set() else "error", "errorMessage": str(error), "timestamp": int(time.time() * 1000),
            }

    def register_virtual_model(self, model: "VirtualModel") -> None:
        """Register a virtual model, as :meth:`Context.register_virtual_model` does (``registerVirtualModel``)."""
        self._context.register_virtual_model(model)

    def unregister_virtual_model(self, provider: str, model_id: str) -> None:
        """Remove a virtual model (``unregisterVirtualModel``)."""
        self._context.unregister_virtual_model(provider, model_id)

    def has_configured_auth(self, model: dict[str, Any]) -> bool:
        return self._state()["providers"].get(model["provider"], {}).get("configured", False)

    def is_using_oauth(self, model: dict[str, Any]) -> bool:
        return self._state()["providers"].get(model["provider"], {}).get("usingOAuth", False)

    def get_provider_auth_status(self, provider: str) -> dict[str, Any]:
        return self._state()["providers"].get(provider, {}).get("authStatus", {"configured": False})

    def get_provider_display_name(self, provider: str) -> str:
        return self._state()["providers"].get(provider, {}).get("name", provider)

    def get_provider_auth(self, provider: str) -> dict[str, Any] | None:
        return self._context._call("getProviderAuth", {"provider": provider}).get("result")

    def get_api_key_for_provider(self, provider: str) -> str | None:
        try:
            auth = self.get_provider_auth(provider)
            return auth["auth"].get("apiKey") if auth else None
        except Exception:
            # Pi's getApiKeyForProvider deliberately catches auth resolution failures.
            return None

    def get_registered_provider_ids(self) -> list[str]:
        return [entry["name"] for entry in self._state()["registered"]]

    def get_registered_provider_config(self, provider: str) -> dict[str, Any] | None:
        return next((entry.get("config") for entry in self._state()["registered"] if entry["name"] == provider), None)

    def get_registered_native_provider(self, provider: str) -> Provider | None:
        for entry in self._state()["registered"]:
            if entry["name"] != provider or not entry.get("native"):
                continue
            declaration = entry["native"]
            extension = self._context.extension
            with extension._provider_lock:
                local = extension._native_providers.get(declaration["key"])
                if local is not None:
                    return local
                key = declaration["id"]
                cached = extension._remote_providers.get(key)
                if cached is None or cached._provider_handle != declaration["handle"]:
                    extension._remote_providers[key] = _remote_provider(self._context, declaration)
                return extension._remote_providers[key]
        return None

    # pig divergence (D78): builtin/composed Provider methods still need a native SDK carrier.
    def get_provider(self, provider: str) -> Provider | None:
        native = self.get_registered_native_provider(provider)
        if native is not None:
            return native
        if provider in self._state().get("providers", {}):
            raise RuntimeError("builtin/composed Provider object carrier is unavailable (D78)")
        return None

    def register_native_provider(self, provider: Provider) -> None:
        declaration = _register_native(self._context.extension, provider)
        self._context._call("registerProvider", {"name": provider.id, "config": {}, "native": declaration})

    def register_provider(self, name: str | Provider, config: dict[str, Any] | None = None) -> None:
        """Apply a provider config at once, as Pi's pi.registerProvider does after the factory finished (types.ts:1766-1803, runner.ts:517-523). The config carries its callables (streamSimple, images, classifiers) as a factory registration does: they stay in the extension and the call names them. A registration the host refuses leaves the callables the provider had."""
        if isinstance(name, Provider):
            self.register_native_provider(name)
            return
        self._context.extension._register_provider_now(name, config, self._context._call)

    def unregister_provider(self, name: str) -> None:
        """Remove a provider and, once the host removed it, drop the callables the extension held for it."""
        self._context.extension._unregister_provider_now(name, self._context._call)

    def refresh(self, options: dict[str, Any] | None = None) -> dict[str, Any]:
        result = self._context._call("refreshModelRegistry", options or {}).get("result")
        return {"aborted": result["aborted"], "errors": result["errors"]}

    def find(self, provider_id: str, model_id: str) -> dict[str, Any] | None:
        model = self._context._call("getModel", {"provider": provider_id, "modelId": model_id}).get("result")
        return model if isinstance(model, dict) else None

    def get_api_key_and_headers(self, model: dict[str, Any]) -> dict[str, Any]:
        provider = model.get("provider", "")
        if isinstance(provider, dict):
            provider = provider.get("id", "")
        model_id = model.get("modelId") or model.get("id", "")
        return self._context._call("getModelAuth", {"provider": provider, "modelId": model_id}).get("result") or {}

    def stream(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> ModelEventStream:
        return self._stream(model, request, options, False)

    def _stream(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None, simple: bool) -> ModelEventStream:
        stream = ModelEventStream()
        extension = self._context.extension
        with extension._model_stream_lock:
            extension._model_stream_seq += 1
            stream_id = f"model-stream-{extension._model_stream_seq}"
            extension._model_streams[stream_id] = stream
        payload = dict(request)
        payload.update(options or {})

        def run() -> None:
            try:
                self._context._call("modelStream", {"streamId": stream_id, "model": model, "request": payload, "simple": simple})
            except Exception as exc:  # noqa: BLE001 - transport failure becomes terminal model error
                stream.push(_model_stream_error_event(exc, model))
            finally:
                with extension._model_stream_lock:
                    extension._model_streams.pop(stream_id, None)

        threading.Thread(target=run, daemon=True).start()
        return stream

    def stream_simple(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> ModelEventStream:
        return self._stream(model, request, options, True)

    def complete(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any] | None:
        return self.stream(model, request, options).result()


_THINKING_BORDER_TOKENS = {
    "off": "thinkingOff",
    "minimal": "thinkingMinimal",
    "low": "thinkingLow",
    "medium": "thinkingMedium",
    "high": "thinkingHigh",
    "xhigh": "thinkingXhigh",
    "max": "thinkingMax",
}


def _theme_text(text: Any) -> str:
    return "" if text is None else str(text)


_FAINT_SGR = "\x1b[2m"


class Theme:
    """Upstream Theme (``ctx.ui.theme``) over the host's active palette.

    The host replicates its active theme with the state snapshot and pushes
    ``theme_change`` when it changes; one instance per extension follows
    those updates, so a held reference stays current. Its semantics are the
    Node runtime's ThemeShim: a token the palette lacks leaves ``fg``/``bg``
    text unstyled, and ``bold`` and the other modifiers draw only when the
    host's styles do (upstream's chalk drops them without color support).
    """

    name: str | None
    source_path: str | None

    def __init__(self) -> None:
        self.name = None
        self.source_path = None
        self._foregrounds: dict[str, str] = {}
        self._backgrounds: dict[str, str] = {}
        self._modifiers = True
        self._mode: str | None = None
        self._appearance: str | None = None
        self._colors: dict[str, dict[str, Any]] = {}

    def _set_palette(self, palette: Any) -> None:
        palette = palette if isinstance(palette, dict) else {}
        foregrounds = palette.get("foregrounds")
        backgrounds = palette.get("backgrounds")
        self._foregrounds = dict(foregrounds) if isinstance(foregrounds, dict) else {}
        self._backgrounds = dict(backgrounds) if isinstance(backgrounds, dict) else {}
        self._modifiers = palette.get("modifiers") is not False
        name = palette.get("name")
        self.name = name if isinstance(name, str) else None
        source_path = palette.get("sourcePath")
        self.source_path = source_path if isinstance(source_path, str) and source_path != "" else None
        self._mode = "256color" if palette.get("mode") == "256color" else "truecolor"
        appearance = palette.get("appearance")
        self._appearance = appearance if appearance in ("light", "dark") else None
        colors = palette.get("colors")
        parsed = {token: parse_color(value) for token, value in colors.items()} if isinstance(colors, dict) else {}
        self._colors = {token: color for token, color in parsed.items() if color is not None}

    def _modifier(self, open_seq: str, close_seq: str, text: Any) -> str:
        value = _theme_text(text)
        return f"{open_seq}{value}{close_seq}" if self._modifiers else value

    def fg(self, token: str, text: Any) -> str:
        value = _theme_text(text)
        open_seq = self._foregrounds.get(token) or ""
        if not open_seq:
            return value
        # The host opens a faint token with SGR 2 (theme.ts:399-402); Pi's fg closes it with SGR 22;39 (theme.ts:363).
        close_seq = "\x1b[22;39m" if open_seq.endswith("\x1b[2m") else "\x1b[39m"
        return f"{open_seq}{value}{close_seq}"

    def bg(self, token: str, text: Any) -> str:
        value = _theme_text(text)
        open_seq = self._backgrounds.get(token) or ""
        return f"{open_seq}{value}\x1b[49m" if open_seq else value

    def bold(self, text: Any) -> str:
        return self._modifier("\x1b[1m", "\x1b[22m", text)

    def italic(self, text: Any) -> str:
        return self._modifier("\x1b[3m", "\x1b[23m", text)

    def underline(self, text: Any) -> str:
        return self._modifier("\x1b[4m", "\x1b[24m", text)

    def inverse(self, text: Any) -> str:
        return self._modifier("\x1b[7m", "\x1b[27m", text)

    def strikethrough(self, text: Any) -> str:
        return self._modifier("\x1b[9m", "\x1b[29m", text)

    def get_fg_ansi(self, token: str) -> str:
        ansi = self._foregrounds.get(token)
        if not ansi:
            raise ValueError(f"Unknown theme color: {token}")
        return ansi

    def get_bg_ansi(self, token: str) -> str:
        ansi = self._backgrounds.get(token)
        if not ansi:
            raise ValueError(f"Unknown theme background color: {token}")
        return ansi

    @property
    def appearance(self) -> str | None:
        """The background the theme is designed for, ``"light"`` or ``"dark"``, or ``None`` before the host has sent a palette. The host resolves it with the palette (upstream ``Theme.appearance``)."""
        return self._appearance

    @property
    def colors(self) -> dict[str, dict[str, Any]]:
        """A concrete color for every theme token (upstream ``Theme.colors``): ``{"kind": "indexed", "index"}``, ``{"kind": "rgb", "r", "g", "b"}`` or ``{"kind": "oklch", "l", "c", "h"}``. The host resolves them with the palette; the dict is a copy."""
        return copy.deepcopy(self._colors)

    def style(self, text: Any, options: dict[str, Any]) -> str:
        """Render ``text`` in ``options`` (upstream ``Theme.style``): ``fg`` and ``bg`` are a theme token, accepted only in its own slot, or a color from :attr:`colors`; ``bold``, ``dim``, ``italic``, ``underline``, ``inverse`` and ``strikethrough`` are attributes, drawn whatever the host's chalk level is. An unknown token raises ``ValueError("Unknown theme color: <token>")``."""
        attributes = dict(options)
        fg_ansi = self._slot_ansi(options.get("fg"), self._foregrounds, False)
        if isinstance(options.get("fg"), str) and fg_ansi.endswith(_FAINT_SGR):
            # The palette's foreground appends SGR 2 to a faint token's color; a color never ends in it.
            fg_ansi = fg_ansi[: -len(_FAINT_SGR)]
            attributes["dim"] = True
        bg_ansi = self._slot_ansi(options.get("bg"), self._backgrounds, True)
        return style_text_with_ansi(_theme_text(text), fg_ansi, bg_ansi, attributes)

    def _slot_ansi(self, value: Any, tokens: dict[str, str], background: bool) -> str:
        if value is None:
            return ""
        if isinstance(value, str):
            ansi = tokens.get(value)
            if not ansi:
                raise ValueError(f"Unknown theme color: {value}")
            return ansi
        color = parse_color(value)
        if color is None:
            raise ValueError(f"Invalid theme color: {value!r}")
        return color_ansi(color, self.get_color_mode(), background)

    def get_color_mode(self) -> str:
        """``"truecolor"`` or ``"256color"``."""
        return self._mode or "truecolor"

    def get_thinking_border_color(self, level: str) -> Callable[[Any], str]:
        token = _THINKING_BORDER_TOKENS.get(level, "thinkingOff")
        return lambda text: self.fg(token, text)

    def get_bash_mode_border_color(self) -> Callable[[Any], str]:
        return lambda text: self.fg("bashMode", text)


# Host calls whose reply lists every registered MCP server (protocol_extension_api.go McpServersResult).
_MCP_REGISTRY_CALLS = frozenset({"registerMcpServer", "unregisterMcpServer"})


class _NestedUpdate:
    """One partial result of a nested call and the answer the host waits for."""

    def __init__(self, partial: Any) -> None:
        self.partial = partial
        self.done = threading.Event()
        self.error: Exception | None = None


class _NestedCall:
    """The partial results of one ``execute_tool`` call. The host sends each as a request and waits for the answer; the request handler queues it here and the caller's thread runs ``on_update`` for it, in order."""

    def __init__(self) -> None:
        self._cond = threading.Condition()
        self._updates: deque[_NestedUpdate] = deque()
        self._done = False
        self._closed = False

    def submit(self, partial: Any) -> Exception | None:
        """Queue one partial result and wait until ``on_update`` ran for it. Return what it raised, which is the answer to the host; an update for a call that already returned is dropped."""
        update = _NestedUpdate(partial)
        with self._cond:
            if self._closed:
                return None
            self._updates.append(update)
            self._cond.notify_all()
        update.done.wait()
        return update.error

    def finish(self) -> None:
        with self._cond:
            self._done = True
            self._cond.notify_all()

    def deliver(self, on_update: Callable[[dict[str, Any]], None] | None) -> BaseException | None:
        """Run ``on_update`` for each partial result until the call finished, and return the first error it raised. A raising callback still sees every later partial result (nested-tool-calls.ts:219-231, agent-loop.ts:833-845); each error is the answer to its own request and the first one rejects the call."""
        failure: BaseException | None = None
        while True:
            with self._cond:
                while not self._updates and not self._done:
                    self._cond.wait()
                batch = list(self._updates)
                self._updates.clear()
                done = self._done
                if done:
                    self._closed = True
            for update in batch:
                try:
                    if on_update is not None:
                        on_update(update.partial)
                except Exception as exc:  # noqa: BLE001 - the callback's throw is the answer to its request
                    update.error = exc
                    if failure is None:
                        failure = exc
                finally:
                    update.done.set()
            if done:
                return failure


class _NestedCallEvent(threading.Event):
    """The completion event of an ``executeTool`` host call: whoever completes the call (result, cancel or shutdown) also wakes the caller's update loop."""

    def __init__(self, nested: _NestedCall) -> None:
        super().__init__()
        self._nested = nested

    def set(self) -> None:
        super().set()
        self._nested.finish()


# pig additive (D19): normal completion retires the request parent while retained Contexts keep the same socket generation.
@dataclass
class _RequestParent:
    request_id: str
    socket: Any
    state: str = "active"
    finished: bool = False


def _surface_args(lines: list[str], width: int) -> dict[str, Any]:
    args: dict[str, Any] = {"lines": lines}
    if width > 0:
        args["width"] = width
    return args


class _SurfaceRenderer:
    """A footer or header renderer, the subprocess form of Pi's component factory.

    pig additive (D19): Pi's TUI renders the in-process component every frame; a
    subprocess SDK renders at the host width itself and sends the width with the rows.

    The TUI calls render(width) for every frame (interactive-mode.ts:2418-2480).
    The SDK renders at the width the host reported, sends the rows with that
    width so the host never paints them at another, and renders again after each
    width_change. Only the newest width is rendered when several arrive together.
    """

    def __init__(self, method: str, render: "Callable[[int], list[str]]") -> None:
        self.method = method
        self.render = render
        # Serializes a render with its push and orders a push against stop(), so
        # a superseded renderer never overwrites the rows that replaced it.
        self._lock = threading.Lock()
        self._stopped = False
        self._refresh_lock = threading.Lock()
        self._refresh_queued = False
        self._refresh_running = False

    def _kind(self) -> str:
        return "header" if self.method == "ui.setHeader" else "footer"

    def push(self, ctx: "Context") -> None:
        with self._lock:
            if self._stopped:
                return
            width = ctx.width
            try:
                rendered = self.render(width)
                # The Node runtime sends [] for a render result that is not an
                # array (runtime.mjs renderSpecialSurface).
                lines = [str(line) for line in rendered] if isinstance(rendered, (list, tuple)) else []
            except Exception as error:  # a failing component must not end the extension
                ctx.notify(f"{self._kind()} render failed: {error}", "error")
                return
            ctx._call(self.method, _surface_args(lines, width))

    def stop(self) -> None:
        with self._lock:
            self._stopped = True

    def refresh(self, ctx: "Context") -> None:
        with self._refresh_lock:
            self._refresh_queued = True
            if self._refresh_running:
                return
            self._refresh_running = True
        threading.Thread(target=self._refresh_loop, args=(ctx,), daemon=True).start()

    def _refresh_loop(self, ctx: "Context") -> None:
        while True:
            with self._refresh_lock:
                if not self._refresh_queued:
                    self._refresh_running = False
                    return
                self._refresh_queued = False
            try:
                self.push(ctx)
            except Exception:  # noqa: BLE001 - the next width change or set reports again  # nosec B110
                # The host connection is gone or the call failed; the next width
                # change or explicit set reports again.
                pass


class _ToolStart:
    """One tool_call request's place in the order the handlers start. ``begin`` returns when every earlier request began its handler or ended without one."""

    def __init__(self, previous: threading.Event | None) -> None:
        self._previous = previous
        self.done = threading.Event()

    def begin(self) -> None:
        if self._previous is not None:
            self._previous.wait()
        self.done.set()

    def end(self) -> None:
        """Hand the place on when the request's thread ends, also for a request that failed before it reached its handler. Like ``begin`` it waits for the earlier requests first, so a request without a handler cannot let a later handler start before an earlier one. Idempotent."""
        self.begin()

    def release(self) -> None:
        """Hand the place on at once, for a request whose thread never started. The reader calls it, so it must not wait for the earlier requests. Idempotent."""
        self.done.set()


class _ToolStartOrder:
    """Starts the handlers of tool_call requests in the order the requests arrive.

    Pi starts every call of a parallel batch through ``Promise.all(calls.map(...))`` and each reaches tool.execute synchronously, so the handlers start in source order (agent-loop.ts:619-647, 820-837). The host writes a batch's tool_call requests in source order, but each request runs on its own thread, and the scheduler is free to run a later thread first.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._tail: threading.Event | None = None

    def reserve(self) -> _ToolStart:
        """Take the next place. The reader calls this in arrival order, before it starts the request's thread."""
        with self._lock:
            start = _ToolStart(self._tail)
            self._tail = start.done
            return start


@dataclass
class Context:
    extension: "Extension"
    tool_call_id: str | None = None
    request_id: str = ""
    _cancelled: threading.Event | None = None
    _cancel_reason: str | None = None
    _reason_provider: Callable[[], str | None] | None = None
    _parent: _RequestParent | None = None
    # A tool_call request: the context has `tools` and `execute_tool` (runner.ts:952-985).
    _tool_context: bool = False
    # A tool_call request: its place in the order the handlers start.
    _tool_start: "_ToolStart | None" = None

    @property
    def session_manager(self) -> "SessionManager":
        return SessionManager(self)

    @property
    def model_registry(self) -> ModelRegistry:
        return ModelRegistry(self)

    @property
    def events(self) -> EventBus:
        """Upstream's ``pi.events`` for calls made from this request."""
        return EventBus(self.extension, self)

    @property
    def signal(self) -> ProviderSignal | None:
        """Upstream's ``ctx.signal``: the cancellation of the run in progress, or None while no run is active.

        Every read during one run returns the same signal, and aborting the run sets it, including for a handler
        still in flight. It is the run's, not the request's: ``is_cancelled()`` reports the request.
        """
        with self.extension._run_signal_lock:
            return None if self.extension._run_signal is None else self.extension._run_signal[1]

    def is_cancelled(self) -> bool:
        if self._parent is not None:
            with self.extension._state_lock:
                if self._parent.state == "completed":
                    return self.extension._shutdown.is_set() or self._parent.socket is not self.extension._sock
        return bool(self._cancelled and self._cancelled.is_set())

    def on_update(self, partial: Any) -> None:
        """Stream a partial result of the running tool, as upstream's onUpdate does.

        ``partial`` is a string or a mapping with the tool-result shape. The
        host shows updates in order, before the tool's final result.
        """
        if not self.tool_call_id or not self.request_id:
            raise RuntimeError("on_update is only available while a tool runs")
        result = {"content": partial} if isinstance(partial, str) else partial
        self.extension._notify("tool_update", {"request_id": self.request_id, "result": result})

    def cancellation_reason(self) -> str | None:
        if self._reason_provider is not None:
            try:
                latest = self._reason_provider()
            except Exception:  # noqa: BLE001 - reason lookup must never raise
                latest = None
            if latest:
                return latest
        return self._cancel_reason

    # Low-level escape hatch -------------------------------------------------

    def _report_request_state(self, state: str, reason: str | None = None) -> None:
        self.extension._request_state(self.request_id, state, reason, self._parent)

    def _call(self, method: str, args: Any = None) -> dict[str, Any]:
        if method not in {"ui.select", "ui.confirm", "ui.input", "ui.editor", "ui.custom"} and self.request_id:
            self._report_request_state("blocked", "host_call")
        try:
            if self._parent is not None:
                return self.extension._call(method, args, self.request_id, self._parent)
            return self.extension._call(method, args, self.request_id)
        finally:
            if self.request_id:
                self._report_request_state("progress")

    def _block_for_user(self) -> None:
        if self.request_id:
            self._report_request_state("blocked", "user")

    def register_tool(self, definition: ToolDefinition) -> None:
        """Register a model-callable tool and wait for the host registry refresh."""
        self.extension.register_tool(definition)

    def call_host(self, method: str, args: Any = None) -> Any:
        return self._call(method, args).get("result")

    # Upstream 0.99.1 extension API ------------------------------------------

    def register_mcp_server(self, name: str, config: dict[str, Any]) -> None:
        """Register an MCP server, as ``pi.registerMcpServer`` does."""
        self.extension._register_mcp_server(name, config, self._call)

    def unregister_mcp_server(self, name: str) -> None:
        """Remove an MCP server this extension registered."""
        self.extension._unregister_mcp_server(name, self._call)

    def get_mcp_servers(self) -> list[dict[str, Any]]:
        """Every registered MCP server, as ``pi.getMcpServers()`` returns them.

        Each entry is ``{"name", "config", "extensionPath"}``. The list is the
        registry as of the host's last state push or this extension's last
        registration call.
        """
        return self.extension._get_mcp_servers()

    def register_virtual_model(self, model: VirtualModel) -> None:
        """Register a virtual model, as ``pi.registerVirtualModel`` does."""
        self.extension._register_virtual_model(model, self._call)

    def unregister_virtual_model(self, provider: str, model_id: str) -> None:
        """Remove a virtual model registered with :meth:`register_virtual_model`."""
        self.extension._unregister_virtual_model(provider, model_id, self._call)

    def get_settings(self) -> dict[str, Any]:
        """The effective settings, as ``pi.getSettings()`` returns them.

        Raises ``RuntimeError`` until the host has sent them, as upstream's
        ``getSettings`` throws before the runner binds.
        """
        return self.extension._get_settings()

    @property
    def tools(self) -> list[dict[str, Any]]:
        """The tools :meth:`execute_tool` can call, as AgentTool dicts. Only available while a tool runs."""
        self._require_tool_context("tools")
        return list(self._reply_field("getCallableTools", "tools"))

    def execute_tool(self, name: str, args: Any, options: ExecuteToolOptions | None = None) -> dict[str, Any]:
        """Run another tool for the calling tool, as ``ctx.executeTool`` does.

        The call gets the id ``<calling id>/<n>``; its ``tool_call``,
        ``tool_result`` and ``tool_execution_*`` events carry
        ``parentToolCallId``. Returns upstream's AgentToolCallOutcome as a dict
        (``toolCall``, ``result``, ``isError``). Tool failures come back with
        ``isError`` true rather than raising; a transport failure or the
        cancellation of the calling request raises. ``options.signal`` defaults
        to the calling tool's cancellation, and a given signal replaces it, as
        upstream's ``options.signal ?? signal`` does; ``options.on_update`` runs on this
        thread, in order, before the call returns. Only available while a tool
        runs.
        """
        self._require_tool_context("execute_tool")
        return self.extension._execute_tool(self, name, args, options or ExecuteToolOptions())

    def _require_tool_context(self, what: str) -> None:
        if not self._tool_context or not self.tool_call_id or not self.request_id:
            raise RuntimeError(f"{what} is only available while a tool runs")



    # Notifications/status ---------------------------------------------------

    def notify(self, message: str, level: str = "info") -> None:
        self._call("ui.notify", {"message": message, "level": level})

    def set_status(self, key: str, text: str) -> None:
        self._call("ui.setStatus", {"key": key, "text": text})

    def set_working_message(self, message: str) -> None:
        self._call("ui.setWorkingMessage", {"message": message})

    def set_working_visible(self, visible: bool) -> None:
        self._call("ui.setWorkingVisible", {"visible": visible})

    def set_working_indicator(self, options: dict[str, Any]) -> None:
        self._call("ui.setWorkingIndicator", options)

    def set_hidden_thinking_label(self, label: str) -> None:
        self._call("ui.setHiddenThinkingLabel", {"label": label})

    def set_title(self, title: str) -> None:
        self._call("ui.setTitle", {"title": title})

    # User interaction -------------------------------------------------------

    # ``timeout`` is upstream ExtensionUIDialogOptions.timeout: milliseconds
    # after which the dialog dismisses itself, answering as a cancel does.

    @staticmethod
    def _dialog_args(args: dict[str, Any], timeout: float | None) -> dict[str, Any]:
        if timeout is not None:
            args["opts"] = {"timeout": timeout}
        return args

    def select(self, title: str, options: list[str], *, timeout: float | None = None) -> tuple[str, bool]:
        self._block_for_user()
        args = self._dialog_args({"title": title, "options": options}, timeout)
        result = self._call("ui.select", args).get("result") or {}
        return str(result.get("selected") or ""), bool(result.get("ok"))

    def confirm(self, title: str, message: str, *, timeout: float | None = None) -> bool:
        self._block_for_user()
        args = self._dialog_args({"title": title, "message": message}, timeout)
        result = self._call("ui.confirm", args).get("result") or {}
        return bool(result.get("confirmed"))

    def input(self, title: str, placeholder: str = "", *, timeout: float | None = None) -> tuple[str, bool]:
        self._block_for_user()
        args = self._dialog_args({"title": title, "placeholder": placeholder}, timeout)
        result = self._call("ui.input", args).get("result") or {}
        return str(result.get("text") or ""), bool(result.get("ok"))

    def editor(self, title: str, prefill: str = "") -> tuple[str, bool]:
        self._block_for_user()
        result = self._call("ui.editor", {"title": title, "prefill": prefill}).get("result") or {}
        return str(result.get("text") or ""), bool(result.get("ok"))

    # Message injection ------------------------------------------------------

    def send_message(self, custom_type: str, content: Any, display: bool = True, trigger_turn: bool | None = None, deliver_as: str | None = None, *, details: Any = None) -> None:
        """Inject a custom message. ``content`` is a string or a list of
        text/image content blocks. ``trigger_turn`` and ``deliver_as`` are
        optional, as upstream's are: None is sent as unset and the host applies
        upstream's default for the session's state. ``details`` is upstream's
        message ``details``, extension data kept with the message and not sent
        to the model; None leaves it unset."""
        options: dict[str, Any] = {}
        if trigger_turn is not None:
            options["triggerTurn"] = trigger_turn
        if deliver_as:
            options["deliverAs"] = deliver_as
        message: dict[str, Any] = {"customType": custom_type, "content": content, "display": display}
        if details is not None:
            _ensure_jsonable(details, "send_message details")
            message["details"] = details
        self._call("sendMessage", {"message": message, "options": options})

    def send_user_message(self, content: str | list[dict[str, Any]], deliver_as: str = "followUp") -> None:
        """Send text or a list of text/image content blocks as a user message."""
        self._call("sendUserMessage", {"content": content, "options": {"deliverAs": deliver_as}})

    def scoped_models(self) -> list[dict[str, Any]]:
        """Return the resolved session scope in selection order."""
        return self._call("getScopedModels", {}).get("result") or []

    def append_entry(self, custom_type: str, data: Any) -> None:
        self._call("appendEntry", {"customType": custom_type, "data": data})

    # Editor/session/model state -------------------------------------------

    @property
    def width(self) -> int:
        with self.extension._state_lock:
            return self.extension._width

    @property
    def height(self) -> int:
        """Terminal height in rows, or 0 when the host has not reported one.

        Updated by ``height_change`` notifications.
        """
        with self.extension._state_lock:
            return self.extension._height

    @property
    def model(self) -> str:
        with self.extension._state_lock:
            return self.extension._model

    @property
    def cwd(self) -> str:
        with self.extension._state_lock:
            return self.extension._cwd

    @property
    def mode(self) -> str:
        """Run mode: "tui", "rpc", "json", or "print". Guard terminal-only
        UI on "tui". Defaults to "print" when unspecified."""
        with self.extension._state_lock:
            return self.extension._mode or "print"

    @property
    def session_name(self) -> str:
        with self.extension._state_lock:
            return self.extension._session_name

    @property
    def config_home(self) -> str:
        return os.environ.get("PIG_HOME") or os.path.join(os.path.expanduser("~"), ".pig")

    def _reply_field(self, method: str, field: str, args: Any = None) -> Any:
        """One field every reply of ``method`` carries; a host failure raises and a missing field is a protocol error, never an empty value."""
        result = self._call(method, args).get("result")
        value = result.get(field) if isinstance(result, dict) else None
        if value is None:
            raise HostCallError(f"host reply to {method} has no {field!r}", "invalid_reply")
        return value

    def _optional_string_field(self, method: str, field: str) -> str | None:
        """Pi's ``string | undefined`` getters carry an empty string for absent state on the wire."""
        result = self._call(method).get("result")
        value = result.get(field) if isinstance(result, dict) else None
        return value if isinstance(value, str) and value else None

    def get_editor_text(self) -> str:
        return self._reply_field("ui.getEditorText", "text")

    def set_editor_text(self, text: str) -> None:
        self._call("ui.setEditorText", {"text": text})

    def paste_to_editor(self, text: str) -> None:
        self._call("ui.pasteToEditor", {"text": text})

    def get_session_name(self) -> str | None:
        """The session name, or None when the session has no name (Pi's ``string | undefined``)."""
        return self._optional_string_field("getSessionName", "name")

    def set_session_name(self, name: str) -> None:
        self._call("setSessionName", {"name": name})

    def set_label(self, entry_id: str, label: str) -> None:
        self._call("setLabel", {"entryId": entry_id, "label": label})

    def get_flag(self, name: str) -> Any:
        """Read the host value or first registered default, preserving falsy overrides."""
        result = self._call("getFlag", {"name": name}).get("result") or {}
        if result.get("value") is not None:
            return result["value"]
        return self.extension._flag_defaults.get(name)

    def get_thinking_level(self) -> str:
        return self._reply_field("getThinkingLevel", "level")

    def set_thinking_level(self, level: str) -> None:
        self._call("setThinkingLevel", {"level": level})

    def set_model(self, model: str) -> tuple[bool, str]:
        result = self._call("setModel", {"model": model}).get("result") or {}
        return bool(result.get("success", result.get("ok", False))), str(result.get("error") or "")

    # Tools/commands/context -------------------------------------------------

    def get_active_tools(self) -> list[str]:
        return list(self._reply_field("getActiveTools", "tools"))

    def get_all_tools(self) -> list[dict[str, Any]]:
        """Every tool in the session's registry, active or not, as upstream's
        ``pi.getAllTools()`` returns them: ``ToolInfo`` dicts with ``name``,
        ``description``, ``parameters``, ``promptGuidelines`` (when set),
        ``exposure`` (always present upstream, ``"direct"`` by default),
        ``namespace`` and ``annotations`` (when set), and ``sourceInfo``.
        Built-in tools come first."""
        return list(self._reply_field("getAllTools", "tools"))

    def set_active_tools(self, tools: list[str]) -> None:
        self._call("setActiveTools", {"tools": tools})

    def refresh_tools(self) -> None:
        self._call("refreshTools")

    def get_commands(self) -> list[dict[str, Any]]:
        """The session's extension commands, prompt templates and skills, as
        upstream's ``pi.getCommands()`` returns them: ``SlashCommandInfo``
        dicts with ``name``, ``description`` (when set), ``source`` and
        ``sourceInfo``."""
        return list(self._reply_field("getCommands", "commands"))

    def get_context_usage(self) -> dict[str, Any] | None:
        result = self._call("getContextUsage").get("result")
        return result if isinstance(result, dict) and result else None

    def get_system_prompt(self) -> str:
        return self._reply_field("getSystemPrompt", "prompt")

    def get_system_prompt_options(self) -> dict[str, Any]:
        """Base inputs pi currently uses to build the system prompt
        (customPrompt, selectedTools, toolSnippets, promptGuidelines,
        appendSystemPrompt, cwd, contextFiles, skills). Reports current
        base inputs only, not per-turn before_agent_start changes. May
        include full context-file contents; treat as sensitive."""
        result = self._call("getSystemPromptOptions").get("result")
        if not isinstance(result, dict):
            raise HostCallError("host reply to getSystemPromptOptions is not an object", "invalid_reply")
        return result

    def get_model_info(self) -> dict[str, Any] | None:
        result = self._call("getModelInfo").get("result")
        return result if isinstance(result, dict) and result.get("id") else None

    def get_branch(self) -> list[dict[str, Any]]:
        self.extension._ensure_session_log()
        return self.extension._session_mirror.get_branch()

    def get_entries(self) -> list[dict[str, Any]]:
        self.extension._ensure_session_log()
        return self.extension._session_mirror.get_entries()

    def get_model_auth(self, provider_id: str, model_id: str) -> Any:
        return self._call("getModelAuth", {"provider": provider_id, "modelId": model_id}).get("result")

    def complete(self, model: dict[str, Any], request: dict[str, Any], auth: dict[str, Any]) -> Any:
        return self._call("complete", {"model": model, "request": request, "auth": auth}).get("result")

    # Theme/widgets/advanced UI ---------------------------------------------

    @property
    def theme(self) -> Theme:
        """Upstream ``ctx.ui.theme``: the host's active theme, kept current."""
        return self.extension._theme

    def has_ui(self) -> bool:
        """Whether the host binds a UI context: interactive and RPC, not print/JSON."""
        with self.extension._state_lock:
            return self.extension._has_ui

    def get_all_themes(self) -> list[dict[str, Any]]:
        return list(self._reply_field("ui.getAllThemes", "themes"))

    def get_theme(self, name: str) -> Any:
        return (self._call("ui.getTheme", {"name": name}).get("result") or {}).get("theme")

    def set_theme(self, name: str) -> tuple[bool, str]:
        result = self._call("ui.setTheme", {"theme": name}).get("result") or {}
        return bool(result.get("success", result.get("ok", False))), str(result.get("error") or "")

    def set_widget(self, key: str, content: Any, options: dict[str, Any] | None = None) -> None:
        """Set or clear a widget.

        A list of strings is content the host lays out as Pi does for
        ``ctx.ui.setWidget(key, string[])``: each entry becomes ``Text(line, 1,
        0)`` at the host's width, the first ten entries are shown, and a muted
        "... (widget truncated)" row follows a longer list
        (interactive-mode.ts:2321-2336). A row wider than the pane wraps.

        A string list with no options goes out as a ``widget_push`` frame
        without a width, which the host applies in arrival order and never
        answers: Pi's setWidget returns nothing, so it may be called from an
        :meth:`on_width_change` handler. Every other shape waits for the host.
        """
        if isinstance(content, list) and all(isinstance(x, str) for x in content) and options is None:
            self.extension._push_widget(key, content)
            return
        self._call("ui.setWidget", {"key": key, "content": content, "options": options or {}})

    def set_footer(self, lines: list[str] | None) -> None:
        """Replace the footer with pre-rendered lines, or clear it for None.

        Pi renders a footer component at the current width every frame, so it
        never paints rows laid out for another width. The rows sent here carry
        the width the SDK holds when they are sent, and the host paints them only
        at that width: after a resize they stay hidden until the extension sends
        rows for the new width (see :meth:`on_width_change` for where to send
        them from). A footer that must never be wrong renders through
        :meth:`set_footer_renderer`. Setting rows replaces any footer renderer.
        """
        self._set_surface("ui.setFooter", lines)

    def clear_footer(self) -> None:
        self._set_surface("ui.setFooter", None)

    def set_footer_renderer(self, render: "Callable[[int], list[str]] | None") -> None:
        """Install a footer that ``render`` lays out at the host's terminal width.

        This is the subprocess form of the component factory Pi's
        ``ctx.ui.setFooter`` takes. The SDK renders at the width the host reports
        and again after every width change, and sends each set of rows with the
        width it was rendered for, so the host never paints rows laid out for
        another width. ``None`` restores the built-in footer. An exception in
        ``render`` is reported to the user as "footer render failed: ..." and
        leaves the previous rows.
        """
        self._set_surface_renderer("ui.setFooter", render)

    def set_login(self, definition: LoginDefinition) -> None:
        """Set the shared header from a native login definition.

        Pig validates the canonical definition. A rejected definition raises
        :class:`HostCallError` and leaves the current header unchanged.
        """
        self._call("ui.setLogin", definition._to_wire())

    def set_header(self, lines: list[str] | None) -> None:
        """Replace the header with pre-rendered lines, or clear it for None.

        As with :meth:`set_footer`, the rows carry the width the SDK holds when
        they are sent.
        """
        self._set_surface("ui.setHeader", lines)

    def clear_header(self) -> None:
        self._set_surface("ui.setHeader", None)

    def set_header_renderer(self, render: "Callable[[int], list[str]] | None") -> None:
        """Install a header that ``render`` lays out at the host's terminal width.

        It follows the contract of :meth:`set_footer_renderer`.
        """
        self._set_surface_renderer("ui.setHeader", render)

    def _set_surface(self, method: str, lines: "list[str] | None") -> None:
        self.extension._replace_surface(method, None)
        if lines is None:
            self._call(method, {"clear": True})
            return
        self._call(method, _surface_args([str(line) for line in lines], self.width))

    def _set_surface_renderer(self, method: str, render: "Callable[[int], list[str]] | None") -> None:
        if render is None:
            self._set_surface(method, None)
            return
        if not callable(render):
            raise TypeError("a surface renderer must be callable")
        surface = _SurfaceRenderer(method, render)
        self.extension._replace_surface(method, surface)
        # The initial push belongs to the caller's request, as the Go SDK's does; later
        # width-change pushes have no parent and use a fresh Context.
        surface.push(self)

    def clear_editor_component(self) -> None:
        self._call("ui.setEditorComponent", {"clear": True})

    def custom(
        self,
        component: RemoteComponent | dict[str, Any] | None = None,
        options: dict[str, Any] | None = None,
    ) -> Any:
        """Open a focused subprocess component, or return None without callbacks when no UI is bound.

        Passing the legacy options-only shape retains the host's explicit
        unsupported response because it has no serializable component.
        """
        if not self.has_ui():
            return None
        self._block_for_user()
        if component is None or isinstance(component, dict):
            raw_options = component if isinstance(component, dict) else options
            return self._call("ui.custom", raw_options or {}).get("result")
        result = self.extension._run_remote_component(component, options or {}, self.request_id, self._parent)
        if self.request_id:
            self.extension._request_state(self.request_id, "progress")
        return result

    def add_autocomplete_provider(self, factory: AutocompleteProviderFactory | None = None) -> None:
        """Append a retained factory and wait for the rebuilt provider chain. Headless contexts ignore it."""
        self.extension._autocomplete.add(self, factory)

    def on_terminal_input(
        self, handler: "Callable[[str], TerminalInputResult | dict[str, Any] | None]"
    ) -> "Callable[[], None]":
        """Subscribe to raw terminal input, receiving every chunk before the editor.

        The host is told to start forwarding only on the first subscription and
        to stop on the last, so an extension that never subscribes costs the
        input loop nothing. With no UI, no subscription is retained. The host blocks on each verdict, so a handler must
        return promptly. Returns an idempotent unsubscribe callable.
        """
        if not self.has_ui():
            return lambda: None
        if not callable(handler):
            raise TypeError("on_terminal_input requires a callable handler")
        return self.extension._add_terminal_input_handler(handler)

    def on_width_change(self, handler: "Callable[[int], None]") -> "Callable[[], None]":
        """Subscribe to terminal resizes, receiving the new width.

        Upstream Pi installs headers and footers as component factories whose
        render(width) runs every frame, so they follow a resize with no work
        from the extension. Rows sent with :meth:`set_footer` or
        :meth:`set_header` are painted only at the width they carry, so after a
        resize they stay hidden until the extension sends rows for the new
        width; this is that trigger. :meth:`set_footer_renderer` and
        :meth:`set_header_renderer` follow a resize with no handler.

        The handler is called after ``width()`` is updated, so it observes that
        width or a newer one. Handlers run on a worker thread, one at a time in
        the order the host sent the widths, never on the thread that reads the
        host's replies, so a handler can make a blocking host call such as
        :meth:`set_footer`. A slow handler delays the later deliveries, not the
        extension's other handlers. A handler that raises is reported on stderr
        and does not stop the others. Returns an idempotent unsubscribe
        callable; a delivery already queued still reaches the handlers that
        were subscribed when the width arrived.
        """
        if not callable(handler):
            raise TypeError("on_width_change requires a callable handler")
        return self.extension._add_width_change_handler(handler)

    def get_tools_expanded(self) -> bool:
        return bool(self._reply_field("ui.getToolsExpanded", "expanded"))

    def set_tools_expanded(self, expanded: bool) -> None:
        self._call("ui.setToolsExpanded", {"expanded": expanded})

    # Session identity -------------------------------------------------------

    def get_session_id(self) -> str:
        """The current session's id, including in-memory sessions."""
        return self._reply_field("getSessionID", "sessionId")

    def get_session_file(self) -> str | None:
        """The current session file path, or None for an in-memory session (Pi's ``string | undefined``)."""
        return self._optional_string_field("getSessionFile", "sessionFile")

    def get_leaf_id(self) -> str | None:
        """The current leaf entry id, or None for an empty session (Pi's ``string | null``)."""
        return self._optional_string_field("getLeafID", "leafId")

    # Shell ------------------------------------------------------------------

    def exec(self, command: str, args: list[str] | None = None, *, timeout: float | None = None, cwd: str | None = None) -> ExecResult:
        """Run a command through the host's executor.

        ``timeout`` (milliseconds) and ``cwd`` are upstream ExecOptions; a
        command the timeout kills reports ``killed``. Raises HostCallError, a
        RuntimeError whose message is the host's, when the host reports a
        failure, such as "spawn EINVAL" for a batch file on Windows, so a
        caller sees the reason rather than an exit code of zero it never
        produced.
        """
        call_args: dict[str, Any] = {"command": command, "args": args or []}
        options: dict[str, Any] = {}
        if timeout is not None:
            options["timeout"] = timeout
        if cwd is not None:
            options["cwd"] = cwd
        if options:
            call_args["options"] = options
        result = self._call("exec", call_args).get("result") or {}
        return ExecResult(
            stdout=str(result.get("stdout") or ""),
            stderr=str(result.get("stderr") or ""),
            exit_code=int(result.get("code") or 0),
            killed=bool(result.get("killed")),
        )

    # Agent/session control --------------------------------------------------

    def is_project_trusted(self) -> bool:
        """Whether the current project is trusted.

        Untrusted projects have project-scoped settings and hooks disabled.
        A host failure raises rather than assuming trust.
        """
        return bool(self._reply_field("isProjectTrusted", "trusted"))

    def is_idle(self) -> bool:
        return bool(self._reply_field("isIdle", "idle"))

    def abort(self) -> None:
        self._call("abort")

    def has_pending_messages(self) -> bool:
        return bool(self._reply_field("hasPendingMessages", "pending"))

    def shutdown(self) -> None:
        self._call("shutdown")

    def compact(
        self,
        opts: dict[str, Any] | None = None,
        *,
        custom_instructions: str | None = None,
        on_complete: Callable[[dict[str, Any]], None] | None = None,
        on_error: Callable[[Exception], None] | None = None,
    ) -> None:
        """Start compacting the session, as upstream ``ctx.compact`` does.

        ``custom_instructions`` is upstream's ``customInstructions``. With
        ``on_complete`` or ``on_error``, this returns at once and a background
        thread waits for the outcome: ``on_complete`` receives upstream's
        CompactionResult, ``on_error`` the failure. That wait belongs to the
        extension, not to the calling handler, so it outlives the handler.
        """
        args = dict(opts or {})
        if custom_instructions is not None:
            args["customInstructions"] = custom_instructions
        if on_complete is None and on_error is None:
            self._call("compact", args)
            return
        args["awaitCompletion"] = True
        extension = self.extension

        def run() -> None:
            try:
                result = extension._call("compact", args).get("result")
            except Exception as exc:  # noqa: BLE001 - the failure goes to on_error
                if on_error is not None:
                    on_error(exc)
                return
            if on_complete is not None:
                on_complete(result if isinstance(result, dict) else {})

        threading.Thread(target=run, name="pig-compact", daemon=True).start()

    def wait_for_idle(self) -> None:
        self._call("waitForIdle")

    def new_session(self, opts: dict[str, Any] | None = None) -> Any:
        return self._call("newSession", opts or {}).get("result")

    def fork(self, entry_id: str, opts: dict[str, Any] | None = None) -> Any:
        args = dict(opts or {})
        args["entryId"] = entry_id
        return self._call("fork", args).get("result")

    def navigate_tree(self, target_id: str, opts: dict[str, Any] | None = None) -> Any:
        args = dict(opts or {})
        args["targetId"] = target_id
        return self._call("navigateTree", args).get("result")

    def switch_session(self, session_path: str, opts: dict[str, Any] | None = None) -> Any:
        args = dict(opts or {})
        args["sessionPath"] = session_path
        return self._call("switchSession", args).get("result")

    def reload(self) -> None:
        self._call("reload")


_OAUTH_METHODS = frozenset(
    {
        "oauth_login",
        "oauth_refresh",
        "oauth_get_api_key",
        "oauth_credential_status",
        "oauth_store_credentials",
        "oauth_delete_credentials",
    }
)


@dataclass
class OAuthCredentials:
    """Pi's OAuth token object (``packages/ai/src/auth/types.ts`` ``OAuthCredentials``).

    ``expires`` holds Pi's exact value: an int or a float, including a fraction. A value that Pi's
    declared number type excludes round-trips unchanged. ``extra`` retains every other provider-owned
    key, including a present empty or ``None`` value of a named optional field. Use ``clear_expires``
    for a credential without an ``expires`` property.
    """

    refresh: str = ""
    access: str = ""
    expires: Any = 0
    project_id: str = ""
    account_id: str = ""
    scope: str = ""
    extra: dict[str, Any] = field(default_factory=dict)
    _expires_absent: bool = field(default=False, repr=False)

    def has_expires(self) -> bool:
        """Whether the credential carries an ``expires`` property. An absent value applies only while ``expires`` is still the integer 0, so assigning an expiry restores the property."""
        return not (self._expires_absent and type(self.expires) is int and self.expires == 0)

    def clear_expires(self) -> None:
        """Remove the ``expires`` property."""
        self.expires = 0
        self._expires_absent = True

    def _to_wire(self) -> dict[str, Any]:
        wire: dict[str, Any] = {**self.extra, "refresh": self.refresh, "access": self.access}
        wire.pop("expires", None)
        if self.has_expires():
            wire["expires"] = _js_number_wire(self.expires)
        for key, value in (("projectId", self.project_id), ("accountId", self.account_id), ("scope", self.scope)):
            if value:
                wire[key] = value
        return wire

    @staticmethod
    def _from_wire(data: dict[str, Any] | None) -> "OAuthCredentials":
        data = dict(data or {})
        creds = OAuthCredentials(refresh=data.pop("refresh", ""), access=data.pop("access", ""))
        if "expires" in data:
            creds.expires = _js_number_read(data.pop("expires"))
        else:
            creds._expires_absent = True
        for key, name in (("projectId", "project_id"), ("accountId", "account_id"), ("scope", "scope")):
            value = data.get(key)
            if isinstance(value, str) and value:
                setattr(creds, name, data.pop(key))
        creds.extra = data
        return creds


_MAX_SAFE_INTEGER = 2**53


def _js_number_read(value: Any) -> Any:
    """JSON.parse of a number: an integer beyond 2**53 is the nearest double, as the Go SDK projects it."""
    if type(value) is int and abs(value) > _MAX_SAFE_INTEGER:
        try:
            return float(value)
        except OverflowError:
            return math.inf if value > 0 else -math.inf
    return value


def _js_number_wire(value: Any) -> Any:
    """JSON.stringify of a JavaScript number: non-finite values are null, -0 is 0, and a double beyond 2**53 below 1e21 is written with Number::toString's digits (2**60 is 1152921504606847000)."""
    value = _js_number_read(value)
    if isinstance(value, float):
        if math.isnan(value) or math.isinf(value):
            return None
        if value == 0:
            return 0
        if value.is_integer() and abs(value) < 1e21:
            return int(decimal.Decimal(repr(value))) if abs(value) > _MAX_SAFE_INTEGER else int(value)
    return value


@dataclass
class OAuthAuthInfo:
    url: str = ""
    instructions: str = ""

    def _to_wire(self) -> dict[str, Any]:
        wire: dict[str, Any] = {"url": self.url}
        if self.instructions:
            wire["instructions"] = self.instructions
        return wire


@dataclass
class OAuthDeviceCodeInfo:
    user_code: str = ""
    verification_uri: str = ""
    interval_seconds: float = 0.0
    expires_in_seconds: float = 0.0

    def _to_wire(self) -> dict[str, Any]:
        return {
            "userCode": self.user_code,
            "verificationUri": self.verification_uri,
            "intervalSeconds": self.interval_seconds,
            "expiresInSeconds": self.expires_in_seconds,
        }


@dataclass
class OAuthPrompt:
    message: str = ""
    placeholder: str = ""
    allow_empty: bool = False

    def _to_wire(self) -> dict[str, Any]:
        wire: dict[str, Any] = {"message": self.message}
        if self.placeholder:
            wire["placeholder"] = self.placeholder
        if self.allow_empty:
            wire["allowEmpty"] = self.allow_empty
        return wire


@dataclass
class OAuthSelectOption:
    id: str = ""
    label: str = ""


@dataclass
class OAuthSelectPrompt:
    message: str = ""
    options: list[OAuthSelectOption] = field(default_factory=list)

    def _to_wire(self) -> dict[str, Any]:
        return {"message": self.message, "options": [{"id": o.id, "label": o.label} for o in self.options]}


@dataclass
class OAuthCredentialStatus:
    present: bool = False
    auth_type: str = ""
    source: str = ""


class OAuthCredentialStore(Protocol):
    """Lets a provider own credential persistence instead of core's auth.json."""

    def credential_status(self) -> OAuthCredentialStatus: ...

    def store_credentials(self, creds: OAuthCredentials) -> str: ...

    def delete_credentials(self) -> bool: ...


class OAuthCancelled(Exception):
    """Raised by a value-returning login callback when the user dismissed the host prompt."""

    def __init__(self) -> None:
        super().__init__("oauth prompt cancelled")


@dataclass
class OAuthProvider:
    """An OAuth capability attached to a model provider. Only the set closures are advertised."""

    name: str = ""
    login: Callable[["OAuthLoginCallbacks"], OAuthCredentials] | None = None
    refresh_token: Callable[[OAuthCredentials], OAuthCredentials] | None = None
    get_api_key: Callable[[OAuthCredentials], str] | None = None
    credential_store: OAuthCredentialStore | None = None
    # Whether access through this OAuth method is subscription-backed.
    is_subscription: bool = False


class OAuthLoginCallbacks:
    """Drives the host login UI from inside a provider's login closure via oauth.cb.* calls."""

    def __init__(self, ext: "Extension", request_id: str) -> None:
        self._ext = ext
        self._request_id = request_id

    def on_auth(self, info: OAuthAuthInfo) -> None:
        self._ext._call("oauth.cb.onAuth", info._to_wire(), self._request_id)
        self._ext._request_state(self._request_id, "progress")

    def on_device_code(self, info: OAuthDeviceCodeInfo) -> None:
        self._ext._call("oauth.cb.onDeviceCode", info._to_wire(), self._request_id)
        self._ext._request_state(self._request_id, "progress")

    def on_progress(self, message: str) -> None:
        self._ext._call("oauth.cb.onProgress", {"message": message}, self._request_id)
        self._ext._request_state(self._request_id, "progress")

    def on_prompt(self, prompt: OAuthPrompt) -> str:
        return self._input_call("oauth.cb.onPrompt", prompt._to_wire())

    def on_select(self, prompt: OAuthSelectPrompt) -> str:
        return self._input_call("oauth.cb.onSelect", prompt._to_wire())

    def on_manual_code_input(self) -> str:
        return self._input_call("oauth.cb.onManualCodeInput", None)

    def _input_call(self, method: str, args: Any) -> str:
        self._ext._request_state(self._request_id, "blocked", "user")
        result = self._ext._call(method, args, self._request_id)
        self._ext._request_state(self._request_id, "progress")
        payload = result.get("result") or {}
        if payload.get("cancel"):
            raise OAuthCancelled()
        return payload.get("value", "")


def _user_bash_event_result(value: Any) -> Any:
    """Preserve a present number-or-undefined exit code (None) across JSON."""
    if not isinstance(value, dict) or not isinstance(value.get("result"), dict):
        return value
    result = dict(value["result"])
    undefined = "exitCode" in result and result["exitCode"] is None
    if undefined:
        del result["exitCode"]
    if "fullOutputPath" in result and result["fullOutputPath"] is None:
        del result["fullOutputPath"]
    return {**value, "result": result, "_pigUserBashExitCodeUndefined": undefined}


class Extension:
    def __init__(self, name: str):
        self._name = name
        self._tools: list[dict[str, Any]] = []
        self._tool_registration_lock = threading.RLock()
        self._tools_live = False
        # Raw terminal-input handlers, consulted synchronously while the host
        # holds the user's keystroke. Empty means the host never round-trips.
        self._term_input: list[tuple[int, Any]] = []
        self._term_input_seq = 0
        self._width_change: list[tuple[int, Any]] = []
        self._width_change_seq = 0
        # Width deliveries wait here for their worker thread: handlers run off the thread that reads the host's replies (see _dispatch_width_change).
        self._width_deliveries: deque[tuple[int, list[Any]]] = deque()
        self._width_worker_running = False
        self._width_dispatch_lock = threading.Lock()
        self._surface_lock = threading.Lock()
        self._surfaces: dict[str, _SurfaceRenderer] = {}
        self._commands: list[dict[str, Any]] = []
        self._shortcuts: list[dict[str, Any]] = []
        self._handlers: list[dict[str, Any]] = []
        self._flags: list[dict[str, Any]] = []
        self._autocomplete = _AutocompleteRegistry()
        self._providers: list[dict[str, Any]] = []
        self._provider_lock = threading.RLock()
        self._native_providers = {}
        self._provider_callbacks = {}
        self._provider_updates = {}
        self._remote_providers = {}
        self._provider_streams: dict[str, Callable] = {}
        self._provider_operations: dict[str, dict[str, dict[str, Callable]]] = {}
        self._provider_active: dict[str, ModelEventStream] = {}
        self._renderers: list[dict[str, Any]] = []
        self._entry_renderers: list[dict[str, Any]] = []
        self._markdown_transformer: Callable[[str, dict[str, Any]], Any] | None = None
        self._oauth_providers: dict[str, OAuthProvider] = {}
        self._tool_handlers: dict[str, ToolHandler] = {}
        self._tool_prepare_handlers: dict[str, ToolPrepareArguments] = {}
        self._tool_starts = _ToolStartOrder()
        self._command_handlers: dict[str, CommandHandler] = {}
        self._command_completions: dict[str, ArgumentCompletionsHandler] = {}
        self._event_handlers: dict[int, EventHandler] = {}
        self._shortcut_handlers: dict[str, ShortcutHandler] = {}
        self._renderer_handlers: dict[str, RendererHandler] = {}
        self._entry_renderer_handlers: dict[str, RendererHandler] = {}
        self._tool_call_renderers: dict[str, ToolRenderCallHandler] = {}
        self._tool_result_renderers: dict[str, ToolRenderResultHandler] = {}
        # One renderer state per tool card; renders of one card run one at a time.
        self._tool_render_cards: dict[str, tuple[threading.Lock, dict[str, Any]]] = {}
        self._tool_render_lock = threading.Lock()
        self._flag_defaults: dict[str, Any] = {}
        self._session_name: str = ""
        self._session_file: str = ""
        self._cwd: str = ""
        self._mode: str = ""
        self._width: int = 0
        self._height: int = 0
        self._model: str = ""
        self._has_ui: bool = False
        self._theme = Theme()
        self._sock: socket.socket | None = None
        self._write_lock = threading.Lock()
        self._state_lock = threading.Lock()
        self._session_mirror = SessionMirror()
        # Serializes the one-time session-log subscribe so concurrent first
        # readers make a single host call.
        self._session_sub_lock = threading.Lock()
        self._session_sub_error: Exception | None = None
        self._pending: dict[str, threading.Event] = {}
        self._pending_results: dict[str, dict[str, Any]] = {}
        self._pending_parents: dict[str, str] = {}
        self._cancelled_calls: set[str] = set()
        self._call_id = 0
        self._active: dict[str, tuple[threading.Event, str | None]] = {}
        self._request_parents: dict[str, _RequestParent] = {}
        self._request_threads: set[threading.Thread] = set()
        self._request_threads_lock = threading.Lock()
        self._overlay_seq = 0
        self._model_stream_seq = 0
        self._model_streams: dict[str, ModelEventStream] = {}
        self._model_stream_lock = threading.Lock()
        self._overlays: dict[str, _RemoteOverlayState] = {}
        self._overlay_lock = threading.Lock()
        self._shutdown = ProviderSignal()
        # The replicated signal of the run in progress: (host run number, signal), None while no run is active (Context.signal).
        self._run_signal: tuple[int, ProviderSignal] | None = None
        self._run_signal_lock = threading.Lock()
        # Upstream 0.99.1 extension API. Registrations made while the factory
        # runs are queued and travel in the register frame, as loader.ts queues
        # them until the factory succeeds; afterwards each is a host call.
        self._mcp_servers_pending: list[dict[str, Any]] = []
        self._virtual_models_pending: list[dict[str, Any]] = []
        # The unregistrations made before the register frame, in call order. Pi filters the runtime-wide queue of models registered before the runner binds (loader.ts:228-232); the host applies these to it.
        self._virtual_model_unregistrations: list[dict[str, str]] = []
        self._virtual_model_routes: dict[tuple[str, str], VirtualModelRouteHandler] = {}
        self._tool_loadout_handlers: dict[str, ToolPrepareLoadoutHandler] = {}
        # State the host replicates (StatePayload.settings, mcpServers), guarded by _state_lock. ctx.tools asks the host when it is read.
        self._settings: dict[str, Any] | None = None
        self._mcp_servers: list[dict[str, Any]] = []
        self._execute_seq = 0
        self._nested_calls: dict[str, _NestedCall] = {}
        # Ids of the MCP registration calls in flight, guarded by _state_lock. The read loop applies each reply's server list in frame order, so a later state push is never replaced by an older reply.
        self._mcp_calls: set[str] = set()
        self._bus = _EventBusHub(self)

    @property
    def name(self) -> str:
        return self._name

    def tool(self, name: str, description: str, schema: Schema, handler: ToolHandler, *, prompt_snippet: str | None = None, prompt_guidelines: list[str] | None = None, source: str | None = None, constrained_sampling: dict[str, Any] | Literal[False] | None = None, prepare_arguments: ToolPrepareArguments | None = None) -> None:
        """Register a tool. Reject a non-object schema before changing registration state."""
        if not isinstance(schema, dict):
            raise ValueError(f'Tool "{name}" registered by extension "{self._name}" must define an object parameter schema.')
        _ensure_jsonable(schema, f"tool schema for {name}")
        td: dict = {"name": name, "description": description, "parameters": schema}
        if constrained_sampling is not None:
            _ensure_jsonable(constrained_sampling, f"constrained_sampling for {name}")
            td["constrained_sampling"] = constrained_sampling
        if prompt_snippet is not None:
            td["prompt_snippet"] = prompt_snippet
        if prompt_guidelines:
            td["prompt_guidelines"] = prompt_guidelines
        if source:
            td["source"] = source
        self._register_tool(td, handler, prepare_arguments)

    def _register_tool(self, declaration, handler, prepare=None, render_call=None, render_result=None, prepare_loadout=None):
        name = declaration["name"]
        # agent-loop.ts:707-716: the host validates, so it asks the extension for the prepared arguments (tool_prepare_arguments).
        declaration = {key: value for key, value in declaration.items() if key != "prepares_arguments"}
        if prepare is not None:
            declaration["prepares_arguments"] = True
        with self._tool_registration_lock:
            index = next((i for i, tool in enumerate(self._tools) if tool["name"] == name), None)
            if index is None:
                self._tools.append(declaration)
            else:
                self._tools[index] = declaration
            self._tool_handlers[name] = handler
            for handlers, callback in ((self._tool_prepare_handlers, prepare), (self._tool_call_renderers, render_call), (self._tool_result_renderers, render_result), (self._tool_loadout_handlers, prepare_loadout)):
                if callback is None:
                    handlers.pop(name, None)
                else:
                    handlers[name] = callback
            if self._tools_live:
                self._call("registerTool", declaration)

    def register_tool(self, definition: ToolDefinition) -> None:
        """Replace a tool in place and refresh a running Session before returning."""
        name = definition.name
        if not name:
            raise ValueError("register_tool requires a definition with name")
        if not isinstance(definition.parameters, dict):
            raise ValueError(f'Tool "{name}" registered by extension "{self._name}" must define an object parameter schema.')
        if definition.render_shell not in {"default", "self"}:
            raise ValueError("render_shell must be 'default' or 'self'")
        if definition.execution_mode not in {None, "sequential", "parallel"}:
            raise ValueError("execution_mode must be 'sequential' or 'parallel'")
        if definition.exposure is not None and definition.exposure not in _TOOL_EXPOSURES:
            raise ValueError("exposure must be one of " + ", ".join(repr(e) for e in _TOOL_EXPOSURES))
        if definition.prepare_loadout is not None and not callable(definition.prepare_loadout):
            raise TypeError("prepare_loadout must be callable")
        declaration = {
            "name": name, "label": definition.label, "description": definition.description,
            "parameters": definition.parameters, "prompt_snippet": definition.prompt_snippet,
            "prompt_guidelines": definition.prompt_guidelines, "execution_mode": definition.execution_mode,
            "constrained_sampling": definition.constrained_sampling, "render_shell": definition.render_shell,
            "renders_call": definition.render_call is not None, "renders_result": definition.render_result is not None,
            "output_schema": definition.output_schema, "exposure": definition.exposure, "namespace": definition.namespace,
            "annotations": definition.annotations, "default_active": definition.default_active,
            "prepares_loadout": True if definition.prepare_loadout is not None else None,
        }
        declaration = {key: value for key, value in declaration.items() if value is not None}
        _ensure_jsonable(declaration, f"tool definition for {name}")
        self._register_tool(declaration, definition.execute, definition.prepare_arguments, definition.render_call, definition.render_result, definition.prepare_loadout)

    def register_mcp_server(self, name: str, config: dict[str, Any]) -> None:
        """Register an MCP server, as ``pi.registerMcpServer`` does (loader.ts:456-465).

        While the factory runs the registration is queued and applied when the
        host accepts this extension; afterwards it applies at once. The host
        validates ``config`` and rejects a name another extension owns: that
        raises :class:`HostCallError` here after load, and fails the load when
        it happens in the factory.
        """
        self._register_mcp_server(name, config, self._call)

    def unregister_mcp_server(self, name: str) -> None:
        """Remove an MCP server this extension registered (loader.ts:472-475)."""
        self._unregister_mcp_server(name, self._call)

    def _register_mcp_server(self, name: str, config: dict[str, Any], call: Callable[[str, Any], dict[str, Any]]) -> None:
        _ensure_jsonable(config, f"MCP server config for {name}")
        declaration = {"name": name, "config": config}
        with self._tool_registration_lock:
            if not self._tools_live:
                # A replaced server keeps its position, as a JavaScript Map does (mcp-servers.ts:212-215).
                index = next((i for i, server in enumerate(self._mcp_servers_pending) if server["name"] == name), None)
                if index is None:
                    self._mcp_servers_pending.append(declaration)
                else:
                    self._mcp_servers_pending[index] = declaration
                return
        call("registerMcpServer", declaration)

    def _unregister_mcp_server(self, name: str, call: Callable[[str, Any], dict[str, Any]]) -> None:
        with self._tool_registration_lock:
            if not self._tools_live:
                self._mcp_servers_pending = [server for server in self._mcp_servers_pending if server["name"] != name]
                return
        call("unregisterMcpServer", {"name": name})

    def _get_mcp_servers(self) -> list[dict[str, Any]]:
        with self._state_lock:
            return copy.deepcopy(self._mcp_servers)

    def register_virtual_model(self, model: VirtualModel) -> None:
        """Register a virtual model, as ``pi.registerVirtualModel`` does (loader.ts:480-490).

        ``model.route`` runs in this extension each time the host routes a
        request to the model.
        """
        self._register_virtual_model(model, self._call)

    def unregister_virtual_model(self, provider: str, model_id: str) -> None:
        """Remove a virtual model registered with :meth:`register_virtual_model` (loader.ts:492-495)."""
        self._unregister_virtual_model(provider, model_id, self._call)

    def _register_virtual_model(self, model: VirtualModel, call: Callable[[str, Any], dict[str, Any]]) -> None:
        if not callable(model.route):
            raise TypeError("virtual model route must be callable")
        declaration: dict[str, Any] = {"provider": model.provider, "id": model.id, "name": model.name}
        for key, value in (("thinkingLevels", model.thinking_levels), ("contextWindow", model.context_window), ("maxTokens", model.max_tokens), ("input", model.input)):
            if value is not None:
                declaration[key] = value
        _ensure_jsonable(declaration, f"virtual model {model.provider}/{model.id}")
        key = (model.provider, model.id)
        with self._tool_registration_lock:
            previous = self._virtual_model_routes.get(key)
            self._virtual_model_routes[key] = model.route
            if not self._tools_live:
                self._virtual_models_pending.append(declaration)
                return
        try:
            call("registerVirtualModel", declaration)
        except BaseException:
            with self._tool_registration_lock:
                if previous is None:
                    self._virtual_model_routes.pop(key, None)
                else:
                    self._virtual_model_routes[key] = previous
            raise

    def _unregister_virtual_model(self, provider: str, model_id: str, call: Callable[[str, Any], dict[str, Any]]) -> None:
        key = (provider, model_id)
        with self._tool_registration_lock:
            if not self._tools_live:
                self._virtual_models_pending = [m for m in self._virtual_models_pending if (m["provider"], m["id"]) != key]
                self._virtual_model_unregistrations.append({"provider": provider, "id": model_id})
                self._virtual_model_routes.pop(key, None)
                return
        call("unregisterVirtualModel", {"provider": provider, "id": model_id})
        with self._tool_registration_lock:
            self._virtual_model_routes.pop(key, None)

    def _get_settings(self) -> dict[str, Any]:
        with self._state_lock:
            settings = self._settings
        if settings is None:
            raise RuntimeError("settings are not available: the host has not sent them")
        return copy.deepcopy(settings)


    def _load_call(self, method: str, args: dict[str, Any]) -> dict[str, Any]:
        """One host call before the register frame, when the read loop has not started: this thread reads until the call's result arrived."""
        call_id, event = self._begin_call(method, args)
        while not event.is_set():
            env = self._read_during_load(event)
            if env is not None:
                raise RuntimeError(f"expected a {method} result, got {env.get('type')}")
        return self._wait_call(call_id, event, method)

    def _read_during_load(self, waiting: threading.Event | None = None) -> dict[str, Any] | None:
        """Read the next message while the extension loads. Pings, call results and listener dispatches are served as the main loop serves them, as a node runtime serves a dispatch during its factory; any other message is returned. With ``waiting`` given, return None once that call's result has arrived."""
        while True:
            env = self._read()
            kind = env.get("type")
            if kind == "ping":
                self._send({"type": "pong", "pong": {"nonce": (env.get("ping") or {}).get("nonce", "")}})
            elif kind == "call_result":
                self._deliver_call_result(env)
                if waiting is not None and waiting.is_set():
                    return None
            elif kind == "request" and (env.get("request") or {}).get("method") == "events.dispatch":
                self._start_request(env)
            else:
                return env

    # ctx.execute_tool (runner.ts:966-983) -------------------------------------

    def _execute_tool(self, ctx: Context, name: str, args: Any, options: ExecuteToolOptions) -> dict[str, Any]:
        _ensure_jsonable(args, f"arguments of tool {name}")
        if options.on_update is not None and not callable(options.on_update):
            raise TypeError("on_update must be callable")
        if options.signal is not None and not callable(getattr(options.signal, "subscribe", None)):
            raise TypeError("signal must be a ProviderSignal")
        with self._state_lock:
            self._execute_seq += 1
            execute_id = f"e{self._execute_seq}"
            nested = _NestedCall()
            self._nested_calls[execute_id] = nested
        payload: dict[str, Any] = {"callerId": ctx.tool_call_id, "name": name, "args": args, "executeId": execute_id}
        if options.on_update is not None:
            payload["wantsUpdates"] = True
        if options.signal is not None:
            payload["ownSignal"] = True
        unsubscribe: Callable[[], None] | None = None
        ctx._report_request_state("blocked", "host_call")
        try:
            if options.signal is None:
                call_id, event = self._begin_call("executeTool", payload, ctx.request_id, ctx._parent, event=_NestedCallEvent(nested))
            else:
                # An explicit signal replaces the calling tool's (runner.ts:980, `options.signal ?? signal`): the call is not tied to the calling request, so cancelling that request neither cancels it on the host nor ends this wait.
                call_id, event = self._begin_call("executeTool", payload, event=_NestedCallEvent(nested))
            if options.signal is not None:
                # After the call is sent: the host applies a cancel only after the call it names started.
                unsubscribe = options.signal.subscribe(lambda: self._cancel_nested(execute_id))
            failure = nested.deliver(options.on_update)
            try:
                reply = self._wait_call(call_id, event, "executeTool")
            except BaseException:
                # The host rejects a call whose on_update raised, after the tool returned (nested-tool-calls.ts:219-248); the call raises what the callback raised, as Pi's rejection carries that error.
                if failure is not None:
                    raise failure from None
                raise
            if failure is not None:
                raise failure
            return reply.get("result") or {}
        finally:
            if unsubscribe is not None:
                unsubscribe()
            with self._state_lock:
                self._nested_calls.pop(execute_id, None)
            ctx._report_request_state("progress")

    def _cancel_nested(self, execute_id: str) -> None:
        """Send the cancel of one nested call from a thread of its own, so the signal's setter never waits for the host."""
        def cancel() -> None:
            try:
                self._call("executeTool.cancel", {"executeId": execute_id})
            except Exception:  # noqa: BLE001 - the call ended or the connection closed: nothing is left to cancel  # nosec B110
                pass

        threading.Thread(target=cancel, name=f"pig-execute-cancel-{execute_id}", daemon=True).start()

    def command(self, name: str, description: str, handler: CommandHandler, *, get_argument_completions: ArgumentCompletionsHandler | None = None) -> None:
        """Register a slash command, as upstream ``pi.registerCommand`` does.

        ``get_argument_completions`` is upstream's ``getArgumentCompletions``. A command without a non-empty string name or a
        callable handler is rejected before it registers (loader.ts:302-311), so the extension fails to load.
        """
        if not isinstance(name, str) or name == "":
            raise ValueError(f'Command registered by extension "{self._name}" must have a non-empty string name. Use pi.registerCommand("name", {{ description, handler }}).')
        if not callable(handler):
            raise ValueError(f'Command "/{name}" registered by extension "{self._name}" must define handler().')
        declaration: dict[str, Any] = {"name": name, "description": description}
        if get_argument_completions is not None:
            declaration["argument_completions"] = True
            self._command_completions[name] = get_argument_completions
        self._commands.append(declaration)
        self._command_handlers[name] = handler

    def shortcut(self, key: str, description: str, handler: ShortcutHandler) -> None:
        self._shortcuts.append({"key": key, "description": description})
        self._shortcut_handlers[key] = handler

    def flag(self, name: str, description: str = "", flag_type: str = "string", default: Any = None) -> None:
        if flag_type not in {"boolean", "string"}:
            raise ValueError("flag_type must be 'boolean' or 'string'")
        _ensure_jsonable(default, f"flag default for {name}")
        decl: dict[str, Any] = {"name": name, "description": description, "type": flag_type}
        if default is not None:
            decl["default"] = default
        self._flags.append(decl)
        if name not in self._flag_defaults or self._flag_defaults[name] is None:
            self._flag_defaults[name] = default

    def register_native_provider(self, provider: Provider) -> None:
        declaration = _register_native(self, provider)
        self._drop_queued_provider(provider.id)
        self._providers.append({"name": provider.id, "config": {}, "native": declaration})

    def register_provider(self, name: str | Provider, config: dict[str, Any] | None = None) -> None:
        """Register or override a model provider. Before :meth:`run` registers the extension the registration is queued; after that it takes effect at once through the host, as Pi's pi.registerProvider does once the runner is bound (types.ts:1766-1803, runner.ts:517-523), and a registration the host refuses raises."""
        if isinstance(name, Provider):
            self.register_native_provider(name)
            return
        with self._tool_registration_lock:
            if not self._tools_live:
                self._providers.append(self._take_provider_declaration(name, config))
                return
        self._register_provider_now(name, config, self._call)

    def _register_provider_now(self, name: str, config: dict[str, Any], call: Callable[[str, Any], dict[str, Any]]) -> None:
        """Send a provider registration through the host. A registration the host refuses leaves the callables the provider had."""
        with self._provider_lock:
            kept = self._held_provider_callbacks(name)
            declaration = self._take_provider_declaration(name, config)
        try:
            call("registerProvider", declaration)
        except BaseException:
            with self._provider_lock:
                self._restore_provider_callbacks(name, kept)
            raise

    def _unregister_provider_now(self, name: str, call: Callable[[str, Any], dict[str, Any]]) -> None:
        """Remove a provider through the host, then drop the callables the extension held for it."""
        call("unregisterProvider", {"name": name})
        with self._provider_lock:
            self._restore_provider_callbacks(name, (None, None))
            self._oauth_providers.pop(name, None)

    def _take_provider_declaration(self, name: str, config: dict[str, Any]) -> dict[str, Any]:
        """Keep the callables of a provider config in the extension and return the declaration the host receives: the register payload's entry and the registerProvider call after the factory carry the same one."""
        config = dict(config)
        callback = config.pop("streamSimple", None)
        if callback is not None:
            if not callable(callback):
                raise TypeError("streamSimple must be callable")
        operations: dict[str, dict[str, Any]] = {}
        for kind in ("images", "classifiers"):
            implementations = config.pop(kind, None)
            if implementations is None:
                continue
            for api, implementation in implementations.items():
                if not callable(implementation):
                    raise TypeError(f"provider {kind} implementation for {api} must be callable")
            operations[kind] = dict(implementations)
        _ensure_jsonable(config, f"provider config for {name}")
        if callback is not None:
            self._provider_streams[name] = callback
        # A re-registration replaces only the kinds it defines, as registerProvider merges defined values (model-runtime.ts registerProvider) and the host keeps the APIs it wired.
        kept = self._provider_operations.setdefault(name, {})
        for kind, implementations in operations.items():
            if implementations:
                kept[kind] = implementations
        if not kept:
            self._provider_operations.pop(name, None)
        declaration = {"name": name, "config": config}
        if callback is not None:
            declaration["stream_simple"] = True
        if operations.get("images"):
            declaration["image_apis"] = sorted(operations["images"])
        if operations.get("classifiers"):
            declaration["classifier_apis"] = sorted(operations["classifiers"])
        return declaration

    def _held_provider_callbacks(self, name: str) -> tuple[Callable | None, dict[str, dict[str, Callable]] | None]:
        operations = self._provider_operations.get(name)
        return self._provider_streams.get(name), None if operations is None else {kind: dict(implementations) for kind, implementations in operations.items()}

    def _restore_provider_callbacks(self, name: str, kept: tuple[Callable | None, dict[str, dict[str, Callable]] | None]) -> None:
        stream, operations = kept
        if stream is None:
            self._provider_streams.pop(name, None)
        else:
            self._provider_streams[name] = stream
        if operations is None:
            self._provider_operations.pop(name, None)
        else:
            self._provider_operations[name] = operations

    def unregister_provider(self, name: str) -> None:
        """Remove a provider registration. Before :meth:`run` registers the extension it drops the queued registration; after that the host removes the provider at once, as Pi's pi.unregisterProvider does (types.ts:1805-1819), and a removal the host refuses raises."""
        with self._tool_registration_lock:
            if not self._tools_live:
                self._drop_queued_provider(name)
                return
        self._unregister_provider_now(name, self._call)

    def _drop_queued_provider(self, name: str) -> None:
        self._provider_streams.pop(name, None)
        self._provider_operations.pop(name, None)
        self._providers = [provider for provider in self._providers if provider.get("name") != name]

    def register_oauth_provider(self, name: str, config: dict[str, Any], provider: OAuthProvider) -> None:
        """Register a model provider that also contributes an OAuth capability.

        The provider's callables are stored for oauth_* dispatch and the config
        gains a serializable "oauth" capability descriptor (never callables).
        """
        config = dict(config or {})
        oauth: dict[str, Any] = {
            "name": provider.name or name,
            "isSubscription": provider.is_subscription is True,
            "has_login": provider.login is not None,
            "has_refresh": provider.refresh_token is not None,
            "has_get_api_key": provider.get_api_key is not None,
        }
        if provider.credential_store is not None:
            oauth["has_credential_store"] = True
        config["oauth"] = oauth
        _ensure_jsonable(config, f"provider config for {name}")
        with self._tool_registration_lock:
            if not self._tools_live:
                self._providers.append({"name": name, "config": config})
                self._oauth_providers[name] = provider
                return
        # After run the registration takes effect at once, as Pi's pi.registerProvider does with an oauth config (types.ts:1766-1803); a refused one keeps the OAuth provider held before.
        with self._provider_lock:
            previous = self._oauth_providers.get(name)
            self._oauth_providers[name] = provider
        try:
            self._call("registerProvider", {"name": name, "config": config})
        except BaseException:
            with self._provider_lock:
                if previous is None:
                    self._oauth_providers.pop(name, None)
                else:
                    self._oauth_providers[name] = previous
            raise

    def tool_renderers(self, name: str, *, render_call: ToolRenderCallHandler | None = None, render_result: ToolRenderResultHandler | None = None, render_shell: str = "default") -> None:
        """Set tool renderers and publish the updated definition in a running Session."""
        if render_shell not in {"default", "self"}:
            raise ValueError("render_shell must be 'default' or 'self'")
        with self._tool_registration_lock:
            for tool in self._tools:
                if tool.get("name") != name:
                    continue
                declaration = dict(tool)
                declaration["render_shell"] = render_shell
                declaration["renders_call"] = render_call is not None
                declaration["renders_result"] = render_result is not None
                self._register_tool(declaration, self._tool_handlers[name], self._tool_prepare_handlers.get(name), render_call, render_result)
                break

    def _render_tool(self, ctx: "Context", name: str, args: dict[str, Any]) -> list[str]:
        card_id = str(args.get("card") or "")
        with self._tool_render_lock:
            card = self._tool_render_cards.setdefault(card_id, (threading.Lock(), {}))
        wire = args.get("context") or {}
        with card[0]:
            render = ToolRenderContext(
                args=args.get("args") or {},
                tool_call_id=str(wire.get("toolCallId") or ""),
                cwd=str(wire.get("cwd") or ""),
                execution_started=bool(wire.get("executionStarted")),
                args_complete=bool(wire.get("argsComplete")),
                is_partial=bool(wire.get("isPartial")),
                expanded=bool(wire.get("expanded")),
                show_images=bool(wire.get("showImages")),
                is_error=bool(wire.get("isError")),
                state=card[1],
                _invalidate=lambda: self._notify("tool_render_invalidate", {"card": card_id}),
            )
            width = int(args.get("width") or 0)
            if args.get("phase") == "result":
                handler = self._tool_result_renderers.get(name)
                if handler is None:
                    raise RuntimeError(f"tool {name} has no result renderer")
                result = args.get("result") or {"content": []}
                return handler(ctx, result, args.get("options") or {}, render, width)
            call = self._tool_call_renderers.get(name)
            if call is None:
                raise RuntimeError(f"tool {name} has no call renderer")
            return call(ctx, args.get("args") or {}, render, width)

    def message_renderer(self, custom_type: str, handler: RendererHandler) -> None:
        self._renderers.append({"custom_type": custom_type})
        self._renderer_handlers[custom_type] = handler

    def entry_renderer(self, custom_type: str, handler: RendererHandler) -> None:
        self._entry_renderers.append({"custom_type": custom_type})
        self._entry_renderer_handlers[custom_type] = handler

    def markdown_transformer(self, transformer: Callable[[str, dict[str, Any]], Any]) -> None:
        """Register the extension's display-only Markdown transform (Pi's
        pi.registerMarkdownTransformer). The host applies it to user and
        assistant Markdown in the interactive transcript, after its own
        transformers; a later registration replaces an earlier one. It receives
        the Markdown and Pi's MarkdownTransformContext (messageType,
        isStreaming, availableWidth); anything but a str return, or a raise,
        keeps the input."""
        self._markdown_transformer = transformer

    def on_project_trust(self, handler: ProjectTrustHandler) -> None:
        """Register an awaited pre-runtime project_trust handler."""
        self.on_event("project_trust", handler)

    @property
    def events(self) -> EventBus:
        """Upstream's ``pi.events``, shared with every other realm of the session."""
        return EventBus(self)

    def on_event(self, event: str, handler: EventHandler, can_block: bool = False) -> None:
        handler_id = len(self._handlers) + 1
        self._handlers.append({"event": event, "can_block": can_block, "handler_id": handler_id})
        self._event_handlers[handler_id] = handler

    def run(self) -> None:
        sock = os.environ.get("PIG_EXT_SOCKET")
        if not sock:
            raise RuntimeError("PIG_EXT_SOCKET not set: extension must be launched by pig")
        self.run_with_socket(sock)

    def run_with_socket(self, sock_path: str) -> None:
        self._sock = _connect_unix(sock_path)
        try:
            self._serve()
        finally:
            # The host's Retire waits for this close while the process lives on for later factory generations (Go's RunWithConn closes on return).
            try:
                self._sock.close()
            except OSError:
                pass

    def _serve(self) -> None:
        self._bus.register_pending()
        with self._tool_registration_lock:
            self._send({"type": "register", "register": {"name": self._name, "tools": self._tools, "commands": self._commands, "shortcuts": self._shortcuts, "handlers": self._handlers, "flags": self._flags, "providers": self._providers, "message_renderers": self._renderers, "entry_renderers": self._entry_renderers, **({"markdown_transformer": True} if self._markdown_transformer is not None else {}), **({"mcp_servers": self._mcp_servers_pending} if self._mcp_servers_pending else {}), **({"virtual_models": self._virtual_models_pending} if self._virtual_models_pending else {}), **({"unregister_virtual_models": self._virtual_model_unregistrations} if self._virtual_model_unregistrations else {})}})
            ready = self._read_during_load()
            if ready.get("type") != "ready":
                raise RuntimeError(f"expected ready, got {ready.get('type')}")
            self._tools_live = True
        ready_data = ready.get("ready") or {}
        self._session_name = ready_data.get("session_name", "")
        self._cwd = ready_data.get("cwd", "")
        self._mode = ready_data.get("mode", "")
        self._width = ready_data.get("width", 0)
        self._height = ready_data.get("height", 0)
        self._model = ready_data.get("model", "")
        # Initialize session mirror from the ready payload state.
        initial_state = ready_data.get("state") or {}
        with self._state_lock:
            self._apply_ui_state(initial_state)
        initial_session = initial_state.get("session")
        if initial_session:
            self._session_file = str(initial_session.get("sessionFile") or "")
            self._session_mirror.apply_update(initial_session)
        while True:
            try:
                env = self._read()
            except EOFError:
                self._stop_runtime()
                return
            if env.get("type") == "ping":
                ping = env.get("ping") or {}
                self._send({"type": "pong", "pong": {"nonce": ping.get("nonce", "")}})
                continue
            if env.get("type") == "call_result":
                self._deliver_call_result(env)
                continue
            if env.get("type") == "notify":
                self._handle_notify(env)
            elif env.get("type") == "request":
                self._start_request(env)
            elif env.get("type") == "cancel":
                req_id = (env.get("cancel") or {}).get("request_id") or env.get("id")
                reason = (env.get("cancel") or {}).get("reason")
                with self._state_lock:
                    active = self._active.get(req_id)
                    if active:
                        self._active[req_id] = (active[0], reason)
                        stream = self._provider_active.get(req_id)
                        if stream is not None:
                            stream.end(None)
                    parent = self._request_parents.get(req_id)
                    if parent is not None:
                        parent.state = "cancelled"
                    cancelled_calls = self._cancel_parent_calls_locked(req_id)
                if active:
                    active[0].set()
                for event in cancelled_calls:
                    event.set()
            elif env.get("type") == "shutdown":
                self._stop_runtime()
                return

    def _deliver_call_result(self, env: dict[str, Any]) -> None:
        cid = env.get("id", "")
        result = env.get("call_result") or {}
        with self._state_lock:
            event = self._pending.pop(cid, None)
            self._pending_parents.pop(cid, None)
            if event:
                self._pending_results[cid] = result
            if cid in self._mcp_calls:
                self._mcp_calls.discard(cid)
                servers = (result.get("result") or {}).get("servers")
                if isinstance(servers, list):
                    self._mcp_servers = servers
        if event:
            event.set()

    def _start_request(self, env: dict[str, Any]) -> None:
        ctx = self._arm_request(env)
        if (env.get("request") or {}).get("method") == "tool_call":
            ctx._tool_start = self._tool_starts.reserve()
        self._request_state(env.get("id", ""), "started")
        worker = threading.Thread(
            target=self._handle_request_thread,
            args=(env, ctx),
            name=f"pig-request-{env.get('id', '')}",
            daemon=True,
        )
        with self._request_threads_lock:
            self._request_threads.add(worker)
        try:
            worker.start()
        except Exception as exc:
            with self._request_threads_lock:
                self._request_threads.discard(worker)
            if ctx._tool_start is not None:
                ctx._tool_start.release()
            self._respond(
                env.get("id", ""),
                None,
                {"message": f"start extension handler: {exc}"},
            )

    def _stop_runtime(self) -> None:
        self._shutdown.set()
        with self._state_lock:
            pending = list(self._pending.values())
            active = list(self._active.values())
            provider_streams = list(self._provider_active.values())
        with self._overlay_lock:
            overlays = list(self._overlays.values())
        for overlay in overlays:
            overlay.stop()
        for event in pending:
            event.set()
        for cancel, _ in active:
            cancel.set()
        for stream in provider_streams:
            stream.end(None)
        deadline = time.monotonic() + 2.0
        while True:
            with self._request_threads_lock:
                threads = [thread for thread in self._request_threads if thread is not threading.current_thread()]
            if not threads:
                self._autocomplete.clear()
                return
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError("extension handlers did not stop before the shutdown deadline")
            for thread in threads:
                thread.join(timeout=max(0.0, deadline - time.monotonic()))

    def _handle_request_thread(self, env: dict[str, Any], ctx: Context) -> None:
        try:
            self._handle_request(env, ctx)
        finally:
            with self._request_threads_lock:
                self._request_threads.discard(threading.current_thread())

    def _apply_run_signal(self, frame: dict) -> None:
        """Install the host's run_signal frame: one signal for the whole run, set when the run aborts."""
        with self._run_signal_lock:
            if not frame.get("active"):
                self._run_signal = None
                return
            run = int(frame.get("run") or 0)
            if self._run_signal is None or self._run_signal[0] != run:
                self._run_signal = (run, ProviderSignal())
            signal = self._run_signal[1]
        if frame.get("aborted"):
            signal.set()

    def _handle_notify(self, env: dict[str, Any]) -> None:
        notify = env.get("notify") or {}
        method = notify.get("method", "")
        args = notify.get("args") or {}
        if method == "autocomplete.release":
            self._autocomplete.release(args.get("id"))
            return
        if method == "tool_render_release":
            with self._tool_render_lock:
                self._tool_render_cards.pop(str(args.get("card") or ""), None)
            return
        if method == "provider_release":
            with self._provider_lock:
                self._native_providers.pop(args.get("key"), None)
            return
        if method == "events.release":
            self._bus.release(str(args.get("handlerId") or ""))
            return
        if method == "run_signal":
            self._apply_run_signal(args)
            return
        if method == "model_stream_event":
            stream_id = str(args.get("streamId") or "")
            with self._model_stream_lock:
                stream = self._model_streams.get(stream_id)
            if stream is not None:
                if args.get("started"):
                    stream.mark_started()
                else:
                    stream.push(args.get("event") or {})
            return
        if method == "ui.custom.input":
            key = str(args.get("key") or "")
            with self._overlay_lock:
                overlay = self._overlays.get(key)
            if overlay is None:
                return
            if not overlay.enqueue_input(str(args.get("data") or "")):
                self._notify(
                    "ui.custom.close",
                    {"key": key, "error": "focused input queue is full"},
                )
            return
        width_changed = False
        with self._state_lock:
            if method == "state_update":
                state = args.get("state") or {}
                # Session replication: apply incremental entries.
                session = state.get("session")
                if session:
                    if session.get("sessionFile"):
                        self._session_file = str(session["sessionFile"])
                    self._session_mirror.apply_update(session)
                model = state.get("model") or {}
                name = model.get("name") or model.get("id") or ""
                if name:
                    self._model = name
                self._apply_ui_state(state)
            elif method == "theme_change":
                palette = notify.get("args")
                if isinstance(palette, str):
                    try:
                        palette = json.loads(palette)
                    except ValueError:
                        palette = {}
                self._theme._set_palette(palette or {})
            elif method == "width_change":
                w = args.get("width", 0)
                if w > 0:
                    self._width = w
                    width_changed = True
            elif method == "height_change":
                h = args.get("height", 0)
                if h > 0:
                    self._height = h
        if width_changed:
            # After the store, so a handler that reads width() sees the new value.
            with self._state_lock:
                new_width = self._width
            self._dispatch_width_change(new_width)
            self._refresh_surfaces()
            with self._overlay_lock:
                overlays = list(self._overlays.values())
            for overlay in overlays:
                overlay.request_render()

    def _dispatch_width_change(self, width: int) -> None:
        """Queue one width delivery for the handlers subscribed now. They run on a worker thread in delivery order, so a handler that blocks on a host call never stops the thread that reads the reply."""
        with self._state_lock:
            handlers = [h for _, h in self._width_change]
        if not handlers:
            return
        with self._width_dispatch_lock:
            if self._shutdown.is_set():
                return
            self._width_deliveries.append((width, handlers))
            if self._width_worker_running:
                return
            self._width_worker_running = True
            worker = threading.Thread(target=self._run_width_deliveries, name="pig-width-change", daemon=True)
            with self._request_threads_lock:
                self._request_threads.add(worker)
        try:
            worker.start()
        except Exception as exc:  # noqa: BLE001 - as the Rust SDK's WidthDeliveries::submit, a worker that cannot start drops the queued widths and the reader keeps serving
            with self._request_threads_lock:
                self._request_threads.discard(worker)
            with self._width_dispatch_lock:
                self._width_deliveries.clear()
                self._width_worker_running = False
            print(f"pig: start width change handlers: {exc}", file=sys.stderr)

    def _run_width_deliveries(self) -> None:
        try:
            self._deliver_widths()
        except BaseException:
            # A handler exception that is not an Exception (SystemExit) ends this worker; the next width starts a new one.
            with self._width_dispatch_lock:
                self._width_deliveries.clear()
                self._width_worker_running = False
            raise
        finally:
            with self._request_threads_lock:
                self._request_threads.discard(threading.current_thread())

    def _deliver_widths(self) -> None:
        while True:
            with self._width_dispatch_lock:
                if not self._width_deliveries or self._shutdown.is_set():
                    self._width_deliveries.clear()
                    self._width_worker_running = False
                    return
                width, handlers = self._width_deliveries.popleft()
            for handler in handlers:
                try:
                    handler(width)
                except Exception:  # noqa: BLE001 - one handler's failure must not stop the others, as a thrown handler error rejects one call in upstream
                    if not self._shutdown.is_set():
                        print(f"pig: width change handler failed:\n{traceback.format_exc()}", file=sys.stderr, end="")

    def _apply_ui_state(self, state: dict[str, Any]) -> None:
        """Record the snapshot's hasUI and theme. Caller holds _state_lock."""
        has_ui = state.get("hasUI")
        if isinstance(has_ui, bool):
            self._has_ui = has_ui
        theme = state.get("theme")
        if isinstance(theme, dict):
            self._theme._set_palette(theme)
        settings = state.get("settings")
        if isinstance(settings, dict):
            self._settings = settings
        mcp_servers = state.get("mcpServers")
        if isinstance(mcp_servers, list):
            self._mcp_servers = mcp_servers

    def _arm_request(self, env: dict[str, Any]) -> Context:
        req_id = env.get("id", "")
        req = env.get("request") or {}
        cancel = ProviderSignal()
        parent = _RequestParent(req_id, self._sock)
        with self._state_lock:
            self._active[req_id] = (cancel, None)
            self._request_parents[req_id] = parent
        ctx = Context(self, req.get("tool_call_id"), req_id, cancel, _parent=parent, _tool_context=req.get("method") == "tool_call")
        ctx._reason_provider = lambda: self._active.get(req_id, (None, None))[1]
        return ctx

    def _handle_request(self, env: dict[str, Any], ctx: Context | None = None) -> None:
        req_id = env.get("id", "")
        req = env.get("request") or {}
        if ctx is None:
            ctx = self._arm_request(env)
        cancel = ctx._cancelled
        try:
            method = req.get("method")
            if method in {"autocomplete.sync", "autocomplete.suggest"}:
                self._respond(req_id, self._autocomplete.dispatch(ctx, req.get("args") or {}), None)
            elif method in {"provider_call", "provider_stream", "provider_sync"}:
                self._respond(req_id, _dispatch_provider(self, ctx, req), None)
            elif method in {"provider_object_callback", "provider_object_callback_sync"}:
                self._respond(req_id, _dispatch_provider_callback(self, req), None)
            elif method == "provider_operation":
                self._respond(req_id, _dispatch_provider_operation(self, ctx, req), None)
            elif method == "provider_stream_simple":
                cancel = ctx._cancelled
                args = req.get("args") or {}
                stream = self._provider_streams[req.get("tool", "")](ctx, args["model"], args["context"], args["options"])
                with self._state_lock:
                    self._provider_active[req_id] = stream
                    if cancel.is_set():
                        stream.end(None)
                try:
                    for event in stream.events():
                        if cancel.is_set():
                            raise RuntimeError("provider stream aborted")
                        self._notify("provider_stream_event", {"request_id": req_id, "result": event})
                    if cancel.is_set():
                        raise RuntimeError("provider stream aborted")
                    self._respond(req_id, stream.result(), None)
                finally:
                    with self._state_lock:
                        self._provider_active.pop(req_id, None)
            elif method == "tool_call":
                name = req.get("tool", "")
                params = req.get("args") or {}
                with self._tool_registration_lock:
                    handler = self._tool_handlers[name]
                # agent-loop.ts:619-647: the handlers of a batch start in source order.
                if ctx._tool_start is not None:
                    ctx._tool_start.begin()
                result = handler(ctx, params)
                payload = result if isinstance(result, dict) else {"content": str(result)}
                self._respond(req_id, payload, None)
            elif method == "tool_prepare_arguments":
                name = req.get("tool", "")
                with self._tool_registration_lock:
                    prepare = self._tool_prepare_handlers.get(name)
                if prepare is None:
                    raise RuntimeError(f"tool {name!r} has no prepare_arguments")
                prepared = prepare(req.get("args") or {})
                if not isinstance(prepared, dict):
                    raise TypeError("prepare_arguments must return a mapping")
                self._respond(req_id, prepared, None)
            elif method == "tool_prepare_loadout":
                name = req.get("tool", "")
                with self._tool_registration_lock:
                    hook = self._tool_loadout_handlers.get(name)
                if hook is None:
                    raise RuntimeError(f"tool {name!r} has no prepare_loadout")
                payload = req.get("args") or {}
                changes = hook(ToolLoadout(
                    declared=list(payload.get("declared") or []), callable=list(payload.get("callable") or []),
                    registered=list(payload.get("registered") or []), exposures=dict(payload.get("exposures") or {}),
                    namespaces=dict(payload.get("namespaces") or {}),
                ))
                if changes is not None and not isinstance(changes, dict):
                    raise TypeError("prepare_loadout must return a mapping or None")
                self._respond(req_id, changes, None)
            elif method == "execute_tool_update":
                # The host waits for this answer before it sends the next partial result or the call's outcome; an exception from on_update is the answer.
                args = req.get("args") or {}
                with self._state_lock:
                    nested = self._nested_calls.get(str(args.get("executeId") or ""))
                failure = nested.submit(args.get("result") or {}) if nested is not None else None
                self._respond(req_id, None, None if failure is None else {"message": str(failure)})
            elif method == "virtual_model_route":
                args = req.get("args") or {}
                provider, model_id = str(args.get("provider") or ""), str(args.get("id") or "")
                with self._tool_registration_lock:
                    route = self._virtual_model_routes.get((provider, model_id))
                if route is None:
                    raise RuntimeError(f"unknown virtual model {provider}/{model_id}")
                request = dict(args.get("request") or {})
                request["signal"] = ctx._cancelled
                routed = route(ctx, request)
                if not isinstance(routed, dict) or not isinstance(routed.get("model"), dict):
                    raise TypeError("virtual model route must return a mapping with a model and a thinkingLevel")
                if "state" in routed and routed["state"] is None:
                    # None is Python's spelling of upstream's undefined state, and of request.state before the first state: both keep the current state (virtual-models.ts:77-83) instead of storing null.
                    routed = {key: value for key, value in routed.items() if key != "state"}
                self._respond(req_id, routed, None)
            elif method == "events.dispatch":
                self._bus.dispatch(ctx, req.get("args") or {})
                self._respond(req_id, None, None)
            elif method == "command":
                name = req.get("tool", "")
                args = req.get("args") or ""
                self._command_handlers[name](ctx, args if isinstance(args, str) else json.dumps(args))
                self._respond(req_id, None, None)
            elif method == "command_argument_completions":
                name = req.get("tool", "")
                prefix = req.get("args") or ""
                items = self._command_completions[name](prefix if isinstance(prefix, str) else "")
                self._respond(req_id, list(items) if items else None, None)
            elif method == "terminal_input":
                # The host is blocked on this reply and upstream's handler is
                # synchronous, so handlers run inline. A raising handler
                # degrades to not-consumed rather than capturing the keystroke.
                original = (req.get("args") or {}).get("data") or ""
                current = original
                consume = False
                for h in self._terminal_input_handlers():
                    try:
                        consumes, data = _terminal_input_verdict(h(current))
                    except Exception:  # a failing handler does not stop the others  # nosec B112
                        continue
                    if consumes:
                        consume = True
                        break
                    if data is not None:
                        current = data
                verdict: dict[str, Any] = {"consume": consume}
                if not consume and current != original:
                    verdict["data"] = current
                self._respond(req_id, verdict, None)
            elif method == "event":
                handler_id = int(req.get("handler_id") or 0)
                handler = self._event_handlers.get(handler_id)
                if handler is None:
                    raise RuntimeError(f"unknown event handler {handler_id} for {req.get('event', '')}")
                data = req.get("args") or {}
                # pig additive (D19): preserve boundary mutations alongside handler errors.
                if req.get("event") in ("agent_before_settle", "turn_end"):
                    entries = data.get("entries", [])
                    result = None
                    error = None
                    try:
                        result = handler(ctx, data)
                    except Exception as exc:  # noqa: BLE001 - Preserve mutations and report the handler error.
                        error = {"message": str(exc)}
                    self._respond(req_id, {"_pigBoundaryEntries": entries, "_pigBoundaryResult": result}, error)
                    return
                if req.get("event") == "before_agent_start":
                    options = data["systemPromptOptions"]
                    # Pi's normalized options always carry every collection; the host omits an empty one.
                    options.setdefault("selectedTools", [])
                    options.setdefault("toolSnippets", {})
                    options.setdefault("toolGuidelines", {})
                    options.setdefault("promptGuidelines", [])
                    options.setdefault("contextFiles", [])
                    options.setdefault("skills", [])
                    result = None
                    error = None
                    try:
                        result = handler(ctx, data)
                    except Exception as exc:  # noqa: BLE001 - Preserve section mutations before reporting the handler error.
                        error = {"message": str(exc)}
                    self._respond(
                        req_id,
                        {"_pigPromptSections": options["sections"], "_pigPromptSelectedTools": options.get("selectedTools"), "_pigPromptOptions": options, "_pigPromptResult": result},
                        error,
                    )
                    return
                messages = data.get("messages")
                snapshot = list(messages) if isinstance(messages, list) else None
                result = handler(ctx, data)
                if req.get("event") == "user_bash":
                    result = _user_bash_event_result(result)
                if req.get("event") in {"context", "context_with_system"} and snapshot is not None:
                    returned = result.get("messages") if isinstance(result, dict) else None
                    if returned is None:
                        returned = messages
                    result = {"messages": returned, "_pigContextUnchanged": len(returned) == len(snapshot) and all(a is b for a, b in zip(returned, snapshot))}
                self._respond(req_id, result, None)
            elif method == "shortcut":
                key = req.get("tool", "")
                self._shortcut_handlers[key](ctx)
                self._respond(req_id, None, None)
            elif method == "render_message":
                custom_type = req.get("tool", "")
                args = req.get("args") or {}
                lines = self._renderer_handlers[custom_type](ctx, args.get("message") or {}, args.get("options") or {}, int(args.get("width") or 0))
                self._respond(req_id, {"lines": lines}, None)
            elif method == "render_tool":
                lines = self._render_tool(ctx, req.get("tool", ""), req.get("args") or {})
                self._respond(req_id, {"lines": lines}, None)
            elif method == "markdown_transform":
                args = req.get("args") or {}
                transformed = None
                if self._markdown_transformer is not None:
                    try:
                        result = self._markdown_transformer(str(args.get("markdown") or ""), args.get("context") or {})
                        transformed = result if isinstance(result, str) else None
                    except Exception:  # noqa: BLE001 - Pi keeps the Markdown when a transformer throws.
                        transformed = None
                self._respond(req_id, transformed, None)
            elif method == "render_entry":
                custom_type = req.get("tool", "")
                args = req.get("args") or {}
                lines = self._entry_renderer_handlers[custom_type](ctx, args.get("entry") or {}, args.get("options") or {}, int(args.get("width") or 0))
                self._respond(req_id, {"lines": lines}, None)
            elif method in _OAUTH_METHODS:
                self._dispatch_oauth(req_id, req)
            else:
                self._respond(req_id, None, {"message": f"unknown method: {method}"})
        except Exception as exc:  # noqa: BLE001 - SDK boundary reports handler errors.
            self._respond(req_id, None, {"message": str(exc)})
        finally:
            if ctx._tool_start is not None:
                ctx._tool_start.end()
            with self._state_lock:
                self._active.pop(req_id, None)
                self._request_parents.pop(req_id, None)

    def _dispatch_oauth(self, req_id: str, req: dict[str, Any]) -> None:
        name = req.get("tool", "")
        provider = self._oauth_providers.get(name)
        if provider is None:
            self._respond(req_id, None, {"message": f"unknown oauth provider: {name}"})
            return
        method = req.get("method")
        if method == "oauth_login":
            if provider.login is None:
                self._respond(req_id, None, {"message": "provider does not support login"})
                return
            creds = provider.login(OAuthLoginCallbacks(self, req_id))
            self._respond(req_id, creds._to_wire(), None)
        elif method == "oauth_refresh":
            if provider.refresh_token is None:
                self._respond(req_id, None, {"message": "provider does not support refresh"})
                return
            creds = provider.refresh_token(OAuthCredentials._from_wire(req.get("args")))
            self._respond(req_id, creds._to_wire(), None)
        elif method == "oauth_get_api_key":
            resolved = OAuthCredentials._from_wire(req.get("args"))
            key = provider.get_api_key(resolved) if provider.get_api_key else ""
            self._respond(req_id, {"apiKey": key}, None)
        elif method == "oauth_credential_status":
            store = provider.credential_store
            if store is None:
                self._respond(req_id, None, {"message": "provider has no credential store"})
                return
            status = store.credential_status()
            self._respond(
                req_id,
                {"present": status.present, "authType": status.auth_type, "source": status.source},
                None,
            )
        elif method == "oauth_store_credentials":
            store = provider.credential_store
            if store is None:
                self._respond(req_id, None, {"message": "provider has no credential store"})
                return
            path = store.store_credentials(OAuthCredentials._from_wire(req.get("args")))
            self._respond(req_id, {"path": path}, None)
        elif method == "oauth_delete_credentials":
            store = provider.credential_store
            if store is None:
                self._respond(req_id, None, {"message": "provider has no credential store"})
                return
            deleted = store.delete_credentials()
            self._respond(req_id, {"deleted": deleted}, None)
        else:
            self._respond(req_id, None, {"message": f"unknown oauth method: {method}"})

    @staticmethod
    def _read_session_entries(path: str) -> list[dict[str, Any]]:
        if not path:
            return []
        entries: list[dict[str, Any]] = []
        try:
            with open(path, encoding="utf-8") as session_file:
                for line in session_file:
                    if len(line.encode("utf-8")) > MAX_FRAME_SIZE:
                        return []
                    entry = json.loads(line)
                    if entry.get("type") != "session":
                        entries.append(entry)
        except (OSError, ValueError, TypeError):
            return []
        return entries

    def _ensure_session_log(self) -> None:
        """Enrol this extension in session-log replication on its first read.

        Blocks until the log is installed so the readers above stay
        synchronous. The host withholds the log until asked, because
        replicating a large session into every loaded extension costs each of
        them the whole log in resident memory for data most never inspect.
        """
        with self._session_sub_lock:
            if self._session_mirror.subscribed:
                if self._session_sub_error is not None:
                    raise self._session_sub_error
                return
            # Set before the call: the host starts sending the log as soon as
            # it registers the subscription, and those pushes must be applied.
            self._session_mirror.subscribed = True
            try:
                self._subscribe_session_log()
            except Exception as error:  # a failed attempt is not retried, so every read reports it
                self._session_sub_error = error
                raise

    def _subscribe_session_log(self) -> None:
        """Install the session log; raises on a host failure."""
        entries = self._read_session_entries(self._session_file)
        cursor = len(entries)
        leaf_id = ""
        while True:
            requested_cursor = cursor
            result = self._call("watchSessionLog", {"cursor": cursor}).get("result")
            if not isinstance(result, dict):
                raise HostCallError("host reply to watchSessionLog is not an object", "invalid_reply")
            page = result.get("entries")
            page_entries = page if isinstance(page, list) else []
            next_cursor = int(result.get("entryCount", cursor))
            if next_cursor - len(page_entries) != requested_cursor:
                entries = []
            entries.extend(page_entries)
            cursor = next_cursor
            leaf_id = result.get("leafId", leaf_id) or leaf_id
            if not result.get("hasMore", False):
                break
        self._session_mirror.seed(entries, leaf_id)

        while True:
            result = self._call(
                "watchSessionLog", {"cursor": cursor, "complete": True}
            ).get("result")
            if not isinstance(result, dict):
                raise HostCallError("host reply to watchSessionLog is not an object", "invalid_reply")
            page = result.get("entries")
            page_entries = page if isinstance(page, list) else []
            cursor = int(result.get("entryCount", cursor))
            leaf_id = result.get("leafId", leaf_id) or leaf_id
            self._session_mirror.apply_update(
                {
                    "entriesAppended": page_entries,
                    "entryCount": cursor,
                    "leafId": leaf_id,
                }
            )
            if not result.get("hasMore", False) and not page_entries:
                return

    def _call(self, method: str, args: Any = None, parent_request_id: str = "", parent: _RequestParent | None = None) -> dict[str, Any]:
        call_id, event = self._begin_call(method, args, parent_request_id, parent)
        return self._wait_call(call_id, event, method)

    def _begin_call(self, method: str, args: Any = None, parent_request_id: str = "", parent: _RequestParent | None = None, event: threading.Event | None = None) -> tuple[str, threading.Event]:
        event = event if event is not None else threading.Event()
        with self._write_lock:
            with self._state_lock:
                if self._shutdown.is_set() or (parent is not None and parent.socket is not self._sock):
                    raise RuntimeError("extension connection is closed or replaced")
                if parent is not None:
                    if parent.state == "cancelled":
                        raise RuntimeError("host call cancelled with its parent request")
                    parent_request_id = "" if parent.state == "completed" else parent.request_id
                self._call_id += 1
                call_id = f"c{self._call_id}"
                self._pending[call_id] = event
                if method in _MCP_REGISTRY_CALLS:
                    self._mcp_calls.add(call_id)
                if parent_request_id:
                    self._pending_parents[call_id] = parent_request_id
            call = {"method": method, "args": args}
            if parent_request_id:
                call["parent_request_id"] = parent_request_id
            try:
                self._send_locked({"type": "call", "id": call_id, "call": call})
            except Exception:
                with self._state_lock:
                    self._pending.pop(call_id, None)
                    self._pending_parents.pop(call_id, None)
                    self._mcp_calls.discard(call_id)
                raise
        return call_id, event

    def _wait_call(self, call_id: str, event: threading.Event, method: str) -> dict[str, Any]:
        event.wait()
        with self._state_lock:
            result = self._pending_results.pop(call_id, {})
            self._pending.pop(call_id, None)
            self._pending_parents.pop(call_id, None)
            cancelled = call_id in self._cancelled_calls
            self._cancelled_calls.discard(call_id)
        if cancelled:
            raise RuntimeError(f"host call {method} cancelled with its parent request")
        if self._shutdown.is_set() and not result:
            raise RuntimeError("extension shut down during host call")
        if result.get("error"):
            err = result["error"]
            raise HostCallError(err.get("message", "host call failed"), err.get("code"))
        return result

    def _notify(self, method: str, args: Any = None) -> None:
        self._send({"type": "notify", "notify": {"method": method, "args": args}})

    def _render_remote_component(self, key: str, overlay: _RemoteOverlayState) -> None:
        with self._state_lock:
            width = self._width
        lines = [str(line) for line in overlay.component.render(width)]
        overlay.last_render = time.monotonic()
        if lines == overlay.last_lines:
            return
        overlay.last_lines = list(lines)
        overlay.seq += 1
        self._notify("ui.custom.render", {"key": key, "lines": lines, "width": width, "seq": overlay.seq})

    def _remote_component_worker(self, key: str, overlay: _RemoteOverlayState) -> None:
        while overlay.active.is_set():
            kind, data = overlay.next_event()
            if kind == "stop" or not overlay.active.is_set():
                return
            try:
                if kind == "input":
                    result = overlay.component.handle_input(data or "")
                    if result.done:
                        overlay.active.clear()
                        self._notify("ui.custom.close", {"key": key, "result": result.value})
                        return
                else:
                    delay = 0.016 - (time.monotonic() - overlay.last_render)
                    if delay > 0:
                        time.sleep(delay)
                    if not overlay.active.is_set():
                        return
                self._render_remote_component(key, overlay)
            except Exception as exc:  # noqa: BLE001 - isolate one component
                overlay.active.clear()
                try:
                    self._notify("ui.custom.close", {"key": key, "error": str(exc)})
                except Exception:  # noqa: BLE001 - the transport may already be gone  # nosec B110
                    pass
                return

    def _run_remote_component(self, component: RemoteComponent, options: dict[str, Any], parent_request_id: str = "", parent: _RequestParent | None = None) -> Any:
        args = dict(options)
        with self._overlay_lock:
            self._overlay_seq += 1
            key = f"custom-{self._overlay_seq}"
            overlay = _RemoteOverlayState(component)
            self._overlays[key] = overlay
        args["key"] = key
        try:
            call_id, event = self._begin_call("ui.custom", args, parent_request_id, parent)
        except Exception:
            with self._overlay_lock:
                self._overlays.pop(key, None)
            _dispose_remote_component(component)
            raise

        overlay_ref = weakref.ref(overlay)
        set_invalidate = getattr(component, "set_invalidate", None)
        if callable(set_invalidate):
            def request_render() -> None:
                current = overlay_ref()
                if current is not None:
                    current.request_render()
            try:
                set_invalidate(request_render)
            except Exception as exc:
                try:
                    self._notify("ui.custom.close", {"key": key, "error": f"attach focused invalidation: {exc}"})
                    try:
                        self._wait_call(call_id, event, "ui.custom")
                    except Exception:  # the attachment error remains primary  # nosec B110
                        pass
                finally:
                    with self._overlay_lock:
                        self._overlays.pop(key, None)
                    overlay.stop()
                    _dispose_remote_component(component)
                raise RuntimeError(f"attach focused invalidation: {exc}") from exc
        overlay.worker = threading.Thread(
            target=self._remote_component_worker,
            args=(key, overlay),
            name=f"pig-overlay-{key}",
            daemon=True,
        )
        try:
            overlay.worker.start()
        except Exception as exc:
            try:
                self._notify("ui.custom.close", {"key": key, "error": f"start focused component worker: {exc}"})
                try:
                    self._wait_call(call_id, event, "ui.custom")
                except Exception:  # the start error remains primary  # nosec B110
                    pass
            finally:
                with self._overlay_lock:
                    self._overlays.pop(key, None)
                overlay.stop()
                _dispose_remote_component(component)
            raise RuntimeError(f"start focused component worker: {exc}") from exc
        overlay.request_render()

        response: dict[str, Any] = {}
        operation_error: Exception | None = None
        try:
            response = self._wait_call(call_id, event, "ui.custom").get("result") or {}
        except Exception as exc:  # preserve the host/transport error after cleanup
            operation_error = exc

        with self._overlay_lock:
            self._overlays.pop(key, None)
        overlay.stop()
        overlay.worker.join(timeout=1.0)
        cleanup_timed_out = overlay.worker.is_alive()
        if not cleanup_timed_out:
            _dispose_remote_component(component)

        if operation_error is not None:
            raise operation_error
        if cleanup_timed_out:
            raise RuntimeError("focused component did not stop before the cleanup deadline")
        return response.get("result") if response.get("ok") else None

    def _push_widget(self, key: str, lines: list[str]) -> None:
        self._send({"type": "widget_push", "widget_push": {"key": key, "lines": lines}})

    def _terminal_input_handlers(self) -> list[Any]:
        with self._state_lock:
            return [h for _, h in self._term_input]

    def _add_terminal_input_handler(self, handler: Any) -> Callable[[], None]:
        with self._state_lock:
            self._term_input_seq += 1
            sub_id = self._term_input_seq
            self._term_input.append((sub_id, handler))
            first = len(self._term_input) == 1
        if first:
            self._call("ui.onTerminalInput", {})

        done = threading.Event()

        def unsubscribe() -> None:
            if done.is_set():
                return
            done.set()
            with self._state_lock:
                self._term_input = [t for t in self._term_input if t[0] != sub_id]
                last = not self._term_input
            if last:
                self._call("ui.offTerminalInput", {})

        return unsubscribe

    def _replace_surface(self, method: str, surface: "_SurfaceRenderer | None") -> None:
        """Retire the renderer installed for method and install surface (None for none)."""
        with self._surface_lock:
            previous = self._surfaces.pop(method, None)
            if surface is not None:
                self._surfaces[method] = surface
        if previous is not None:
            previous.stop()

    def _refresh_surfaces(self) -> None:
        with self._surface_lock:
            surfaces = list(self._surfaces.values())
        ctx = Context(self)
        for surface in surfaces:
            surface.refresh(ctx)

    def _add_width_change_handler(self, handler: Any) -> Callable[[], None]:
        with self._state_lock:
            self._width_change_seq += 1
            sub_id = self._width_change_seq
            self._width_change.append((sub_id, handler))

        done = threading.Event()

        def unsubscribe() -> None:
            if done.is_set():
                return
            done.set()
            with self._state_lock:
                self._width_change = [w for w in self._width_change if w[0] != sub_id]

        return unsubscribe

    def _cancel_parent_calls_locked(self, req_id: str) -> list[threading.Event]:
        events = []
        for call_id, parent_id in list(self._pending_parents.items()):
            if parent_id != req_id:
                continue
            event = self._pending.pop(call_id, None)
            self._pending_parents.pop(call_id, None)
            self._pending_results.pop(call_id, None)
            self._cancelled_calls.add(call_id)
            if event is not None:
                events.append(event)
        return events

    def _respond(self, req_id: str, result: Any, error: dict[str, Any] | None) -> None:
        with self._write_lock:
            with self._state_lock:
                parent = self._request_parents.pop(req_id, None)
                self._active.pop(req_id, None)
                if parent is not None:
                    parent.finished = True
                    if parent.state == "active":
                        parent.state = "completed"
                # Pi's host calls belong to no request: a call the handler left running when it returned continues until it ends. A cancelled request's calls were cancelled by the cancel frame. A completed request's pending calls are no longer scoped to it, so a later cancel frame for it does not reach them.
                for call_id, parent_id in list(self._pending_parents.items()):
                    if parent_id == req_id:
                        self._pending_parents[call_id] = ""
            self._send_locked({"type": "request_state", "request_state": {"request_id": req_id, "state": "completed"}})
            self._send_locked({"type": "response", "id": req_id, "response": {"result": result, "error": error}})

    def _request_state(self, req_id: str, state: str, reason: str | None = None, parent: _RequestParent | None = None) -> None:
        payload = {"request_id": req_id, "state": state}
        if reason:
            payload["reason"] = reason
        with self._write_lock:
            if parent is not None:
                with self._state_lock:
                    if parent.finished or (parent.state == "cancelled" and state != "progress") or parent.socket is not self._sock:
                        return
            self._send_locked({"type": "request_state", "request_state": payload})

    def _send(self, env: dict[str, Any]) -> None:
        with self._write_lock:
            self._send_locked(env)

    def _send_locked(self, env: dict[str, Any]) -> None:
        if self._sock is None:
            raise RuntimeError("extension socket is not connected")
        data = json.dumps(env, separators=(",", ":")).encode()
        if len(data) > MAX_FRAME_SIZE:
            raise RuntimeError(f"frame too large: {len(data)} bytes exceeds {MAX_FRAME_SIZE}")
        self._sock.sendall(struct.pack(">I", len(data)))
        self._sock.sendall(data)

    def _read(self) -> dict[str, Any]:
        if self._sock is None:
            raise RuntimeError("extension socket is not connected")
        hdr = self._read_exact(4)
        if not hdr:
            raise EOFError
        size = struct.unpack(">I", hdr)[0]
        if size > MAX_FRAME_SIZE:
            raise RuntimeError(f"frame too large: {size}")
        return json.loads(self._read_exact(size).decode())

    def _read_exact(self, n: int) -> bytes:
        if self._sock is None:
            raise RuntimeError("not connected")
        chunks = bytearray()
        while len(chunks) < n:
            chunk = self._sock.recv(n - len(chunks))
            if not chunk:
                if not chunks:
                    return b""
                raise EOFError
            chunks.extend(chunk)
        return bytes(chunks)
