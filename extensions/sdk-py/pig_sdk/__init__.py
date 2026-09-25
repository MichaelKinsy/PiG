"""Pig subprocess extension SDK for Python.

Factory-style Python extensions should expose a function such as::

    def new_extension() -> pig_sdk.Extension: ...

Generated standalone and packed runners call that factory and then run the
returned extension over a host-provided subprocess socket.
"""

from __future__ import annotations

import json
import os
import queue
import socket
import struct
import threading
import time
import weakref
from collections import deque
from dataclasses import dataclass, field
from typing import Any, Callable, Literal, NotRequired, Protocol, TypeAlias, TypedDict

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

Schema: TypeAlias = dict[str, Any]
ToolPrepareArguments: TypeAlias = Callable[[dict[str, Any]], dict[str, Any]]
ToolHandler: TypeAlias = Callable[["Context", dict[str, Any]], Any]
CommandHandler: TypeAlias = Callable[["Context", str], None]
EventHandler: TypeAlias = Callable[["Context", dict[str, Any]], Any]


class ProjectTrustResult(TypedDict):
    trusted: Literal["yes", "no", "undecided"]
    remember: NotRequired[bool]


ProjectTrustHandler: TypeAlias = Callable[["Context", dict[str, Any]], ProjectTrustResult]
ShortcutHandler: TypeAlias = Callable[["Context"], None]
RendererHandler: TypeAlias = Callable[["Context", dict[str, Any], dict[str, Any], int], list[str]]
Factory: TypeAlias = Callable[[], "Extension"]


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
            return False

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


class ModelEventStream:
    def __init__(self) -> None:
        self._condition = threading.Condition()
        self._events: deque[dict[str, Any]] = deque()
        self._terminal = False
        self._result: dict[str, Any] | None = None

    def push(self, event: dict[str, Any]) -> None:
        with self._condition:
            if self._terminal:
                return
            self._events.append(event)
            if event.get("type") in {"done", "error"}:
                self._terminal = True
                self._result = event.get("message") if event.get("type") == "done" else event.get("error")
            self._condition.notify_all()

    def events(self):
        while True:
            with self._condition:
                while not self._events and not self._terminal:
                    self._condition.wait()
                if self._events:
                    event = self._events.popleft()
                else:
                    return
            yield event

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
                self._context._call("modelStream", {"streamId": stream_id, "model": model, "request": payload})
            except Exception as exc:  # noqa: BLE001 - transport failure becomes terminal model error
                stream.push(_model_stream_error_event(exc, model))
            finally:
                with extension._model_stream_lock:
                    extension._model_streams.pop(stream_id, None)

        threading.Thread(target=run, daemon=True).start()
        return stream

    def stream_simple(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> ModelEventStream:
        return self.stream(model, request, options)

    def complete(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any] | None:
        return self.stream(model, request, options).result()


@dataclass
class Context:
    extension: "Extension"
    tool_call_id: str | None = None
    request_id: str = ""
    _cancelled: threading.Event | None = None
    _cancel_reason: str | None = None
    _reason_provider: Callable[[], str | None] | None = None

    @property
    def model_registry(self) -> ModelRegistry:
        return ModelRegistry(self)

    def is_cancelled(self) -> bool:
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

    def _call(self, method: str, args: Any = None) -> dict[str, Any]:
        if method not in {"ui.select", "ui.confirm", "ui.input", "ui.editor", "ui.custom"} and self.request_id:
            self.extension._request_state(self.request_id, "blocked", "host_call")
        try:
            return self.extension._call(method, args, self.request_id)
        finally:
            if self.request_id:
                self.extension._request_state(self.request_id, "progress")

    def _block_for_user(self) -> None:
        if self.request_id:
            self.extension._request_state(self.request_id, "blocked", "user")

    def call_host(self, method: str, args: Any = None) -> Any:
        return self._call(method, args).get("result")

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

    def select(self, title: str, options: list[str]) -> tuple[str, bool]:
        self._block_for_user()
        result = self._call("ui.select", {"title": title, "options": options}).get("result") or {}
        return str(result.get("selected") or ""), bool(result.get("ok"))

    def confirm(self, title: str, message: str) -> bool:
        self._block_for_user()
        result = self._call("ui.confirm", {"title": title, "message": message}).get("result") or {}
        return bool(result.get("confirmed"))

    def input(self, title: str, placeholder: str = "") -> tuple[str, bool]:
        self._block_for_user()
        result = self._call("ui.input", {"title": title, "placeholder": placeholder}).get("result") or {}
        return str(result.get("text") or ""), bool(result.get("ok"))

    def editor(self, title: str, prefill: str = "") -> tuple[str, bool]:
        self._block_for_user()
        result = self._call("ui.editor", {"title": title, "prefill": prefill}).get("result") or {}
        return str(result.get("text") or ""), bool(result.get("ok"))

    # Message injection ------------------------------------------------------

    def send_message(self, custom_type: str, content: Any, display: bool = True, trigger_turn: bool | None = None, deliver_as: str | None = None) -> None:
        """Inject a custom message. ``trigger_turn`` and ``deliver_as`` are
        optional, as upstream's are: None is sent as unset and the host applies
        upstream's default for the session's state."""
        options: dict[str, Any] = {}
        if trigger_turn is not None:
            options["triggerTurn"] = trigger_turn
        if deliver_as:
            options["deliverAs"] = deliver_as
        self._call("sendMessage", {"message": {"customType": custom_type, "content": content, "display": display}, "options": options})

    def send_user_message(self, content: str | list[dict[str, Any]], deliver_as: str = "followUp") -> None:
        """Send text or a list of text/image content blocks as a user message."""
        self._call("sendUserMessage", {"content": content, "options": {"deliverAs": deliver_as}})

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

    def get_editor_text(self) -> str:
        return str((self._call("ui.getEditorText").get("result") or {}).get("text") or "")

    def set_editor_text(self, text: str) -> None:
        self._call("ui.setEditorText", {"text": text})

    def paste_to_editor(self, text: str) -> None:
        self._call("ui.pasteToEditor", {"text": text})

    def get_session_name(self) -> str:
        return str((self._call("getSessionName").get("result") or {}).get("name") or "")

    def set_session_name(self, name: str) -> None:
        self._call("setSessionName", {"name": name})

    def set_label(self, entry_id: str, label: str) -> None:
        self._call("setLabel", {"entryId": entry_id, "label": label})

    def get_flag(self, name: str) -> Any:
        result = self._call("getFlag", {"name": name}).get("result") or {}
        if "value" in result:
            return result["value"]
        return self.extension._flag_defaults.get(name)

    def get_thinking_level(self) -> str:
        return str((self._call("getThinkingLevel").get("result") or {}).get("level") or "")

    def set_thinking_level(self, level: str) -> None:
        self._call("setThinkingLevel", {"level": level})

    def set_model(self, model: str) -> tuple[bool, str]:
        result = self._call("setModel", {"model": model}).get("result") or {}
        return bool(result.get("success", result.get("ok", False))), str(result.get("error") or "")

    # Tools/commands/context -------------------------------------------------

    def get_active_tools(self) -> list[str]:
        return list((self._call("getActiveTools").get("result") or {}).get("tools") or [])

    def get_all_tools(self) -> list[dict[str, Any]]:
        return list((self._call("getAllTools").get("result") or {}).get("tools") or [])

    def set_active_tools(self, tools: list[str]) -> None:
        self._call("setActiveTools", {"tools": tools})

    def refresh_tools(self) -> None:
        self._call("refreshTools")

    def get_commands(self) -> list[dict[str, Any]]:
        return list((self._call("getCommands").get("result") or {}).get("commands") or [])

    def get_context_usage(self) -> dict[str, Any] | None:
        result = self._call("getContextUsage").get("result")
        return result if isinstance(result, dict) and result else None

    def get_system_prompt(self) -> str:
        return str((self._call("getSystemPrompt").get("result") or {}).get("prompt") or "")

    def get_system_prompt_options(self) -> dict[str, Any]:
        """Base inputs pi currently uses to build the system prompt
        (customPrompt, selectedTools, toolSnippets, promptGuidelines,
        appendSystemPrompt, cwd, contextFiles, skills). Reports current
        base inputs only, not per-turn before_agent_start changes. May
        include full context-file contents; treat as sensitive."""
        result = self._call("getSystemPromptOptions").get("result")
        return result if isinstance(result, dict) else {}

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

    def get_all_themes(self) -> list[dict[str, Any]]:
        return list((self._call("ui.getAllThemes").get("result") or {}).get("themes") or [])

    def get_theme(self, name: str) -> Any:
        return (self._call("ui.getTheme", {"name": name}).get("result") or {}).get("theme")

    def set_theme(self, name: str) -> tuple[bool, str]:
        result = self._call("ui.setTheme", {"theme": name}).get("result") or {}
        return bool(result.get("success", result.get("ok", False))), str(result.get("error") or "")

    def set_widget(self, key: str, content: Any, options: dict[str, Any] | None = None) -> None:
        if isinstance(content, list) and all(isinstance(x, str) for x in content) and options is None:
            self.extension._push_widget(key, content)
            return
        self._call("ui.setWidget", {"key": key, "content": content, "options": options or {}})

    def clear_footer(self) -> None:
        self._call("ui.setFooter", {"clear": True})

    def set_login(self, definition: LoginDefinition) -> None:
        """Set the shared header from a native login definition.

        Pig validates the canonical definition. A rejected definition raises
        :class:`HostCallError` and leaves the current header unchanged.
        """
        self._call("ui.setLogin", definition._to_wire())

    def clear_header(self) -> None:
        self._call("ui.setHeader", {"clear": True})

    def clear_editor_component(self) -> None:
        self._call("ui.setEditorComponent", {"clear": True})

    def custom(
        self,
        component: RemoteComponent | dict[str, Any] | None = None,
        options: dict[str, Any] | None = None,
    ) -> Any:
        """Open a focused subprocess component.

        Passing the legacy options-only shape retains the host's explicit
        unsupported response because it has no serializable component.
        """
        self._block_for_user()
        if component is None or isinstance(component, dict):
            raw_options = component if isinstance(component, dict) else options
            return self._call("ui.custom", raw_options or {}).get("result")
        result = self.extension._run_remote_component(component, options or {}, self.request_id)
        if self.request_id:
            self.extension._request_state(self.request_id, "progress")
        return result

    def add_autocomplete_provider(self) -> None:
        self._call("ui.addAutocompleteProvider", {})

    def on_terminal_input(
        self, handler: "Callable[[str], TerminalInputResult | dict[str, Any] | None]"
    ) -> "Callable[[], None]":
        """Subscribe to raw terminal input, receiving every chunk before the editor.

        The host is told to start forwarding only on the first subscription and
        to stop on the last, so an extension that never subscribes costs the
        input loop nothing. The host blocks on each verdict, so a handler must
        return promptly. Returns an idempotent unsubscribe callable.
        """
        if not callable(handler):
            raise TypeError("on_terminal_input requires a callable handler")
        return self.extension._add_terminal_input_handler(handler)

    def on_width_change(self, handler: "Callable[[int], None]") -> "Callable[[], None]":
        """Subscribe to terminal resizes, receiving the new width.

        Upstream Pi installs headers and footers as component factories whose
        render(width) runs every frame, so they follow a resize with no work
        from the extension. A pig extension is a subprocess and sends static
        lines instead, so a footer keeps the width it was built for until
        something re-pushes it. This is that trigger.

        The handler is called after ``width()`` is updated, so it observes the
        new value. Handlers run on the message loop and must not block: re-push
        the lines and return. Returns an idempotent unsubscribe callable.
        """
        if not callable(handler):
            raise TypeError("on_width_change requires a callable handler")
        return self.extension._add_width_change_handler(handler)

    def get_tools_expanded(self) -> bool:
        return bool((self._call("ui.getToolsExpanded").get("result") or {}).get("expanded"))

    def set_tools_expanded(self, expanded: bool) -> None:
        self._call("ui.setToolsExpanded", {"expanded": expanded})

    # Session identity -------------------------------------------------------

    def get_session_id(self) -> str:
        """The current session's id, or "" when the host does not answer."""
        return str((self._call("getSessionID").get("result") or {}).get("id") or "")

    def get_session_file(self) -> str:
        """Path to the current session's file, or "" when unavailable."""
        if self.extension._session_file:
            return self.extension._session_file
        return str((self._call("getSessionFile").get("result") or {}).get("path") or "")

    def get_leaf_id(self) -> str:
        """Id of the current branch leaf entry, or "" when unavailable."""
        return str((self._call("getLeafID").get("result") or {}).get("id") or "")

    # Shell ------------------------------------------------------------------

    def exec(self, command: str, args: list[str] | None = None) -> ExecResult:
        """Run a command through the host's executor.

        Raises RuntimeError when the host reports a failure, so a caller sees
        the reason rather than an exit code of zero it never produced.
        """
        response = self._call("exec", {"command": command, "args": args or []})
        error = response.get("error")
        if error:
            raise RuntimeError(f"{error.get('code')}: {error.get('message')}")
        result = response.get("result") or {}
        return ExecResult(
            stdout=str(result.get("stdout") or ""),
            stderr=str(result.get("stderr") or ""),
            exit_code=int(result.get("code") or 0),
        )

    # Agent/session control --------------------------------------------------

    def is_project_trusted(self) -> bool:
        """Whether the current project is trusted.

        Untrusted projects have project-scoped settings and hooks disabled.
        Defaults to trusted when the host does not answer, matching the
        upstream runner.
        """
        return bool((self._call("isProjectTrusted").get("result") or {}).get("trusted", True))

    def is_idle(self) -> bool:
        return bool((self._call("isIdle").get("result") or {}).get("idle", True))

    def abort(self) -> None:
        self._call("abort")

    def has_pending_messages(self) -> bool:
        return bool((self._call("hasPendingMessages").get("result") or {}).get("pending"))

    def shutdown(self) -> None:
        self._call("shutdown")

    def compact(self, opts: dict[str, Any] | None = None) -> None:
        self._call("compact", opts or {})

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
    """A set of OAuth credentials. Serializes to the wire shape shared with the host."""

    refresh: str = ""
    access: str = ""
    expires: int = 0
    project_id: str = ""

    def _to_wire(self) -> dict[str, Any]:
        wire: dict[str, Any] = {"refresh": self.refresh, "access": self.access, "expires": self.expires}
        if self.project_id:
            wire["projectId"] = self.project_id
        return wire

    @staticmethod
    def _from_wire(data: dict[str, Any] | None) -> "OAuthCredentials":
        data = data or {}
        return OAuthCredentials(
            refresh=data.get("refresh", ""),
            access=data.get("access", ""),
            expires=int(data.get("expires", 0) or 0),
            project_id=data.get("projectId", ""),
        )


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


class Extension:
    def __init__(self, name: str):
        self._name = name
        self._tools: list[dict[str, Any]] = []
        # Raw terminal-input handlers, consulted synchronously while the host
        # holds the user's keystroke. Empty means the host never round-trips.
        self._term_input: list[tuple[int, Any]] = []
        self._term_input_seq = 0
        self._width_change: list[tuple[int, Any]] = []
        self._width_change_seq = 0
        self._commands: list[dict[str, Any]] = []
        self._shortcuts: list[dict[str, Any]] = []
        self._handlers: list[dict[str, Any]] = []
        self._flags: list[dict[str, Any]] = []
        self._providers: list[dict[str, Any]] = []
        self._renderers: list[dict[str, Any]] = []
        self._entry_renderers: list[dict[str, Any]] = []
        self._oauth_providers: dict[str, OAuthProvider] = {}
        self._tool_handlers: dict[str, ToolHandler] = {}
        self._tool_prepare_handlers: dict[str, ToolPrepareArguments] = {}
        self._command_handlers: dict[str, CommandHandler] = {}
        self._event_handlers: dict[int, EventHandler] = {}
        self._shortcut_handlers: dict[str, ShortcutHandler] = {}
        self._renderer_handlers: dict[str, RendererHandler] = {}
        self._entry_renderer_handlers: dict[str, RendererHandler] = {}
        self._flag_defaults: dict[str, Any] = {}
        self._session_name: str = ""
        self._session_file: str = ""
        self._cwd: str = ""
        self._mode: str = ""
        self._width: int = 0
        self._height: int = 0
        self._model: str = ""
        self._sock: socket.socket | None = None
        self._write_lock = threading.Lock()
        self._state_lock = threading.Lock()
        self._session_mirror = SessionMirror()
        # Serializes the one-time session-log subscribe so concurrent first
        # readers make a single host call.
        self._session_sub_lock = threading.Lock()
        self._pending: dict[str, threading.Event] = {}
        self._pending_results: dict[str, dict[str, Any]] = {}
        self._pending_parents: dict[str, str] = {}
        self._cancelled_calls: set[str] = set()
        self._call_id = 0
        self._active: dict[str, tuple[threading.Event, str | None]] = {}
        self._request_threads: set[threading.Thread] = set()
        self._request_threads_lock = threading.Lock()
        self._overlay_seq = 0
        self._model_stream_seq = 0
        self._model_streams: dict[str, ModelEventStream] = {}
        self._model_stream_lock = threading.Lock()
        self._overlays: dict[str, _RemoteOverlayState] = {}
        self._overlay_lock = threading.Lock()
        self._shutdown = threading.Event()

    @property
    def name(self) -> str:
        return self._name

    def tool(self, name: str, description: str, schema: Schema, handler: ToolHandler, *, prompt_guidelines: list[str] | None = None, source: str | None = None, constrained_sampling: dict | None = None, prepare_arguments: ToolPrepareArguments | None = None) -> None:
        _ensure_jsonable(schema, f"tool schema for {name}")
        td: dict = {"name": name, "description": description, "parameters": schema}
        if constrained_sampling is not None:
            _ensure_jsonable(constrained_sampling, f"constrained_sampling for {name}")
            td["constrained_sampling"] = constrained_sampling
        if prompt_guidelines:
            td["prompt_guidelines"] = prompt_guidelines
        if source:
            td["source"] = source
        self._tools.append(td)
        self._tool_handlers[name] = handler
        if prepare_arguments is not None:
            self._tool_prepare_handlers[name] = prepare_arguments

    def command(self, name: str, description: str, handler: CommandHandler) -> None:
        self._commands.append({"name": name, "description": description})
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
        self._flag_defaults[name] = default

    def register_provider(self, name: str, config: dict[str, Any]) -> None:
        _ensure_jsonable(config, f"provider config for {name}")
        self._providers.append({"name": name, "config": config})

    def unregister_provider(self, name: str) -> None:
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
        self._providers.append({"name": name, "config": config})
        self._oauth_providers[name] = provider

    def message_renderer(self, custom_type: str, handler: RendererHandler) -> None:
        self._renderers.append({"custom_type": custom_type})
        self._renderer_handlers[custom_type] = handler

    def entry_renderer(self, custom_type: str, handler: RendererHandler) -> None:
        self._entry_renderers.append({"custom_type": custom_type})
        self._entry_renderer_handlers[custom_type] = handler

    def on_project_trust(self, handler: ProjectTrustHandler) -> None:
        """Register an awaited pre-runtime project_trust handler."""
        self.on_event("project_trust", handler)

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
        self._send({"type": "register", "register": {"name": self._name, "tools": self._tools, "commands": self._commands, "shortcuts": self._shortcuts, "handlers": self._handlers, "flags": self._flags, "providers": self._providers, "message_renderers": self._renderers, "entry_renderers": self._entry_renderers}})
        ready = self._read()
        if ready.get("type") != "ready":
            raise RuntimeError(f"expected ready, got {ready.get('type')}")
        ready_data = ready.get("ready") or {}
        self._session_name = ready_data.get("session_name", "")
        self._cwd = ready_data.get("cwd", "")
        self._mode = ready_data.get("mode", "")
        self._width = ready_data.get("width", 0)
        self._height = ready_data.get("height", 0)
        self._model = ready_data.get("model", "")
        # Initialize session mirror from the ready payload state.
        initial_state = ready_data.get("state") or {}
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
                cid = env.get("id", "")
                with self._state_lock:
                    event = self._pending.pop(cid, None)
                    self._pending_parents.pop(cid, None)
                    if event:
                        self._pending_results[cid] = env.get("call_result") or {}
                if event:
                    event.set()
                continue
            if env.get("type") == "notify":
                self._handle_notify(env)
            elif env.get("type") == "request":
                self._request_state(env.get("id", ""), "started")
                worker = threading.Thread(
                    target=self._handle_request_thread,
                    args=(env,),
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
                    self._respond(
                        env.get("id", ""),
                        None,
                        {"message": f"start extension handler: {exc}"},
                    )
                    continue
            elif env.get("type") == "cancel":
                req_id = (env.get("cancel") or {}).get("request_id") or env.get("id")
                reason = (env.get("cancel") or {}).get("reason")
                with self._state_lock:
                    active = self._active.get(req_id)
                    if active:
                        self._active[req_id] = (active[0], reason)
                    cancelled_calls = []
                    for call_id, parent_id in list(self._pending_parents.items()):
                        if parent_id != req_id:
                            continue
                        event = self._pending.pop(call_id, None)
                        self._pending_parents.pop(call_id, None)
                        self._pending_results.pop(call_id, None)
                        self._cancelled_calls.add(call_id)
                        if event:
                            cancelled_calls.append(event)
                if active:
                    active[0].set()
                for event in cancelled_calls:
                    event.set()
            elif env.get("type") == "shutdown":
                self._stop_runtime()
                return

    def _stop_runtime(self) -> None:
        self._shutdown.set()
        with self._state_lock:
            pending = list(self._pending.values())
            active = list(self._active.values())
        with self._overlay_lock:
            overlays = list(self._overlays.values())
        for overlay in overlays:
            overlay.stop()
        for event in pending:
            event.set()
        for cancel, _ in active:
            cancel.set()
        deadline = time.monotonic() + 2.0
        while True:
            with self._request_threads_lock:
                threads = [thread for thread in self._request_threads if thread is not threading.current_thread()]
            if not threads:
                return
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError("extension handlers did not stop before the shutdown deadline")
            for thread in threads:
                thread.join(timeout=max(0.0, deadline - time.monotonic()))

    def _handle_request_thread(self, env: dict[str, Any]) -> None:
        try:
            self._handle_request(env)
        finally:
            with self._request_threads_lock:
                self._request_threads.discard(threading.current_thread())

    def _handle_notify(self, env: dict[str, Any]) -> None:
        notify = env.get("notify") or {}
        method = notify.get("method", "")
        args = notify.get("args") or {}
        if method == "model_stream_event":
            stream_id = str(args.get("streamId") or "")
            with self._model_stream_lock:
                stream = self._model_streams.get(stream_id)
            if stream is not None:
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
                width_subs = [h for _, h in self._width_change]
                new_width = self._width
            for handler in width_subs:
                handler(new_width)
            with self._overlay_lock:
                overlays = list(self._overlays.values())
            for overlay in overlays:
                overlay.request_render()

    def _handle_request(self, env: dict[str, Any]) -> None:
        req_id = env.get("id", "")
        req = env.get("request") or {}
        cancel = threading.Event()
        with self._state_lock:
            self._active[req_id] = (cancel, None)
        ctx = Context(self, req.get("tool_call_id"), req_id, cancel)
        ctx._reason_provider = lambda: self._active.get(req_id, (None, None))[1]
        try:
            method = req.get("method")
            if method == "tool_call":
                name = req.get("tool", "")
                params = req.get("args") or {}
                prepare = self._tool_prepare_handlers.get(name)
                if prepare is not None:
                    params = prepare(params)
                    if not isinstance(params, dict):
                        raise TypeError("prepare_arguments must return a mapping")
                result = self._tool_handlers[name](ctx, params)
                payload = result if isinstance(result, dict) else {"content": str(result)}
                self._respond(req_id, payload, None)
            elif method == "command":
                name = req.get("tool", "")
                args = req.get("args") or ""
                self._command_handlers[name](ctx, args if isinstance(args, str) else json.dumps(args))
                self._respond(req_id, None, None)
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
                if req.get("event") == "agent_before_settle":
                    entries = data.get("entries", [])
                    result = None
                    error = None
                    try:
                        result = handler(ctx, data)
                    except Exception as exc:  # noqa: BLE001 - Preserve mutations and report the handler error.
                        error = {"message": str(exc)}
                    self._respond(req_id, {"_pigBoundaryEntries": entries, "_pigBoundaryResult": result}, error)
                    return
                messages = data.get("messages")
                snapshot = list(messages) if isinstance(messages, list) else None
                result = handler(ctx, data)
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
            with self._state_lock:
                self._active.pop(req_id, None)

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
                return
            # Set before the call: the host starts sending the log as soon as
            # it registers the subscription, and those pushes must be applied.
            self._session_mirror.subscribed = True
            entries = self._read_session_entries(self._session_file)
            cursor = len(entries)
            leaf_id = ""
            while True:
                requested_cursor = cursor
                result = self._call("watchSessionLog", {"cursor": cursor}).get("result")
                if not isinstance(result, dict):
                    return
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
                    return
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

    def _call(self, method: str, args: Any = None, parent_request_id: str = "") -> dict[str, Any]:
        call_id, event = self._begin_call(method, args, parent_request_id)
        return self._wait_call(call_id, event, method)

    def _begin_call(self, method: str, args: Any = None, parent_request_id: str = "") -> tuple[str, threading.Event]:
        if self._shutdown.is_set():
            raise RuntimeError("extension is shutting down")
        event = threading.Event()
        with self._state_lock:
            self._call_id += 1
            call_id = f"c{self._call_id}"
            self._pending[call_id] = event
            if parent_request_id:
                self._pending_parents[call_id] = parent_request_id
        call = {"method": method, "args": args}
        if parent_request_id:
            call["parent_request_id"] = parent_request_id
        self._send({"type": "call", "id": call_id, "call": call})
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

    def _run_remote_component(self, component: RemoteComponent, options: dict[str, Any], parent_request_id: str = "") -> Any:
        args = dict(options)
        with self._overlay_lock:
            self._overlay_seq += 1
            key = f"custom-{self._overlay_seq}"
            overlay = _RemoteOverlayState(component)
            self._overlays[key] = overlay
        args["key"] = key
        try:
            call_id, event = self._begin_call("ui.custom", args, parent_request_id)
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

    def _respond(self, req_id: str, result: Any, error: dict[str, Any] | None) -> None:
        self._request_state(req_id, "completed")
        self._send({"type": "response", "id": req_id, "response": {"result": result, "error": error}})

    def _request_state(self, req_id: str, state: str, reason: str | None = None) -> None:
        payload = {"request_id": req_id, "state": state}
        if reason:
            payload["reason"] = reason
        self._send({"type": "request_state", "request_state": payload})

    def _send(self, env: dict[str, Any]) -> None:
        if self._sock is None:
            raise RuntimeError("extension socket is not connected")
        data = json.dumps(env, separators=(",", ":")).encode()
        if len(data) > MAX_FRAME_SIZE:
            raise RuntimeError(f"frame too large: {len(data)} bytes exceeds {MAX_FRAME_SIZE}")
        with self._write_lock:
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
