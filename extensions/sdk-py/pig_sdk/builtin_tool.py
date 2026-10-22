"""Pi's create<Tool>ToolDefinition, create<Tool>Tool, createCodingTools, createReadOnlyTools and createLocalBashOperations (core/tools/*.ts, exported from the
package index), for a Python extension.

Pi builds the built-in tool in the extension's own process. Here the host owns the implementation, so the factories reach it through host calls: the
definition's metadata (``builtinTool.definition``), its ``execute`` (``builtinTool.execute``) and the local shell operations (``builtinTool.localExec``). An
override that wraps a built-in is written as in Pi::

    definition = pig_sdk.create_read_tool_definition(cwd)
    run = definition.execute

    def execute(ctx, params):
        result = run(ctx, params)
        ...post-process...
        return result

    ext.register_tool(dataclasses.replace(definition, execute=execute))

Results and partial results are mappings in the shape a tool returns: ``content`` as the list of blocks, ``details``, ``structured_content``, ``usage``,
``is_error`` and ``terminate`` (the names of the dict a tool handler returns), so a result can be returned from a handler or passed to ``ctx.on_update`` as it is.

Async contract (Pi's ``execute`` returns a Promise): the call is awaited, so it returns when the tool has finished, with the tool's result, or raises the error
the tool threw. ``execute`` forwards the calling request's cancellation, as Pi's ``execute(...args)`` forwards its own signal; ``execute_with`` takes the signal
explicitly, and ``None`` is Pi's undefined (the request does not cancel the call). ``on_update`` runs on the calling thread, in order and before the call
returns. Pi's ``operations`` and ``spawn_hook`` functions stay in this process and run, on other threads, while the call does.
"""
from __future__ import annotations

import base64
import threading
from dataclasses import dataclass, field
from typing import Any, Callable

from . import _NestedCall, _NestedCallEvent, _ensure_jsonable, host_helpers, ToolDefinition
from .provider import ProviderSignal

_CALL_DEFINITION = "builtinTool.definition"
_CALL_EXECUTE = "builtinTool.execute"
_CALL_LOCAL_EXEC = "builtinTool.localExec"
_CALL_EXECUTE_CANCEL = "executeTool.cancel"


def _member(source: Any, name: str) -> Any:
    """A member of an options or operations object: an attribute, or a key of a mapping."""
    if source is None:
        return None
    if isinstance(source, dict):
        return source.get(name)
    return getattr(source, name, None)


@dataclass
class ModelImageResizeOptions:
    """Pi's ModelImageResizeOptions: the resize profile of an image sent to a model. None members are Pi's undefined."""

    max_width: int | None = None
    max_height: int | None = None
    max_bytes: int | None = None
    jpeg_quality: int | None = None

    def _to_wire(self) -> dict[str, int]:
        wire = {"maxWidth": self.max_width, "maxHeight": self.max_height, "maxBytes": self.max_bytes, "jpegQuality": self.jpeg_quality}
        return {key: value for key, value in wire.items() if value is not None}


@dataclass
class ReadOperations:
    """Pi's ReadOperations: pluggable file reading. ``read_file(path) -> bytes`` and ``access(path)`` are required; ``detect_image_mime_type(path)`` returns a
    MIME type or None and is optional (without it every file is read as text)."""

    read_file: Callable[[str], bytes]
    access: Callable[[str], None]
    detect_image_mime_type: Callable[[str], str | None] | None = None


@dataclass
class ReadToolOptions:
    """Pi's ReadToolOptions. ``auto_resize_images`` None is Pi's default, True."""

    auto_resize_images: bool | None = None
    resize_options: ModelImageResizeOptions | dict[str, int] | None = None
    operations: ReadOperations | None = None


@dataclass
class WriteOperations:
    """Pi's WriteOperations: ``write_file(path, content)`` and ``mkdir(dir)``, both required."""

    write_file: Callable[[str, str], None]
    mkdir: Callable[[str], None]


@dataclass
class WriteToolOptions:
    operations: WriteOperations | None = None


@dataclass
class EditOperations:
    """Pi's EditOperations: ``read_file(path) -> bytes``, ``write_file(path, content)`` and ``access(path)``, all required."""

    read_file: Callable[[str], bytes]
    write_file: Callable[[str, str], None]
    access: Callable[[str], None]


@dataclass
class EditToolOptions:
    operations: EditOperations | None = None


@dataclass
class GrepOperations:
    """Pi's GrepOperations: ``is_directory(path) -> bool`` (required; raises when the path does not exist) and ``read_file(path) -> str`` (optional; without it
    matched files are streamed from the local file system)."""

    is_directory: Callable[[str], bool]
    read_file: Callable[[str], str] | None = None


@dataclass
class GrepToolOptions:
    operations: GrepOperations | None = None


@dataclass
class FindOperations:
    """Pi's FindOperations: ``exists(path) -> bool`` and ``glob(pattern, cwd, options) -> list[str]``, both required. ``options`` is ``{"ignore": [...],
    "limit": n}``."""

    exists: Callable[[str], bool]
    glob: Callable[[str, str, dict[str, Any]], list[str]]


@dataclass
class FindToolOptions:
    operations: FindOperations | None = None


@dataclass
class LsStat:
    """The stat Pi's LsOperations.stat returns: only ``isDirectory()`` is read."""

    is_directory: bool


@dataclass
class LsOperations:
    """Pi's LsOperations: ``exists(path) -> bool``, ``stat(path) -> LsStat`` (raises when the path is not found) and ``readdir(path) -> list[str]``, all
    required."""

    exists: Callable[[str], bool]
    stat: Callable[[str], LsStat]
    readdir: Callable[[str], list[str]]


@dataclass
class LsToolOptions:
    operations: LsOperations | None = None


# Pi's BashSpawnHook: (context) -> context, where a context is {"command", "cwd", "env"}. A raised error is the hook's throw: the tool call fails with it.
BashSpawnHook = Callable[[dict[str, Any]], dict[str, Any]]


@dataclass
class BashToolOptions:
    """Pi's BashToolOptions. ``operations`` is an object with ``exec(command, cwd, options)`` (see ``BashOperations``); None is the local shell."""

    operations: Any = None
    command_prefix: str | None = None
    shell_path: str | None = None
    expose_session_environment: bool | None = None
    spawn_hook: BashSpawnHook | None = None


@dataclass
class PowerShellToolOptions:
    """Pi's PowerShellToolOptions: BashToolOptions' operations, expose_session_environment and spawn_hook."""

    operations: Any = None
    expose_session_environment: bool | None = None
    spawn_hook: BashSpawnHook | None = None


@dataclass
class ToolsOptions:
    """Pi's ToolsOptions: the options of each built-in tool, for ``create_coding_tools`` and ``create_read_only_tools``."""

    read: ReadToolOptions | None = None
    bash: BashToolOptions | None = None
    powershell: PowerShellToolOptions | None = None
    write: WriteToolOptions | None = None
    edit: EditToolOptions | None = None
    grep: GrepToolOptions | None = None
    find: FindToolOptions | None = None
    ls: LsToolOptions | None = None


@dataclass
class _Spec:
    """One create<Tool>ToolDefinition(cwd, options) call: which tool, where, and with what options. ``callbacks`` holds the functions that stay here."""

    name: str
    cwd: str
    options: dict[str, Any] = field(default_factory=dict)
    callbacks: dict[str, Any] = field(default_factory=dict)


def _require(tool: str, **members: Any) -> None:
    for name, value in members.items():
        if not callable(value):
            raise TypeError(f"{tool}: operations.{name} is required")


def _operations_names(options: dict[str, Any], names: list[str]) -> None:
    options["operations"] = names


def _read_spec(cwd: str, options: Any) -> _Spec:
    spec = _Spec("read", cwd)
    if options is None:
        return spec
    resize = _member(options, "resize_options")
    if _member(options, "auto_resize_images") is not None:
        spec.options["autoResizeImages"] = bool(_member(options, "auto_resize_images"))
    if resize is not None:
        spec.options["resizeOptions"] = resize._to_wire() if isinstance(resize, ModelImageResizeOptions) else dict(resize)
    ops = _member(options, "operations")
    if ops is not None:
        read_file, access, detect = _member(ops, "read_file"), _member(ops, "access"), _member(ops, "detect_image_mime_type")
        _require("read", read_file=read_file, access=access)
        names = ["readFile", "access"]
        spec.callbacks.update({"operations.readFile": lambda args: base64.b64encode(bytes(read_file(args["path"]))).decode("ascii"), "operations.access": lambda args: access(args["path"])})
        if detect is not None:
            names.append("detectImageMimeType")
            spec.callbacks["operations.detectImageMimeType"] = lambda args: detect(args["path"]) or None
        _operations_names(spec.options, names)
    return spec


def _write_spec(cwd: str, options: Any) -> _Spec:
    spec = _Spec("write", cwd)
    ops = _member(options, "operations")
    if ops is not None:
        write_file, mkdir = _member(ops, "write_file"), _member(ops, "mkdir")
        _require("write", write_file=write_file, mkdir=mkdir)
        spec.callbacks.update({"operations.writeFile": lambda args: write_file(args["path"], args["content"]), "operations.mkdir": lambda args: mkdir(args["path"])})
        _operations_names(spec.options, ["writeFile", "mkdir"])
    return spec


def _edit_spec(cwd: str, options: Any) -> _Spec:
    spec = _Spec("edit", cwd)
    ops = _member(options, "operations")
    if ops is not None:
        read_file, write_file, access = _member(ops, "read_file"), _member(ops, "write_file"), _member(ops, "access")
        _require("edit", read_file=read_file, write_file=write_file, access=access)
        spec.callbacks.update({
            "operations.readFile": lambda args: base64.b64encode(bytes(read_file(args["path"]))).decode("ascii"),
            "operations.writeFile": lambda args: write_file(args["path"], args["content"]),
            "operations.access": lambda args: access(args["path"]),
        })
        _operations_names(spec.options, ["readFile", "writeFile", "access"])
    return spec


def _grep_spec(cwd: str, options: Any) -> _Spec:
    spec = _Spec("grep", cwd)
    ops = _member(options, "operations")
    if ops is not None:
        is_directory, read_file = _member(ops, "is_directory"), _member(ops, "read_file")
        _require("grep", is_directory=is_directory)
        spec.callbacks["operations.isDirectory"] = lambda args: bool(is_directory(args["path"]))
        names = ["isDirectory"]
        if read_file is not None:
            names.append("readFile")
            spec.callbacks["operations.readFile"] = lambda args: read_file(args["path"])
        _operations_names(spec.options, names)
    return spec


def _find_spec(cwd: str, options: Any) -> _Spec:
    spec = _Spec("find", cwd)
    ops = _member(options, "operations")
    if ops is not None:
        exists, glob = _member(ops, "exists"), _member(ops, "glob")
        _require("find", exists=exists, glob=glob)
        spec.callbacks.update({
            "operations.exists": lambda args: bool(exists(args["path"])),
            "operations.glob": lambda args: list(glob(args["pattern"], args["cwd"], {"ignore": args.get("ignore") or [], "limit": args.get("limit")}) or []),
        })
        _operations_names(spec.options, ["exists", "glob"])
    return spec


def _ls_spec(cwd: str, options: Any) -> _Spec:
    spec = _Spec("ls", cwd)
    ops = _member(options, "operations")
    if ops is not None:
        exists, stat, readdir = _member(ops, "exists"), _member(ops, "stat"), _member(ops, "readdir")
        _require("ls", exists=exists, stat=stat, readdir=readdir)

        def stat_of(args: dict[str, Any]) -> dict[str, bool]:
            value = stat(args["path"])
            directory = _member(value, "is_directory")
            if callable(directory):
                directory = directory()
            return {"isDirectory": bool(directory)}

        spec.callbacks.update({"operations.exists": lambda args: bool(exists(args["path"])), "operations.stat": stat_of, "operations.readdir": lambda args: list(readdir(args["path"]))})
        _operations_names(spec.options, ["exists", "stat", "readdir"])
    return spec


def _shell_spec(name: str, cwd: str, options: Any) -> _Spec:
    spec = _Spec(name, cwd)
    if options is None:
        return spec
    if _member(options, "expose_session_environment") is not None:
        spec.options["exposeSessionEnvironment"] = bool(_member(options, "expose_session_environment"))
    if name == "bash":
        if _member(options, "command_prefix"):
            spec.options["commandPrefix"] = _member(options, "command_prefix")
        if _member(options, "shell_path"):
            spec.options["shellPath"] = _member(options, "shell_path")
    operations = _member(options, "operations")
    if operations is not None:
        if not callable(_member(operations, "exec")):
            raise TypeError(f"{name}: operations.exec is required")
        _operations_names(spec.options, ["exec"])
        spec.callbacks["_bash_operations"] = operations
    hook = _member(options, "spawn_hook")
    if hook is not None:
        spec.options["spawnHook"] = True
        spec.callbacks["spawnHook"] = lambda args: hook(args)
    return spec


_SPECS: dict[str, Callable[[str, Any], _Spec]] = {
    "read": _read_spec, "write": _write_spec, "edit": _edit_spec, "grep": _grep_spec, "find": _find_spec, "ls": _ls_spec,
    "bash": lambda cwd, options: _shell_spec("bash", cwd, options), "powershell": lambda cwd, options: _shell_spec("powershell", cwd, options),
}


class _Registry:
    """The functions the host calls back, for the calls in flight: the options' callbacks by handle and the output sinks of local executions by execute id. Ids
    are unique across the process, so one table serves every extension of a packed cell."""

    lock = threading.Lock()
    seq = 0
    callbacks: dict[str, dict[str, Any]] = {}
    exec_data: dict[str, Callable[[bytes], None]] = {}

    @classmethod
    def register(cls, callbacks: dict[str, Any]) -> str:
        with cls.lock:
            cls.seq += 1
            handle = f"builtin-{cls.seq}"
            cls.callbacks[handle] = callbacks
            return handle

    @classmethod
    def release(cls, handle: str) -> None:
        with cls.lock:
            cls.callbacks.pop(handle, None)


def _wire_result(result: dict[str, Any]) -> dict[str, Any]:
    """The host's AgentToolResult (Pi's member names) as the dict a tool returns, keeping the order of its members."""
    names = {"structuredContent": "structured_content", "isError": "is_error"}
    return {names.get(key, key): value for key, value in result.items()}


def _running_extension() -> Any:
    running = host_helpers._running_for(host_helpers._socket_path())
    if running is None:
        raise RuntimeError("the extension is not running: a built-in tool without a context can execute only after run has started")
    return running


def _definition_reply(spec: _Spec) -> dict[str, Any]:
    args: dict[str, Any] = {"name": spec.name, "cwd": spec.cwd}
    if spec.options:
        args["options"] = spec.options
    return host_helpers._helper_call(_CALL_DEFINITION, args)


def _execute(ext: Any, ctx: Any, spec: _Spec, tool_call_id: str, params: Any, signal: ProviderSignal | None, forward_signal: bool, on_update: Callable[[dict[str, Any]], None] | None) -> dict[str, Any]:
    """``execute(toolCallId, params, signal, onUpdate, ctx)`` of a built-in tool's definition. ``ctx`` None is an undefined context."""
    _ensure_jsonable(params, f"arguments of tool {spec.name}")
    if on_update is not None and not callable(on_update):
        raise TypeError("on_update must be callable")
    if signal is not None and not callable(getattr(signal, "subscribe", None)):
        raise TypeError("signal must be a ProviderSignal")
    with ext._state_lock:
        ext._execute_seq += 1
        execute_id = f"e{ext._execute_seq}"
        nested = _NestedCall()
        ext._nested_calls[execute_id] = nested
    handle = ""
    if spec.callbacks:
        callbacks = {key: value for key, value in spec.callbacks.items() if key != "_bash_operations"}
        handle = _Registry.register(callbacks)
        if "_bash_operations" in spec.callbacks:
            # The shell tool's exec rides the user_bash exec request under the same handle.
            ext._bash_operations.register_as(handle, spec.callbacks["_bash_operations"])
    payload: dict[str, Any] = {"name": spec.name, "cwd": spec.cwd, "toolCallId": tool_call_id, "params": params, "executeId": execute_id}
    if spec.options:
        payload["options"] = spec.options
    if on_update is not None:
        payload["wantsUpdates"] = True
    if ctx is not None:
        payload["context"] = True
    if handle:
        payload["callbacks"] = handle
    if signal is not None and signal.is_set():
        payload["aborted"] = True
    unsubscribe: Callable[[], None] | None = None
    if ctx is not None:
        ctx._report_request_state("blocked", "host_call")
    try:
        if forward_signal and ctx is not None:
            call_id, event = ext._begin_call(_CALL_EXECUTE, payload, ctx.request_id, ctx._parent, event=_NestedCallEvent(nested))
        else:
            # A signal other than the request's detaches the call from the request; it aborts through a second call, sent after the call's frame.
            call_id, event = ext._begin_call(_CALL_EXECUTE, payload, event=_NestedCallEvent(nested))
        if signal is not None:
            unsubscribe = signal.subscribe(lambda: ext._cancel_nested(execute_id))
        failure = nested.deliver(on_update)
        try:
            reply = ext._wait_call(call_id, event, _CALL_EXECUTE)
        except BaseException:
            # The host fails a call whose on_update raised, after the tool returned; the call raises what the callback raised.
            if failure is not None:
                raise failure from None
            raise
        if failure is not None:
            raise failure
        return _wire_result(reply.get("result") or {})
    finally:
        if unsubscribe is not None:
            unsubscribe()
        with ext._state_lock:
            ext._nested_calls.pop(execute_id, None)
        if handle:
            _Registry.release(handle)
            ext._bash_operations.release(handle)
        if ctx is not None:
            ctx._report_request_state("progress")


def dispatch_callback(args: dict[str, Any]) -> Any:
    """Run one function of a call's options for the host's ``builtin_tool_callback``. Raising is the callback's throw."""
    with _Registry.lock:
        callbacks = _Registry.callbacks.get(str(args.get("handle") or ""))
    if callbacks is None:
        raise RuntimeError(f"unknown built-in tool callbacks: {args.get('handle')}")
    member = str(args.get("member") or "")
    function = callbacks.get(member)
    if function is None:
        raise RuntimeError(f"no built-in tool callback {member}")
    return function(args.get("args") or {})


def dispatch_exec_data(args: dict[str, Any]) -> None:
    """Deliver one output chunk of a local execution to its ``on_data``; an exception it raises is the answer to the host."""
    with _Registry.lock:
        sink = _Registry.exec_data.get(str(args.get("executeId") or ""))
    if sink is not None:
        sink(base64.b64decode(args.get("data") or ""))


@dataclass(kw_only=True)
class BuiltinToolDefinition(ToolDefinition):
    """The ToolDefinition Pi's create<Tool>ToolDefinition returns for a built-in tool: its name, label, description, prompt snippet and guidelines, schemas and
    execution mode, with an ``execute`` that runs the built-in tool on the host. Register it with ``Extension.register_tool`` to override the built-in, using
    ``dataclasses.replace`` to change fields or ``execute`` first to wrap it.

    The host draws the built-in tool's call and result itself, so a definition that sets no renderers keeps the built-in's, as an override of a built-in does
    in Pi."""

    _spec: Any = field(default=None, repr=False, compare=False)

    def execute_with(self, ctx: Any, tool_call_id: str, params: Any, signal: ProviderSignal | None = None, on_update: Callable[[dict[str, Any]], None] | None = None) -> dict[str, Any]:
        """Pi's ``definition.execute(toolCallId, params, signal, onUpdate, ctx)``. A None signal is Pi's undefined: only the call itself ends it. The tool reads
        the session's working directory, model and identity from ``ctx``, as ``ctx?.cwd || cwd`` and ``ctx?.model`` do; the cwd given to the factory applies
        only to a tool without a context (``BuiltinTool``)."""
        return _execute(ctx.extension, ctx, self._spec, tool_call_id, params, signal, False, on_update)


@dataclass
class BuiltinTool:
    """The AgentTool Pi's create<Tool>Tool returns: the built-in tool without an extension context, so it works in the working directory given to the factory and
    without the session's model and identity."""

    name: str
    label: str
    description: str
    parameters: dict[str, Any]
    output_schema: dict[str, Any] | None = None
    constrained_sampling: dict[str, Any] | None = None
    execution_mode: str | None = None
    #: Set for the shell tools, as Pi's createBashTool and createPowerShellTool do.
    prompt_snippet: str | None = None
    prompt_guidelines: list[str] | None = None
    _spec: _Spec | None = field(default=None, repr=False, compare=False)

    def execute(self, tool_call_id: str, params: Any, signal: ProviderSignal | None = None, on_update: Callable[[dict[str, Any]], None] | None = None) -> dict[str, Any]:
        """Pi's ``tool.execute(toolCallId, params, signal, onUpdate)``. It runs over the connection of the running extension, so it needs ``run`` to have started."""
        assert self._spec is not None
        return _execute(_running_extension(), None, self._spec, tool_call_id, params, signal, False, on_update)


def _create_definition(spec: _Spec) -> Any:
    reply = _definition_reply(spec)
    spec_copy = spec

    def execute(ctx: Any, params: dict[str, Any]) -> dict[str, Any]:
        forward = None
        if ctx.tool_call_id and ctx.request_id:
            forward = ctx.on_update
        return _execute(ctx.extension, ctx, spec_copy, ctx.tool_call_id, params, None, True, forward)

    fields: dict[str, Any] = {
        "name": reply["name"], "label": reply["label"], "description": reply["description"], "parameters": reply["parameters"], "execute": execute, "_spec": spec,
    }
    if reply.get("promptSnippet"):
        fields["prompt_snippet"] = reply["promptSnippet"]
    if reply.get("promptGuidelines"):
        fields["prompt_guidelines"] = list(reply["promptGuidelines"])
    if reply.get("constrainedSampling") is not None:
        fields["constrained_sampling"] = reply["constrainedSampling"]
    if reply.get("executionMode"):
        fields["execution_mode"] = reply["executionMode"]
    if reply.get("outputSchema") is not None:
        fields["output_schema"] = reply["outputSchema"]
    if reply.get("renderShell") == "self":
        fields["render_shell"] = "self"
    return BuiltinToolDefinition(**fields)


def _create_tool(spec: _Spec) -> BuiltinTool:
    reply = _definition_reply(spec)
    tool = BuiltinTool(
        name=reply["name"], label=reply["label"], description=reply["description"], parameters=reply["parameters"], output_schema=reply.get("outputSchema"),
        constrained_sampling=reply.get("constrainedSampling"), execution_mode=reply.get("executionMode") or None, _spec=spec,
    )
    if spec.name in ("bash", "powershell"):
        tool.prompt_snippet = reply.get("promptSnippet") or None
        tool.prompt_guidelines = list(reply["promptGuidelines"]) if reply.get("promptGuidelines") else None
    return tool


def create_read_tool_definition(cwd: str, options: ReadToolOptions | None = None) -> Any:
    """Pi's createReadToolDefinition(cwd, options). Like every ``create_*_tool_definition`` it asks the host for the built-in tool's definition, so it may be
    called while the factory runs, before ``run``."""
    return _create_definition(_read_spec(cwd, options))


def create_write_tool_definition(cwd: str, options: WriteToolOptions | None = None) -> Any:
    """Pi's createWriteToolDefinition(cwd, options)."""
    return _create_definition(_write_spec(cwd, options))


def create_edit_tool_definition(cwd: str, options: EditToolOptions | None = None) -> Any:
    """Pi's createEditToolDefinition(cwd, options)."""
    return _create_definition(_edit_spec(cwd, options))


def create_bash_tool_definition(cwd: str, options: BashToolOptions | None = None) -> Any:
    """Pi's createBashToolDefinition(cwd, options)."""
    return _create_definition(_shell_spec("bash", cwd, options))


def create_grep_tool_definition(cwd: str, options: GrepToolOptions | None = None) -> Any:
    """Pi's createGrepToolDefinition(cwd, options)."""
    return _create_definition(_grep_spec(cwd, options))


def create_find_tool_definition(cwd: str, options: FindToolOptions | None = None) -> Any:
    """Pi's createFindToolDefinition(cwd, options)."""
    return _create_definition(_find_spec(cwd, options))


def create_ls_tool_definition(cwd: str, options: LsToolOptions | None = None) -> Any:
    """Pi's createLsToolDefinition(cwd, options)."""
    return _create_definition(_ls_spec(cwd, options))


def create_powershell_tool_definition(cwd: str, options: PowerShellToolOptions | None = None) -> Any:
    """Pi's createPowerShellToolDefinition(cwd, options)."""
    return _create_definition(_shell_spec("powershell", cwd, options))


def create_read_tool(cwd: str, options: ReadToolOptions | None = None) -> BuiltinTool:
    """Pi's createReadTool(cwd, options)."""
    return _create_tool(_read_spec(cwd, options))


def create_write_tool(cwd: str, options: WriteToolOptions | None = None) -> BuiltinTool:
    """Pi's createWriteTool(cwd, options)."""
    return _create_tool(_write_spec(cwd, options))


def create_edit_tool(cwd: str, options: EditToolOptions | None = None) -> BuiltinTool:
    """Pi's createEditTool(cwd, options)."""
    return _create_tool(_edit_spec(cwd, options))


def create_bash_tool(cwd: str, options: BashToolOptions | None = None) -> BuiltinTool:
    """Pi's createBashTool(cwd, options)."""
    return _create_tool(_shell_spec("bash", cwd, options))


def create_grep_tool(cwd: str, options: GrepToolOptions | None = None) -> BuiltinTool:
    """Pi's createGrepTool(cwd, options)."""
    return _create_tool(_grep_spec(cwd, options))


def create_find_tool(cwd: str, options: FindToolOptions | None = None) -> BuiltinTool:
    """Pi's createFindTool(cwd, options)."""
    return _create_tool(_find_spec(cwd, options))


def create_ls_tool(cwd: str, options: LsToolOptions | None = None) -> BuiltinTool:
    """Pi's createLsTool(cwd, options)."""
    return _create_tool(_ls_spec(cwd, options))


def create_powershell_tool(cwd: str, options: PowerShellToolOptions | None = None) -> BuiltinTool:
    """Pi's createPowerShellTool(cwd, options)."""
    return _create_tool(_shell_spec("powershell", cwd, options))


def create_coding_tools(cwd: str, options: ToolsOptions | None = None) -> list[BuiltinTool]:
    """Pi's createCodingTools(cwd, options): read, bash, edit and write."""
    return [
        create_read_tool(cwd, _member(options, "read")), create_bash_tool(cwd, _member(options, "bash")),
        create_edit_tool(cwd, _member(options, "edit")), create_write_tool(cwd, _member(options, "write")),
    ]


def create_read_only_tools(cwd: str, options: ToolsOptions | None = None) -> list[BuiltinTool]:
    """Pi's createReadOnlyTools(cwd, options): read, grep, find and ls."""
    return [
        create_read_tool(cwd, _member(options, "read")), create_grep_tool(cwd, _member(options, "grep")),
        create_find_tool(cwd, _member(options, "find")), create_ls_tool(cwd, _member(options, "ls")),
    ]


def _local_exec(shell: str, shell_path: str | None, command: str, cwd: str, options: Any) -> dict[str, Any]:
    ext = _running_extension()
    with ext._state_lock:
        ext._execute_seq += 1
        execute_id = f"e{ext._execute_seq}"
    on_data = _member(options, "on_data")
    signal = _member(options, "signal")
    failure: list[BaseException] = []

    def sink(data: bytes) -> None:
        if failure:
            raise failure[0]
        if on_data is None:
            return
        try:
            on_data(data)
        except BaseException as exc:  # noqa: BLE001 - a throw in onData ends the command with it, as Pi's would reject the exec
            failure.append(exc)
            raise

    with _Registry.lock:
        _Registry.exec_data[execute_id] = sink
    payload: dict[str, Any] = {"shell": shell, "command": command, "cwd": cwd, "executeId": execute_id}
    if shell_path:
        payload["shellPath"] = shell_path
    if _member(options, "timeout") is not None:
        payload["timeout"] = _member(options, "timeout")
    if _member(options, "env") is not None:
        payload["env"] = dict(_member(options, "env"))
    if signal is not None and signal.is_set():
        payload["aborted"] = True
    unsubscribe: Callable[[], None] | None = None
    try:
        call_id, event = ext._begin_call(_CALL_LOCAL_EXEC, payload)
        if signal is not None:
            unsubscribe = signal.subscribe(lambda: ext._cancel_nested(execute_id))
        try:
            reply = ext._wait_call(call_id, event, _CALL_LOCAL_EXEC)
        except BaseException:
            if failure:
                raise failure[0] from None
            raise
        if failure:
            raise failure[0]
        return {"exitCode": (reply.get("result") or {}).get("exitCode")}
    finally:
        if unsubscribe is not None:
            unsubscribe()
        with _Registry.lock:
            _Registry.exec_data.pop(execute_id, None)


def create_local_bash_operations(options: dict[str, Any] | None = None) -> Any:
    """Pi's createLocalBashOperations(options): the BashOperations that run a command in the local shell, which an extension wraps to rewrite commands while it
    keeps Pi's local shell behavior (for example in a user_bash handler or a bash tool's operations). ``options`` is ``{"shell_path": ...}``.

    The command runs on the host; ``exec(command, cwd, options)`` streams its output to ``options.on_data`` in order and returns Pi's ``{"exitCode": ...}``
    when the command ends, or raises the error Pi's exec rejects with ("aborted" when ``options.signal`` was set, ``"timeout:<seconds>"`` when
    ``options.timeout`` elapsed). It runs over the connection of the running extension, so it needs ``run`` to have started."""
    from .user_bash import BashOperations

    shell_path = _member(options, "shell_path")
    return BashOperations(exec=lambda command, cwd, exec_options: _local_exec("bash", shell_path, command, cwd, exec_options))


def create_local_powershell_operations() -> Any:
    """Pi's createLocalPowerShellOperations(): ``create_local_bash_operations`` for PowerShell."""
    from .user_bash import BashOperations

    return BashOperations(exec=lambda command, cwd, exec_options: _local_exec("powershell", None, command, cwd, exec_options))
