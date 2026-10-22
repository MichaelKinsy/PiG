"""Pig subprocess extension SDK for Python.

Factory-style Python extensions should expose a function such as::

    def new_extension() -> pig_sdk.Extension: ...

Generated standalone and packed runners call that factory and then run the
returned extension over a host-provided subprocess socket.
"""

from __future__ import annotations

import base64
import copy
import dataclasses
import decimal
import io
import json
import math
import os
import queue
import re
import socket
import struct
import sys
import threading
import time
import traceback
import weakref
from collections import deque
from collections.abc import Sequence
from contextvars import ContextVar
from dataclasses import dataclass, field
from typing import Any, Callable, Literal, NotRequired, Protocol, TypeAlias, TypedDict
from . import editor_component as _editor_component
from .editor_component import EditorBase as EditorBase, EditorComponent as EditorComponent, EditorFactory as EditorFactory, EditorStatus as EditorStatus
from .autocomplete import AutocompleteProvider as AutocompleteProvider, AutocompleteProviderFactory, _AutocompleteRegistry
from .session_manager import ReadonlySessionManager, SessionManager as SessionManager, SetupSessionManager
from .settings_manager import SettingsManager as SettingsManager
from .resource_loader import DefaultResourceLoader as DefaultResourceLoader
from .model_runtime import ModelRuntime as ModelRuntime
from .package_manager import DefaultPackageManager as DefaultPackageManager
from .project_trust import ProjectTrustStore as ProjectTrustStore, has_trust_requiring_project_resources as has_trust_requiring_project_resources
from .host_info import HostInfoUnavailable as HostInfoUnavailable, ShellConfig as ShellConfig, get_agent_dir as get_agent_dir, get_config_dir_name as get_config_dir_name, get_package_dir as get_package_dir, get_shell_config as get_shell_config, get_version as get_version
from .file_mutation_queue import with_file_mutation_queue as with_file_mutation_queue
from .language import get_language_from_path as get_language_from_path
from .compaction_cutpoint import CutPointResult as CutPointResult, find_cut_point as find_cut_point, find_turn_start_index as find_turn_start_index
from .compaction_estimate import estimate_tokens as estimate_tokens
from .branch_summary import (
    BranchPreparation as BranchPreparation,
    CollectEntriesResult as CollectEntriesResult,
    FileOperations as FileOperations,
    collect_entries_for_branch_summary as collect_entries_for_branch_summary,
    prepare_branch_entries as prepare_branch_entries,
)
from .child_session import AgentSession as AgentSession, CreateAgentSessionOptions as CreateAgentSessionOptions, ResourceOptions as ResourceOptions, _ChildSessions, create_agent_session as _create_agent_session
from .image_dimension_note import ResizedImage as ResizedImage, format_dimension_note as format_dimension_note
from .llm_convert import convert_to_llm as convert_to_llm
from .serialize_conversation import _ordered_keys, serialize_conversation as serialize_conversation
from .session_entry_messages import session_entry_to_context_messages as session_entry_to_context_messages
from .stored_credential import read_stored_credential as read_stored_credential
from .pi_ai_helpers import (
    DEFAULT_MAX_AGENT_RETRY_DELAY_MS as DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
    calculate_cost as calculate_cost,
    clamp_thinking_level as clamp_thinking_level,
    collapse_system_messages as collapse_system_messages,
    content_text as content_text,
    create_initial_system_message as create_initial_system_message,
    env_api_key_auth as env_api_key_auth,
    get_model_type as get_model_type,
    has_api as has_api,
    is_model_type as is_model_type,
    models_are_equal as models_are_equal,
    get_current_system_message as get_current_system_message,
    get_current_system_prompt as get_current_system_prompt,
    get_current_tools as get_current_tools,
    get_declared_tools as get_declared_tools,
    get_initial_system_message as get_initial_system_message,
    get_supported_thinking_levels as get_supported_thinking_levels,
    get_system_message_text as get_system_message_text,
    has_non_additive_tool_changes as has_non_additive_tool_changes,
    is_context_overflow as is_context_overflow,
    is_recoverable_length as is_recoverable_length,
    is_retryable_assistant_error as is_retryable_assistant_error,
    normalize_context as normalize_context,
    render_system_message_update as render_system_message_update,
    resolve_transcript as resolve_transcript,
    resolve_transcript_tools as resolve_transcript_tools,
    retry_delay_ms as retry_delay_ms,
    string_enum as string_enum,
    without_initial_system_message as without_initial_system_message,
)
from .host_helpers import ConvertedImage as ConvertedImage, DiffString as DiffString, ImageResizeOptions as ImageResizeOptions, ParsedFrontmatter as ParsedFrontmatter, convert_to_png as convert_to_png, copy_to_clipboard as copy_to_clipboard, generate_diff_string as generate_diff_string, generate_unified_patch as generate_unified_patch, resize_image as resize_image, parse_date as parse_date, parse_frontmatter as parse_frontmatter, set_host_helpers as set_host_helpers, strip_frontmatter as strip_frontmatter
from .compaction_usage import DEFAULT_COMPACTION_SETTINGS as DEFAULT_COMPACTION_SETTINGS, CompactionSettings as CompactionSettings, calculate_context_tokens as calculate_context_tokens, get_last_assistant_usage as get_last_assistant_usage, should_compact as should_compact
from .session_skill_helpers import CURRENT_SESSION_VERSION as CURRENT_SESSION_VERSION, VIRTUAL_MODEL_STATE_ENTRY as VIRTUAL_MODEL_STATE_ENTRY, ParsedSkillBlock as ParsedSkillBlock, format_skills_for_prompt as format_skills_for_prompt, get_latest_compaction_entry as get_latest_compaction_entry, parse_skill_block as parse_skill_block
from .theme_helpers import EditorTheme as EditorTheme, MarkdownTheme as MarkdownTheme, SelectListTheme as SelectListTheme, SettingsListTheme as SettingsListTheme, get_editor_theme as get_editor_theme, get_markdown_theme as get_markdown_theme, get_select_list_theme as get_select_list_theme, get_settings_list_theme as get_settings_list_theme, highlight_code as highlight_code, init_theme as init_theme
from .visual_truncate import VisualTruncateResult as VisualTruncateResult, truncate_to_visual_lines as truncate_to_visual_lines
from .keybinding_hints import format_key_text as format_key_text, key_display_text as key_display_text, key_hint as key_hint, key_text as key_text, raw_key_hint as raw_key_hint
from .truncate import DEFAULT_MAX_BYTES as DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES as DEFAULT_MAX_LINES, GREP_MAX_LINE_LENGTH as GREP_MAX_LINE_LENGTH, TruncationResult as TruncationResult, format_size as format_size, truncate_head as truncate_head, truncate_line as truncate_line, truncate_tail as truncate_tail
from ._color import color_ansi, parse_color, style_text_with_ansi
from . import kit as kit
from . import _tui_state
from . import tui as tui  # loaded here with every other module: tests/test_staged_directory_independence.py
from .kit import ViewComponent as ViewComponent
from .event_bus import EventBus as EventBus, EventBusHandler as EventBusHandler, _EventBusHub
from .state_feed import StateFeed
from .user_bash import BashExecOptions as BashExecOptions, BashOperations as BashOperations, _BashOperationsTable, _data_update, _run_user_bash_exec, _user_bash_operations_reply
from .provider import (
    Provider, ProviderSignal,
    ProviderAuth as ProviderAuth, APIKeyAuth as APIKeyAuth, OAuthAuth as OAuthAuth,
    AuthContext as AuthContext, APIKeyAuthInput as APIKeyAuthInput, AuthResult as AuthResult,
    AuthCheck as AuthCheck, AuthInteraction as AuthInteraction, LoginOptions as LoginOptions,
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


def _clone_stream_value(value: Any) -> Any:
    """Copies the containers of a decoded event payload; strings are immutable and shared."""
    if isinstance(value, dict):
        return {key: _clone_stream_value(member) for key, member in value.items()}
    if isinstance(value, list):
        return [_clone_stream_value(member) for member in value]
    return value


def _apply_stream_ops(root: Any, ops: Any) -> Any:
    """Applies the operations of an event stream delta: set writes the value at a path (an object key, or an array index no further than the array's length), append adds a string to the string at the path."""
    if not isinstance(ops, list):
        raise ValueError("event stream: ops are not a list")
    for op in ops:
        path, value, name = op.get("path"), op.get("value"), op.get("op")
        if not isinstance(path, list):
            raise ValueError("event stream: bad path")
        if not path:
            if name != "set":
                raise ValueError("event stream: bad root operation")
            root = value
            continue
        parent = root
        for element in path[:-1]:
            parent = _stream_member(parent, element)
        last = path[-1]
        if name == "append":
            current = _stream_member(parent, last)
            if not isinstance(current, str) or not isinstance(value, str):
                raise ValueError("event stream: append to a non-string")
            _stream_store(parent, last, current + value)
        elif name == "set":
            _stream_store(parent, last, value)
        else:
            raise ValueError(f"event stream: unknown operation {name!r}")
    return root


def _stream_member(parent: Any, key: Any) -> Any:
    if isinstance(parent, dict) and isinstance(key, str) and key in parent:
        return parent[key]
    if isinstance(parent, list) and isinstance(key, int) and not isinstance(key, bool) and 0 <= key < len(parent):
        return parent[key]
    raise ValueError("event stream: bad path")


def _stream_store(parent: Any, key: Any, value: Any) -> None:
    if isinstance(parent, dict) and isinstance(key, str):
        parent[key] = value
    elif isinstance(parent, list) and isinstance(key, int) and not isinstance(key, bool) and 0 <= key <= len(parent):
        if key == len(parent):
            parent.append(value)
        else:
            parent[key] = value
    else:
        raise ValueError("event stream: bad path")


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
    aliases: NotRequired[list[str]]
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
    prompt_guidelines: dict[str, list[str]] = field(default_factory=dict)

    def get_exposure(self, name: str) -> ToolExposure:
        """The tool's exposure; a tool without a definition is ``"direct"`` (agent-session.ts:1480-1482)."""
        return self.exposures.get(name, "direct")  # type: ignore[return-value]

    def get_namespace(self, name: str) -> ToolNamespace | None:
        """The tool's namespace, or None (agent-session.ts:1520)."""
        return self.namespaces.get(name)

    def get_prompt_guidelines(self, name: str) -> list[str]:
        """The tool's ``promptGuidelines`` as the system prompt has them (trimmed, without duplicates); empty for a tool
        without any. Hidden declarations leave them out of the system prompt (agent-session.ts getPromptGuidelines)."""
        return list(self.prompt_guidelines.get(name, []))


ToolPrepareLoadoutHandler: TypeAlias = Callable[[ToolLoadout], "ToolLoadoutChanges | None"]
# Upstream ``reachesTools``: which tools a call with ``input`` may reach, ``"all"`` or the words of the input that may name them.
ToolReachesToolsHandler: TypeAlias = Callable[[dict[str, Any]], "Literal['all'] | Sequence[str]"]


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
    #: Horizontal padding configured by the ``outputPad`` setting. Renderers with ``renderShell: "self"`` apply it themselves.
    output_pad: int
    state: dict[str, Any]
    # Recorded execution time of a final result in milliseconds. Absent while the result is partial and for results stored without one. upstream: ToolRenderContext.durationMs
    duration_ms: int | None = None
    _invalidate: Callable[[], None] = field(repr=False, default=lambda: None)

    def invalidate(self) -> None:
        self._invalidate()


# A call renderer: (ctx, args, render context, width) -> lines.
ToolRenderCallHandler: TypeAlias = Callable[["Context", dict[str, Any], ToolRenderContext, int], list[str]]
# A result renderer: (ctx, {content, details}, {expanded, isPartial}, render context, width) -> lines.
ToolRenderResultHandler: TypeAlias = Callable[["Context", dict[str, Any], dict[str, Any], ToolRenderContext, int], list[str]]
# The view forms (D107): the same arguments, returning a kit.View the host renders at the card's width.
ToolRenderCallViewHandler: TypeAlias = Callable[["Context", dict[str, Any], ToolRenderContext, int], kit.View]
ToolRenderResultViewHandler: TypeAlias = Callable[["Context", dict[str, Any], dict[str, Any], ToolRenderContext, int], kit.View]
Factory: TypeAlias = Callable[[], "Extension"]


@dataclass(kw_only=True)
class ToolRenderers:
    """How calls to a tool are drawn (upstream ToolRenderers): render_shell, render_call and render_result.

    ``call_view`` and ``result_view`` are the view forms (D107): each declares its phase rendered and wins over the
    line form set for the same phase.
    """

    render_call: ToolRenderCallHandler | None = None
    render_result: ToolRenderResultHandler | None = None
    render_shell: str = "default"
    call_view: ToolRenderCallViewHandler | None = None
    result_view: ToolRenderResultViewHandler | None = None


# A tool renderer resolver (upstream ToolRendererResolver): (tool name, next) -> renderers or None. next() returns the
# renderers the remaining resolvers, then the registered tool, would use. pig divergence (D89): next() returns a marker
# for renderers the host draws; returning it keeps them, and its render_call and render_result are None. A marker given
# a render_call or render_result is the resolver's own renderers.
ToolRendererResolver: TypeAlias = Callable[[str, Callable[[], "ToolRenderers | None"]], "ToolRenderers | None"]


@dataclass(kw_only=True)
class ToolDefinition:
    """Upstream ToolDefinition, the argument of :meth:`Extension.register_tool`.

    Fields are keyword-only, as upstream's are object-literal keys.
    ``execute`` is this SDK's tool handler ``(ctx, params) -> result``: the
    tool call id, cancellation and ``onUpdate`` of upstream's
    ``execute(toolCallId, params, signal, onUpdate, ctx)`` are
    ``ctx.tool_call_id``, ``ctx.is_cancelled()`` and ``ctx.on_update()``.
    ``render_call`` and ``render_result`` are the line renderers
    :meth:`Extension.tool_renderers` takes; ``call_view`` and
    ``result_view`` its view forms (D107), which win over them.
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
    call_view: ToolRenderCallViewHandler | None = None
    result_view: ToolRenderResultViewHandler | None = None
    output_schema: Schema | None = None
    exposure: ToolExposure | None = None
    namespace: ToolNamespace | None = None
    annotations: ToolAnnotations | None = None
    default_active: bool | None = None
    prepare_loadout: ToolPrepareLoadoutHandler | None = None
    reaches_tools: ToolReachesToolsHandler | None = None


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


# pig divergence (D2): Python extensions add a sprite to PiG's /sprite catalogue.
@dataclass(frozen=True)
class SpriteDefinition:
    """One sprite for PiG's /sprite catalogue.

    ``mascot`` is the 16-by-14 pig the startup header draws and /sprite
    preview shows beside the wordmark; ``palette`` colors it. Grid
    cells are printable ASCII palette symbols; ``.`` is transparent.
    """

    id: str
    name: str
    tagline: str
    mascot: list[str]
    palette: dict[str, str]

    def _to_wire(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "name": self.name,
            "tagline": self.tagline,
            "mascot": list(self.mascot),
            "palette": dict(self.palette),
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


def _non_finite_to_null(value: Any) -> Any:
    """The value with every NaN and infinity replaced by None, as JSON.stringify writes them."""
    if isinstance(value, float):
        return value if math.isfinite(value) else None
    if isinstance(value, dict):
        return {key: _non_finite_to_null(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_non_finite_to_null(item) for item in value]
    return value


def message_role(data: dict[str, Any]) -> str:
    """Role of an event's message payload, or "" when the event carries none or its role is not a string.

    Message-shaped events carry upstream's flat role-discriminated union:
    ``{"type": "message_end", "message": {"role": "assistant", "content": [...]}}``
    where content is a block array, or a string for a user message.
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


def is_tool_call_event_type(tool_name: str, event: Any) -> bool:
    """Pi's ``isToolCallEventType``: whether a ``tool_call`` event is a call of the named tool, built-in or custom. It compares ``event["toolName"]`` with
    ``tool_name``; an event without a string ``toolName`` is no call of any tool."""
    return isinstance(event, dict) and isinstance(event.get("toolName"), str) and event["toolName"] == tool_name


def is_bash_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the bash tool.

    Pi core/extensions/types.ts:1315 ``isBashToolResult``: ``e.toolName === "bash"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "bash"


def is_powershell_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the powershell tool.

    Pi core/extensions/types.ts:1318 ``isPowerShellToolResult``: ``e.toolName === "powershell"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "powershell"


def is_read_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the read tool.

    Pi core/extensions/types.ts:1321 ``isReadToolResult``: ``e.toolName === "read"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "read"


def is_edit_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the edit tool.

    Pi core/extensions/types.ts:1324 ``isEditToolResult``: ``e.toolName === "edit"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "edit"


def is_write_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the write tool.

    Pi core/extensions/types.ts:1327 ``isWriteToolResult``: ``e.toolName === "write"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "write"


def is_grep_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the grep tool.

    Pi core/extensions/types.ts:1330 ``isGrepToolResult``: ``e.toolName === "grep"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "grep"


def is_find_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the find tool.

    Pi core/extensions/types.ts:1333 ``isFindToolResult``: ``e.toolName === "find"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "find"


def is_ls_tool_result(data: dict[str, Any]) -> bool:
    """Whether a ``tool_result`` event is the result of the ls tool.

    Pi core/extensions/types.ts:1336 ``isLsToolResult``: ``e.toolName === "ls"``.
    A payload without a string ``toolName`` is the result of no built-in tool.
    """
    return isinstance(data, dict) and data.get("toolName") == "ls"


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
        # revision increments on every change to the log or the leaf; the branch
        # cache is served only while the mirror is still at the revision it was
        # built at. reset_gen increments whenever the log is replaced rather
        # than extended (session switch, resync, seed, a repeated entry id).
        self._revision: int = 0
        self._reset_gen: int = 0
        # The root->leaf path. After a miss it is extended with the entries
        # appended since when the new leaf descends from the old one, and
        # rebuilt otherwise (leaf move, fork, rewind). _branch_tail is the id of
        # its last entry, "" when it holds the whole log because there is no
        # leaf; _branch_gen is the reset_gen it was built under;
        # _branch_scanned is the log length when it was built.
        self._branch_cache: list[dict[str, Any]] | None = None
        self._branch_cache_for: int = -1
        self._branch_tail: str = ""
        self._branch_gen: int = -1
        self._branch_scanned: int = 0

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
            self._reset_gen += 1
            changed = True

        # The leaf is small and always tracked. The log itself is applied only
        # once subscribed, and never from a push carrying no entries and a zero
        # count: that is the shape sent to an unsubscribed extension, and
        # reading it as an empty session would discard a mirror a concurrent
        # subscribe had just filled.
        if not self.subscribed or (entry_count == 0 and not appended):
            if leaf_id and leaf_id != self._leaf_id:
                self._leaf_id = leaf_id
                changed = True
            if changed:
                self._revision += 1
            return changed

        if expected_base != len(self._entries):
            self._entries = []
            self._index = {}
            self._reset_gen += 1
            changed = True

        for entry in appended:
            pos = len(self._entries)
            self._entries.append(entry)
            eid = entry.get("id", "")
            if eid:
                if eid in self._index:
                    self._reset_gen += 1
                self._index[eid] = (pos, entry.get("parentId", ""))
            changed = True

        if leaf_id and leaf_id != self._leaf_id:
            self._leaf_id = leaf_id
            changed = True

        if changed:
            self._revision += 1
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
                if eid in self._index:
                    self._reset_gen += 1
                self._index[eid] = (pos, entry.get("parentId", ""))
        self._entries = list(entries)
        self._branch_cache = None
        self._reset_gen += 1
        self._revision += 1

    def get_entries(self) -> list[dict[str, Any]]:
        return list(self._entries)

    def get_branch(self) -> list[dict[str, Any]]:
        if self._branch_cache is None or self._branch_cache_for != self._revision:
            if not self._extend_branch():
                self._rebuild_branch()
            self._branch_cache_for = self._revision
        return list(self._branch_cache or [])

    def _extend_branch(self) -> bool:
        """Bring the cached path up to the current leaf by walking parent links
        from the leaf back to the cached tail, visiting only entries appended
        since the cache was built. False, with the cache untouched, when the leaf
        does not descend from the cached tail."""
        cache = self._branch_cache
        if not cache or not self._branch_tail or self._branch_gen != self._reset_gen or not self._leaf_id:
            return False
        scanned = self._branch_scanned
        budget = len(self._entries) - scanned
        fresh: list[dict[str, Any]] = []
        current = self._leaf_id
        while current != self._branch_tail:
            meta = self._index.get(current)
            if meta is None:
                return False
            pos, parent_id = meta
            if pos < scanned or len(fresh) >= budget:
                return False
            fresh.append(self._entries[pos])
            current = parent_id
        if fresh:
            fresh.reverse()
            cache.extend(fresh)
            self._branch_tail = self._leaf_id
        self._branch_scanned = len(self._entries)
        return True

    def _rebuild_branch(self) -> None:
        self._branch_gen = self._reset_gen
        self._branch_scanned = len(self._entries)
        self._branch_tail = ""
        if not self._leaf_id or not self._index:
            self._branch_cache = list(self._entries)
            return
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
        if path:
            self._branch_tail = self._leaf_id
        self._branch_cache = path


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


_OVERLAY_PERCENT = re.compile(r"^(\d+(?:\.\d+)?)%$")


def _is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def _parse_overlay_size(value: Any, reference: float) -> float | None:
    """Upstream parseSizeValue (tui.ts:228-237): a number is cells, "N%" is a floored share of reference, anything else is unset."""
    if _is_number(value):
        return float(value)
    if isinstance(value, str):
        match = _OVERLAY_PERCENT.match(value)
        if match:
            return float(math.floor(reference * float(match.group(1)) / 100))
    return None


def _resolve_overlay_width(layout: dict[str, Any], term_width: int) -> int:
    """The width rule of upstream TUI.resolveOverlayLayout (tui.ts:1212-1233)."""
    margin = layout.get("margin")
    if _is_number(margin):
        left = right = float(margin)
    elif isinstance(margin, dict):
        left = float(margin["left"]) if _is_number(margin.get("left")) else 0.0
        right = float(margin["right"]) if _is_number(margin.get("right")) else 0.0
    else:
        left = right = 0.0
    avail = max(1.0, term_width - max(0.0, left) - max(0.0, right))
    width = _parse_overlay_size(layout.get("width"), float(term_width))
    if width is None:
        width = min(80.0, avail)
    if _is_number(layout.get("minWidth")):
        width = max(width, float(layout["minWidth"]))
    return math.trunc(max(1.0, min(width, avail)))


def _overlay_render_width(options: dict[str, Any]) -> Callable[[int], int]:
    """Pi renders an overlay component at the width TUI.resolveOverlayLayout resolves, and an inline component at the terminal width.

    The host composites an overlay with the same layout unless it opens the legacy titled modal (no overlayOptions, with title, widthFraction or heightFraction), which renders at the terminal width.
    """
    if options.get("overlay") is not True:
        return lambda width: width
    layout = options.get("overlayOptions")
    legacy = bool(options.get("title")) or (options.get("widthFraction") or 0) > 0 or (options.get("heightFraction") or 0) > 0
    if not isinstance(layout, dict):
        if legacy:
            return lambda width: width
        layout = {}
    return lambda width: _resolve_overlay_width(layout, width)


# Mouse event types after which Pi's renderer does not draw again by default (tui-alt-screen.ts applyMouseDispatchResult).
_MOUSE_NO_RENDER = frozenset(("move", "release"))


@dataclass
class MouseEvent:
    """pi-tui's ``TuiMouseEvent``: one cell-based pointer event, zero-based, with ``x``/``y`` relative to the component's
    first rendered cell. ``type`` is ``"press"``, ``"release"``, ``"move"``, ``"drag"``, ``"click"`` or ``"wheel"``, and
    ``button`` is ``"left"``, ``"middle"``, ``"right"`` or ``"none"``."""

    type: str = ""
    button: str = ""
    x: int = 0
    y: int = 0
    screen_x: int = 0
    screen_y: int = 0
    width: int = 0
    height: int = 0
    shift: bool = False
    alt: bool = False
    ctrl: bool = False
    # Lines a wheel event scrolls; negative scrolls up.
    wheel_delta: int = 0
    # 1, 2 or 3 for consecutive clicks on one cell.
    click_count: int = 0

    @classmethod
    def _from_wire(cls, raw: Any) -> MouseEvent | None:
        if not isinstance(raw, dict):
            return None
        return cls(
            type=str(raw.get("type") or ""),
            button=str(raw.get("button") or ""),
            x=int(raw.get("x") or 0),
            y=int(raw.get("y") or 0),
            screen_x=int(raw.get("screenX") or 0),
            screen_y=int(raw.get("screenY") or 0),
            width=int(raw.get("width") or 0),
            height=int(raw.get("height") or 0),
            shift=bool(raw.get("shift")),
            alt=bool(raw.get("alt")),
            ctrl=bool(raw.get("ctrl")),
            wheel_delta=int(raw.get("wheelDelta") or 0),
            click_count=int(raw.get("clickCount") or 0),
        )


class MouseHandler(Protocol):
    """The optional part of a :class:`RemoteComponent` or ``kit.ViewComponent`` that takes the mouse, as an upstream
    component with ``handleMouse`` does. Pi routes the mouse to components only in fullscreen mode, and so does PiG.

    While the overlay shows, every event inside the component's bounds arrives in order: press, release, move, drag,
    click and wheel; a left click arrives as press, release, then click. The host answers the terminal at once, so the
    component handles every event in its bounds and text selection does not start over it. A view component's own
    components take an event first, as Pi's do (a select-list row selects on press and fires select on click), and
    ``handle_mouse`` receives only the events they leave. It runs on the worker ``handle_input`` runs on, and its result
    means the same."""

    def handle_mouse(self, event: MouseEvent) -> RemoteComponentResult: ...


@dataclass
class _RemoteOverlayState:
    component: RemoteComponent | ViewComponent
    last_lines: list[str] = field(default_factory=list)
    # The last view frame's wire body, without image data, and the width it went out for (D107).
    last_view: tuple[dict[str, Any], int] | None = None
    seq: int = 0
    events: queue.Queue[tuple[str, Any]] = field(default_factory=lambda: queue.Queue(maxsize=64))
    active: threading.Event = field(default_factory=threading.Event)
    render_pending: bool = False
    event_lock: threading.Lock = field(default_factory=threading.Lock)
    worker: threading.Thread | None = None
    last_render: float = float("-inf")
    # Maps the terminal width to the component's render width; None renders at the terminal width.
    render_width: Callable[[int], int] | None = None
    # The image refs the last view frame names; force_view sends the next view frame even when it equals the last one,
    # after the host evicted one of its images. Both are guarded by event_lock.
    view_refs: list[str] = field(default_factory=list)
    force_view: bool = False
    # ui.custom()'s onHandle option (Pi OverlayOptions.onHandle): called once with the mounted overlay's handle, on the component's worker.
    on_handle: Callable[["OverlayHandle"], Any] | None = None
    handle_state: dict[str, Any] = field(default_factory=lambda: {"hidden": False, "focused": False, "visible": False})
    state_lock: threading.Lock = field(default_factory=threading.Lock)

    def __post_init__(self) -> None:
        self.active.set()

    def apply_handle_state(self, state: dict[str, Any]) -> None:
        """Replace the mirrored handle state with the host's (ui.custom.opened and ui.custom.input carry it)."""
        with self.state_lock:
            self.handle_state = {"hidden": bool(state.get("hidden")), "focused": bool(state.get("focused")), "visible": bool(state.get("visible")), "bounds": state.get("bounds")}
        component = self.component
        if hasattr(component, "focused"):
            try:
                component.focused = bool(state.get("focused"))  # type: ignore[union-attr]
            except Exception:  # noqa: BLE001 - a read-only attribute is the component's own choice  # nosec B110
                pass

    def apply_focus(self, focused: bool) -> None:
        """Record a change of the component's TUI focus the host published (ui.custom.focus), as Pi's setFocus clears and sets it."""
        with self.state_lock:
            self.handle_state = {**self.handle_state, "focused": focused}
        component = self.component
        if hasattr(component, "focused"):
            try:
                component.focused = focused  # type: ignore[union-attr]
            except Exception:  # noqa: BLE001 - a read-only attribute is the component's own choice  # nosec B110
                pass

    def enqueue_opened(self, state: dict[str, Any]) -> bool:
        """Queue the host's ui.custom.opened behind the input already queued."""
        return self._enqueue("opened", state)

    def enqueue_focus(self, focused: bool) -> bool:
        """Queue the host's ui.custom.focus behind the input already queued."""
        return self._enqueue("focus", focused)

    @property
    def is_view(self) -> bool:
        """Whether the component describes its frames as kit views."""
        return callable(getattr(self.component, "view", None))

    def resend_if_references(self, evicted: set[str]) -> None:
        """Render a view component again, bypassing dedup, when its last frame names an evicted image."""
        with self.event_lock:
            if not any(ref in evicted for ref in self.view_refs):
                return
            self.force_view = True
        self.request_render()

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
        return self._enqueue("input", data)

    def enqueue_view_event(self, event: kit.Event) -> bool:
        """Queue a ``ui.view.event`` behind the input already queued."""
        return self._enqueue("view_event", event)

    def enqueue_mouse(self, event: MouseEvent) -> bool:
        """Queue a ``ui.custom.mouse`` event behind the input already queued."""
        return self._enqueue("mouse", event)

    @property
    def takes_mouse(self) -> bool:
        """Whether the component takes the mouse (has ``handle_mouse``)."""
        return callable(getattr(self.component, "handle_mouse", None))

    def _enqueue(self, kind: str, data: Any) -> bool:
        if not self.active.is_set():
            return True
        try:
            self.events.put_nowait((kind, data))
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

    def next_event(self) -> tuple[str, Any]:
        event = self.events.get()
        if event[0] == "render":
            with self.event_lock:
                self.render_pending = False
        return event


_NO_TARGET: Any = object()


class OverlayHandle:
    """Pi's OverlayHandle (tui.ts) of a mounted ``ctx.ui.custom(component, {"overlay": True, "onHandle": fn})`` overlay.

    ``hide`` removes the overlay permanently (Pi: hide() disposes it); ``set_hidden`` toggles it. ``focus`` and ``unfocus`` move keyboard
    focus; ``unfocus`` takes Pi's ``{target}`` as the ``target`` argument: ``None`` focuses nothing, the editor component (as
    ``ctx.ui.set_editor_component`` built it) the editor, a mounted overlay component of this extension that overlay. Each control is a host
    call and returns after the host applied it, so ``is_hidden``, ``is_focused`` and ``get_bounds`` read the state it returned. After the
    overlay closed they read the last state and the controls do nothing, except ``set_hidden``, which records its value."""

    def __init__(self, extension: "Extension", key: str, overlay: _RemoteOverlayState) -> None:
        self._extension = extension
        self._key = key
        self._overlay = overlay

    def _control(self, action: str, hidden: bool | None = None, target: dict[str, Any] | None = None) -> None:
        overlay = self._overlay
        if not overlay.active.is_set():
            if action == "setHidden":
                with overlay.state_lock:
                    overlay.handle_state = {**overlay.handle_state, "hidden": bool(hidden)}
            return
        args: dict[str, Any] = {"key": self._key, "action": action}
        if hidden is not None:
            args["hidden"] = hidden
        if target is not None:
            args["target"] = target
        state = Context(self._extension)._call("ui.custom.control", args).get("result")
        if isinstance(state, dict):
            overlay.apply_handle_state(state)

    def hide(self) -> None:
        self._control("hide")

    def set_hidden(self, hidden: bool) -> None:
        self._control("setHidden", bool(hidden))

    def is_hidden(self) -> bool:
        with self._overlay.state_lock:
            return bool(self._overlay.handle_state["hidden"])

    def focus(self) -> None:
        self._control("focus")

    def unfocus(self, target: Any = _NO_TARGET) -> None:
        if target is _NO_TARGET:
            self._control("unfocus")
            return
        self._control("unfocus", target=self._focus_target(target))

    def is_focused(self) -> bool:
        with self._overlay.state_lock:
            return bool(self._overlay.handle_state["focused"])

    def get_bounds(self) -> dict[str, int] | None:
        with self._overlay.state_lock:
            state = self._overlay.handle_state
            bounds = state.get("bounds")
            return dict(bounds) if state["visible"] and isinstance(bounds, dict) else None

    def _focus_target(self, target: Any) -> dict[str, Any]:
        if target is None:
            return {"kind": "null"}
        extension = self._extension
        with extension._overlay_lock:
            for key, mounted in extension._overlays.items():
                if mounted.active.is_set() and mounted.component is target:
                    return {"kind": "overlay", "key": key}
        with extension._editor_lock:
            session = extension._editor
        if session is not None and session.component is target:
            return {"kind": "editor"}
        # pig divergence (D73): a component that is not mounted by this extension has no main-process identity to focus.
        raise ValueError("Overlay unfocus(target) requires None, the editor component, or a mounted overlay of this extension (D73)")


def _dispose_remote_component(component: RemoteComponent) -> None:
    set_invalidate = getattr(component, "set_invalidate", None)
    if callable(set_invalidate):
        try:
            set_invalidate(None)
        except Exception:  # noqa: BLE001 - reported on stderr, the extension log, as the Go SDK's overlay stop does
            print(f"extension: recovered from exception in a component's set_invalidate(None):\n{traceback.format_exc()}", file=sys.stderr, end="")
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


# Pi's ProviderRequestOptions callbacks that ctx.model_registry.stream forwards (the host asks for them by name through model_stream_callback).
_MODEL_STREAM_CALLBACKS = ("onPayload", "onResponse", "transformHeaders", "fetch")


class _IterableBody:
    """File-like reads over an iterable of byte chunks."""

    def __init__(self, chunks: Any) -> None:
        self._chunks = iter(chunks)
        self._pending = b""

    def read(self, size: int) -> bytes:
        while not self._pending:
            try:
                self._pending = bytes(next(self._chunks))
            except StopIteration:
                return b""
        chunk, self._pending = self._pending[:size], self._pending[size:]
        return chunk

    def close(self) -> None:
        close = getattr(self._chunks, "close", None)
        if callable(close):
            close()


def _fetch_body_reader(body: Any) -> Any:
    if isinstance(body, (bytes, bytearray, memoryview)):
        return io.BytesIO(bytes(body))
    if isinstance(body, str):
        return io.BytesIO(body.encode("utf-8"))
    if callable(getattr(body, "read", None)):
        return body
    return _IterableBody(body)


def _fetch_header_pairs(headers: Any) -> list[list[str]]:
    pairs: list[list[str]] = []
    items = headers.items() if isinstance(headers, dict) else (headers or [])
    for name, value in items:
        for entry in (value if isinstance(value, (list, tuple)) else [value]):
            pairs.append([str(name), str(entry)])
    return pairs


def _close_fetch_reader(reader: Any) -> None:
    close = getattr(reader, "close", None)
    if callable(close):
        close()


class _ModelFetchTransport:
    """The extension's ``fetch`` option of one stream (Pi's ProviderRequestOptions.fetch), served as runtime-node/model-fetch.mjs serves it.

    ``fetch(request)`` gets a dict with ``url``, ``method``, ``headers``, ``body`` and ``signal``, a ProviderSignal that is Pi's
    ``init.signal``: it is set when the host cancels the fetch or a body read, when the host closes the response, and when the stream
    ends. A response that arrives after the stream ended or after its signal was set is closed, and the fetch fails.
    """

    def __init__(self, fetch: Callable[..., Any]) -> None:
        self._fetch = fetch
        self._lock = threading.Lock()
        self._closed = False
        # fetch id -> [signal, body reader or None while the fetch is in flight]
        self._entries: dict[str, list[Any]] = {}

    def serve(self, name: str, value: Any, cancelled: ProviderSignal | None) -> Any:
        value = value or {}
        fetch_id = str(value.get("id"))
        if name == "fetch":
            return self._serve_fetch(fetch_id, value, cancelled)
        if name == "fetchRead":
            with self._lock:
                entry = self._entries.get(fetch_id)
            if entry is None or entry[1] is None:
                raise RuntimeError(f"unknown model fetch response {value.get('id')}")
            size = value.get("size")
            if isinstance(size, bool) or not isinstance(size, int) or size < 1 or size > 64 * 1024:
                raise RuntimeError("invalid model fetch read size")
            unsubscribe = cancelled.subscribe(entry[0].set) if cancelled is not None else None
            try:
                chunk = entry[1].read(size)
            finally:
                if unsubscribe is not None:
                    unsubscribe()
            chunk = b"" if chunk is None else bytes(chunk)
            return {"data": base64.b64encode(chunk).decode("ascii"), "done": chunk == b""}
        # fetchClose is idempotent: a deferred Body.Close may follow the terminal stream event.
        with self._lock:
            entry = self._entries.get(fetch_id)
            if entry is None or entry[1] is None:
                return None
            del self._entries[fetch_id]
        entry[0].set()
        _close_fetch_reader(entry[1])
        return None

    def _serve_fetch(self, fetch_id: str, value: dict[str, Any], cancelled: ProviderSignal | None) -> Any:
        signal = ProviderSignal()
        entry: list[Any] = [signal, None]
        with self._lock:
            if self._closed:
                raise RuntimeError("model fetch transport is closed")
            self._entries[fetch_id] = entry
        encoded = value.get("body")
        request = {
            "url": value["url"],
            "method": value["method"],
            "headers": value.get("headers") or {},
            "body": base64.b64decode(encoded) if encoded else None,
            "signal": signal,
        }
        unsubscribe = cancelled.subscribe(signal.set) if cancelled is not None else None
        try:
            response = self._fetch(request)
        except BaseException:
            with self._lock:
                if self._entries.get(fetch_id) is entry:
                    del self._entries[fetch_id]
            raise
        finally:
            if unsubscribe is not None:
                unsubscribe()
        body = response.get("body") if isinstance(response, dict) else getattr(response, "body", None)
        status = response["status"] if isinstance(response, dict) else response.status
        status_text = (response.get("statusText") if isinstance(response, dict) else getattr(response, "status_text", "")) or ""
        headers = response.get("headers") if isinstance(response, dict) else getattr(response, "headers", None)
        reader = None if body is None else _fetch_body_reader(body)
        with self._lock:
            closed = self._closed
            aborted = closed or signal.is_set()
            if aborted or reader is None:
                if self._entries.get(fetch_id) is entry:
                    del self._entries[fetch_id]
            else:
                entry[1] = reader
        if aborted:
            if reader is not None:
                _close_fetch_reader(reader)
            raise RuntimeError("model fetch transport is closed" if closed else "model fetch was cancelled")
        return {"status": int(status), "statusText": str(status_text), "headers": _fetch_header_pairs(headers), "body": reader is not None}

    def dispose(self) -> None:
        """End the transport with its stream: set every fetch's signal and close the bodies the host did not close."""
        with self._lock:
            self._closed = True
            entries = list(self._entries.values())
            self._entries.clear()
        for signal, reader in entries:
            signal.set()
            if reader is not None:
                _close_fetch_reader(reader)


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
        # A stream made by create_assistant_message_event_stream times its response as pi-ai's AssistantMessageEventStream does (see _time_message); one the SDK makes for a
        # host operation does not, because the host already timed that response.
        self._timed = False
        self._started_at = 0
        self._started_monotonic = 0.0

    def _time_message(self, message: Any) -> None:
        """AssistantMessageEventStream's #time (packages/ai/src/utils/event-stream.ts): the final message gets durationMs, the milliseconds since the stream was created on a
        monotonic clock, unless the message already has one or its timestamp predates the stream (a response that started elsewhere, such as a deferred result fetched later,
        stays untimed). The caller holds the condition."""
        if not self._timed or self._terminal or not isinstance(message, dict) or "durationMs" in message:
            return
        timestamp = message.get("timestamp")
        if isinstance(timestamp, (int, float)) and not isinstance(timestamp, bool) and timestamp < self._started_at:
            return
        message["durationMs"] = max(0, math.floor((time.monotonic() - self._started_monotonic) * 1000 + 0.5))

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
                self._time_message(event.get("message") if event.get("type") == "done" else event.get("error"))
                self._terminal = True
                self._result = event.get("message") if event.get("type") == "done" else event.get("error")
            self._condition.notify_all()

    def end(self, result: dict[str, Any] | None) -> None:
        with self._condition:
            if self._terminal:
                return
            self._time_message(result)
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


def create_assistant_message_event_stream() -> ModelEventStream:
    """pi-ai's createAssistantMessageEventStream: a provider-owned event stream of one assistant response. It times the response as Pi's does: the final message (the done event's
    message, the error event's error, or the result passed to ``end``) gets durationMs, measured from this call, unless it already has one or its timestamp predates the stream."""
    stream = ModelEventStream()
    stream._timed = True
    stream._started_at = int(time.time() * 1000)
    stream._started_monotonic = time.monotonic()
    return stream


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

    def generate_images(self, model: dict[str, Any], context: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any]:
        """Generate images with request-time authentication (``generateImages``).

        It never raises: a failure, and a cancelled request, are a result with ``stopReason`` ``error`` or ``aborted`` that names the model.
        ``context`` is ``{"input": [...]}`` with text and image blocks; the result's ``output`` holds the generated blocks.
        """
        args: dict[str, Any] = {"model": model, "context": context}
        if options is not None:
            args["options"] = options
        try:
            return self._context._call("generateImages", args).get("result")
        except Exception as error:  # noqa: BLE001 - generateImages reports every failure as a result
            return {
                "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"), "output": [],
                "stopReason": "aborted" if self._context._cancelled.is_set() else "error", "errorMessage": str(error), "timestamp": int(time.time() * 1000),
            }

    def register_virtual_model(self, model: "VirtualModel") -> None:
        """Register a virtual model, as :meth:`Context.register_virtual_model` does (``registerVirtualModel``)."""
        self._context.register_virtual_model(model)

    def unregister_virtual_model(self, provider: str, model_id: str) -> None:
        """Remove a virtual model (``unregisterVirtualModel``)."""
        self._context.unregister_virtual_model(provider, model_id)

    def _provider_state(self, model: dict[str, Any]) -> dict[str, Any]:
        """The registry state of the model's provider. A model with no string provider has no provider state: Pi's ``model.provider`` lookup in the configured and OAuth sets misses (model-runtime.ts:543-553)."""
        providers = self._state()["providers"]
        provider = model.get("provider")
        return providers.get(provider, {}) if isinstance(provider, str) else {}

    def has_configured_auth(self, model: dict[str, Any]) -> bool:
        return self._provider_state(model).get("configured", False)

    def is_using_oauth(self, model: dict[str, Any]) -> bool:
        return self._provider_state(model).get("usingOAuth", False)

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

    def _stream(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None, simple: bool, runtime: str | None = None) -> ModelEventStream:
        stream = ModelEventStream()
        extension = self._context.extension
        with extension._model_stream_lock:
            extension._model_stream_seq += 1
            stream_id = f"model-stream-{extension._model_stream_seq}"
            extension._model_streams[stream_id] = stream
        payload = dict(request)
        # Pi's ProviderRequestOptions callbacks (types.ts) are functions: they stay in this process, and the host is told which exist.
        request_options = dict(options or {})
        callbacks = {name: request_options.pop(name) for name in _MODEL_STREAM_CALLBACKS if callable(request_options.get(name))}
        payload.update(request_options)
        transport = _ModelFetchTransport(callbacks["fetch"]) if "fetch" in callbacks else None
        if transport is not None:
            callbacks["fetch"] = transport
        if callbacks:
            with extension._model_stream_lock:
                extension._model_stream_callbacks[stream_id] = (model, callbacks)

        def run() -> None:
            try:
                call = {"streamId": stream_id, "model": model, "request": payload, "simple": simple}
                if runtime:
                    call["runtime"] = runtime
                call.update({name: True for name in callbacks})
                self._context._call("modelStream", call)
            except Exception as exc:  # noqa: BLE001 - transport failure becomes terminal model error
                stream.push(_model_stream_error_event(exc, model))
            finally:
                with extension._model_stream_lock:
                    extension._model_streams.pop(stream_id, None)
                    extension._model_stream_callbacks.pop(stream_id, None)
                if transport is not None:
                    transport.dispose()

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


def _encase_line_breaks(value: str, prefix: str, postfix: str) -> str:
    """chalk's stringEncaseCRLFWithFirstIndex: ``prefix`` before and ``postfix`` after every ``\\n``, a preceding ``\\r`` staying with it."""
    out = []
    end = 0
    index = value.find("\n")
    while index != -1:
        if index > 0 and value[index - 1] == "\r":
            out.append(value[end : index - 1] + prefix + "\r\n" + postfix)
        else:
            out.append(value[end:index] + prefix + "\n" + postfix)
        end = index + 1
        index = value.find("\n", end)
    out.append(value[end:])
    return "".join(out)


def _theme_text(text: Any) -> str:
    return "" if text is None else str(text)


_FAINT_SGR = "\x1b[2m"


class Theme:
    """Upstream Theme (``ctx.ui.theme``) over the host's active palette.

    The host replicates its active theme with the state snapshot and pushes
    ``theme_change`` when it changes; one instance per extension follows
    those updates, so a held reference stays current. Its semantics are the
    Node runtime's ThemeShim: a token the palette lacks raises
    ``ValueError("Unknown theme color: <token>")`` from ``fg``/``bg`` once a palette has
    arrived, and ``bold`` and the other modifiers draw only when the
    host's styles do (upstream's chalk drops them without color support).
    """

    name: str | None
    source_path: str | None
    source_info: dict[str, Any] | None

    def __init__(self) -> None:
        self.name = None
        self.source_path = None
        self.source_info = None
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
        source_info = palette.get("sourceInfo")
        self.source_info = dict(source_info) if isinstance(source_info, dict) else None
        self._mode = "256color" if palette.get("mode") == "256color" else "truecolor"
        appearance = palette.get("appearance")
        self._appearance = appearance if appearance in ("light", "dark") else None
        colors = palette.get("colors")
        parsed = {token: parse_color(value) for token, value in colors.items()} if isinstance(colors, dict) else {}
        self._colors = {token: color for token, color in parsed.items() if color is not None}

    def _modifier(self, open_seq: str, close_seq: str, text: Any) -> str:
        # chalk's applyStyle, which upstream Theme's bold, italic, underline, inverse and strikethrough are (theme.ts:378-396; chalk 6.0.0 source/index.js, source/utilities.js): an empty text stays empty, an inner close of the style re-opens it, and each line break (CR LF kept together) is closed before and re-opened after.
        value = _theme_text(text)
        if not self._modifiers or value == "":
            return value
        if "\x1b" in value:
            value = value.replace(close_seq, close_seq + open_seq)
        if "\n" in value:
            value = _encase_line_breaks(value, close_seq, open_seq)
        return f"{open_seq}{value}{close_seq}"

    def fg(self, token: str, text: Any) -> str:
        value = _theme_text(text)
        open_seq = self._foregrounds.get(token) or ""
        if not open_seq:
            return self._unknown_or_unstyled(token, value)
        # The host opens a faint token with SGR 2 (theme.ts:399-402); Pi's fg closes it with SGR 22;39 (theme.ts:363).
        close_seq = "\x1b[22;39m" if open_seq.endswith("\x1b[2m") else "\x1b[39m"
        return f"{open_seq}{value}{close_seq}"

    def bg(self, token: str, text: Any) -> str:
        value = _theme_text(text)
        open_seq = self._backgrounds.get(token) or ""
        return f"{open_seq}{value}\x1b[49m" if open_seq else self._unknown_or_unstyled(token, value)

    def _unknown_or_unstyled(self, token: str, value: str) -> str:
        # A token the palette lacks is upstream's `Unknown theme color` throw (theme.ts:374); a theme the host has not sent a palette to has no tokens to lack.
        if not self._foregrounds and not self._backgrounds:
            return value
        raise ValueError(f"Unknown theme color: {token}")

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
            raise ValueError(f"Unknown theme color: {token}")
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


from .builtin_tool import (  # noqa: E402 - after the classes it builds on
    BashToolOptions as BashToolOptions, BuiltinTool as BuiltinTool, BuiltinToolDefinition as BuiltinToolDefinition, EditOperations as EditOperations, EditToolOptions as EditToolOptions,
    FindOperations as FindOperations, FindToolOptions as FindToolOptions, GrepOperations as GrepOperations, GrepToolOptions as GrepToolOptions, LsOperations as LsOperations,
    LsStat as LsStat, LsToolOptions as LsToolOptions, ModelImageResizeOptions as ModelImageResizeOptions, PowerShellToolOptions as PowerShellToolOptions,
    ReadOperations as ReadOperations, ReadToolOptions as ReadToolOptions, ToolsOptions as ToolsOptions, WriteOperations as WriteOperations, WriteToolOptions as WriteToolOptions,
    create_bash_tool as create_bash_tool, create_bash_tool_definition as create_bash_tool_definition, create_coding_tools as create_coding_tools,
    create_edit_tool as create_edit_tool, create_edit_tool_definition as create_edit_tool_definition, create_find_tool as create_find_tool,
    create_find_tool_definition as create_find_tool_definition, create_grep_tool as create_grep_tool, create_grep_tool_definition as create_grep_tool_definition,
    create_local_bash_operations as create_local_bash_operations, create_local_powershell_operations as create_local_powershell_operations,
    create_ls_tool as create_ls_tool, create_ls_tool_definition as create_ls_tool_definition, create_powershell_tool as create_powershell_tool,
    create_powershell_tool_definition as create_powershell_tool_definition, create_read_only_tools as create_read_only_tools, create_read_tool as create_read_tool,
    create_read_tool_definition as create_read_tool_definition, create_write_tool as create_write_tool, create_write_tool_definition as create_write_tool_definition,
    dispatch_callback as _dispatch_builtin_callback, dispatch_exec_data as _dispatch_builtin_exec_data,
)


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

    def __init__(self, method: str, render: "Callable[[int], list[str]]", key: str | None = None, options: dict[str, Any] | None = None) -> None:
        self.method = method
        self.render = render
        # The widget a renderer of ui.setWidget lays out, and its placement options.
        self.key = key
        self.options = options
        # Serializes a render with its push and orders a push against stop(), so
        # a superseded renderer never overwrites the rows that replaced it.
        self._lock = threading.Lock()
        self._stopped = False
        self._refresh_lock = threading.Lock()
        self._refresh_queued = False
        self._refresh_running = False

    def _kind(self) -> str:
        if self.method == "ui.setWidget":
            return "widget"
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
            args = _surface_args(lines, width)
            if self.method == "ui.setWidget":
                args["key"] = self.key
                if self.options:
                    args["options"] = self.options
            ctx._call(self.method, args)

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


_DIALOG_METHODS = frozenset({"ui.select", "ui.confirm", "ui.input", "ui.editor", "ui.custom"})


class _SettleGate:
    """Orders actions by the order their slots settled: a slot settles when the reader reads the answer its handler waits for, and its action runs after every slot that settled before it finished. A slot that was never settled runs at once."""

    def __init__(self) -> None:
        self._cond = threading.Condition()
        # The slots that settled and have not yet finished or yielded, in settle order.
        self._settled: list[_SettleSlot] = []

    def new_slot(self, parked: Callable[[], None] | None = None) -> "_SettleSlot":
        return _SettleSlot(self, parked)


class _SettleSlot:
    def __init__(self, gate: _SettleGate, parked: Callable[[], None] | None = None) -> None:
        self._gate = gate
        self._settled = False
        self._finished = False
        self._parked = parked

    def settle(self) -> None:
        """Give the slot its place after every slot that settled before. The reader calls it before it hands the answer to the waiting handler. A slot that already holds a place keeps it."""
        gate = self._gate
        with gate._cond:
            if self._settled or self._finished:
                return
            self._settled = True
            gate._settled.append(self)

    def yield_(self) -> None:
        """Give the place up: the handler's continuation reached its next await, so the handlers that settled after it go on. The next answer settles the slot again."""
        with self._gate._cond:
            self._leave_locked()

    def _leave_locked(self) -> None:
        if not self._settled:
            return
        gate = self._gate
        gate._settled.remove(self)
        self._settled = False
        gate._cond.notify_all()

    def run(self, action: Callable[[], None]) -> None:
        """Run ``action`` after every slot that settled before this one left its place. A slot that holds no place runs it at once. The slot closes when the action ends."""
        gate = self._gate
        with gate._cond:
            while self._settled and not self._finished and gate._settled[0] is not self:
                if self._parked is not None:
                    self._parked()
                gate._cond.wait()
        try:
            action()
        finally:
            self.close()

    def close(self) -> None:
        """End the slot: it stops holding up the slots behind it. Idempotent."""
        with self._gate._cond:
            self._leave_locked()
            self._finished = True


class _ToolStart:
    """One tool_call request's place in the orders ``_ToolStartOrder`` keeps. ``begin`` returns when every earlier request began its handler or ended without one."""

    def __init__(self, previous: threading.Event | None, order: "_ToolStartOrder") -> None:
        self._previous = previous
        self.done = threading.Event()
        self._order = order
        # Set when the request waits for its first host call and every earlier request passed its place; made only when the request has to wait. It, _dialog_passed, _dialog_opened, _ended and _slot are guarded by the order's lock.
        self._dialog_waiting: threading.Event | None = None
        self._dialog_passed = False
        self._dialog_opened = False
        self._ended = False
        # The request's place in the response order, made when the request opens a dialog: a handler that opens none is never gated.
        self._slot: _SettleSlot | None = None

    def begin(self) -> None:
        if self._previous is not None:
            self._previous.wait()
        self.done.set()

    def end(self) -> None:
        """Hand the places on when the request's thread ends, also for a request that failed before it reached its handler. Like ``begin`` it waits for the earlier requests first, so a request without a handler cannot let a later handler start before an earlier one. Idempotent."""
        self.begin()
        self._finish()

    def release(self) -> None:
        """Hand the places on at once, for a request whose thread never started. The reader calls it, so it must not wait for the earlier requests. Idempotent."""
        self.done.set()
        self._finish()

    def _finish(self) -> None:
        self.pass_dialog()
        order = self._order
        with order._lock:
            first = not self._ended
            self._ended = True
            slot = self._slot
        if slot is not None:
            slot.close()
        if first:
            order._request_ended()

    def response_slot(self) -> "_SettleSlot | None":
        """The request's place in the response order, made on first use. None once the request ended: a retained context's dialog must not leave a slot that nothing closes."""
        order = self._order
        with order._lock:
            if self._ended:
                return None
            if self._slot is None:
                self._slot = order._settle.new_slot(order.parked)
            return self._slot

    def before_call(self) -> None:
        """Run before the handler writes a host call, of any kind. The call is an await of the handler's continuation: a handler the reader settled yields its place in the response order. A handler's first host call waits for the first host calls of the earlier requests, and ``after_call`` then passes its dialog place on, as Pi's single thread lets a later call run once an earlier call reached its first await."""
        slot = self._slot
        if slot is not None:
            slot.yield_()
        order = self._order
        # pig divergence (D187): the first host call of a call waits for the first host calls of the earlier calls of its batch, also while an earlier call is blocked in work the SDK cannot see (its own fetch, timer or child process); Pi's single thread lets a later call's dialog overtake an earlier call that awaits real I/O.
        with order._lock:
            if self._dialog_opened:
                return
            self._dialog_opened = True
            queue = order._dialog_queue
            if not queue or queue[0] is self or self._dialog_passed:
                return
            waiting = self._dialog_waiting = threading.Event()
        parked = order.parked
        if parked is not None:
            parked()
        waiting.wait()

    def after_call(self) -> None:
        """Run once the host call's frame is written, or failed to be. The first host call of any kind gives the dialog place up: Pi's handler that awaits an exec has yielded the thread, so a later call's dialog may open before the handler's own."""
        self.pass_dialog()

    def pass_dialog(self) -> None:
        """Give up the request's dialog place: the first request that still holds one may write its first host call. It never waits, so a request that ends while an earlier one still runs is not held by it. Idempotent."""
        order = self._order
        with order._lock:
            if self._dialog_passed:
                return
            self._dialog_passed = True
            order._dialog_queue.remove(self)
            if order._dialog_queue:
                head = order._dialog_queue[0]
                if head._dialog_waiting is not None:
                    head._dialog_waiting.set()
                    head._dialog_waiting = None

    def respond(self, send: Callable[[], None]) -> None:
        """Write the request's response after the response of every handler whose dialog answer was read before this handler's."""
        slot = self._slot
        if slot is None:
            send()
        else:
            # pig divergence (D187): the response waits for the handlers whose dialog answers were read earlier, until each returns or makes its next host call; Pi's continuations run to their next await, never to the end of blocking work.
            slot.run(send)


class _RequestStart:
    """One request's place in the order the handlers start (``_starts_in_arrival_order``). ``begin`` returns when every earlier request began its handler or ended without one."""

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


def _starts_in_arrival_order(request: dict[str, Any] | None) -> bool:
    """Whether a request runs a handler whose start Pi orders by call order: a tool_call, an event or a command. Other requests (renders, provider streams, completions) hold no place, so a long one never holds a handler back."""
    return (request or {}).get("method") in ("tool_call", "event", "command")


class _RequestStartOrder:
    """Starts the handlers of the requests that run extension code in the order the requests arrive (``_starts_in_arrival_order``).

    Pi starts every call of a parallel batch through ``Promise.all(calls.map(...))`` and each reaches tool.execute synchronously, so the handlers start in source order (agent-loop.ts:619-647, 820-837); concurrent emits, such as the tool_result hooks of that batch, start their handlers in call order too (runner.ts:1096-1125). The host writes the requests in that order, but each request runs on its own thread, and the scheduler is free to run a later thread first.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._tail: threading.Event | None = None

    def reserve(self) -> _RequestStart:
        """Take the next place. The reader calls this in arrival order, before it starts the request's thread."""
        with self._lock:
            start = _RequestStart(self._tail)
            self._tail = start.done
            return start


@dataclass
class _ReplacementState:
    """The values a withSession context answers locally, from the replacement Session's ready payload."""


class _ToolStartOrder:
    """Orders the calls of one tool batch the way Pi's single thread does.

    Pi starts every call of a parallel batch through ``Promise.all(calls.map(...))`` and each reaches tool.execute synchronously, so the handlers start in source order (agent-loop.ts:619-647, 820-837). The host writes a batch's tool_call requests in source order, but each request runs on its own thread, and the scheduler is free to run a later thread first. Three orders follow from Pi's one thread, and each is kept here, per batch (``_ToolBatches`` keeps one order per batch id the host names on the batch's tool_call requests; a request outside a batch takes part in none):

    - Start: a request's handler is invoked only after the handler of every earlier request was invoked or its request ended without one.
    - Dialog: a handler's first host call, of any kind, is written only after the first host call of every earlier request was written or its request ended, and a request passes its dialog place on once that first call is written, so the first dialogs of a batch open in the order the calls started, and a call that awaits an exec before its dialog is overtaken by the later calls' dialogs, as in Pi. A handler that waits on something other than a host call before its first host call holds the later calls' first host calls, which Pi's single thread lets overtake it; that is the price of an order the handlers' own code cannot be asked for.
    - Continuation: when two dialogs are answered back to back, Pi runs the two handlers' continuations in the order of the answers, and each continuation runs to its next await or its return before the next one starts. The reader settles a handler's slot when it reads the answer to the handler's dialog, and the handler's response is written only after the response of every handler that settled before it, or after that handler's next host call (the await its continuation reached).

    ``parked``, when set, runs each time a call starts to wait for an earlier one, so a test knows it is held without timing.
    """

    def __init__(self, batches: "_ToolBatches | None" = None, batch: str = "") -> None:
        self._lock = threading.Lock()
        self._tail: threading.Event | None = None
        # The requests whose dialog place has not passed, in arrival order. The first is the one whose first host call may be written; a request passes its place once that call is written, whatever the call is.
        self._dialog_queue: list[_ToolStart] = []
        # Orders the responses of the handlers whose dialogs were answered.
        self._settle = _SettleGate()
        self.parked: Callable[[], None] | None = None
        # The registry that drops this order once its last request ended, and the batch id it keeps; the requests that have not ended count in _live.
        self._batches = batches
        self._batch = batch
        self._live = 0

    def reserve(self) -> _RequestStart:
        """Take the next place. The reader calls this in arrival order, before it starts the request's thread."""
        with self._lock:
            start = _ToolStart(self._tail, self)
            self._tail = start.done
            self._dialog_queue.append(start)
            self._live += 1
            return start

    def _request_ended(self) -> None:
        """A request ended: the order is dropped from its registry once no request is live."""
        with self._lock:
            self._live -= 1
            empty = self._live == 0
        if empty and self._batches is not None:
            self._batches._drop(self._batch, self)


class _ToolBatches:
    """One ``_ToolStartOrder`` per parallel batch of tool_call requests, keyed by the host's batch id. The orders are scoped to a batch: a long request of one batch, or a request outside any batch, holds nothing of another. A batch's order is dropped when its last request ended, so a request that arrives after every earlier one of its batch ended finds a fresh order."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._orders: dict[str, _ToolStartOrder] = {}

    def reserve(self, batch: str) -> _ToolStart:
        """Take the next place in the batch's order. The reader calls this in arrival order, before it starts the request's thread."""
        with self._lock:
            order = self._orders.get(batch)
            if order is None:
                order = self._orders[batch] = _ToolStartOrder(self, batch)
            return order.reserve()

    def _drop(self, batch: str, order: _ToolStartOrder) -> None:
        with self._lock:
            with order._lock:
                if order._live == 0 and self._orders.get(batch) is order:
                    del self._orders[batch]


def _model_identity(model: Any) -> tuple[str, str]:
    """The id (the name when the model has no id) and the provider id of a state snapshot's model, ("", "") for no model: Pi's ``ctx.model.id`` and ``ctx.model.provider``. The Go and Rust SDKs read the same members."""
    if not isinstance(model, dict):
        return "", ""
    identity = model.get("id")
    if not (isinstance(identity, str) and identity):
        identity = model.get("name")
    provider = model.get("provider")
    if isinstance(provider, dict):
        provider = provider.get("id")
    return (identity if isinstance(identity, str) else ""), (provider if isinstance(provider, str) else "")


@dataclass
class _ReplacementState:
    """The values a withSession context answers locally, from the replacement Session's ready payload."""

    cwd: str = ""
    mode: str = ""
    has_ui: bool = False
    model: str = ""
    model_provider: str = ""
    session_name: str = ""


class _WithSessionEntry:
    def __init__(self, callback: Callable[[Any], Any]) -> None:
        self.callback = callback
        self.error: BaseException | None = None


class _WithSessionRegistry:
    """The withSession callbacks of replacement calls in flight, by the handle each call names."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._next = 0
        self._entries: dict[str, _WithSessionEntry] = {}

    def add(self, prefix: str, callback: Callable[[Any], Any]) -> tuple[str, _WithSessionEntry]:
        with self._lock:
            self._next += 1
            handle = f"{prefix}:{self._next}"
            entry = _WithSessionEntry(callback)
            self._entries[handle] = entry
            return handle, entry

    def remove(self, handle: str) -> None:
        with self._lock:
            self._entries.pop(handle, None)

    def dispatch_setup(self, ctx: "Context", args: dict[str, Any]) -> None:
        """Run the callback a setup request names with the replacement Session's manager (agent-session-runtime.ts:254-257). The request's context carries its host calls."""
        handle = str(args.get("handle") or "")
        with self._lock:
            entry = self._entries.get(handle)
        if entry is None:
            raise RuntimeError(f"unknown setup callback {handle}")
        try:
            entry.callback(SetupSessionManager(ctx))
        except BaseException as error:
            entry.error = error
            raise

    def dispatch(self, ctx: "Context", args: dict[str, Any]) -> None:
        """Run the callback a with_session request names with a context of the replacement Session (agent-session-runtime.ts:187-194)."""
        handle = str(args.get("handle") or "")
        with self._lock:
            entry = self._entries.get(handle)
        if entry is None:
            raise RuntimeError(f"unknown withSession callback {handle}")
        ready = args.get("ready") or {}
        state = ready.get("state") or {}
        model, model_provider = _model_identity(state.get("model"))
        session = state.get("session") if isinstance(state.get("session"), dict) else {}
        replacement = _ReplacementState(
            cwd=str(ready.get("cwd") or ""),
            mode=str(ready.get("mode") or ""),
            has_ui=state.get("hasUI") is True,
            model=model,
            model_provider=model_provider,
            session_name=str(session.get("sessionName") or ""),
        )
        replaced = ReplacedSessionContext(**{item.name: getattr(ctx, item.name) for item in dataclasses.fields(Context)})
        replaced._replacement = replacement
        try:
            entry.callback(replaced)
        except BaseException as error:
            entry.error = error
            raise


def _home_directory() -> str:
    """The home directory as Go's os.UserHomeDir finds it: $HOME (%USERPROFILE% on Windows), never the passwd database."""
    home = os.environ.get("USERPROFILE" if sys.platform == "win32" else "HOME", "")
    if not home:
        raise RuntimeError("locate home directory: home environment variable is not defined")
    return home


def _join(base: str, rest: str) -> str:
    """Go's filepath.Join for two non-empty-base elements: the elements joined by the separator, then lexically cleaned.

    os.path.join would discard ``base`` when ``rest`` is absolute (``~//p`` must stay under the home directory) and keeps ``..``, repeated and trailing
    separators, which the host's internal/configroot removes. posixpath.normpath keeps exactly two leading slashes; Go's Clean reduces them to one.
    """
    joined = os.path.normpath(base + os.sep + rest)
    if os.sep == "/" and joined.startswith("//"):
        joined = "/" + joined.lstrip("/")
    return joined


def _expand_home(path: str) -> str:
    """A leading ``~`` or ``~/`` is the home directory; every other path, including ``~user``, stays literal."""
    if path == "~":
        return _home_directory()
    if path.startswith("~/"):
        return _join(_home_directory(), path[2:])
    return path


def _config_home() -> str:
    pig_home = os.environ.get("PIG_HOME", "")
    if pig_home:
        return _expand_home(pig_home)
    xdg = os.environ.get("XDG_CONFIG_HOME", "")
    if xdg:
        return _join(_expand_home(xdg), "pig")
    return _join(_home_directory(), ".pig")


class FooterData:
    """Pi's ReadonlyFooterDataProvider: the host's Git branch, extension statuses and available provider count. The values replicate the host's, so reads never call the host."""

    def __init__(self, extension: "Extension") -> None:
        self._extension = extension

    def get_git_branch(self) -> str | None:
        """The current Git branch, or ``None`` when there is none."""
        with self._extension._state_lock:
            return self._extension._footer_data["branch"]

    def get_extension_statuses(self) -> dict[str, str]:
        """Every extension's status text by key; the caller owns the dict."""
        with self._extension._state_lock:
            return dict(self._extension._footer_data["statuses"])

    def get_available_provider_count(self) -> int:
        """The number of providers with configured auth."""
        with self._extension._state_lock:
            return self._extension._footer_data["count"]

    def on_branch_change(self, callback: Callable[[], Any]) -> Callable[[], None]:
        """Run ``callback`` when the Git branch changes; return the function that unsubscribes it. A non-callable registers nothing."""
        if not callable(callback):
            return lambda: None
        extension = self._extension
        with extension._state_lock:
            extension._branch_subscriber_seq += 1
            sub_id = extension._branch_subscriber_seq
            extension._branch_subscribers[sub_id] = callback

        def unsubscribe() -> None:
            with extension._state_lock:
                extension._branch_subscribers.pop(sub_id, None)

        return unsubscribe


class _SystemPrompt:
    """Pi's before_agent_start ``event.systemPrompt`` getter (runner.ts:1430-1449): the prompt the emission's shared
    options render now, so a handler sees its own edit to ``systemPromptOptions`` at once. It holds the prompt the host
    rendered for the options the handler received; options that differ from those the host renders again."""

    def __init__(self, ctx: "Context", options: dict[str, Any], prompt: Any) -> None:
        self._ctx = ctx
        self._options = options
        self._key = json.dumps(options)
        self._prompt = prompt if isinstance(prompt, str) else ""
        self._lock = threading.Lock()

    def get(self) -> str:
        key = json.dumps(self._options)
        with self._lock:
            if key != self._key:
                self._prompt = self._ctx._reply_field("renderSystemPrompt", "prompt", {"options": self._options})
                self._key = key
            return self._prompt


class _BeforeAgentStartEvent(dict):
    """A before_agent_start event whose ``"systemPrompt"`` reads the prompt its shared options render now, as Pi's getter does."""

    def __init__(self, data: dict[str, Any], system_prompt: _SystemPrompt) -> None:
        super().__init__(data)
        self._system_prompt = system_prompt

    def __getitem__(self, key: Any) -> Any:
        if key == "systemPrompt" and super().__contains__(key):
            return self._system_prompt.get()
        return super().__getitem__(key)

    def get(self, key: Any, default: Any = None) -> Any:
        return self[key] if super().__contains__(key) else default

    def items(self) -> Any:  # json.dumps reads a dict subclass through items()
        return [(key, self[key]) for key in self.keys()]

    def values(self) -> Any:
        return [self[key] for key in self.keys()]


# The UI calls Pi's RPC mode applies at once, inside the handler's synchronous part (rpc-mode.ts createExtensionUIContext: notify,
# setStatus, setWidget, setTitle and setEditorText write their request; the rest do nothing). The host applies a handler's calls
# before it acts on a suspension the handler reports after them, so in RPC mode such a call reports none, as the Node runtime
# sends it: a suspension reported before the call lets the host go on, answering the call that started a nested handler or
# writing a command's response, while the effect is still on its way.
_RPC_SYNCHRONOUS_UI_EFFECTS = frozenset({
    "ui.notify", "ui.setStatus", "ui.setWidget", "ui.setTitle", "ui.setEditorText", "ui.pasteToEditor",
    "ui.setWorkingMessage", "ui.setWorkingVisible", "ui.setWorkingIndicator", "ui.setHiddenThinkingLabel",
    "ui.setToolsExpanded", "ui.setFooter", "ui.setHeader",
})


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
    # A tool_call, event or command request: its place in the order the handlers start.
    _request_start: "_RequestStart | None" = None
    # A tool_call request: the call's position in its assistant message's batch, and the JSON text of its arguments as the host sent it.
    _tool_index: int | None = None
    _tool_args: Any = None
    # A withSession context: the replacement Session's locally answered values.
    _replacement: "_ReplacementState | None" = None
    # A before_agent_start request: the prompt its shared options render now (runner.ts:1430-1433).
    _system_prompt: _SystemPrompt | None = None
    # A context made for a listener of this extension that the host dispatched while the factory runs (host_helpers.loading_call): the extension has not registered yet, so its host calls use the connection the factory opened, and only pi.events calls and the parse helpers are answered.
    _loading: bool = False

    @property
    def session_manager(self) -> "ReadonlySessionManager":
        return ReadonlySessionManager(self)

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
        if self._loading and not self.extension._bus._live:
            # The host answers only the bus before the extension registers, and a call it does not answer would wait for ever.
            if not method.startswith("events.") and method not in ("frontmatter.parse", "date.parse"):
                raise RuntimeError(f"{method}: the extension has not registered with the host yet")
            from . import host_helpers

            return {"result": host_helpers.loading_call(method, args, self.extension)}
        # An RPC UI effect is no suspension of the handler, as the Node runtime sends it (_RPC_SYNCHRONOUS_UI_EFFECTS).
        reports = bool(self.request_id) and not (method in _RPC_SYNCHRONOUS_UI_EFFECTS and self._rpc_mode())
        if reports and method not in _DIALOG_METHODS:
            self._report_request_state("blocked", "host_call")
        try:
            # A tool handler's host calls take part in the batch order (_ToolStartOrder); other contexts pass nothing.
            extra = {"start": self._tool_start} if self._tool_start is not None else {}
            if self._parent is not None:
                return self.extension._call(method, args, self.request_id, self._parent, **extra)
            return self.extension._call(method, args, self.request_id, **extra)
        finally:
            if reports:
                self._report_request_state("progress")

    def _rpc_mode(self) -> bool:
        """Whether the host runs RPC mode. Unlike ``mode`` it does not check that the context is current: a call from a replaced context fails on its own terms."""
        if self._replacement is not None and self._replacement.mode:
            return self._replacement.mode == "rpc"
        with self.extension._state_lock:
            return self.extension._mode == "rpc"

    def _block_for_user(self) -> None:
        if self.request_id:
            self._report_request_state("blocked", "user")

    def register_tool(self, definition: ToolDefinition) -> None:
        """Register a model-callable tool and wait for the host registry refresh."""
        self.extension.register_tool(definition)

    def call_host(self, method: str, args: Any = None) -> Any:
        return self._call(method, args).get("result")

    def create_agent_session(self, options: CreateAgentSessionOptions | None = None) -> AgentSession:
        """Pi's ``createAgentSession``: create a child agent session in the host and return once it is ready.

        The session belongs to this extension's connection and is disposed with it; see :mod:`pig_sdk.child_session` for the async contract.
        """
        return _create_agent_session(self.extension, self._call, options)

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
        (``toolCall``, ``result``, ``isError`` and ``durationMs`` when the tool ran). Tool failures come back with
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

    def notify(self, message: str, level: str = "") -> None:
        """An empty level is Pi's omitted notify type: shown as info, and RPC writes no notifyType."""
        self._call("ui.notify", {"message": message, "level": level})

    def set_status(self, key: str, text: str) -> None:
        """An empty text is a text, as in Pi (RPC mode writes it); :meth:`clear_status` removes the entry, as Pi's ``setStatus(key, undefined)`` does."""
        self._call("ui.setStatus", {"key": key, "text": text})

    def clear_status(self, key: str) -> None:
        """Remove the status entry for ``key``: Pi's ``setStatus(key, undefined)``."""
        self._call("ui.setStatus", {"key": key})

    def set_working_message(self, message: str | None = None) -> None:
        """Set the working label. ``None`` (or no argument) restores the default label, as Pi's ``setWorkingMessage()`` does; an empty string is a
        message and shows an empty label (``message ?? defaultWorkingMessage``)."""
        self._call("ui.setWorkingMessage", {} if message is None else {"message": message})

    def set_working_visible(self, visible: bool) -> None:
        self._call("ui.setWorkingVisible", {"visible": visible})

    def set_working_indicator(self, options: dict[str, Any]) -> None:
        self._call("ui.setWorkingIndicator", options)

    def set_hidden_thinking_label(self, label: str | None = None) -> None:
        """Set the hidden-thinking label. ``None`` (or no argument) restores the default label, as Pi's ``setHiddenThinkingLabel()`` does; an empty string is a
        label and shows an empty one (``label ?? defaultHiddenThinkingLabel``)."""
        self._call("ui.setHiddenThinkingLabel", {} if label is None else {"label": label})

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

    # select, input and editor return Pi's result: the JSON value the user's
    # client answered with (a str for every client that follows Pi's protocol,
    # and in RPC mode whatever JSON the client sent: number, bool, None, dict or
    # list) and whether Pi's result was defined. ok is False for undefined
    # (cancelled, timed out, an answer without a value), with None as the
    # value; a JSON null answer is (None, True). confirm returns the client's
    # ``confirmed`` the same way, False when cancelled or unanswered.

    @staticmethod
    def _dialog_value(result: dict[str, Any], member: str) -> tuple[Any, bool]:
        if result.get("ok") is not True:
            return None, False
        return result.get(member), True

    def select(self, title: str, options: list[str], *, timeout: float | None = None) -> tuple[Any, bool]:
        self._block_for_user()
        args = self._dialog_args({"title": title, "options": options}, timeout)
        return self._dialog_value(self._call("ui.select", args).get("result") or {}, "selected")

    def confirm(self, title: str, message: str, *, timeout: float | None = None) -> Any:
        self._block_for_user()
        args = self._dialog_args({"title": title, "message": message}, timeout)
        result = self._call("ui.confirm", args).get("result") or {}
        return result.get("confirmed", False)

    def input(self, title: str, placeholder: str | None = None, *, timeout: float | None = None) -> tuple[Any, bool]:
        """``placeholder=None`` is Pi's omitted placeholder (RPC mode writes no placeholder member); an empty string is a placeholder (``placeholder: ""``)."""
        self._block_for_user()
        args: dict[str, Any] = {"title": title}
        if placeholder is not None:
            args["placeholder"] = placeholder
        return self._dialog_value(self._call("ui.input", self._dialog_args(args, timeout)).get("result") or {}, "text")

    def editor(self, title: str, prefill: str | None = None) -> tuple[Any, bool]:
        """``prefill=None`` is Pi's omitted prefill (RPC mode writes no prefill member); an empty string is a prefill (``prefill: ""``)."""
        self._block_for_user()
        args: dict[str, Any] = {"title": title}
        if prefill is not None:
            args["prefill"] = prefill
        return self._dialog_value(self._call("ui.editor", args).get("result") or {}, "text")

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

    def _assert_active(self) -> None:
        """Raise the Host's stale message once the session was replaced or reloaded, as Pi's ExtensionContext getters throw it (runner.ts:571-600). A replacement context belongs to the new session and is not stale."""
        if self._replacement is not None:
            return
        with self.extension._state_lock:
            message = self.extension._stale_message
        if message is not None:
            raise RuntimeError(message)

    def _current_model(self) -> tuple[str, str]:
        """The model's id and provider id. A replacement context asks the host, since this process replicates only the requesting Session's model."""
        if self._replacement is not None:
            try:
                return _model_identity(self._call("getModelInfo").get("result"))
            except HostCallError:
                return self._replacement.model, self._replacement.model_provider
        with self.extension._state_lock:
            return self.extension._model, self.extension._model_provider

    @property
    def model(self) -> str:
        """The active model's id: Pi's ``ctx.model.id``, the identity every SDK reports (Go ``Model()`` and Rust ``model()`` return the same id). ``""`` while the Session has no model."""
        self._assert_active()
        return self._current_model()[0]

    @property
    def model_provider(self) -> str:
        """The active model's provider id (Pi's ``ctx.model.provider``), ``""`` while the Session has no model."""
        self._assert_active()
        return self._current_model()[1]

    @property
    def model_qualified(self) -> str:
        """The ``provider/id`` string ``set_model`` takes; just the id when the provider is unknown."""
        self._assert_active()
        identity, provider = self._current_model()
        return f"{provider}/{identity}" if provider else identity

    @property
    def cwd(self) -> str:
        self._assert_active()
        if self._replacement is not None:
            return self._replacement.cwd
        with self.extension._state_lock:
            return self.extension._cwd

    @property
    def mode(self) -> str:
        """Run mode: "tui", "rpc", "json", or "print". Guard terminal-only
        UI on "tui". Defaults to "print" when unspecified."""
        self._assert_active()
        if self._replacement is not None and self._replacement.mode:
            return self._replacement.mode
        with self.extension._state_lock:
            return self.extension._mode or "print"

    @property
    def session_name(self) -> str:
        """The session's name, ``""`` while it has none: the replicated value behind Pi's ``ctx.sessionManager.getSessionName()``. State updates keep it current and ``set_session_name`` is visible to the next read."""
        self._assert_active()
        if self._replacement is not None:
            try:
                return self.get_session_name() or ""
            except HostCallError:
                return self._replacement.session_name
        with self.extension._state_lock:
            return self.extension._session_name

    @property
    def config_home(self) -> str:
        """The pig config root: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig.

        An empty variable falls through to the next choice, a leading ``~`` or ``~/`` expands to the home directory, and any other value stays
        literal. Raises ``RuntimeError``, never returns a relative path, when the home directory is needed and cannot be found. The host's
        ``internal/configroot`` and the Go, Rust and Node SDKs follow the same policy.
        """
        return _config_home()

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

    def parse_frontmatter(self, content: str) -> "ParsedFrontmatter":
        """Pi's parseFrontmatter over this handler's own connection; see :func:`pig_sdk.parse_frontmatter`."""
        from . import host_helpers

        return host_helpers._decode_frontmatter(self._call("frontmatter.parse", {"content": content}).get("result") or {})

    def parse_date(self, text: str) -> "float | None":
        """``Date.parse(text)`` over this handler's own connection; see :func:`pig_sdk.parse_date`."""
        from . import host_helpers

        return host_helpers._decode_date(self._call("date.parse", {"text": text}).get("result") or {})

    def generate_diff_string(self, old_content: str, new_content: str, context_lines: "int | None" = None) -> "DiffString":
        """Pi's generateDiffString over this handler's own connection; see :func:`pig_sdk.generate_diff_string`."""
        from . import host_helpers

        return host_helpers._decode_diff_string(self._call("diff.string", host_helpers._diff_args(old_content, new_content, context_lines)).get("result") or {})

    def generate_unified_patch(self, path: str, old_content: str, new_content: str, context_lines: "int | None" = None) -> str:
        """Pi's generateUnifiedPatch over this handler's own connection; see :func:`pig_sdk.generate_unified_patch`."""
        from . import host_helpers

        return (self._call("diff.patch", host_helpers._patch_args(path, old_content, new_content, context_lines)).get("result") or {}).get("patch", "")

    def copy_to_clipboard(self, text: str) -> None:
        """Pi's copyToClipboard over this handler's own connection; see :func:`pig_sdk.copy_to_clipboard`."""
        self._call("clipboard.copy", {"text": text})

    def convert_to_png(self, base64_data: str, mime_type: str) -> "ConvertedImage | None":
        """Pi's convertToPng over this handler's own connection; see :func:`pig_sdk.convert_to_png`."""
        from . import host_helpers

        return host_helpers._decode_converted(self._call("image.convertToPng", {"data": base64_data, "mimeType": mime_type}).get("result") or {})

    def resize_image(self, input_bytes: bytes, mime_type: str, options: "ImageResizeOptions | None" = None) -> "ResizedImage | None":
        """Pi's resizeImage over this handler's own connection; see :func:`pig_sdk.resize_image`."""
        from . import host_helpers

        return host_helpers._decode_resized(self._call("image.resize", host_helpers._resize_args(input_bytes, mime_type, options)).get("result") or {})

    def strip_frontmatter(self, content: str) -> str:
        """Pi's stripFrontmatter over this handler's own connection."""
        return self.parse_frontmatter(content).body

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
        ``namespace``, ``annotations`` and ``reachesTools`` (when set), and
        ``sourceInfo``. Built-in tools come first.

        ``reachesTools`` (types.ts:2122-2130) is a function of a call's input
        that answers ``"all"`` or the words that may name the tools the call
        reaches. A function cannot cross the wire, so the host marks the tool
        and the function runs the hook through the host; a host failure
        raises ``HostCallError``, as the hook's throw."""
        tools = list(self._reply_field("getAllTools", "tools"))
        for tool in tools:
            if isinstance(tool, dict) and tool.get("reachesTools") is True:
                tool["reachesTools"] = self._tool_reaches_tools(str(tool.get("name", "")))
        return tools

    def _tool_reaches_tools(self, name: str) -> ToolReachesToolsHandler:
        """The ``reachesTools`` of a tool :meth:`get_all_tools` lists: the host runs the hook of the tool it lists under the name."""

        def reaches_tools(input: dict[str, Any]) -> Any:
            return self._call("tool.reachesTools", {"tool": name, "input": input if input is not None else {}}).get("result")

        return reaches_tools

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
        """The current system prompt. In a before_agent_start handler it is the prompt the emission's shared options
        render now, including this handler's own edits, the same value as ``data["systemPrompt"]`` (runner.ts:1430-1433)."""
        if self._system_prompt is not None:
            self._assert_active()
            return self._system_prompt.get()
        return self._reply_field("getSystemPrompt", "prompt")

    def get_system_prompt_options(self) -> dict[str, Any]:
        """Base inputs pi currently uses to build the system prompt
        (customPrompt, selectedTools, hiddenTools, toolSnippets, promptGuidelines,
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
        if self._replacement is not None:
            # The local mirror replicates the Session that requested the replacement.
            return self.session_manager.get_branch() or []
        self.extension._ensure_session_log()
        return self.extension._session_mirror.get_branch()

    def get_entries(self) -> list[dict[str, Any]]:
        if self._replacement is not None:
            return self.session_manager.get_entries() or []
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

    @property
    def footer_data(self) -> "FooterData":
        """Pi's ``ReadonlyFooterDataProvider`` (footer-data-provider.ts:103-163,387), the third argument of a ``ui.setFooter`` factory. A footer renderer reads it from the context it was installed with; ``set_footer_renderer`` renders again when the host's footer data changes."""
        return FooterData(self.extension)

    def has_ui(self) -> bool:
        """Whether the host binds a UI context: interactive and RPC, not print/JSON."""
        self._assert_active()
        if self._replacement is not None:
            return self._replacement.has_ui
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
        :meth:`on_width_change` handler. In RPC mode, where the width never
        changes and Pi writes one request per call, in call order and before
        the handler's response, the list waits for the host like every other
        shape. Every other shape waits for the host.

        A :class:`kit.View` (D107) is rendered by the host at the widget's
        width, the subprocess form of Pi's ``setWidget(key, factory)`` for a
        tree of Pi's components. Without options it goes out as a
        ``widget_push`` frame, as a string list does; with options (upstream
        ``ExtensionWidgetOptions``) it waits for the host.
        """
        if isinstance(content, kit.View):
            self.extension._set_widget_view(self, key, content, options)
            return
        with self.extension._views.widgets_lock:
            self.extension._views.widgets.pop(key, None)
        self.extension._replace_surface_id(f"ui.setWidget:{key}", None)
        if isinstance(content, list) and all(isinstance(x, str) for x in content) and options is None and not self._rpc_mode():
            self.extension._push_widget(key, content)
            return
        self._call("ui.setWidget", {"key": key, "content": content, "options": options or {}})

    def set_widget_renderer(self, key: str, render: "Callable[[int], list[str]] | None", options: dict[str, Any] | None = None) -> None:
        """Install the widget ``key`` with a renderer that lays its rows out at the host's terminal width, the subprocess form of the component factory Pi's ``ctx.ui.setWidget(key, factory, options)`` takes.

        It follows the contract of :meth:`set_footer_renderer`: the SDK renders at the width the host reports and again after every width change, sends each set of rows with the width it was rendered for, and the host paints them only at that width, so a widget never shows rows laid out for the old width. ``options`` is the placement (``{"placement": "aboveEditor" | "belowEditor"}``). ``None`` clears the widget. An exception in ``render`` is reported as "widget render failed: ..." and leaves the previous rows. :meth:`set_widget` with the same key replaces the renderer.
        """
        if render is None:
            self.set_widget(key, None, options)
            return
        if not callable(render):
            raise TypeError("a widget renderer must be callable")
        surface = _SurfaceRenderer("ui.setWidget", render, key, options)
        with self.extension._views.widgets_lock:
            self.extension._views.widgets.pop(key, None)
        self.extension._replace_surface_id(f"ui.setWidget:{key}", surface)
        surface.push(self)

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

    def register_sprite(self, definition: SpriteDefinition) -> None:
        """Add a sprite to /sprite, where the user can choose and save it.

        Pig validates the definition; a rejected definition raises
        :class:`HostCallError`. Registering the same ID again replaces this
        extension's sprite, and the sprite leaves /sprite when the extension
        unloads.
        """
        self._call("ui.registerSprite", definition._to_wire())

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

    def set_footer_view(self, view: kit.View) -> None:
        """Replace the footer with a :class:`kit.View` (D107), which the host
        renders at its width every frame, as Pi renders a footer component.
        It replaces any footer rows or renderer; :meth:`clear_footer` restores
        the default."""
        self._set_surface_view("ui.setFooter", view)

    def set_header_view(self, view: kit.View) -> None:
        """Replace the header with a :class:`kit.View` (D107), as
        :meth:`set_footer_view` does for the footer."""
        self._set_surface_view("ui.setHeader", view)

    def _set_surface(self, method: str, lines: "list[str] | None") -> None:
        self.extension._replace_surface(method, None)
        if lines is None:
            self._call(method, {"clear": True})
            return
        self._call(method, _surface_args([str(line) for line in lines], self.width))

    def _set_surface_view(self, method: str, view: kit.View) -> None:
        # An authoritative view: no rows and no width, since the host renders it at its own width.
        views = self.extension._views
        encoded = views.encode(view)
        self.extension._replace_surface(method, None)
        # Held across the send, so an eviction's resend and this frame keep their order.
        with views.surfaces_lock:
            views.surfaces[method] = encoded
            self._call(method, {"view": views.wire(encoded)})

    def _set_surface_renderer(self, method: str, render: "Callable[[int], list[str]] | None") -> None:
        if render is None:
            self._set_surface(method, None)
            return
        # upstream: rpc-mode.ts setFooter/setHeader ignore the factory and runner.ts noOpUIContext does too: Pi calls a footer or header factory only in the TUI.
        if not self.has_ui() or self.mode == "rpc":
            return
        if not callable(render):
            raise TypeError("a surface renderer must be callable")
        surface = _SurfaceRenderer(method, render)
        self.extension._replace_surface(method, surface)
        # The initial push belongs to the caller's request, as the Go SDK's does; later
        # width-change pushes have no parent and use a fresh Context.
        surface.push(self)

    def set_editor_component(self, factory: EditorFactory | None) -> None:
        """Install an editor in place of the host's, as Pi's ``ctx.ui.setEditorComponent`` does. The factory receives the host's default editor (the component's ``super``); ``None`` restores the host's editor. With no UI, or in RPC mode, it is ignored, as in Pi."""
        # upstream: modes/rpc/rpc-mode.ts setEditorComponent is a no-op, so the factory never runs there.
        if not self.has_ui() or self.mode == "rpc":
            return
        if factory is None:
            self.clear_editor_component()
            return
        self.extension._editor_seq += 1
        _editor_component.install_editor(self.extension, self.extension._editor_seq, factory)

    def clear_editor_component(self) -> None:
        _editor_component.clear_editor(self.extension)
        self._call("ui.setEditorComponent", {"clear": True})

    def get_editor_component(self) -> EditorFactory | None:
        """Return the factory this extension installed with :meth:`set_editor_component` while that editor is the host's, as Pi's
        ``ctx.ui.getEditorComponent`` returns the configured factory (interactive-mode.ts:2664). ``None`` before an install, once this or another
        extension restored the host's editor, and with no UI or in RPC mode, where installing does nothing (rpc-mode.ts:281). Another extension's
        factory lives in that extension's process, so after another extension installs its editor the result is ``None`` where Pi returns that
        factory."""
        with self.extension._editor_lock:
            session = self.extension._editor
            return session.factory if session is not None else None

    def custom(
        self,
        component: RemoteComponent | ViewComponent | dict[str, Any] | None = None,
        options: dict[str, Any] | None = None,
    ) -> Any:
        """Open a focused subprocess component, or return None without callbacks when no UI is bound.

        A component with ``view(width)`` is a :class:`ViewComponent` (D107):
        its frames are kit views the host renders, the host owns the state of
        their lists, and the lists' callbacks reach ``handle_view_event`` in
        order with the input.

        Passing the legacy options-only shape retains the host's explicit
        unsupported response because it has no serializable component. With no UI, or in
        RPC mode, nothing runs and the result is None, as Pi's no-op and RPC UIs resolve undefined.
        """
        if not self.has_ui():
            return None
        if self.mode == "rpc":
            # upstream: rpc-mode.ts custom() resolves undefined and never runs the component. The host call keeps the dialog scope, as the Node runtime's does.
            with self.extension._overlay_lock:
                self.extension._overlay_seq += 1
                key = f"custom-{self.extension._overlay_seq}"
            self._call("ui.custom", {"key": key})
            return None
        self._block_for_user()
        if component is None or isinstance(component, dict):
            raw_options = component if isinstance(component, dict) else options
            return self._call("ui.custom", raw_options or {}).get("result")
        result = self.extension._run_remote_component(component, options or {}, self.request_id, self._parent, start=self._tool_start)
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

    def on_theme_change(self, handler: "Callable[[Theme], Any]") -> "Callable[[], None]":
        """Subscribe to changes of the host's theme: the user switching theme, a custom theme's file reloading, the terminal reporting new colors. ``handler(theme)`` receives :attr:`theme` after it took the new palette.

        Pi calls a widget component's ``invalidate()`` on a theme change, and the component reads ``ctx.ui.theme`` at its next render, so it never paints the colors of an old theme. A subprocess extension pushes rows that were colored when it built them, so it re-renders from this subscription. :meth:`set_widget_renderer` colors through the live theme at every render and needs no handler for it.

        Handlers run on a worker thread, one at a time in the order the changes arrived, never on the thread that reads the host's replies, so a handler can make a blocking host call. A handler that raises is reported on stderr and does not stop the others. Returns an idempotent unsubscribe callable; a delivery already queued still reaches the handlers subscribed when the change arrived. A palette equal to the one before is not a change.
        """
        if not callable(handler):
            raise TypeError("on_theme_change requires a callable handler")
        return self.extension._theme_feed.subscribe(handler)

    def on_tools_expanded_change(self, handler: "Callable[[bool], Any]") -> "Callable[[], None]":
        """Subscribe to the host toggling tool-output expansion (Pi's expand key, or ``set_tools_expanded`` from any extension). ``handler(expanded)`` receives the new state after it changed.

        The original of a widget reads ``ui.getToolsExpanded()`` at every frame and Pi redraws on its expand key; a subprocess extension pushes rows built for one state and re-renders from this subscription. It follows the contract of :meth:`on_theme_change`.
        """
        if not callable(handler):
            raise TypeError("on_tools_expanded_change requires a callable handler")
        return self.extension._tools_expanded_feed.subscribe(handler)

    def bell(self) -> None:
        """Ring the terminal bell (BEL) of the interactive terminal between frames, the call a Pi extension makes by writing ``"\\x07"`` to ``process.stdout``. It does nothing where there is no terminal UI: print, JSON and RPC modes."""
        self._call("ui.bell", {})

    def extension_source_info(self) -> dict[str, Any] | None:
        """The provenance the loader stamped on this extension: upstream's ``extension.sourceInfo`` (``types.ts`` ``SourceInfo``), where ``ExtensionRunner.getExtensionPaths`` reads the extension's path from. ``path`` is the entry file and ``baseDir`` the package root when the extension came from a Package; a Pi extension reads both from ``import.meta.url``, which a subprocess extension has no equivalent of. A dict with ``path``, ``source``, ``scope``, ``origin`` and, for a Package, ``baseDir``, as ``get_commands`` reports a command's ``sourceInfo``. None when the host holds no source for this extension."""
        with self.extension._state_lock:
            info = self.extension._source_info
        return None if info is None else dict(info)

    def tool_call_index(self) -> int | None:
        """The running tool call's position among the tool calls of the assistant message that issued it, the batch agent-loop.ts executes (the ``toolCall`` content blocks of that message, counted from 0). None outside a tool handler and for a call no assistant message issued, such as a nested :meth:`execute_tool` call. The host sends it with the call, so a batch is ordered without reading the session's leaf entry."""
        return self._tool_index

    def tool_input(self) -> str | None:
        """The running tool call's arguments as the JSON text ``JSON.stringify`` gives for the object Pi hands ``execute``: members in the order the model wrote them (array-index keys first, ascending, as JavaScript orders them), numbers as JavaScript prints them. The arguments are the ones the host sent the handler, after the tool's ``prepare_arguments``. None outside a tool handler."""
        if self._tool_args is None:
            return None
        from .serialize_conversation import _json_stringify

        return _json_stringify(self._tool_args)

    def tool_params(self) -> dict[str, Any] | None:
        """The running tool call's arguments with every object, at any depth, in the member order JavaScript's ``Object.keys`` gives (array-index keys ascending, then the others in insertion order), which Pi's ``execute`` params keep and an extension that writes the arguments back out (``JSON.stringify(params.metadata)``) relies on. None outside a tool handler or when the arguments are not an object."""
        return _js_ordered(self._tool_args) if isinstance(self._tool_args, dict) else None

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

    def exec(self, command: str, args: list[str] | None = None, *, timeout: float | None = None, cwd: str | None = None, signal: ProviderSignal | None = None) -> ExecResult:
        """Run a command through the host's executor.

        ``timeout`` (milliseconds), ``cwd`` and ``signal`` are upstream
        ExecOptions; a command the timeout kills reports ``killed``. Setting
        the ``signal`` (a :class:`ProviderSignal`) terminates the command, as
        Pi does: the command alone receives SIGTERM (the process is killed on
        Windows), no descendant is signalled, and the call returns the output
        written so far with ``killed`` set and ``exit_code`` 0, which is what a
        command that a signal ended reports. A signal that is set before the
        call starts the command and ends it at once. Nothing follows the
        SIGTERM, as upstream's: a command that ignores it keeps the call
        waiting. Without a ``signal`` the command ends when the calling request
        is cancelled; a ``signal`` replaces that cancellation, so cancelling
        the request then leaves the command running and the call waits for its
        result. Raises HostCallError, a RuntimeError whose message is the
        host's, when the host reports a failure, such as "spawn EINVAL" for a
        batch file on Windows, so a caller sees the reason rather than an exit
        code of zero it never produced.
        """
        if signal is not None and not callable(getattr(signal, "subscribe", None)):
            raise TypeError("signal must be a ProviderSignal")
        call_args: dict[str, Any] = {"command": command, "args": args or []}
        options: dict[str, Any] = {}
        if timeout is not None:
            options["timeout"] = timeout
        if cwd is not None:
            options["cwd"] = cwd
        if options:
            call_args["options"] = options
        if signal is None:
            reply = self._call("exec", call_args)
        else:
            reply = self.extension._exec_with_signal(self, call_args, signal)
        result = reply.get("result") or {}
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
        """Start a new session. ``opts`` may hold ``parentSession``, a ``setup`` callable taking a SetupSessionManager that seeds the new session before it starts (types.ts:411), and a ``withSession`` callable taking a ReplacedSessionContext."""
        args = dict(opts or {})
        setup = args.pop("setup", None)
        if setup is None:
            return self._call_replacement("newSession", args)
        if not callable(setup):
            raise TypeError("setup must be callable")
        handle, entry = self.extension._setups.add(self.extension.name + ":setup", setup)
        args["setup"] = handle
        try:
            return self._call_replacement("newSession", args)
        except HostCallError:
            if entry.error is not None:
                raise entry.error from None
            raise
        finally:
            self.extension._setups.remove(handle)

    def fork(self, entry_id: str, opts: dict[str, Any] | None = None) -> Any:
        """Fork at an entry. ``opts`` may hold ``position`` and a ``withSession`` callable taking a ReplacedSessionContext."""
        args = dict(opts or {})
        args["entryId"] = entry_id
        return self._call_replacement("fork", args)

    def navigate_tree(self, target_id: str, opts: dict[str, Any] | None = None) -> Any:
        args = dict(opts or {})
        args["targetId"] = target_id
        return self._call("navigateTree", args).get("result")

    def switch_session(self, session_path: str, opts: dict[str, Any] | None = None) -> Any:
        """Switch to a session file. ``opts`` may hold a ``withSession`` callable taking a ReplacedSessionContext."""
        args = dict(opts or {})
        args["sessionPath"] = session_path
        return self._call_replacement("switchSession", args)

    def _call_replacement(self, method: str, args: dict[str, Any]) -> Any:
        """A withSession callback cannot cross the process boundary, so the call names it by handle and the host runs it with a with_session request before the call returns. A callback exception fails the call with that exception, as Pi's awaited callback rejects the call."""
        callback = args.pop("withSession", None)
        if callback is None:
            return self._call(method, args).get("result")
        if not callable(callback):
            raise TypeError("withSession must be callable")
        handle, entry = self.extension._with_sessions.add(self.extension.name, callback)
        args["withSession"] = handle
        try:
            return self._call(method, args).get("result")
        except HostCallError:
            if entry.error is not None:
                raise entry.error from None
            raise
        finally:
            self.extension._with_sessions.remove(handle)

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
class ReplacedSessionContext(Context):
    """Pi's ReplacedSessionContext (types.ts:442-452): the command context of the replacement Session that new_session, fork and switch_session pass to their ``withSession`` callback.

    Its host calls act on the replacement Session, and send_message and send_user_message return after the Session operation, including the turn they trigger. cwd, mode, has_ui and model answer for the replacement Session.
    """


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
    """Raised by a value-returning login callback when the user dismissed the host prompt.

    Its message is Pi's "Login cancelled", so a flow that lets it propagate ends the login as cancelled.
    """

    def __init__(self) -> None:
        super().__init__("Login cancelled")


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


def _context_messages_encoding(messages: Any) -> str | None:
    """The encoding a reply to a context event would carry for messages, or None when they do not encode: the reply then carries the list."""
    try:
        return json.dumps(messages)
    except Exception:  # noqa: BLE001 - Any value a handler leaves in a message may fail to encode.
        return None


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


def _js_ordered(value: Any) -> Any:
    """``value`` with every object's members in the order JavaScript's Object.keys gives: array-index keys ascending, then the others in insertion order."""
    if isinstance(value, dict):
        return {key: _js_ordered(value[key]) for key in _ordered_keys(value)}
    if isinstance(value, list):
        return [_js_ordered(item) for item in value]
    return value


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
        # Changes of the replicated theme and tool-expansion state reach on_theme_change / on_tools_expanded_change handlers on workers of their own (StateFeed). *_known says the host sent a first value, so a later different one is a change.
        self._theme_feed = StateFeed(self, "theme")
        self._tools_expanded_feed = StateFeed(self, "tools expanded")
        self._theme_palette: Any = None
        self._theme_known = False
        self._tools_expanded = False
        self._tools_expanded_known = False
        # The provenance the host stamped on this extension (Context.extension_source_info), from the ready payload.
        self._source_info: dict[str, Any] | None = None
        # on_loaded handlers run on a worker after ready; requests wait for _loaded (see _handle_request_thread).
        self._loaded_handlers: list[Callable[[Context], Any]] = []
        self._loaded = threading.Event()
        self._loaded.set()
        self._footer_data: dict[str, Any] = {"branch": None, "statuses": {}, "count": 0}
        self._branch_subscribers: dict[int, Callable[[], Any]] = {}
        self._branch_subscriber_seq = 0
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
        self._tool_renderer_resolvers: list[ToolRendererResolver] = []
        self._resolved_tool_renderers: dict[str, ToolRenderers] = {}
        self._oauth_providers: dict[str, OAuthProvider] = {}
        self._tool_handlers: dict[str, ToolHandler] = {}
        self._tool_prepare_handlers: dict[str, ToolPrepareArguments] = {}
        self._children = _ChildSessions()
        self._tool_batches = _ToolBatches()
        self._request_starts = _RequestStartOrder()
        self._command_handlers: dict[str, CommandHandler] = {}
        self._with_sessions = _WithSessionRegistry()
        self._setups = _WithSessionRegistry()
        self._command_completions: dict[str, ArgumentCompletionsHandler] = {}
        self._event_handlers: dict[int, EventHandler] = {}
        self._event_replicas: dict[tuple[str, str], tuple[int, Any]] = {}
        self._event_lock = threading.Lock()
        self._event_next_id = 0
        self._shortcut_handlers: dict[str, ShortcutHandler] = {}
        self._renderer_handlers: dict[str, RendererHandler] = {}
        self._entry_renderer_handlers: dict[str, RendererHandler] = {}
        self._message_view_renderers: dict[str, Callable[..., kit.View]] = {}
        self._entry_view_renderers: dict[str, Callable[..., kit.View]] = {}
        self._tool_call_renderers: dict[str, ToolRenderCallHandler] = {}
        self._tool_result_renderers: dict[str, ToolRenderResultHandler] = {}
        self._tool_call_view_renderers: dict[str, ToolRenderCallViewHandler] = {}
        self._tool_result_view_renderers: dict[str, ToolRenderResultViewHandler] = {}
        # One renderer state per tool card; renders of one card run one at a time.
        self._tool_render_cards: dict[str, tuple[threading.Lock, dict[str, Any]]] = {}
        self._tool_render_lock = threading.Lock()
        self._flag_defaults: dict[str, Any] = {}
        self._session_name: str = ""
        self._model_provider: str = ""
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
        # Frames queued by weakref finalizers. A finalizer runs on whichever thread triggers garbage collection, possibly
        # one already holding _write_lock, so it only enqueues here (SimpleQueue.put is safe there) and never sends.
        self._finalizer_frames: queue.SimpleQueue[dict[str, Any]] = queue.SimpleQueue()
        self._state_lock = threading.Lock()
        # The Host's invalidate message; the first one wins (runner.ts:725-727).
        self._stale_message: str | None = None
        self._session_mirror = SessionMirror()
        # Serializes the one-time session-log subscribe so concurrent first
        # readers make a single host call.
        self._session_sub_lock = threading.Lock()
        self._session_sub_error: Exception | None = None
        self._pending: dict[str, threading.Event] = {}
        self._pending_results: dict[str, dict[str, Any]] = {}
        self._pending_parents: dict[str, str] = {}
        # A dialog call made by a tool handler maps to that handler's response slot; the reader settles the slot when it reads the answer.
        self._pending_slots: dict[str, _SettleSlot] = {}
        self._cancelled_calls: set[str] = set()
        self._call_id = 0
        self._active: dict[str, tuple[threading.Event, str | None]] = {}
        self._request_parents: dict[str, _RequestParent] = {}
        self._request_threads: set[threading.Thread] = set()
        self._request_threads_lock = threading.Lock()
        self._overlay_seq = 0
        self._model_stream_seq = 0
        self._model_streams: dict[str, ModelEventStream] = {}
        self._model_stream_callbacks: dict[str, tuple[dict[str, Any], dict[str, Callable[..., Any]]]] = {}
        self._model_stream_lock = threading.Lock()
        self._overlays: dict[str, _RemoteOverlayState] = {}
        self._editor_lock = threading.Lock()
        self._editor: Any = None
        self._editor_seq = 0
        self._overlay_lock = threading.Lock()
        # The frontend flag, the image refs sent, and the live widget, header and footer views (D107).
        self._views = kit._ViewLedger()
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
        self._tool_reach_handlers: dict[str, ToolReachesToolsHandler] = {}
        # State the host replicates (StatePayload.settings, mcpServers), guarded by _state_lock. ctx.tools asks the host when it is read.
        self._settings: dict[str, Any] | None = None
        self._mcp_servers: list[dict[str, Any]] = []
        self._execute_seq = 0
        self._exec_seq = 0
        self._nested_calls: dict[str, _NestedCall] = {}
        self._bash_operations = _BashOperationsTable()
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

    def _register_tool(self, declaration, handler, prepare=None, render_call=None, render_result=None, prepare_loadout=None, call_view=None, result_view=None, reaches_tools=None):
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
            for handlers, callback in ((self._tool_prepare_handlers, prepare), (self._tool_call_renderers, render_call), (self._tool_result_renderers, render_result), (self._tool_loadout_handlers, prepare_loadout), (self._tool_reach_handlers, reaches_tools), (self._tool_call_view_renderers, call_view), (self._tool_result_view_renderers, result_view)):
                if callback is None:
                    handlers.pop(name, None)
                else:
                    handlers[name] = callback
            if self._tools_live:
                self._call("registerTool", declaration)

    def register_tool(self, definition: ToolDefinition) -> None:
        """Replace a tool in place and refresh a running Session before returning."""
        declaration = self._tool_declaration(definition, "tool definition")
        self._register_tool(declaration, definition.execute, definition.prepare_arguments, definition.render_call, definition.render_result, definition.prepare_loadout, definition.call_view, definition.result_view, definition.reaches_tools)

    def _tool_handler(self, req: dict[str, Any], name: str) -> ToolHandler:
        """The execute of a tool_call: a registered tool, or a custom tool of the child session the request names."""
        child = req.get("child_session")
        if child:
            session = self._children.lookup(str(child))
            if session is None or name not in session._tools:
                raise KeyError(name)
            return session._tools[name]
        with self._tool_registration_lock:
            return self._tool_handlers[name]

    def _tool_prepare_handler(self, req: dict[str, Any], name: str) -> ToolPrepareArguments | None:
        """The prepare_arguments of a request; see :meth:`_tool_handler`."""
        child = req.get("child_session")
        if child:
            session = self._children.lookup(str(child))
            return None if session is None else session._prepares.get(name)
        with self._tool_registration_lock:
            return self._tool_prepare_handlers.get(name)

    def _tool_declaration(self, definition: ToolDefinition, what: str) -> dict[str, Any]:
        """The wire declaration of a tool definition, after the checks of Pi's registerTool (loader.ts)."""
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
        if definition.reaches_tools is not None and not callable(definition.reaches_tools):
            raise TypeError("reaches_tools must be callable")
        declaration = {
            "name": name, "label": definition.label, "description": definition.description,
            "parameters": definition.parameters, "prompt_snippet": definition.prompt_snippet,
            "prompt_guidelines": definition.prompt_guidelines, "execution_mode": definition.execution_mode,
            "constrained_sampling": definition.constrained_sampling, "render_shell": definition.render_shell,
            "renders_call": definition.render_call is not None or definition.call_view is not None,
            "renders_result": definition.render_result is not None or definition.result_view is not None,
            "output_schema": definition.output_schema, "exposure": definition.exposure, "namespace": definition.namespace,
            "annotations": definition.annotations, "default_active": definition.default_active,
            "prepares_loadout": True if definition.prepare_loadout is not None else None,
            "reaches_tools": True if definition.reaches_tools is not None else None,
        }
        declaration = {key: value for key, value in declaration.items() if value is not None}
        _ensure_jsonable(declaration, f"{what} for {name}")
        return declaration

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
                call_id, event = self._begin_call("executeTool", payload, ctx.request_id, ctx._parent, event=_NestedCallEvent(nested), start=ctx._tool_start)
            else:
                # An explicit signal replaces the calling tool's (runner.ts:980, `options.signal ?? signal`): the call is not tied to the calling request, so cancelling that request neither cancels it on the host nor ends this wait.
                call_id, event = self._begin_call("executeTool", payload, event=_NestedCallEvent(nested), start=ctx._tool_start)
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

    def _exec_with_signal(self, ctx: Context, call_args: dict[str, Any], signal: ProviderSignal) -> dict[str, Any]:
        """pi.exec with options.signal: the call is not tied to the calling request (core/exec.ts has no signal but the caller's), and the signal's abort reaches the host as exec.cancel."""
        with self._state_lock:
            self._exec_seq += 1
            exec_id = f"exec-{self._exec_seq}"
        payload = {**call_args, "execId": exec_id, "ownSignal": True}
        ctx._report_request_state("blocked", "host_call")
        unsubscribe: Callable[[], None] | None = None
        try:
            call_id, event = self._begin_call("exec", payload)
            # After the call is sent: the host applies a cancel only after the call it names started the command.
            unsubscribe = signal.subscribe(lambda: self._cancel_exec(exec_id))
            return self._wait_call(call_id, event, "exec")
        finally:
            if unsubscribe is not None:
                unsubscribe()
            ctx._report_request_state("progress")

    def _cancel_exec(self, exec_id: str) -> None:
        """Send the cancel of one exec call from a thread of its own, so the signal's setter never waits for the host."""
        def cancel() -> None:
            try:
                self._call("exec.cancel", {"execId": exec_id})
            except Exception:  # noqa: BLE001 - the call ended or the connection closed: nothing is left to cancel  # nosec B110
                pass

        threading.Thread(target=cancel, name=f"pig-exec-cancel-{exec_id}", daemon=True).start()

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
        with self._tool_registration_lock:
            self._commands.append(declaration)
            self._command_handlers[name] = handler
            self._publish_declaration({"commands": [declaration]})

    def _publish_declaration(self, declarations: dict[str, Any]) -> None:
        """Send a command, shortcut, flag or renderer registered after load to the host (registerDeclaration): Pi writes each into the extension's own Map and every runner getter reads it live (loader.ts). Before the register message the registration waits in it. The caller holds the registration lock."""
        if self._tools_live:
            self._call("registerDeclaration", declarations)

    def shortcut(self, key: str, description: str, handler: ShortcutHandler) -> None:
        declaration = {"key": key, "description": description}
        with self._tool_registration_lock:
            self._shortcuts.append(declaration)
            self._shortcut_handlers[key] = handler
            self._publish_declaration({"shortcuts": [declaration]})

    def flag(self, name: str, description: str = "", flag_type: str = "string", default: Any = None) -> None:
        if flag_type not in {"boolean", "string"}:
            raise ValueError("flag_type must be 'boolean' or 'string'")
        _ensure_jsonable(default, f"flag default for {name}")
        decl: dict[str, Any] = {"name": name, "description": description, "type": flag_type}
        if default is not None:
            decl["default"] = default
        with self._tool_registration_lock:
            self._flags.append(decl)
            if name not in self._flag_defaults or self._flag_defaults[name] is None:
                self._flag_defaults[name] = default
            self._publish_declaration({"flags": [decl]})

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

    def tool_renderers(
        self,
        name: str,
        *,
        render_call: ToolRenderCallHandler | None = None,
        render_result: ToolRenderResultHandler | None = None,
        render_shell: str = "default",
        call_view: ToolRenderCallViewHandler | None = None,
        result_view: ToolRenderResultViewHandler | None = None,
    ) -> None:
        """Set tool renderers and publish the updated definition in a running Session.

        ``call_view`` and ``result_view`` are the view forms (D107): they return a :class:`kit.View` the host renders
        at the card's width, declare their phase rendered, and win over the line form set for the same phase.
        """
        if render_shell not in {"default", "self"}:
            raise ValueError("render_shell must be 'default' or 'self'")
        with self._tool_registration_lock:
            for tool in self._tools:
                if tool.get("name") != name:
                    continue
                declaration = dict(tool)
                declaration["render_shell"] = render_shell
                declaration["renders_call"] = render_call is not None or call_view is not None
                declaration["renders_result"] = render_result is not None or result_view is not None
                self._register_tool(declaration, self._tool_handlers[name], self._tool_prepare_handlers.get(name), render_call, render_result, call_view=call_view, result_view=result_view)
                break

    def _render_tool(self, ctx: "Context", name: str, args: dict[str, Any]) -> tuple[Any, bool]:
        """Run the tool's renderer for the request's phase: its lines, or its view when the renderer is a view form
        (which wins over the line form), with whether it is a view."""
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
                output_pad=int(wire.get("outputPad") or 0),
                duration_ms=None if wire.get("durationMs") is None else int(wire["durationMs"]),
                state=card[1],
                _invalidate=lambda: self._notify("tool_render_invalidate", {"card": card_id}),
            )
            width = int(args.get("width") or 0)
            resolved = self._resolved_tool_renderers.get(str(args.get("renderers") or "")) if args.get("renderers") else None
            result_phase = args.get("phase") == "result"
            if resolved is not None:
                view, lines = (resolved.result_view, resolved.render_result) if result_phase else (resolved.call_view, resolved.render_call)
            elif result_phase:
                view, lines = self._tool_result_view_renderers.get(name), self._tool_result_renderers.get(name)
            else:
                view, lines = self._tool_call_view_renderers.get(name), self._tool_call_renderers.get(name)
            handler = view if view is not None else lines
            if handler is None:
                raise RuntimeError(f"tool {name} has no {'result' if result_phase else 'call'} renderer")
            if result_phase:
                return handler(ctx, args.get("result") or {"content": []}, args.get("options") or {}, render, width), view is not None
            return handler(ctx, args.get("args") or {}, render, width), view is not None

    def tool_renderer(self, resolver: ToolRendererResolver) -> None:
        """Register a tool renderer resolver (Pi's pi.registerToolRenderer). Resolvers run in extension load order, and
        an extension's resolvers in registration order."""
        with self._tool_render_lock:
            self._tool_renderer_resolvers.append(resolver)
            count = len(self._tool_renderer_resolvers)
        if self._tools_live:
            self._notify("tool_renderers", {"count": count})

    def _resolve_tool_renderers(self, args: dict[str, Any]) -> dict[str, Any]:
        wire_next = args.get("next")
        marker = ToolRenderers(render_shell=str(wire_next.get("render_shell") or "default")) if isinstance(wire_next, dict) else None
        with self._tool_render_lock:
            resolvers = list(self._tool_renderer_resolvers)
        tool = str(args.get("tool") or "")

        def resolve(index: int) -> ToolRenderers | None:
            if index < len(resolvers):
                return resolvers[index](tool, lambda: resolve(index + 1))
            return marker

        got = resolve(0)
        if got is None:
            return {"use": "none"}
        # A marker given render functions is the resolver's own renderers.
        renders_call = got.render_call is not None or got.call_view is not None
        renders_result = got.render_result is not None or got.result_view is not None
        if got is marker and not renders_call and not renders_result:
            return {"use": "next"}
        with self._tool_render_lock:
            renderer_id = f"r{len(self._resolved_tool_renderers) + 1}"
            self._resolved_tool_renderers[renderer_id] = got
        out: dict[str, Any] = {"use": "own", "renderers": renderer_id}
        if got.render_shell == "self":
            out["render_shell"] = "self"
        if renders_call:
            out["renders_call"] = True
        if renders_result:
            out["renders_result"] = True
        return out

    def message_renderer(self, custom_type: str, handler: RendererHandler) -> None:
        declaration = {"custom_type": custom_type}
        with self._tool_registration_lock:
            self._renderers.append(declaration)
            self._message_view_renderers.pop(custom_type, None)
            self._renderer_handlers[custom_type] = handler
            self._publish_declaration({"message_renderers": [declaration]})

    def message_view_renderer(self, custom_type: str, handler: "Callable[[Context, dict[str, Any], dict[str, Any], int], kit.View]") -> None:
        """Register a custom message renderer that returns a :class:`kit.View` (D107), which the host renders at the
        transcript's width. It replaces a :meth:`message_renderer` for ``custom_type``."""
        declaration = {"custom_type": custom_type}
        with self._tool_registration_lock:
            self._renderers.append(declaration)
            self._renderer_handlers.pop(custom_type, None)
            self._message_view_renderers[custom_type] = handler
            self._publish_declaration({"message_renderers": [declaration]})

    def entry_renderer(self, custom_type: str, handler: RendererHandler) -> None:
        declaration = {"custom_type": custom_type}
        with self._tool_registration_lock:
            self._entry_renderers.append(declaration)
            self._entry_view_renderers.pop(custom_type, None)
            self._entry_renderer_handlers[custom_type] = handler
            self._publish_declaration({"entry_renderers": [declaration]})

    def entry_view_renderer(self, custom_type: str, handler: "Callable[[Context, dict[str, Any], dict[str, Any], int], kit.View]") -> None:
        """Register a custom session-entry renderer that returns a :class:`kit.View` (D107). It replaces an
        :meth:`entry_renderer` for ``custom_type``."""
        declaration = {"custom_type": custom_type}
        with self._tool_registration_lock:
            self._entry_renderers.append(declaration)
            self._entry_renderer_handlers.pop(custom_type, None)
            self._entry_view_renderers[custom_type] = handler
            self._publish_declaration({"entry_renderers": [declaration]})

    def markdown_transformer(self, transformer: Callable[[str, dict[str, Any]], Any]) -> None:
        """Register the extension's display-only Markdown transform (Pi's
        pi.registerMarkdownTransformer). The host applies it to user and
        assistant Markdown in the interactive transcript, after its own
        transformers; a later registration replaces an earlier one. It receives
        the Markdown and Pi's MarkdownTransformContext (messageType,
        isStreaming, availableWidth); anything but a str return, or a raise,
        keeps the input."""
        self._markdown_transformer = transformer

    def on_project_trust(self, handler: ProjectTrustHandler) -> Callable[[], None]:
        """Register an awaited pre-runtime project_trust handler and return its unsubscribe function."""
        return self.on_event("project_trust", handler)

    @property
    def events(self) -> EventBus:
        """Upstream's ``pi.events``, shared with every other realm of the session."""
        return EventBus(self)

    def on_event(self, event: str, handler: EventHandler) -> Callable[[], None]:
        """Register an event handler and return an idempotent unsubscribe function (Pi's ``pi.on`` returns ``() => void``).

        Before the extension connects the handler is a declaration of the register frame, and unsubscribing removes it. After
        it connects the host is told with ``event.subscribe`` / ``event.unsubscribe``, as the Go SDK does.
        """
        with self._event_lock:
            self._event_next_id += 1
            handler_id = self._event_next_id
            self._handlers.append({"event": event, "handler_id": handler_id})
            self._event_handlers[handler_id] = handler
            live = self._bus._live
        if live:
            # Upstream's pi.on throws when the runtime refuses it; the unsubscribe-only API reports the refusal instead.
            try:
                self._call("event.subscribe", {"event": event, "handlerId": handler_id})
            except Exception as exc:  # noqa: BLE001 - report where the host shows extension output
                print(f"extension: event.subscribe {event!r}: {exc}", file=sys.stderr)
        removed = threading.Lock()

        def unsubscribe() -> None:
            if not removed.acquire(blocking=False):
                return
            with self._event_lock:
                connected = self._bus._live
                if not connected:
                    self._handlers[:] = [d for d in self._handlers if d["handler_id"] != handler_id]
                    self._event_handlers.pop(handler_id, None)
            if connected:
                try:
                    self._call("event.unsubscribe", {"event": event, "handlerId": handler_id})
                except Exception as exc:  # noqa: BLE001 - upstream's unsubscribe cannot fail; report it
                    print(f"extension: event.unsubscribe {event!r}: {exc}", file=sys.stderr)

        return unsubscribe

    def run(self) -> None:
        sock = os.environ.get("PIG_EXT_SOCKET")
        if not sock:
            raise RuntimeError("PIG_EXT_SOCKET not set: extension must be launched by pig")
        self.run_with_socket(sock)

    def run_with_socket(self, sock_path: str) -> None:
        from . import host_helpers

        self._sock = host_helpers.take_helper_socket(sock_path) or _connect_unix(sock_path)
        self._served_socket = sock_path
        host_helpers._State.running.append(self)
        try:
            self._serve()
        finally:
            if self in host_helpers._State.running:
                host_helpers._State.running.remove(self)
            # The host's Retire waits for this close while the process lives on for later factory generations (Go's RunWithConn closes on return).
            try:
                self._sock.close()
            except OSError:
                pass

    def _serve(self) -> None:
        self._bus.register_pending()
        with self._tool_registration_lock:
            self._send({"type": "register", "register": {"name": self._name, "tools": self._tools, "commands": self._commands, "shortcuts": self._shortcuts, "handlers": self._handlers, "flags": self._flags, "providers": self._providers, "message_renderers": self._renderers, "entry_renderers": self._entry_renderers, **({"markdown_transformer": True} if self._markdown_transformer is not None else {}), **({"tool_renderers": len(self._tool_renderer_resolvers)} if self._tool_renderer_resolvers else {}), **({"mcp_servers": self._mcp_servers_pending} if self._mcp_servers_pending else {}), **({"virtual_models": self._virtual_models_pending} if self._virtual_models_pending else {}), **({"unregister_virtual_models": self._virtual_model_unregistrations} if self._virtual_model_unregistrations else {})}})
            ready = self._read_during_load()
            if ready.get("type") != "ready":
                raise RuntimeError(f"expected ready, got {ready.get('type')}")
            self._tools_live = True
        ready_data = ready.get("ready") or {}
        source_info = ready_data.get("sourceInfo")
        self._source_info = dict(source_info) if isinstance(source_info, dict) else None
        self._cwd = ready_data.get("cwd", "")
        self._mode = ready_data.get("mode", "")
        self._width = ready_data.get("width", 0)
        self._height = ready_data.get("height", 0)
        # Initialize session mirror from the ready payload state.
        initial_state = ready_data.get("state") or {}
        with self._state_lock:
            self._apply_ui_state(initial_state)
        initial_session = initial_state.get("session")
        if initial_session:
            self._session_file = str(initial_session.get("sessionFile") or "")
            self._session_mirror.apply_update(initial_session)
        self._run_loaded()
        while True:
            try:
                env = self._read()
            except EOFError:
                self._stop_runtime()
                return
            self._flush_finalizer_frames()
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
                self._apply_event_stream(env.get("request") or {})
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
            slot = self._pending_slots.pop(cid, None)
            if event:
                self._pending_results[cid] = result
            if cid in self._mcp_calls:
                self._mcp_calls.discard(cid)
                servers = (result.get("result") or {}).get("servers")
                if isinstance(servers, list):
                    self._mcp_servers = servers
        # Pi resumes the handlers whose dialogs were answered in the order the answers arrived. This reader reads them in that order, which the threads that wait do not keep.
        if slot is not None:
            slot.settle()
        if event:
            event.set()

    def _start_request(self, env: dict[str, Any]) -> None:
        ctx = self._arm_request(env)
        req = env.get("request") or {}
        batch = req.get("batch")
        # Only the calls of one parallel batch (the host's `batch` member, the same on each of them) take part in the dialog and continuation orders; any other tool_call is never gated.
        if req.get("method") == "tool_call" and isinstance(batch, str) and batch:
            ctx._tool_start = self._tool_batches.reserve(batch)
        if _starts_in_arrival_order(req):
            ctx._request_start = self._request_starts.reserve()
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
            if ctx._request_start is not None:
                ctx._request_start.release()
            if ctx._tool_start is not None:
                ctx._tool_start.release()
            self._respond(
                env.get("id", ""),
                None,
                {"message": f"start extension handler: {exc}"},
            )

    def _stop_runtime(self) -> None:
        self._shutdown.set()
        # The sessions end with the connection, as the host disposes them: no listener runs after it.
        self._children.close_all()
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

    def _apply_event_stream(self, req: dict[str, Any]) -> None:
        """Replaces the request's args with the full payload its stream member produces (EventStream in the host's protocol.go). The read loop calls it as the frame arrives, in order, so the replica follows the host's frames whether or not a handler runs for them."""
        stream = req.get("stream")
        if stream is None:
            return
        name = req.get("event") or ""
        # A payload belongs to the sequence the event and the key name: tool_execution_update has one for each tool call.
        ident = (name, str(stream.get("key") or ""))
        replicas = self._event_replicas
        try:
            if req.get("args") is not None:
                root = req["args"]
            else:
                replica = replicas.get(ident)
                base = int(stream.get("base") or 0)
                if replica is None or replica[0] != base:
                    raise RuntimeError(f"{name} delta applies to payload {base}, the replica holds a different one")
                root = _apply_stream_ops(replica[1], stream.get("ops"))
            replicas[ident] = (int(stream.get("seq") or 0), root)
            req["args"] = _clone_stream_value(root)
        except Exception as exc:  # noqa: BLE001 - The request that follows reports why its payload is missing.
            replicas.pop(ident, None)
            req["_stream_error"] = str(exc)
        for evicted in stream.get("evict") or ():
            replicas.pop((name, str(evicted)), None)

    def on_loaded(self, handler: "Callable[[Context], Any]") -> None:
        """Run ``handler(ctx)`` once the extension is registered with the host and has received its state, before it serves any request (session_start included).

        It is the place for work a factory cannot do: a :class:`Context` exists, so the handler can read session state, call the host and emit on ``ctx.events``. Handlers run in registration order, one at a time on a thread of their own, so a host call the host answers itself is answered. The host's requests wait behind them (a listener dispatch excepted), so a handler must not wait for something that needs this extension to serve a request, such as a call that runs one of its own tools or event handlers: that wait never ends. A handler that raises is reported on stderr and does not stop the next. Pi has no such hook: its factory runs with no context and the extension's first handler is ``session_start``. A handler registered after the extension started never runs.
        """
        if not callable(handler):
            raise TypeError("on_loaded requires a callable handler")
        self._loaded_handlers.append(handler)

    def _run_loaded(self) -> None:
        handlers, self._loaded_handlers = self._loaded_handlers, []
        if not handlers:
            return
        self._loaded.clear()

        def run() -> None:
            try:
                for handler in handlers:
                    try:
                        handler(Context(self))
                    except Exception:  # noqa: BLE001 - one handler's failure must not stop the next
                        print(f"pig: on_loaded handler failed:\n{traceback.format_exc()}", file=sys.stderr, end="")
            finally:
                self._loaded.set()
                with self._request_threads_lock:
                    self._request_threads.discard(threading.current_thread())

        worker = threading.Thread(target=run, name="pig-on-loaded", daemon=True)
        with self._request_threads_lock:
            self._request_threads.add(worker)
        worker.start()

    def _handle_request_thread(self, env: dict[str, Any], ctx: Context) -> None:
        try:
            if (env.get("request") or {}).get("method") != "events.dispatch":
                self._loaded.wait()
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
        if method == "childSession.event":
            self._children.deliver(args)
            return
        if method == "bash_operations_release":
            self._bash_operations.release(str(args.get("handle") or ""))
            return
        if method == "provider_superseded":
            # Another extension registered this provider after this one. Pi keeps one effective registration per provider and merges a later registration's defined values over it (model-runtime.ts:921-940), and this SDK's registration is a snapshot no other process merges, so the host's merged registration keeps calling this extension for every stream_simple, image, classifier and OAuth callback the later registration did not redefine. The extension therefore keeps them; unregister_provider and teardown release them.
            return
        if method == "provider_release":
            with self._provider_lock:
                self._native_providers.pop(args.get("key"), None)
            return
        if method == "events.release":
            self._bus.release(str(args.get("handlerId") or ""))
            return
        if method == "ui.view.event":
            # Events of a closed surface are dropped, as the host drops them.
            decoded = kit.Event._from_wire(args)
            if decoded is None:
                return
            key, event = decoded
            with self._overlay_lock:
                overlay = self._overlays.get(key)
            if overlay is not None and not overlay.enqueue_view_event(event):
                self._notify("ui.custom.close", {"key": key, "error": "focused input queue is full"})
            return
        if method == "ui.view.evicted":
            refs = {str(ref) for ref in args.get("refs") or []}
            if refs:
                self._resend_evicted_views(refs)
            return
        if method == "run_signal":
            self._apply_run_signal(args)
            return
        if method == "invalidate":
            message = args.get("message")
            with self._state_lock:
                if self._stale_message is None and isinstance(message, str) and message:
                    self._stale_message = message
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
        if _editor_component.editor_notify(self, method, args):
            return
        if method == "ui.custom.mouse":
            key = str(args.get("key") or "")
            mouse = MouseEvent._from_wire(args.get("event"))
            with self._overlay_lock:
                overlay = self._overlays.get(key)
            if overlay is None or mouse is None:
                return
            if not overlay.enqueue_mouse(mouse):
                self._notify("ui.custom.close", {"key": key, "error": "focused input queue is full"})
            return
        if method == "ui.custom.opened":
            key = str(args.get("key") or "")
            with self._overlay_lock:
                overlay = self._overlays.get(key)
            if overlay is not None and not overlay.enqueue_opened(args):
                self._notify("ui.custom.close", {"key": key, "error": "focused input queue is full"})
            return
        if method == "ui.custom.focus":
            key = str(args.get("key") or "")
            with self._overlay_lock:
                overlay = self._overlays.get(key)
            if overlay is not None and not overlay.enqueue_focus(bool(args.get("focused"))):
                self._notify("ui.custom.close", {"key": key, "error": "focused input queue is full"})
            return
        if method == "ui.custom.input":
            key = str(args.get("key") or "")
            with self._overlay_lock:
                overlay = self._overlays.get(key)
            if overlay is None:
                return
            if isinstance(args.get("state"), dict):
                overlay.apply_handle_state(args["state"])
            if not overlay.enqueue_input(str(args.get("data") or "")):
                self._notify(
                    "ui.custom.close",
                    {"key": key, "error": "focused input queue is full"},
                )
            return
        width_changed = False
        footer_changed = branch_changed = False
        with self._state_lock:
            if method == "state_update":
                state = args.get("state") or {}
                footer_changed, branch_changed = self._apply_footer_data(state.get("footerData"))
                # Session replication: apply incremental entries.
                session = state.get("session")
                if session:
                    if session.get("sessionFile"):
                        self._session_file = str(session["sessionFile"])
                    self._session_mirror.apply_update(session)
                self._apply_ui_state(state)
            elif method == "theme_change":
                palette = notify.get("args")
                if isinstance(palette, str):
                    try:
                        palette = json.loads(palette)
                    except ValueError:
                        palette = {}
                self._set_theme_palette(palette if isinstance(palette, dict) and palette else {})
                _tui_state.set_host_theme(palette)
            elif method == "width_change":
                w = args.get("width", 0)
                if w > 0:
                    self._width = w
                    width_changed = True
            elif method == "height_change":
                h = args.get("height", 0)
                if h > 0:
                    self._height = h
        if footer_changed:
            self._notify_footer_data(branch_changed)
        if width_changed:
            # After the store, so a handler that reads width() sees the new value.
            with self._state_lock:
                new_width = self._width
            self._dispatch_width_change(new_width)
            self._refresh_surfaces()
            with self._editor_lock:
                editor = self._editor
            if editor is not None:
                editor.post(("render", None))
            with self._overlay_lock:
                overlays = list(self._overlays.values())
            for overlay in overlays:
                overlay.request_render()

    def _apply_footer_data(self, data: Any) -> tuple[bool, bool]:
        """Install the footer data of a state update; return whether it changed and whether the branch did. A state without footer data keeps the last one. The caller holds the state lock."""
        if not isinstance(data, dict):
            return False, False
        statuses = data.get("extensionStatuses")
        new = {"branch": data.get("gitBranch") or None, "statuses": dict(statuses) if isinstance(statuses, dict) else {}, "count": int(data.get("availableProviderCount") or 0)}
        previous = self._footer_data
        self._footer_data = new
        return new != previous, new["branch"] != previous["branch"]

    def _notify_footer_data(self, branch_changed: bool) -> None:
        """Render the footer again and tell the branch subscribers; the host's footer data changed."""
        with self._surface_lock:
            footer = self._surfaces.get("ui.setFooter")
        if footer is not None:
            footer.refresh(Context(self))
        if not branch_changed:
            return
        with self._state_lock:
            callbacks = [callback for _, callback in sorted(self._branch_subscribers.items())]
        for callback in callbacks:
            try:
                callback()
            except Exception as error:  # noqa: BLE001 - a failing subscriber is reported, as the Node runtime does
                self._notify("ui.notify", {"message": f"footer branch subscriber failed: {error}", "level": "error"})

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

    def _set_theme_palette(self, palette: dict[str, Any]) -> None:
        """Install the host's palette and deliver it to the on_theme_change handlers when it differs from the one before. The caller holds the state lock."""
        changed = self._theme_known and self._theme_palette != palette
        self._theme._set_palette(palette)
        self._theme_palette = palette
        self._theme_known = True
        if changed:
            self._theme_feed.publish(self._theme)

    def _apply_ui_state(self, state: dict[str, Any]) -> None:
        """Record the snapshot's replicated values: hasUI, theme, settings, MCP servers, the model and the session name. The ready state and every state_update pass through here. Caller holds _state_lock."""
        self._views.apply_state(state)
        if "model" in state:
            # An absent key keeps the replica; an explicit null clears it (the Session holds no model).
            self._model, self._model_provider = _model_identity(state["model"])
        session = state.get("session")
        if isinstance(session, dict):
            # Every session snapshot carries the name; "" is the host's carrier for a session with no name.
            self._session_name = str(session.get("sessionName") or "")
        has_ui = state.get("hasUI")
        if isinstance(has_ui, bool):
            self._has_ui = has_ui
        theme = state.get("theme")
        if isinstance(theme, dict):
            self._set_theme_palette(theme)
        _tui_state.apply_host_state(state)
        tools_expanded = state.get("toolsExpanded")
        if isinstance(tools_expanded, bool):
            changed = self._tools_expanded_known and self._tools_expanded != tools_expanded
            self._tools_expanded = tools_expanded
            self._tools_expanded_known = True
            if changed:
                self._tools_expanded_feed.publish(tools_expanded)
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
        ctx = Context(self, req.get("tool_call_id"), req_id, cancel, _parent=parent, _tool_context=req.get("method") == "tool_call", _tool_index=req.get("tool_call_index"), _tool_args=req.get("args") if req.get("method") == "tool_call" else None)
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
            elif method == "user_bash_exec":
                # The host waits for the output chunks, in order, before this answer; the request's cancellation is the exec's signal.
                result = _run_user_bash_exec(self._bash_operations, req, cancel, lambda data: self._notify("tool_update", {"request_id": req_id, "result": _data_update(data)}))
                self._respond(req_id, result, None)
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
                handler = self._tool_handler(req, name)
                # agent-loop.ts:619-647: the handlers of a batch start in source order.
                if ctx._request_start is not None:
                    ctx._request_start.begin()
                if ctx._tool_start is not None:
                    ctx._tool_start.begin()
                result = handler(ctx, params)
                payload = result if isinstance(result, dict) else {"content": str(result)}
                # A handler whose dialog was answered before another's responds after it, as Pi's continuations run in the order of the answers.
                if ctx._tool_start is not None:
                    ctx._tool_start.respond(lambda: self._respond(req_id, payload, None))
                else:
                    self._respond(req_id, payload, None)
            elif method == "tool_prepare_arguments":
                name = req.get("tool", "")
                prepare = self._tool_prepare_handler(req, name)
                if prepare is None:
                    raise RuntimeError(f"tool {name!r} has no prepare_arguments")
                prepared = prepare(req.get("args") or {})
                if not isinstance(prepared, dict):
                    raise TypeError("prepare_arguments must return a mapping")
                self._respond(req_id, prepared, None)
            elif method == "tool_reaches_tools":
                name = req.get("tool", "")
                with self._tool_registration_lock:
                    hook = self._tool_reach_handlers.get(name)
                if hook is None:
                    raise RuntimeError(f"tool {name!r} has no reaches_tools")
                reach = hook(req.get("args") or {})
                if reach != "all":
                    if isinstance(reach, str) or not isinstance(reach, Sequence) or not all(isinstance(word, str) for word in reach):
                        raise TypeError("reaches_tools must return 'all' or a sequence of strings")
                    reach = list(reach)
                self._respond(req_id, reach, None)
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
                    prompt_guidelines={tool: list(items) for tool, items in (payload.get("promptGuidelines") or {}).items()},
                ))
                if changes is not None and not isinstance(changes, dict):
                    raise TypeError("prepare_loadout must return a mapping or None")
                self._respond(req_id, changes, None)
            elif method == "builtin_tool_callback":
                self._respond(req_id, _dispatch_builtin_callback(req.get("args") or {}), None)
            elif method == "builtin_tool_exec_data":
                try:
                    _dispatch_builtin_exec_data(req.get("args") or {})
                except Exception as failure:  # noqa: BLE001 - the callback's throw is the answer to its request
                    self._respond(req_id, None, {"message": str(failure)})
                else:
                    self._respond(req_id, None, None)
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
            elif method == "model_stream_callback":
                args = req.get("args") or {}
                with self._model_stream_lock:
                    owned = self._model_stream_callbacks.get(str(args.get("streamId") or ""))
                name = str(args.get("callback") or "")
                transport = owned[1].get("fetch") if owned is not None else None
                if name in ("fetch", "fetchRead", "fetchClose") and isinstance(transport, _ModelFetchTransport):
                    self._respond(req_id, transport.serve(name, args.get("value"), cancel), None)
                    return
                if name == "fetchClose" and owned is None:
                    self._respond(req_id, None, None)  # a deferred Body.Close may follow the terminal stream event
                    return
                callback = owned[1].get(name) if owned else None
                if callback is None or name == "fetch":
                    raise RuntimeError(f"unknown model stream callback {args.get('streamId')}/{name}")
                result = callback(args.get("value"), owned[0])
                self._respond(req_id, {"defined": result is not None, "value": result} if name == "onPayload" else result, None)
            elif method == "with_session":
                self._with_sessions.dispatch(ctx, req.get("args") or {})
                self._respond(req_id, None, None)
            elif method == "setup":
                self._setups.dispatch_setup(ctx, req.get("args") or {})
                self._respond(req_id, None, None)
            elif method == "sessionGraph.callback":
                request = req.get("args") or {}
                callback = getattr(self, "_graph_callbacks", {}).get(request.get("id"))
                if callback is None:
                    raise RuntimeError(f"unknown session graph callback {request.get('id')}")
                self._respond(req_id, callback(*(request.get("args") or [])), None)
            elif method == "command":
                name = req.get("tool", "")
                args = req.get("args") or ""
                # runner.ts:1096-1125: the handlers of concurrent requests start in arrival order.
                if ctx._request_start is not None:
                    ctx._request_start.begin()
                self._command_handlers[name](ctx, args if isinstance(args, str) else json.dumps(args))
                self._respond(req_id, None, None)
            elif method == "command_argument_completions":
                name = req.get("tool", "")
                prefix = req.get("args") or ""
                items = self._command_completions[name](prefix if isinstance(prefix, str) else "")
                self._respond(req_id, list(items) if items else None, None)
            elif method == "terminal_input":
                # The host is blocked on this reply and upstream's handler is
                # synchronous, so handlers run inline. A raising handler is
                # reported on stderr, which the host captures in the extension
                # log, and degrades to not-consumed rather than capturing the
                # keystroke.
                original = (req.get("args") or {}).get("data") or ""
                current = original
                consume = False
                for h in self._terminal_input_handlers():
                    try:
                        consumes, data = _terminal_input_verdict(h(current))
                    except Exception:  # a failing handler does not stop the others
                        print(f"extension: recovered from exception in a terminal input handler:\n{traceback.format_exc()}", file=sys.stderr, end="")
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
                if req.get("_stream_error"):
                    raise RuntimeError(req["_stream_error"])
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
                        if ctx._request_start is not None:
                            ctx._request_start.begin()
                        result = handler(ctx, data)
                    except Exception as exc:  # noqa: BLE001 - Preserve mutations and report the handler error.
                        error = {"message": str(exc)}
                    self._respond(req_id, {"_pigBoundaryEntries": entries, "_pigBoundaryResult": result}, error)
                    return
                # Pi's handler edits event.input in place and the runner reads it back (runner.ts emitToolCall). The host cannot share the dict, so the reply carries the input the handler left when it differs from the one it received, member order included, even if the handler failed. The tool runs with the object the event carried (agent-loop.ts prepareToolCall), so assigning a new dict to data["input"] changes nothing the tool receives.
                if req.get("event") == "tool_call":
                    tool_input = data.get("input")
                    before = json.dumps(tool_input)
                    result = None
                    error = None
                    try:
                        if ctx._request_start is not None:
                            ctx._request_start.begin()
                        result = handler(ctx, data)
                    except Exception as exc:  # noqa: BLE001 - Preserve input edits before reporting the handler error.
                        error = {"message": str(exc)}
                    reply: dict[str, Any] = {"_pigToolCallResult": result}
                    if json.dumps(tool_input) != before:
                        reply["_pigToolCallInput"] = tool_input
                    self._respond(req_id, reply, error)
                    return
                if req.get("event") == "before_agent_start":
                    options = data["systemPromptOptions"]
                    # Pi's normalized options always carry every collection; the host omits an empty one.
                    options.setdefault("selectedTools", [])
                    options.setdefault("hiddenTools", [])
                    options.setdefault("toolSnippets", {})
                    options.setdefault("toolGuidelines", {})
                    options.setdefault("promptGuidelines", [])
                    options.setdefault("contextFiles", [])
                    options.setdefault("skills", [])
                    ctx._system_prompt = _SystemPrompt(ctx, options, data.get("systemPrompt"))
                    data = _BeforeAgentStartEvent(data, ctx._system_prompt)
                    result = None
                    error = None
                    try:
                        if ctx._request_start is not None:
                            ctx._request_start.begin()
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
                # A reply for a list the handler left as it was, with no edit to any message, omits the list: the host already holds it.
                encoded_before = _context_messages_encoding(messages) if snapshot is not None and req.get("event") in {"context", "context_with_system"} else None
                if ctx._request_start is not None:
                    ctx._request_start.begin()
                result = handler(ctx, data)
                if req.get("event") == "user_bash":
                    result = _user_bash_operations_reply(self._bash_operations, result) or _user_bash_event_result(result)
                if req.get("event") in {"context", "context_with_system"} and snapshot is not None:
                    returned = result.get("messages") if isinstance(result, dict) else None
                    if returned is None:
                        returned = messages
                    unchanged = len(returned) == len(snapshot) and all(a is b for a, b in zip(returned, snapshot))
                    if unchanged and encoded_before is not None and _context_messages_encoding(returned) == encoded_before:
                        result = {"_pigContextUnchanged": True}
                    else:
                        result = {"messages": returned, "_pigContextUnchanged": unchanged}
                self._respond(req_id, result, None)
            elif method == "shortcut":
                key = req.get("tool", "")
                self._shortcut_handlers[key](ctx)
                self._respond(req_id, None, None)
            elif method == "render_message":
                custom_type = req.get("tool", "")
                args = req.get("args") or {}
                view_handler = self._message_view_renderers.get(custom_type)
                handler = view_handler or self._renderer_handlers[custom_type]
                value = handler(ctx, args.get("message") or {}, args.get("options") or {}, int(args.get("width") or 0))
                self._respond(req_id, self._views.result(value, view_handler is not None), None)
            elif method == "render_tool":
                value, is_view = self._render_tool(ctx, req.get("tool", ""), req.get("args") or {})
                self._respond(req_id, self._views.result(value, is_view), None)
            elif method == "resolve_tool_renderers":
                self._respond(req_id, self._resolve_tool_renderers(req.get("args") or {}), None)
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
                view_handler = self._entry_view_renderers.get(custom_type)
                handler = view_handler or self._entry_renderer_handlers[custom_type]
                value = handler(ctx, args.get("entry") or {}, args.get("options") or {}, int(args.get("width") or 0))
                self._respond(req_id, self._views.result(value, view_handler is not None), None)
            elif method in _OAUTH_METHODS:
                self._dispatch_oauth(req_id, req)
            else:
                self._respond(req_id, None, {"message": f"unknown method: {method}"})
        except Exception as exc:  # noqa: BLE001 - SDK boundary reports handler errors.
            # A handler whose dialog was answered before another's responds after it, error or not (_ToolStartOrder).
            error = {"message": str(exc)}
            if ctx._tool_start is not None:
                ctx._tool_start.respond(lambda: self._respond(req_id, None, error))
            else:
                self._respond(req_id, None, error)
        finally:
            if ctx._request_start is not None:
                ctx._request_start.end()
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

    def _call(self, method: str, args: Any = None, parent_request_id: str = "", parent: _RequestParent | None = None, start: _ToolStart | None = None) -> dict[str, Any]:
        call_id, event = self._begin_call(method, args, parent_request_id, parent, start=start)
        return self._wait_call(call_id, event, method)

    def _begin_call(self, method: str, args: Any = None, parent_request_id: str = "", parent: _RequestParent | None = None, event: threading.Event | None = None, start: _ToolStart | None = None) -> tuple[str, threading.Event]:
        # A tool handler's host call, of any kind, is an await of Pi's handler: it orders the handler among the calls of its batch (_ToolStartOrder).
        if start is not None:
            start.before_call()
        try:
            # The reader settles the handler's slot when it reads a dialog's answer, so only a dialog call carries the slot.
            return self._write_call(method, args, parent_request_id, parent, event, start.response_slot() if start is not None and method in _DIALOG_METHODS else None)
        finally:
            if start is not None:
                start.after_call()

    def _write_call(self, method: str, args: Any, parent_request_id: str, parent: _RequestParent | None, event: threading.Event | None, slot: _SettleSlot | None) -> tuple[str, threading.Event]:
        event = event if event is not None else threading.Event()
        timer = False
        with self._write_lock:
            with self._state_lock:
                if self._shutdown.is_set() or (parent is not None and parent.socket is not self._sock):
                    raise RuntimeError("extension connection is closed or replaced")
                if parent is not None:
                    if parent.state == "cancelled":
                        raise RuntimeError("host call cancelled with its parent request")
                    parent_request_id = "" if parent.state == "completed" else parent.request_id
                    # pig additive (D189): a call made through a request's context after its handler returned is deferred work, which Pi runs once its loop turns; the host holds it for that turn.
                    timer = parent.state == "completed"
                self._call_id += 1
                call_id = f"c{self._call_id}"
                self._pending[call_id] = event
                if slot is not None:
                    self._pending_slots[call_id] = slot
                if method in _MCP_REGISTRY_CALLS:
                    self._mcp_calls.add(call_id)
                if parent_request_id:
                    self._pending_parents[call_id] = parent_request_id
            call = {"method": method, "args": args}
            if parent_request_id:
                call["parent_request_id"] = parent_request_id
            if timer:
                call["timer"] = True
            try:
                self._send_locked({"type": "call", "id": call_id, "call": call})
            except Exception:
                with self._state_lock:
                    self._pending.pop(call_id, None)
                    self._pending_parents.pop(call_id, None)
                    self._pending_slots.pop(call_id, None)
                    self._mcp_calls.discard(call_id)
                raise
        return call_id, event

    def _wait_call(self, call_id: str, event: threading.Event, method: str) -> dict[str, Any]:
        event.wait()
        with self._state_lock:
            result = self._pending_results.pop(call_id, {})
            self._pending.pop(call_id, None)
            self._pending_parents.pop(call_id, None)
            self._pending_slots.pop(call_id, None)
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
        if overlay.is_view:
            self._render_remote_view(key, overlay, width)
            return
        render_width = overlay.render_width(width) if overlay.render_width is not None else width
        lines = [str(line) for line in overlay.component.render(render_width)]
        overlay.last_render = time.monotonic()
        if lines == overlay.last_lines:
            return
        overlay.last_lines = list(lines)
        overlay.seq += 1
        self._notify("ui.custom.render", self._frame_args(overlay, {"key": key, "lines": lines, "width": width, "seq": overlay.seq}))

    def _render_remote_view(self, key: str, overlay: _RemoteOverlayState, width: int) -> None:
        """Send a view frame (D107): the view with no lines key. A frame equal to the last one at the same width is not
        sent, unless it names images the connection has not sent or the host evicted one of its images."""
        view = overlay.component.view(width)  # type: ignore[union-attr]
        overlay.last_render = time.monotonic()
        encoded = self._views.encode(view)
        with overlay.event_lock:
            force = overlay.force_view
            overlay.force_view = False
        if not force and overlay.last_view == (encoded.body, width) and not self._views.has_unsent(encoded):
            return
        overlay.last_view = (encoded.body, width)
        with overlay.event_lock:
            overlay.view_refs = encoded.refs()
        overlay.seq += 1
        self._notify("ui.custom.render", self._frame_args(overlay, {"key": key, "view": self._views.wire(encoded), "width": width, "seq": overlay.seq}))

    @staticmethod
    def _frame_args(overlay: _RemoteOverlayState, args: dict[str, Any]) -> dict[str, Any]:
        """Mark a frame of a component that takes the mouse; the host then hands it mouse events."""
        if overlay.takes_mouse:
            args["mouse"] = True
        return args

    def _remote_component_worker(self, key: str, overlay: _RemoteOverlayState) -> None:
        while overlay.active.is_set():
            kind, data = overlay.next_event()
            if kind == "stop" or not overlay.active.is_set():
                return
            try:
                if kind == "opened":
                    if overlay.on_handle is None:
                        continue
                    overlay.apply_handle_state(data)
                    handle = OverlayHandle(self, key, overlay)
                    overlay.on_handle(handle)
                    continue
                if kind == "focus":
                    overlay.apply_focus(bool(data))
                elif kind in ("input", "view_event", "mouse"):
                    if kind == "input":
                        result = overlay.component.handle_input(data or "")
                    elif kind == "mouse":
                        result = overlay.component.handle_mouse(data)  # type: ignore[union-attr]
                    else:
                        # A component without handle_view_event, and a line component, ignore events.
                        handler = getattr(overlay.component, "handle_view_event", None) if overlay.is_view else None
                        if not callable(handler):
                            continue
                        result = handler(data)
                    if result.done:
                        overlay.active.clear()
                        self._notify("ui.custom.close", {"key": key, "result": result.value})
                        return
                    if kind == "mouse" and data.type in _MOUSE_NO_RENDER:
                        # Pi's renderer draws again after press, click, drag and wheel only.
                        continue
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

    def _run_remote_component(self, component: RemoteComponent, options: dict[str, Any], parent_request_id: str = "", parent: _RequestParent | None = None, start: _ToolStart | None = None) -> Any:
        args = dict(options)
        on_handle = args.pop("onHandle", None)
        with self._overlay_lock:
            self._overlay_seq += 1
            key = f"custom-{self._overlay_seq}"
            overlay = _RemoteOverlayState(component)
            overlay.render_width = _overlay_render_width(args)
            if callable(on_handle) and args.get("overlay"):
                overlay.on_handle = on_handle
                args["hasHandle"] = True
            self._overlays[key] = overlay
        args["key"] = key
        try:
            call_id, event = self._begin_call("ui.custom", args, parent_request_id, parent, start=start)
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

    def _set_widget_view(self, ctx: Context, key: str, view: kit.View, options: dict[str, Any] | None) -> None:
        views = self._views
        encoded = views.encode(view)
        self._replace_surface_id(f"ui.setWidget:{key}", None)
        # Held across the send, so an eviction's resend and this frame keep their order.
        with views.widgets_lock:
            views.widgets[key] = (encoded, options)
            self._send_widget_view(ctx, key, views.wire(encoded), options)

    def _send_widget_view(self, ctx: Context, key: str, view: dict[str, Any], options: dict[str, Any] | None) -> None:
        # lines absent: the view is authoritative (D107).
        if options is None:
            self._send({"type": "widget_push", "widget_push": {"key": key, "view": view}})
            return
        ctx._call("ui.setWidget", {"key": key, "view": view, "options": options})

    def _resend_evicted_views(self, refs: set[str]) -> None:
        """Handle ``ui.view.evicted``: forget the refs, and send again, with the
        image data, the latest frame of every live surface that names one.

        Runs on the thread that reads host frames, so the widget, header and
        footer sends, which may wait for the host, run on a thread of their
        own, each holding its registry so a newer frame the author sends goes
        out after it.
        """
        views = self._views
        views.forget(refs)
        with self._overlay_lock:
            overlays = list(self._overlays.values())
        for overlay in overlays:
            overlay.resend_if_references(refs)

        def resend() -> None:
            ctx = Context(self)
            try:
                with views.widgets_lock:
                    for key, (encoded, options) in list(views.widgets.items()):
                        if encoded.references(refs):
                            self._send_widget_view(ctx, key, views.wire(encoded), options)
                with views.surfaces_lock:
                    for method, encoded in list(views.surfaces.items()):
                        if encoded.references(refs):
                            ctx._call(method, {"view": views.wire(encoded)})
            except Exception as exc:  # noqa: BLE001 - a failed resend leaves the host's previous frame, as a failed send would
                if not self._shutdown.is_set():
                    print(f"pig: resend evicted view images: {exc}", file=sys.stderr)

        threading.Thread(target=resend, name="pig-view-evicted", daemon=True).start()

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
        """Retire the renderer or view installed for method and install surface (None for none)."""
        with self._views.surfaces_lock:
            self._views.surfaces.pop(method, None)
        with self._surface_lock:
            previous = self._surfaces.pop(method, None)
            if surface is not None:
                self._surfaces[method] = surface
        if previous is not None:
            previous.stop()

    def _replace_surface_id(self, surface_id: str, surface: "_SurfaceRenderer | None") -> None:
        """Retire the renderer installed under surface_id and install surface (None for none)."""
        with self._surface_lock:
            previous = self._surfaces.pop(surface_id, None)
            if surface is not None:
                self._surfaces[surface_id] = surface
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
            self._pending_slots.pop(call_id, None)
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
        self._flush_finalizer_frames()

    def _send_from_finalizer(self, env: dict[str, Any]) -> None:
        """Queue a frame from a weakref finalizer; the next send or read writes it."""
        self._finalizer_frames.put(env)

    def _flush_finalizer_frames(self) -> None:
        while True:
            try:
                env = self._finalizer_frames.get_nowait()
            except queue.Empty:
                return
            try:
                with self._write_lock:
                    self._send_locked(env)
            except (OSError, RuntimeError):
                pass  # Connection close releases everything the finalizers would release.

    def _send_locked(self, env: dict[str, Any]) -> None:
        if self._sock is None:
            raise RuntimeError("extension socket is not connected")
        try:
            data = json.dumps(env, separators=(",", ":"), allow_nan=False).encode()
        except ValueError:
            # Python writes NaN and Infinity as bare words, which is not JSON: the host refused the frame and the extension's connection ended.
            # JSON.stringify writes them as null, so each SDK sends null.
            data = json.dumps(_non_finite_to_null(env), separators=(",", ":"), allow_nan=False).encode()
        if len(data) > MAX_FRAME_SIZE:
            raise RuntimeError(f"frame too large: {len(data)} bytes exceeds {MAX_FRAME_SIZE}")
        # One write: a header and a body sent apart cost the host two reads and, on a stream socket, two segments.
        self._sock.sendall(struct.pack(">I", len(data)) + data)

    def _read(self) -> dict[str, Any]:
        if self._sock is None:
            raise RuntimeError("extension socket is not connected")
        hdr = self._read_exact(4)
        if not hdr:
            raise EOFError
        size = struct.unpack(">I", hdr)[0]
        if size > MAX_FRAME_SIZE:
            raise RuntimeError(f"frame too large: {size}")
        return json.loads(self._read_exact(size))

    def _read_exact(self, n: int) -> bytes | bytearray:
        """Read exactly ``n`` bytes straight into one buffer: a large frame is not grown chunk by chunk and copied again. ``b""`` means the peer closed before the first byte."""
        if self._sock is None:
            raise RuntimeError("not connected")
        buf = bytearray(n)
        view = memoryview(buf)
        got = 0
        while got < n:
            count = self._sock.recv_into(view[got:], n - got)
            if not count:
                if not got:
                    return b""
                raise EOFError
            got += count
        return buf
