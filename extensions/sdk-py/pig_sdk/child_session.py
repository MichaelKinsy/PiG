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
* The other members of Pi's AgentSession run on the session the host owns (``childSession.op``), so each returns what the session holds now. Pi's getters
  are properties; a member Pi types ``X | undefined`` is ``None``; values are Pi's JSON shapes as dicts and lists. A member Pi declares ``async``
  (``wait_for_idle``, ``reload``, ``compact``, ``navigate_tree``, ``execute_bash``, ``export_to_html``, ``summarize_for_bug_report``, ``set_model``,
  ``send_custom_message``, ``send_user_message``) blocks until its Promise settles, starts in call order and never holds a later call: abort a running
  ``compact``, ``navigate_tree`` or ``execute_bash`` with ``abort_compaction``, ``abort_branch_summary`` or ``abort_bash`` from another thread. Scope: a
  tool's ``execute`` and hooks do not cross the wire, ``execute_bash`` has no ``onChunk`` (the chunks reach ``subscribe`` listeners).
"""

from __future__ import annotations

import threading
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Callable

from ._diagnostic import diagnose

if TYPE_CHECKING:
    from . import Extension, ToolDefinition
    from .session_manager import SessionManager


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
    #: Pi's ``sessionStartEvent``: the session_start event the session's extensions receive when they bind. ``None`` is Pi's default, reason ``"startup"``.
    session_start_event: "SessionStartEvent | None" = None


@dataclass(kw_only=True)
class SessionStartEvent:
    """Pi's SessionStartEvent (types.ts) as the ``sessionStartEvent`` option takes it: why a session starts and, when it replaces another, that session's file. Its ``type`` is always ``"session_start"``."""

    #: ``"startup"``, ``"reload"``, ``"new"``, ``"resume"`` or ``"fork"``.
    reason: str
    #: The file of the session this one replaces; ``None`` is Pi's undefined.
    previous_session_file: str | None = None


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
        #: ``CreateAgentSessionResult.modelFallbackMessage``, or ``None``.
        self.model_fallback_message: str | None = None
        #: The errors of ``CreateAgentSessionResult.extensionsResult.errors``, as ``"path: error"``.
        self.extension_errors: list[str] = []

    def _call(self, method: str, args: dict[str, Any]) -> Any:
        """A host call that belongs to the session, not to the request that happens to use it: the session outlives the command that created it."""
        return self._extension._call(method, {"id": self._id, **args}).get("result")

    def _member(self, name: str, args: dict[str, Any] | None = None) -> Any:
        op: dict[str, Any] = {"op": name}
        if args is not None:
            op["args"] = args
        return self._call("childSession.op", op)

    def _member_async(self, name: str, args: dict[str, Any] | None = None) -> Any:
        op: dict[str, Any] = {"op": name}
        if args is not None:
            op["args"] = args
        return self._call("childSession.opAsync", op)

    # -- state reads ---------------------------------------------------------------------------------------------------------------------------

    @property
    def is_streaming(self) -> bool:
        """``session.isStreaming``."""
        return bool(self._member("isStreaming"))

    @property
    def is_idle(self) -> bool:
        """``session.isIdle``: neither streaming nor compacting."""
        return bool(self._member("isIdle"))

    @property
    def is_compacting(self) -> bool:
        """``session.isCompacting``."""
        return bool(self._member("isCompacting"))

    @property
    def is_retrying(self) -> bool:
        """``session.isRetrying``."""
        return bool(self._member("isRetrying"))

    @property
    def is_bash_running(self) -> bool:
        """``session.isBashRunning``."""
        return bool(self._member("isBashRunning"))

    @property
    def has_pending_bash_messages(self) -> bool:
        """``session.hasPendingBashMessages``: bash results wait to be flushed after the agent turn."""
        return bool(self._member("hasPendingBashMessages"))

    @property
    def retry_attempt(self) -> int:
        """``session.retryAttempt``."""
        return int(self._member("retryAttempt") or 0)

    @property
    def pending_message_count(self) -> int:
        """``session.pendingMessageCount``: the queued steering and follow-up messages."""
        return int(self._member("pendingMessageCount") or 0)

    @property
    def session_id(self) -> str:
        """``session.sessionId``."""
        return str(self._member("sessionId") or "")

    @property
    def session_file(self) -> str | None:
        """``session.sessionFile``; ``None`` for an in-memory session, Pi's undefined."""
        return self._member("sessionFile") or None

    @property
    def session_name(self) -> str | None:
        """``session.sessionName``; ``None`` when the session has no name, Pi's undefined."""
        return self._member("sessionName") or None

    @property
    def system_prompt(self) -> str:
        """``session.systemPrompt``."""
        return str(self._member("systemPrompt") or "")

    @property
    def thinking_level(self) -> str:
        """``session.thinkingLevel``."""
        return str(self._member("thinkingLevel") or "")

    @property
    def model(self) -> dict[str, Any] | None:
        """``session.model``: the session's model object, ``None`` without one."""
        return self._member("model")

    @property
    def routed_model(self) -> dict[str, Any] | None:
        """``session.routedModel``: under a virtual selection, ``{"model", "thinkingLevel"}`` of the latest successful response, ``None`` otherwise."""
        return self._member("routedModel")

    @property
    def scoped_models(self) -> list[dict[str, Any]]:
        """``session.scopedModels``: the ``{"model", "thinkingLevel"}`` entries the session cycles through, empty for all available models."""
        return list(self._member("scopedModels") or [])

    @property
    def steering_mode(self) -> str:
        """``session.steeringMode``: ``"all"`` or ``"one-at-a-time"``."""
        return str(self._member("steeringMode") or "")

    @property
    def follow_up_mode(self) -> str:
        """``session.followUpMode``: ``"all"`` or ``"one-at-a-time"``."""
        return str(self._member("followUpMode") or "")

    @property
    def auto_compaction_enabled(self) -> bool:
        """``session.autoCompactionEnabled``."""
        return bool(self._member("autoCompactionEnabled"))

    @property
    def auto_retry_enabled(self) -> bool:
        """``session.autoRetryEnabled``."""
        return bool(self._member("autoRetryEnabled"))

    @property
    def prompt_templates(self) -> list[dict[str, Any]]:
        """``session.promptTemplates``."""
        return list(self._member("promptTemplates") or [])

    @property
    def cache_warming_status(self) -> dict[str, Any] | None:
        """``session.cacheWarmingStatus`` (``{"state", ...}``): ``None`` when the session has no cache warmer."""
        return self._member("cacheWarmingStatus")

    @property
    def active_tools(self) -> list[str]:
        """The names of the active tools, as :meth:`get_active_tool_names` returns them."""
        return self.get_active_tool_names()

    def supports_thinking(self) -> bool:
        """``session.supportsThinking()``."""
        return bool(self._member("supportsThinking"))

    def get_available_thinking_levels(self) -> list[str]:
        """``session.getAvailableThinkingLevels()``."""
        return list(self._member("getAvailableThinkingLevels") or [])

    def get_last_assistant_text(self) -> str | None:
        """``session.getLastAssistantText()``; ``None`` when the session has none, Pi's undefined."""
        return self._member("getLastAssistantText")

    def get_active_tool_names(self) -> list[str]:
        """``session.getActiveToolNames()``."""
        return list(self._member("getActiveToolNames") or [])

    def get_callable_tool_names(self) -> list[str]:
        """``session.getCallableToolNames()``."""
        return list(self._member("getCallableToolNames") or [])

    def get_steering_messages(self) -> list[str]:
        """``session.getSteeringMessages()``: the steering messages that have not been delivered."""
        return list(self._member("getSteeringMessages") or [])

    def get_follow_up_messages(self) -> list[str]:
        """``session.getFollowUpMessages()``: the follow-up messages that have not been delivered."""
        return list(self._member("getFollowUpMessages") or [])

    def get_all_tools(self) -> list[dict[str, Any]]:
        """``session.getAllTools()``: ``ToolInfo`` dicts. A tool's ``reachesTools`` hook is a function of the extension that registered it and is not carried."""
        return list(self._member("getAllTools") or [])

    def get_tool_definition(self, name: str) -> dict[str, Any] | None:
        """``session.getToolDefinition(name)``: the definition's data, ``None`` when no tool has the name. Its ``execute`` and hooks run in the extension that registered the tool and are not carried."""
        return self._member("getToolDefinition", {"name": name})

    def get_context_usage(self) -> dict[str, Any] | None:
        """``session.getContextUsage()`` (``{"tokens", "contextWindow", "percent"}``): ``None`` when no usable model context window is available."""
        return self._member("getContextUsage")

    def get_session_stats(self) -> dict[str, Any]:
        """``session.getSessionStats()``."""
        return self._member("getSessionStats") or {}

    def get_user_messages_for_forking(self) -> list[dict[str, str]]:
        """``session.getUserMessagesForForking()``: ``{"entryId", "text"}`` entries."""
        return list(self._member("getUserMessagesForForking") or [])

    def has_extension_handlers(self, event_type: str) -> bool:
        """``session.hasExtensionHandlers(eventType)``: an extension the session loaded handles the event."""
        return bool(self._member("hasExtensionHandlers", {"eventType": event_type}))

    # -- changes -------------------------------------------------------------------------------------------------------------------------------

    def cycle_model(self, direction: str = "forward", *, persist: bool = False) -> dict[str, Any] | None:
        """``session.cycleModel(direction, options)``: ``direction`` is ``"forward"`` or ``"backward"``. Returns ``{"model", "thinkingLevel", "isScoped"}``, ``None`` when there is no model to cycle to."""
        return self._member("cycleModel", {"direction": direction, "persist": persist})

    def set_thinking_level(self, level: str, *, persist: bool = False) -> None:
        """``session.setThinkingLevel(level, options)``; the level is clamped to the model."""
        self._member("setThinkingLevel", {"level": level, "persist": persist})

    def cycle_thinking_level(self, *, persist: bool = False) -> str | None:
        """``session.cycleThinkingLevel(options)``: the new level, ``None`` when the model does not support thinking, Pi's undefined."""
        return self._member("cycleThinkingLevel", {"persist": persist})

    def set_scoped_models(self, models: list[dict[str, Any]]) -> None:
        """``session.setScopedModels(models)``: ``{"model": <model object>, "thinkingLevel": ...}`` entries."""
        self._member("setScopedModels", {"models": list(models)})

    def set_active_tools_by_name(self, names: list[str]) -> None:
        """``session.setActiveToolsByName(names)``."""
        self._member("setActiveToolsByName", {"names": list(names)})

    def set_steering_mode(self, mode: str) -> None:
        """``session.setSteeringMode(mode)``: ``"all"`` or ``"one-at-a-time"``."""
        self._member("setSteeringMode", {"mode": mode})

    def set_follow_up_mode(self, mode: str) -> None:
        """``session.setFollowUpMode(mode)``: ``"all"`` or ``"one-at-a-time"``."""
        self._member("setFollowUpMode", {"mode": mode})

    def set_auto_compaction_enabled(self, enabled: bool) -> None:
        """``session.setAutoCompactionEnabled(enabled)``."""
        self._member("setAutoCompactionEnabled", {"enabled": enabled})

    def set_auto_retry_enabled(self, enabled: bool) -> None:
        """``session.setAutoRetryEnabled(enabled)``."""
        self._member("setAutoRetryEnabled", {"enabled": enabled})

    def set_cache_warming_mode(self, mode: str) -> None:
        """``session.setCacheWarmingMode(mode)``: ``"off"``, ``"streaming"`` or ``"idle"``."""
        self._member("setCacheWarmingMode", {"mode": mode})

    def set_session_name(self, name: str) -> None:
        """``session.setSessionName(name)``."""
        self._member("setSessionName", {"name": name})

    def clear_queue(self) -> dict[str, list[str]]:
        """``session.clearQueue()``: removes the queued messages and returns ``{"steering": [...], "followUp": [...]}``."""
        queued = self._member("clearQueue") or {}
        return {"steering": list(queued.get("steering") or []), "followUp": list(queued.get("followUp") or [])}

    def record_bash_result(self, command: str, result: dict[str, Any], *, exclude_from_context: bool | None = None) -> None:
        """``session.recordBashResult(command, result, options)``: ``result`` is a ``BashResult`` dict (``output``, ``exitCode``, ``cancelled``, ``truncated``)."""
        args: dict[str, Any] = {"command": command, "result": result}
        if exclude_from_context is not None:
            args["excludeFromContext"] = exclude_from_context
        self._member("recordBashResult", args)

    def export_to_jsonl(self, output_path: str | None = None) -> str:
        """``session.exportToJsonl(outputPath)``: writes the session as JSONL and returns its path."""
        return str(self._member("exportToJsonl", {} if output_path is None else {"outputPath": output_path}))

    def abort_bash(self) -> None:
        """``session.abortBash()``."""
        self._member("abortBash")

    def abort_branch_summary(self) -> None:
        """``session.abortBranchSummary()``."""
        self._member("abortBranchSummary")

    def abort_compaction(self) -> None:
        """``session.abortCompaction()``."""
        self._member("abortCompaction")

    def abort_retry(self) -> None:
        """``session.abortRetry()``."""
        self._member("abortRetry")

    # -- awaited members -----------------------------------------------------------------------------------------------------------------------

    def set_model(self, model: dict[str, Any], *, persist: bool = False) -> None:
        """``session.setModel(model, options)``: ``model`` is a Pi Model object whose ``provider`` and ``id`` name a model of the host's registry. Raises for a model whose provider has no credentials."""
        self._member_async("setModel", {"model": model, "persist": persist})

    def wait_for_idle(self) -> None:
        """``session.waitForIdle()``: returns when the session is neither streaming nor compacting."""
        self._member_async("waitForIdle")

    def reload(self) -> None:
        """``session.reload()``: settings, extensions and resources are loaded again. Pi rejects it while the session runs or compacts."""
        self._member_async("reload")

    def compact(self, custom_instructions: str | None = None) -> dict[str, Any]:
        """``session.compact(customInstructions)``: manual compaction, returning the ``CompactionResult`` dict. A session with nothing to compact raises, as Pi's rejection. Abort it with :meth:`abort_compaction` from another thread."""
        return self._member_async("compact", {} if custom_instructions is None else {"customInstructions": custom_instructions}) or {}

    def navigate_tree(self, target_id: str, *, summarize: bool | None = None, custom_instructions: str | None = None,
                      replace_instructions: bool | None = None, label: str | None = None) -> dict[str, Any]:
        """``session.navigateTree(targetId, options)``: ``{"cancelled", "aborted"?, "editorText"?, "summaryEntry"?}``. Abort it with :meth:`abort_branch_summary` from another thread."""
        args: dict[str, Any] = {"targetId": target_id}
        for value, key in ((summarize, "summarize"), (custom_instructions, "customInstructions"), (replace_instructions, "replaceInstructions"), (label, "label")):
            if value is not None:
                args[key] = value
        return self._member_async("navigateTree", args) or {}

    def execute_bash(self, command: str, *, exclude_from_context: bool | None = None) -> dict[str, Any]:
        """``session.executeBash(command, onChunk, options)``: the command runs outside the agent loop and the ``BashResult`` dict is recorded for the next turn. ``onChunk`` is not carried: the output chunks reach :meth:`subscribe` listeners as the session's events do. Abort it with :meth:`abort_bash` from another thread."""
        args: dict[str, Any] = {"command": command}
        if exclude_from_context is not None:
            args["excludeFromContext"] = exclude_from_context
        return self._member_async("executeBash", args) or {}

    def export_to_html(self, output_path: str | None = None, *, theme_name: str | None = None) -> str:
        """``session.exportToHtml(outputPath, options)``: writes the session as an HTML file and returns its path. ``None`` is Pi's default location."""
        args: dict[str, Any] = {}
        if output_path is not None:
            args["outputPath"] = output_path
        if theme_name is not None:
            args["themeName"] = theme_name
        return str(self._member_async("exportToHtml", args))

    def summarize_for_bug_report(self, hint: str | None = None) -> str:
        """``session.summarizeForBugReport({hint})``."""
        return str(self._member_async("summarizeForBugReport", {} if hint is None else {"hint": hint}))

    def send_custom_message(self, message: dict[str, Any], options: dict[str, Any] | None = None) -> None:
        """``session.sendCustomMessage(message, options)``: ``message`` is ``{"customType", "content", "display", "details"?}``, ``options`` ``{"triggerTurn"?, "deliverAs"?}``."""
        self._member_async("sendCustomMessage", {"message": message, "options": options or {}})

    def send_user_message(self, content: str | list[dict[str, Any]], options: dict[str, Any] | None = None) -> None:
        """``session.sendUserMessage(content, options)``: ``content`` is a string or text and image content blocks, ``options`` ``{"deliverAs"?, "expandPromptTemplates"?}``."""
        self._member_async("sendUserMessage", {"content": content, "options": options or {}})

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

    def refresh_context(self) -> None:
        """``session.refreshContext()``: the session's finalized transcript is read again from its log, after the caller appended entries to the log :meth:`session_manager` returns."""
        self._call("childSession.refreshContext", {})

    def session_manager(self) -> "SessionManager":
        """``session.sessionManager``: the log the session appends to, the very one a ``SessionManager`` passed in ``CreateAgentSessionOptions`` names."""
        from .session_manager import SessionManager

        reply = self._call("childSession.sessionManager", {}) or {}
        if not reply.get("handle"):
            raise RuntimeError("host returned no session graph handle")
        manager = object.__new__(SessionManager)
        manager._context = self._extension
        manager.handle = reply["handle"]
        manager._released = False
        return manager

    def _emit_extension_event(self, event: dict[str, Any]) -> tuple[Any, bool]:
        """``session.extensionRunner.hasHandlers(event.type)`` and then ``emit(event)``: the result, and whether an extension had a handler for the type. It serves ``AgentSessionRuntime``, which Pi gives these two calls on the runner of the session it replaces."""
        reply = self._call("childSession.emit", {"event": event}) or {}
        return reply.get("result"), bool(reply.get("emitted"))

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
            diagnose(f"pig: dispose of child session {self._id} failed: {error}" + "\n")

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
            diagnose(f"pig: child session {self._id}: {error}" + "\n")
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
                    diagnose(f"pig: child session {self._id} listener raised: {error!r}" + "\n")
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
    if options.session_start_event is not None:
        event: dict[str, Any] = {"reason": options.session_start_event.reason}
        if options.session_start_event.previous_session_file is not None:
            event["previousSessionFile"] = options.session_start_event.previous_session_file
        args["sessionStartEvent"] = event
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
    session.model_fallback_message = info.get("modelFallbackMessage") or None
    session.extension_errors = list(info.get("extensionErrors") or [])
    return session
