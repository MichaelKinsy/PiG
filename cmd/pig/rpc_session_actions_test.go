package main

import (
	"path/filepath"
	"testing"
)

// startSessionActionsRPC starts RPC mode with the session-actions.mjs
// extension and waits until its commands are listed.
func startSessionActionsRPC(t *testing.T) *rpcProcess {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-actions.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
	}, "--model", "test-faux/faux-1", "--no-session", "-e", fixture)
	p.send(`{"id":"commands","type":"get_commands"}`)
	p.await("session-actions command listing", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "commands" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		for _, value := range commands {
			if command, _ := value.(map[string]any); command["name"] == "start-run" {
				return true
			}
		}
		t.Fatalf("session-actions extension did not load; get_commands = %v\n%s", record, p.stderr.String())
		return false
	})
	return p
}

func assistantMessageEndText(record rpcRecord) (string, bool) {
	message, _ := record["message"].(map[string]any)
	if record["type"] != "message_end" || message["role"] != "assistant" {
		return "", false
	}
	text := ""
	content, _ := message["content"].([]any)
	for _, block := range content {
		if block, ok := block.(map[string]any); ok && block["type"] == "text" {
			text += block["text"].(string)
		}
	}
	return text, true
}

// TestRPCExtensionFollowUpFromAgentEndContinuesRun pins upstream rpc-mode,
// which binds the session to extensions: sendUserMessage with deliverAs
// followUp from an agent_end handler continues the same run. Pig bound no
// session actions in RPC mode, so the call failed with not_ready.
func TestRPCExtensionFollowUpFromAgentEndContinuesRun(t *testing.T) {
	p := startSessionActionsRPC(t)
	p.send(`{"id":"arm","type":"prompt","message":"/arm-follow-up"}`)
	p.await("arm response", func(record rpcRecord) bool { return isSuccessResponse(record, "arm") })

	p.send(`{"id":"first","type":"prompt","message":"reply with exactly: first"}`)
	var answers []string
	p.await("the follow-up answer before the run settles", func(record rpcRecord) bool {
		if text, ok := assistantMessageEndText(record); ok {
			answers = append(answers, text)
		}
		if record["type"] != "agent_settled" {
			return false
		}
		if len(answers) != 2 || answers[0] != "first" || answers[1] != "follow-up-ok" {
			t.Fatalf("answers before agent_settled = %q, want [first follow-up-ok]\n%s", answers, p.stderr.String())
		}
		return true
	})
	p.closeAndWait("after the follow-up run")
}

// TestRPCAbortStopsExtensionStartedRun pins upstream rpc-mode abort,
// `await session.abort()`, which stops whatever run is active, and
// get_state's isStreaming, which is session.isStreaming. Pig's abort only
// cancelled the context of an RPC prompt and tracked streaming itself, so a
// run an extension started could not be stopped.
func TestRPCAbortStopsExtensionStartedRun(t *testing.T) {
	p := startSessionActionsRPC(t)
	p.send(`{"id":"start","type":"prompt","message":"/start-run"}`)
	p.await("the extension-started run reaching its tool", func(record rpcRecord) bool {
		return record["type"] == "tool_execution_start"
	})

	p.send(`{"id":"state","type":"get_state"}`)
	p.await("state while the extension run streams", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		if data["isStreaming"] != true {
			t.Fatalf("get_state isStreaming = %v during the extension-started run", data["isStreaming"])
		}
		return true
	})

	p.send(`{"id":"abort","type":"abort"}`)
	settled := false
	p.await("abort response after the run settles", func(record rpcRecord) bool {
		if record["type"] == "agent_settled" {
			settled = true
		}
		if !isSuccessResponse(record, "abort") {
			return false
		}
		if !settled {
			t.Fatal("abort answered before the extension-started run settled")
		}
		return true
	})
	p.closeAndWait("after aborting the extension-started run")
}
