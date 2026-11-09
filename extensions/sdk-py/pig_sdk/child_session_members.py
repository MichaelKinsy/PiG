"""The members of Pi's ``AgentSession`` (core/agent-session.ts) an extension reaches over the wire, after the ones in :mod:`pig_sdk.child_session`.

Each is the host's own member (``childSession.op``): the value is read from, or the change made on, the session the host owns, so a call returns what the
session holds now. Pi's getters are properties here; a member Pi types ``X | undefined`` is ``None`` for undefined. Values are Pi's JSON shapes as dicts and
lists (``ToolInfo``, ``SessionStats``, ``CompactionResult`` ...). The scope limits are Go's (docs/extension-api-parity.md): a tool's ``execute`` and hooks do
not cross the wire, ``execute_bash`` has no ``onChunk`` (the chunks reach ``subscribe`` listeners), ``bind_extensions`` is headless.

Async contract: a member Pi declares ``async`` (``wait_for_idle``, ``reload``, ``compact``, ``navigate_tree``, ``execute_bash``, ``export_to_html``,
``summarize_for_bug_report``, ``set_model``, ``send_custom_message``, ``send_user_message``) blocks until its Promise settles and returns its value or raises
the host's error. It starts in call order, and a later call from another thread (``abort_compaction``, ``abort_branch_summary``, ``abort_bash``) never
waits for it: that is how a running ``compact``, ``navigate_tree`` or ``execute_bash`` is aborted. A synchronous member is a blocking call that raises the
host's error.
"""

from __future__ import annotations

from typing import Any


class _SessionMembers:
    """The AgentSession members of ``childSession.op``; mixed into ``AgentSession``, which provides ``_call``."""

    def _member(self, name: str, args: dict[str, Any] | None = None) -> Any:
        op: dict[str, Any] = {"op": name}
        if args is not None:
            op["args"] = args
        return self._call("childSession.op", op)  # type: ignore[attr-defined]

    def _member_async(self, name: str, args: dict[str, Any] | None = None) -> Any:
        op: dict[str, Any] = {"op": name}
        if args is not None:
            op["args"] = args
        return self._call("childSession.opAsync", op)  # type: ignore[attr-defined]

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
