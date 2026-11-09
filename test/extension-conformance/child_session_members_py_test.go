package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// childMembersPython is the child_members scenario of the Go fixture (testfixture/childsession/members.go) for the Python SDK: the same session, the same members in the same order, the same observation keys, so childMembersWant is the golden of both.
const childMembersPython = `import json
import os
import threading
import time
import pig_sdk


def new_extension():
    ext = pig_sdk.Extension("child-session")

    def child_members(ctx, args):
        directory = args["dir"]
        sentinel = pig_sdk.ToolDefinition(
            name="sentinel_tool", label="sentinel", description="uppercases x",
            parameters={"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]},
            execute=lambda c, params: params["x"].upper() + "!",
        )
        session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(
            model={"provider": "sentinel", "id": "echo"}, tools=["sentinel_tool"], in_memory=True, settings={"compaction": {"enabled": False}},
            resources=pig_sdk.ResourceOptions(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True),
            custom_tools=[sentinel],
        ))
        out = {}

        def refused(call):
            try:
                call()
            except pig_sdk.HostCallError as error:
                return error.message
            return ""

        try:
            # Reads of a fresh session.
            session_id = session.session_id
            out["sessionId"] = session_id != ""
            out["sessionFile"] = session.session_file or ""
            out["model"] = (session.model or {}).get("id")
            out["isIdle"] = session.is_idle
            out["isStreaming"] = session.is_streaming
            out["isCompacting"] = session.is_compacting
            out["isRetrying"] = session.is_retrying
            out["retryAttempt"] = session.retry_attempt
            out["pendingMessageCount"] = session.pending_message_count
            out["steeringMode"] = session.steering_mode
            out["followUpMode"] = session.follow_up_mode
            out["autoCompactionEnabled"] = session.auto_compaction_enabled
            out["autoRetryEnabled"] = session.auto_retry_enabled
            out["sessionName"] = session.session_name or ""
            out["scopedModelsStart"] = len(session.scoped_models)
            out["promptTemplates"] = len(session.prompt_templates)
            out["hasHandlers"] = session.has_extension_handlers("agent_start")
            out["lastTextStart"] = session.get_last_assistant_text() is None
            out["systemPromptLen"] = len(session.system_prompt) > 0

            # Changes read back: each value is one no default holds.
            session.set_steering_mode("all")
            session.set_follow_up_mode("all")
            session.set_auto_compaction_enabled(True)
            session.set_auto_retry_enabled(False)
            session.set_session_name("named-by-members")
            session.set_scoped_models([{"model": {"provider": "sentinel", "id": "echo"}, "thinkingLevel": "medium"}])
            out["steeringModeSet"] = session.steering_mode
            out["followUpModeSet"] = session.follow_up_mode
            out["autoCompactionSet"] = session.auto_compaction_enabled
            out["autoRetrySet"] = session.auto_retry_enabled
            out["sessionNameSet"] = session.session_name or ""
            scoped = session.scoped_models
            if len(scoped) == 1:
                out["scopedModelsSet"] = f"{scoped[0]['model']['id']}:{scoped[0].get('thinkingLevel', '')}"
            out["cycleOnlyModel"] = session.cycle_model("forward") is None
            session.set_active_tools_by_name([])
            out["activeToolsEmpty"] = session.active_tools
            session.set_active_tools_by_name(["sentinel_tool"])
            out["activeTools"] = session.get_active_tool_names()
            out["allTools"] = [tool["name"] for tool in session.get_all_tools()]
            definition = session.get_tool_definition("sentinel_tool")
            if definition is not None:
                out["toolDefinition"] = definition["description"]
            out["toolDefinitionMissing"] = session.get_tool_definition("no_such_tool") is None
            out["setModelRefused"] = refused(lambda: session.set_model({"provider": "nokey", "id": "locked"}))
            session.set_model({"provider": "sentinel", "id": "echo"})
            session.set_thinking_level("off")
            out["thinkingLevel"] = session.thinking_level
            out["availableThinkingLevels"] = session.get_available_thinking_levels()
            session.set_cache_warming_mode("off")

            # A run, observed through the members.
            session.prompt("hello")
            text = session.get_last_assistant_text()
            if text is not None:
                out["lastText"] = text
            stats = session.get_session_stats()
            out["stats"] = {"user": stats["userMessages"], "assistant": stats["assistantMessages"], "total": stats["totalMessages"], "idMatches": stats["sessionId"] == session_id}
            forks = session.get_user_messages_for_forking()
            if len(forks) == 1:
                out["fork"] = forks[0]["text"]

            # Awaited members: a user message that starts a run, the wait for it, a custom message, bash, compaction, tree navigation, exports, the bug report.
            session.send_user_message("via-send")
            session.wait_for_idle()
            text = session.get_last_assistant_text()
            if text is not None:
                out["sentText"] = text
            session.send_custom_message({"customType": "member-note", "content": "noted", "display": True})
            bash = session.execute_bash("echo member-bash")
            out["bash"] = {"output": bash["output"], "exit": bash.get("exitCode") == 0}
            session.record_bash_result("recorded-cmd", {"output": "recorded-out", "exitCode": 0, "cancelled": False, "truncated": False})
            out["bashRunning"] = session.is_bash_running
            out["roles"] = [message["role"] for message in session.messages()]
            out["compactRefused"] = refused(session.compact)
            if len(forks) == 1:
                nav = session.navigate_tree(forks[0]["entryId"])
                out["navigate"] = {"cancelled": nav["cancelled"], "editorText": nav.get("editorText", "")}
            jsonl = os.path.join(directory, "export.jsonl")
            out["exportedJsonl"] = session.export_to_jsonl(jsonl) == jsonl
            with open(jsonl, encoding="utf-8") as handle:
                out["jsonlHasSession"] = session_id in handle.read()
            out["bugReport"] = session.summarize_for_bug_report("member-hint").startswith("sentinel-model:echo:")
            cleared = session.clear_queue()
            out["cleared"] = [len(cleared["steering"]), len(cleared["followUp"])]
            out["callableTools"] = session.get_callable_tool_names()
            out["steeringMessages"] = len(session.get_steering_messages())
            out["followUpMessages"] = len(session.get_follow_up_messages())
            assert isinstance(session, pig_sdk.AgentSession)
            session.follow_up("queued-late")
            assert session.get_follow_up_messages() == ["queued-late"] and session.pending_message_count == 1
            assert session.clear_queue()["followUp"] == ["queued-late"]
            out["routedModel"] = session.routed_model is None
            out["pendingBash"] = session.has_pending_bash_messages
            out["supportsThinking"] = session.supports_thinking()
            out["cycledThinking"] = session.cycle_thinking_level() or ""
            status = session.cache_warming_status
            if status is not None:
                out["cacheWarming"] = status["state"]
            out["exportHtmlInMemory"] = refused(lambda: session.export_to_html(os.path.join(directory, "export.html")))
            session.reload()
            out["afterReload"] = session.session_id == session_id
            # An awaited member never holds a later call: execute_bash blocks in one thread and abort_bash, sent from this one, ends it.
            running = {}
            runner = threading.Thread(target=lambda: running.update(result=session.execute_bash("sleep 20")))
            runner.start()
            deadline = time.monotonic() + 10
            while not session.is_bash_running and time.monotonic() < deadline:
                time.sleep(0.01)
            session.abort_bash()
            runner.join(10)
            if runner.is_alive() or not (running.get("result") or {}).get("cancelled"):
                raise RuntimeError("execute_bash was not ended by abort_bash: the awaited member held the later call")
            session.abort_bash()
            session.abort_branch_summary()
            session.abort_compaction()
            session.abort_retry()
            return json.dumps(out)
        finally:
            session.dispose()

    ext.register_tool(pig_sdk.ToolDefinition(name="child_members", label="child_members", description="child_members", parameters={"type": "object"}, execute=child_members))
    return ext
`

// TestChildAgentSessionMembersPython runs the Python child_members scenario isolated and packed with a sibling; childMembersWant is the golden of the Go scenario.
func TestChildAgentSessionMembersPython(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
			root := findModuleRoot(t)
			cfg := packedFlagFactory(t, root, "python", "child-session", false)
			cfg.Isolation = isolation
			cfg.ContentHash = "python-child-members-" + fmt.Sprint(len(childMembersPython))
			if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(childMembersPython), 0o600); err != nil {
				t.Fatal(err)
			}
			configs := []subprocess.ExtConfig{cfg}
			if isolation == "shared-ok" {
				peer := packedFlagFactory(t, root, "python", "child-session-peer", false)
				peer.Isolation = isolation
				configs = append(configs, peer)
			}
			loaded, failures := host.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v", failures)
			}
			checkChildMembers(t, loaded[0])
		})
	}
}
