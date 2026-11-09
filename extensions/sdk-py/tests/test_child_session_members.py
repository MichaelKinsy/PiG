import pytest

from pig_sdk import HostCallError
from pig_sdk.child_session import AgentSession


class Host:
    """The extension side of one child session: records each host call and answers it from ``replies`` by member name."""

    def __init__(self, replies=None):
        self.calls = []
        self.replies = replies or {}

    def _call(self, method, args):
        self.calls.append((method, args))
        reply = self.replies.get(args.get("op") or method)
        if isinstance(reply, Exception):
            raise reply
        return {"result": reply}


def session_with(replies=None):
    host = Host(replies)
    return AgentSession(host, "c1"), host


def test_getters_are_live_host_reads():
    session, host = session_with({"sessionId": "s-1", "sessionFile": None, "model": {"id": "echo"}, "thinkingLevel": "high", "isIdle": True,
                                  "sessionName": None, "getActiveToolNames": ["a"], "systemPrompt": "p"})
    assert session.session_id == "s-1"
    assert session.session_file is None
    assert session.model == {"id": "echo"}
    assert session.thinking_level == "high"
    assert session.is_idle is True
    assert session.session_name is None
    assert session.active_tools == ["a"]
    assert session.system_prompt == "p"
    assert host.calls[0] == ("childSession.op", {"id": "c1", "op": "sessionId"})
    host.replies["sessionId"] = "s-2"
    assert session.session_id == "s-2"


def test_changes_send_pis_member_arguments():
    session, host = session_with({"clearQueue": {"steering": ["x"], "followUp": None}})
    session.set_steering_mode("all")
    session.set_model({"provider": "p", "id": "m"}, persist=True)
    session.cycle_model("backward")
    assert session.clear_queue() == {"steering": ["x"], "followUp": []}
    session.compact()
    session.navigate_tree("e1", summarize=True)
    session.execute_bash("echo hi", exclude_from_context=True)
    session.send_user_message("hello")
    session.follow_up("later")
    assert host.calls[0] == ("childSession.op", {"id": "c1", "op": "setSteeringMode", "args": {"mode": "all"}})
    assert host.calls[1] == ("childSession.opAsync", {"id": "c1", "op": "setModel", "args": {"model": {"provider": "p", "id": "m"}, "persist": True}})
    assert host.calls[2] == ("childSession.op", {"id": "c1", "op": "cycleModel", "args": {"direction": "backward", "persist": False}})
    assert host.calls[4] == ("childSession.opAsync", {"id": "c1", "op": "compact", "args": {}})
    assert host.calls[5] == ("childSession.opAsync", {"id": "c1", "op": "navigateTree", "args": {"targetId": "e1", "summarize": True}})
    assert host.calls[6] == ("childSession.opAsync", {"id": "c1", "op": "executeBash", "args": {"command": "echo hi", "excludeFromContext": True}})
    assert host.calls[7] == ("childSession.opAsync", {"id": "c1", "op": "sendUserMessage", "args": {"content": "hello", "options": {}}})
    assert host.calls[8] == ("childSession.queue", {"id": "c1", "behavior": "followUp", "text": "later"})


def test_host_errors_propagate():
    session, _ = session_with({"reload": HostCallError("busy")})
    with pytest.raises(HostCallError):
        session.reload()


# Pi 1.5.0 core/agent-session.ts declares these members `async` (waitForIdle, setModel, cycleModel, compact, reload, executeBash, navigateTree,
# exportToHtml, summarizeForBugReport, sendCustomMessage, sendUserMessage); every other member of childSession.op is synchronous.
PI_ASYNC = {"sendUserMessage", "waitForIdle", "setModel", "cycleModel", "compact", "reload", "executeBash", "navigateTree", "exportToHtml",
            "summarizeForBugReport", "sendCustomMessage"}

CALLS = [
    ("isStreaming", lambda s: s.is_streaming), ("isIdle", lambda s: s.is_idle), ("isCompacting", lambda s: s.is_compacting),
    ("isRetrying", lambda s: s.is_retrying), ("isBashRunning", lambda s: s.is_bash_running),
    ("hasPendingBashMessages", lambda s: s.has_pending_bash_messages), ("retryAttempt", lambda s: s.retry_attempt),
    ("pendingMessageCount", lambda s: s.pending_message_count), ("sessionId", lambda s: s.session_id), ("sessionFile", lambda s: s.session_file),
    ("sessionName", lambda s: s.session_name), ("systemPrompt", lambda s: s.system_prompt), ("thinkingLevel", lambda s: s.thinking_level),
    ("model", lambda s: s.model), ("routedModel", lambda s: s.routed_model), ("scopedModels", lambda s: s.scoped_models),
    ("steeringMode", lambda s: s.steering_mode), ("followUpMode", lambda s: s.follow_up_mode),
    ("autoCompactionEnabled", lambda s: s.auto_compaction_enabled), ("autoRetryEnabled", lambda s: s.auto_retry_enabled),
    ("promptTemplates", lambda s: s.prompt_templates), ("cacheWarmingStatus", lambda s: s.cache_warming_status),
    ("getActiveToolNames", lambda s: s.active_tools), ("supportsThinking", lambda s: s.supports_thinking()),
    ("getAvailableThinkingLevels", lambda s: s.get_available_thinking_levels()), ("getLastAssistantText", lambda s: s.get_last_assistant_text()),
    ("getCallableToolNames", lambda s: s.get_callable_tool_names()), ("getSteeringMessages", lambda s: s.get_steering_messages()),
    ("getFollowUpMessages", lambda s: s.get_follow_up_messages()), ("getAllTools", lambda s: s.get_all_tools()),
    ("getToolDefinition", lambda s: s.get_tool_definition("t")), ("getContextUsage", lambda s: s.get_context_usage()),
    ("getSessionStats", lambda s: s.get_session_stats()), ("getUserMessagesForForking", lambda s: s.get_user_messages_for_forking()),
    ("hasExtensionHandlers", lambda s: s.has_extension_handlers("x")), ("cycleModel", lambda s: s.cycle_model()),
    ("setThinkingLevel", lambda s: s.set_thinking_level("off")), ("cycleThinkingLevel", lambda s: s.cycle_thinking_level()),
    ("setScopedModels", lambda s: s.set_scoped_models([])), ("setActiveToolsByName", lambda s: s.set_active_tools_by_name([])),
    ("setSteeringMode", lambda s: s.set_steering_mode("all")), ("setFollowUpMode", lambda s: s.set_follow_up_mode("all")),
    ("setAutoCompactionEnabled", lambda s: s.set_auto_compaction_enabled(True)), ("setAutoRetryEnabled", lambda s: s.set_auto_retry_enabled(True)),
    ("setCacheWarmingMode", lambda s: s.set_cache_warming_mode("off")), ("setSessionName", lambda s: s.set_session_name("n")),
    ("clearQueue", lambda s: s.clear_queue()), ("recordBashResult", lambda s: s.record_bash_result("c", {})),
    ("exportToJsonl", lambda s: s.export_to_jsonl()), ("abortBash", lambda s: s.abort_bash()),
    ("abortBranchSummary", lambda s: s.abort_branch_summary()), ("abortCompaction", lambda s: s.abort_compaction()),
    ("abortRetry", lambda s: s.abort_retry()), ("setModel", lambda s: s.set_model({})), ("waitForIdle", lambda s: s.wait_for_idle()),
    ("reload", lambda s: s.reload()), ("compact", lambda s: s.compact()), ("navigateTree", lambda s: s.navigate_tree("e")),
    ("executeBash", lambda s: s.execute_bash("c")), ("exportToHtml", lambda s: s.export_to_html()),
    ("summarizeForBugReport", lambda s: s.summarize_for_bug_report()), ("sendCustomMessage", lambda s: s.send_custom_message({})),
    ("sendUserMessage", lambda s: s.send_user_message("m")),
]


@pytest.mark.parametrize("op,call", CALLS, ids=[op for op, _ in CALLS])
def test_each_member_uses_the_lane_pi_declares(op, call):
    session, host = session_with({"getSessionStats": {}, "clearQueue": {}, "exportToJsonl": "p", "exportToHtml": "p", "summarizeForBugReport": "r"})
    call(session)
    (method, args), = host.calls
    assert args["op"] == op
    assert method == ("childSession.opAsync" if op in PI_ASYNC else "childSession.op"), f"{op} is on the wrong lane"


def test_every_member_is_covered_by_the_lane_table():
    assert PI_ASYNC <= {op for op, _ in CALLS}
