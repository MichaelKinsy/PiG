"""Child agent sessions: Pi's ``createAgentSession`` (core/sdk.ts) for a Python extension.

The host owns each session for this extension's connection. The extension prompts it, listens to its events, aborts it and disposes it; the host runs the
agent loop and calls back into the extension for the tools it serves (``custom_tools``). The Node runtime serves Pi's own ``createAgentSession`` in the
extension process; this is the same call for the native SDKs, with the same observable behavior (``TestChildAgentSessionsAcrossSDKs``).

Async contract (Pi's promises, kept as blocking calls):

* :meth:`Context.create_agent_session` is awaited: resources are loaded and the model resolved before it returns, and a failure raises.
* :meth:`AgentSession.prompt` returns when the run has settled, after every listener has run through the run's last event. A rejected prompt (no model, no
  credentials, a streaming session without a delivery) raises. After :meth:`AgentSession.abort` the run settles and ``prompt`` returns normally.
* :meth:`AgentSession.abort` returns once the session is idle. :meth:`AgentSession.dispose` is synchronous and idempotent.
* Listeners run in event order on one thread of the session, never on the connection's reader, so a listener may call the host or the session. A slow
  listener delays later events, not the agent.
* The session ends with the connection: its run is aborted, its tools' contexts are cancelled and no listener runs afterwards.
"""

from __future__ import annotations

import sys
import threading
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Callable

if TYPE_CHECKING:
    from . import Extension, ToolDefinition


@dataclass(kw_only=True)
class ResourceOptions:
    """The DefaultResourceLoader options of Pi (resource-loader.ts) that select resources."""

    no_extensions: bool = False
    no_skills: bool = False
    no_prompt_templates: bool = False
    no_themes: bool = False
    no_context_files: bool = False
    #: Loaded after the resolved resources.
    additional_extension_paths: list[str] | None = None
    additional_skill_paths: list[str] | None = None
    additional_prompt_template_paths: list[str] | None = None
    #: Keep only the extensions whose path contains one of the names, compared without case (Pi's ``extensionsOverride``). ``None`` keeps all, ``[]`` none.
    extension_names: list[str] | None = None
    #: Keep only the skills named exactly by one of the names (Pi's ``skillsOverride``). ``None`` keeps all, ``[]`` none.
    skill_names: list[str] | None = None
    #: Replace SYSTEM.md / APPEND_SYSTEM.md discovery; each entry is text or the path of a file holding it. ``None`` discovers.
    system_prompt: str | None = None
    append_system_prompt: list[str] | None = None


@dataclass(kw_only=True)
class CreateAgentSessionOptions:
    """The members of Pi's CreateAgentSessionOptions (core/sdk.ts) that cross the process boundary. ``None`` is Pi's ``undefined``.

    The child shares the model runtime of the host's Session (Pi's ``modelRuntime: ctx.modelRegistry.runtime``), so every provider the extensions
    registered is available to it.
    """

    #: Working directory for project-local discovery. ``None`` is the host's.
    cwd: str | None = None
    #: Global config directory. ``None`` is the host's.
    agent_dir: str | None = None
    #: A model dict such as ``ctx.model_registry.find(...)`` returns; resolve a name or pattern through the registry first, as Pi's option takes a Model. ``None`` is Pi's default: the settings' default, else the first available model.
    model: dict[str, Any] | None = None
    #: The requested level, clamped to the model. ``None`` is the settings' default.
    thinking_level: str | None = None
    #: Allowlist of tool names or ``*`` patterns, or a list of only ``+name`` and ``-name`` entries. ``None`` is Pi's undefined; ``[]`` allows nothing.
    tools: list[str] | None = None
    #: Denylist applied after ``tools``.
    exclude_tools: list[str] | None = None
    #: ``"all"`` (no tools) or ``"builtin"`` (no default built-in tools; extension and custom tools stay enabled).
    no_tools: str | None = None
    #: Tools this extension serves to the session. ``execute``, ``prepare_arguments``, ``execution_mode`` and the model-facing metadata apply; renderers,
    #: ``prepare_loadout`` and ``reaches_tools`` have no meaning in a child.
    custom_tools: list["ToolDefinition"] | None = None
    #: In-memory settings, Pi's ``SettingsManager.inMemory(settings)``: the session reads no settings file, so ``{"compaction": {"enabled": False}}`` turns
    #: compaction off. ``None`` is the settings files of ``cwd`` and ``agent_dir``.
    settings: dict[str, Any] | None = None
    #: ``SessionManager.inMemory(cwd)``: the session log is never written. ``False`` is ``SessionManager.create(cwd)``.
    in_memory: bool = False
    #: What the session's resource loader loads. ``None`` is Pi's default loader.
    resources: ResourceOptions | None = None
    #: Pi's ``sessionManager``, ``settingsManager``, ``resourceLoader`` and ``modelRuntime`` options: objects this extension built (``SessionManager.create``, ``SettingsManager.create``, ``DefaultResourceLoader``, ``ModelRuntime.create``). The session uses the very objects. Each replaces the option that builds its own (``in_memory``, ``settings``, ``resources``; the host's model runtime), and naming both is an error.
    session_manager: Any = None
    settings_manager: Any = None
    resource_loader: Any = None
    model_runtime: Any = None
    #: Pi's ``scopedModels``: a list of ``{"model": <model object>, "thinkingLevel": ...}`` the session cycles through.
    scoped_models: list[dict[str, Any]] | None = None


def _resources_wire(resources: ResourceOptions | None) -> dict[str, Any] | None:
    if resources is None:
        return None
    wire: dict[str, Any] = {}
    for flag, key in ((resources.no_extensions, "noExtensions"), (resources.no_skills, "noSkills"), (resources.no_prompt_templates, "noPromptTemplates"),
                      (resources.no_themes, "noThemes"), (resources.no_context_files, "noContextFiles")):
        if flag:
            wire[key] = True
    for value, key in ((resources.additional_extension_paths, "additionalExtensionPaths"), (resources.additional_skill_paths, "additionalSkillPaths"),
                       (resources.additional_prompt_template_paths, "additionalPromptTemplatePaths"), (resources.extension_names, "extensionNames"),
                       (resources.skill_names, "skillNames"), (resources.system_prompt, "systemPrompt"), (resources.append_system_prompt, "appendSystemPrompt")):
        if value is not None:
            wire[key] = value
    return wire or None


class _ChildSessions:
    """The extension's live child sessions."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._by_id: dict[str, AgentSession] = {}
        self._closed = False
        self._next = 0

    def next_id(self) -> str:
        with self._lock:
            self._next += 1
            return f"c{self._next}"

    def lookup(self, session_id: str) -> "AgentSession | None":
        with self._lock:
            return self._by_id.get(session_id)

    def add(self, session: "AgentSession") -> bool:
        with self._lock:
            if self._closed:
                return False
            self._by_id[session._id] = session
            return True

    def remove(self, session_id: str) -> None:
        with self._lock:
            self._by_id.pop(session_id, None)

    def close_all(self) -> None:
        """End every session without telling the host: it disposes them as the connection closes."""
        with self._lock:
            self._closed = True
            sessions = list(self._by_id.values())
            self._by_id.clear()
        for session in sessions:
            session._stop()

    def deliver(self, args: dict[str, Any]) -> None:
        """Queue one ``childSession.event`` notification on its session. The reader calls it, so it only queues."""
        session = self.lookup(str(args.get("id") or ""))
        if session is not None:
            seq = int(args.get("seq") or 0)
            stream = args.get("stream")
            session._enqueue(seq, args.get("event") if stream is None else session._apply_update(stream, args.get("event")))


class AgentSession:
    """A child agent session: Pi's AgentSession as an extension holds it. Create it with :meth:`Context.create_agent_session`."""

    def __init__(self, extension: "Extension", session_id: str) -> None:
        self._extension = extension
        self._id = session_id
        self._tools: dict[str, Callable[..., Any]] = {}
        self._prepares: dict[str, Callable[..., Any]] = {}
        self._cond = threading.Condition()
        self._listeners: list[Callable[[dict[str, Any]], None]] = []
        self._pending: list[tuple[int, Any]] = []
        self._delivered = 0
        #: The message_update payload the host last sent, with its number; the connection's reader owns it.
        self._replica: tuple[int, Any] | None = None
        self._stopped = False
        self._running = False
        self._disposed = False
        #: ``session.sessionId`` and ``session.sessionFile`` (``None`` for an in-memory session).
        self.session_id = ""
        self.session_file: str | None = None
        #: What the session held when it was created. Read the current values with :meth:`state`.
        self.model: dict[str, Any] | None = None
        self.thinking_level = ""
        self.active_tools: list[str] = []
        self.system_prompt = ""
        #: ``CreateAgentSessionResult.modelFallbackMessage``, or ``None``.
        self.model_fallback_message: str | None = None
        #: The errors of ``CreateAgentSessionResult.extensionsResult.errors``, as ``"path: error"``.
        self.extension_errors: list[str] = []

    def _call(self, method: str, args: dict[str, Any]) -> Any:
        """A host call that belongs to the session, not to the request that happens to use it: the session outlives the command that created it."""
        return self._extension._call(method, {"id": self._id, **args}).get("result")

    def prompt(self, text: str, *, expand_prompt_templates: bool | None = None, images: list[dict[str, str]] | None = None,
               streaming_behavior: str | None = None, source: str | None = None) -> None:
        """AgentSession.prompt: run the agent on ``text`` and return when the run has settled, after the listeners have seen its last event.

        A rejected prompt raises :class:`HostCallError`. ``streaming_behavior`` is ``"steer"`` or ``"followUp"``; a prompt to a running session needs it.
        """
        args: dict[str, Any] = {"text": text}
        if images:
            args["images"] = images
        if expand_prompt_templates is not None:
            args["expandPromptTemplates"] = expand_prompt_templates
        if streaming_behavior is not None:
            args["streamingBehavior"] = streaming_behavior
        if source is not None:
            args["source"] = source
        result = self._call("childSession.prompt", args) or {}
        self._wait_delivered(int(result.get("events") or 0))

    def steer(self, text: str, images: list[dict[str, str]] | None = None) -> None:
        """AgentSession.steer: queue ``text`` for the running session after the current turn's tool calls, before its next model call."""
        self._queue("steer", text, images)

    def follow_up(self, text: str, images: list[dict[str, str]] | None = None) -> None:
        """AgentSession.followUp: queue ``text`` for after the run's current work finishes."""
        self._queue("followUp", text, images)

    def _queue(self, behavior: str, text: str, images: list[dict[str, str]] | None) -> None:
        args: dict[str, Any] = {"behavior": behavior, "text": text}
        if images:
            args["images"] = images
        self._call("childSession.queue", args)

    def abort(self) -> None:
        """AgentSession.abort: abort the run and return once the session is idle. An aborted run settles normally, so its ``prompt`` returns."""
        self._call("childSession.abort", {})

    def bind_extensions(self) -> None:
        """AgentSession.bindExtensions({}): the extensions the session's resources loaded start, headless, and receive ``session_start``."""
        self._call("childSession.bindExtensions", {})

    def state(self) -> dict[str, Any]:
        """The session's current state: ``sessionId``, ``sessionFile``, ``model``, ``isStreaming``, ``isIdle``, ``thinkingLevel``, ``activeTools``,
        ``systemPrompt``, ``pendingMessageCount`` and ``lastAssistantText`` (absent when there is none)."""
        return self._call("childSession.state", {}) or {}

    def messages(self) -> list[dict[str, Any]]:
        """``session.messages``: the transcript the session holds, in Pi's message shape."""
        return list(self._call("childSession.messages", {}) or [])

    def subscribe(self, listener: Callable[[dict[str, Any]], None]) -> Callable[[], None]:
        """AgentSession.subscribe: ``listener`` receives the session's events (Pi's AgentSessionEvent as a dict) in order until the returned function
        unsubscribes it, the session is disposed or the connection ends. The dict is shared by the session's listeners."""
        with self._cond:
            self._listeners.append(listener)

        def unsubscribe() -> None:
            with self._cond:
                if listener in self._listeners:
                    self._listeners.remove(listener)

        return unsubscribe

    def dispose(self) -> None:
        """AgentSession.dispose: abort the run, release the session in the host and remove its listeners. Idempotent."""
        with self._cond:
            if self._disposed:
                return
            self._disposed = True
        self._stop()
        try:
            self._call("childSession.dispose", {})
        except Exception as error:  # noqa: BLE001 - reported where the host shows extension output, as Pi's dispose never fails
            print(f"pig: dispose of child session {self._id} failed: {error}", file=sys.stderr)

    def _stop(self) -> None:
        """End the delivery: queued events are dropped and no listener runs again."""
        with self._cond:
            self._disposed = True
            self._stopped = True
            self._listeners = []
            self._pending = []
            self._cond.notify_all()

    def _apply_update(self, stream: dict[str, Any], keyframe: Any) -> Any:
        """The full message_update event the stream member produces (EventStream in the host's protocol.go), or ``None`` when it cannot be: the event is then not delivered, but its number still counts. The connection's reader calls it, in arrival order."""
        from . import _apply_stream_ops, _clone_stream_value

        try:
            if keyframe is not None:
                root = keyframe
            else:
                base = int(stream.get("base") or 0)
                if self._replica is None or self._replica[0] != base:
                    raise RuntimeError(f"message_update delta applies to payload {base}, the replica holds a different one")
                root = _apply_stream_ops(self._replica[1], stream.get("ops"))
            self._replica = (int(stream.get("seq") or 0), root)
            return _clone_stream_value(root)
        except Exception as error:  # noqa: BLE001 - a protocol violation drops the event, not the connection
            self._replica = None
            print(f"pig: child session {self._id}: {error}", file=sys.stderr)
            return None

    def _enqueue(self, seq: int, event: Any) -> None:
        with self._cond:
            if self._stopped:
                return
            self._pending.append((seq, event))
            start = not self._running
            self._running = True
        if start:
            threading.Thread(target=self._deliver_loop, name=f"pig-child-session-{self._id}", daemon=True).start()

    def _deliver_loop(self) -> None:
        while True:
            with self._cond:
                if self._stopped or not self._pending:
                    self._running = False
                    return
                seq, event = self._pending.pop(0)
                listeners = list(self._listeners)
            for listener in listeners if event is not None else ():
                try:
                    listener(event)
                except Exception as error:  # noqa: BLE001 - a throwing listener does not stop Pi's emission
                    print(f"pig: child session {self._id} listener raised: {error!r}", file=sys.stderr)
            with self._cond:
                self._delivered = seq
                self._cond.notify_all()

    def _wait_delivered(self, seq: int) -> None:
        with self._cond:
            while self._delivered < seq and not self._stopped:
                self._cond.wait()


def create_agent_session(extension: "Extension", call: Callable[[str, Any], dict[str, Any]], options: CreateAgentSessionOptions | None) -> AgentSession:
    """Pi's createAgentSession: create a child agent session in the host and return once it is ready. ``call`` is the host call of the request that makes it."""
    options = options or CreateAgentSessionOptions()
    session = AgentSession(extension, extension._children.next_id())
    args: dict[str, Any] = {"id": session._id}
    for value, key in ((options.cwd, "cwd"), (options.agent_dir, "agentDir"), (options.model, "model"), (options.thinking_level, "thinkingLevel"),
                       (options.tools, "tools"), (options.exclude_tools, "excludeTools"), (options.no_tools, "noTools"), (options.settings, "settings")):
        if value is not None:
            args[key] = value
    if options.in_memory:
        args["inMemory"] = True
    for value, key in ((options.session_manager, "sessionManager"), (options.settings_manager, "settingsManager"), (options.resource_loader, "resourceLoader"), (options.model_runtime, "modelRuntime")):
        if value is not None:
            args[key] = value.handle
    if options.scoped_models is not None:
        args["scopedModels"] = options.scoped_models
    resources = _resources_wire(options.resources)
    if resources is not None:
        args["resources"] = resources
    declarations = []
    for definition in options.custom_tools or []:
        declaration = extension._tool_declaration(definition, "child session tool")
        if definition.prepare_arguments is not None:
            declaration["prepares_arguments"] = True
            session._prepares[definition.name] = definition.prepare_arguments
        declarations.append(declaration)
        session._tools[definition.name] = definition.execute
    if declarations:
        args["customTools"] = declarations
    # The tools are served from the moment the host knows the session, which can be before the call returns.
    if not extension._children.add(session):
        raise RuntimeError("Extension connection is closed")
    try:
        info = call("childSession.create", args).get("result") or {}
    except BaseException:
        extension._children.remove(session._id)
        raise
    session.session_id = str(info.get("sessionId") or "")
    session.session_file = info.get("sessionFile") or None
    session.model = info.get("model")
    session.thinking_level = str(info.get("thinkingLevel") or "")
    session.active_tools = list(info.get("activeTools") or [])
    session.system_prompt = str(info.get("systemPrompt") or "")
    session.model_fallback_message = info.get("modelFallbackMessage") or None
    session.extension_errors = list(info.get("extensionErrors") or [])
    return session
