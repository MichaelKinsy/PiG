package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's plan-mode example persists [] distinctly from an absent snapshot. Drive the actual Go factory, RPC bridge, disk Session, process restart, and next model turn.
func TestRPCPlanModeEmptyToolsSurviveProcessResume(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	example := filepath.Join(root, "examples", "extensions", "plan-mode")
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"defaultTools":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agentDir,
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic",
		"PIG_SDK_GO_ROOT=" + filepath.Join(root, "extensions", "sdk"),
	}
	cwd := t.TempDir()
	p := startRPCProcessAt(t, cwd, env, "--model", "test-faux/faux-1", "--session-dir", filepath.Join(home, "sessions"), "-e", example)
	awaitPlanModeCommand(t, p)
	// An assistant entry commits the Session to disk before plan-mode appends its state.
	p.send(`{"id":"seed","type":"prompt","message":"reply with exactly: seed"}`)
	p.await("seed settles", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	p.send(`{"id":"enable","type":"prompt","message":"/plan"}`)
	var saved map[string]any
	p.await("plan enabled and persisted", func(record rpcRecord) bool {
		if record["type"] == "entry_appended" {
			entry, _ := record["entry"].(map[string]any)
			if entry["customType"] == "plan-mode" {
				saved, _ = entry["data"].(map[string]any)
			}
		}
		return isSuccessResponse(record, "enable")
	})
	if tools, ok := saved["toolsBeforePlanMode"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("Session received snapshot %v, want explicit empty tools", saved)
	}
	p.send(`{"id":"path","type":"get_state"}`)
	var sessionPath string
	p.await("persisted session path", func(record rpcRecord) bool {
		if !isSuccessResponse(record, "path") {
			return false
		}
		data, _ := record["data"].(map[string]any)
		sessionPath, _ = data["sessionFile"].(string)
		return true
	})
	p.closeAndWait("after persisting plan mode")
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("Session was not persisted: %v", err)
	}
	// The new process has ordinary defaults. They must not replace the saved empty set.
	if err := os.WriteFile(settings, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resumed := startRPCProcessAt(t, cwd, env, "--model", "test-faux/faux-1", "--session", sessionPath, "-e", example)
	awaitPlanModeCommand(t, resumed)
	resumed.send(`{"id":"disable","type":"prompt","message":"/plan"}`)
	resumed.await("plan disabled", func(record rpcRecord) bool { return isSuccessResponse(record, "disable") })
	resumed.send(`{"id":"check","type":"prompt","message":"reply with exactly: restored"}`)
	resumed.await("post-resume turn settles", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	resumed.send(`{"id":"messages","type":"get_messages"}`)
	resumed.await("post-resume tool declaration", func(record rpcRecord) bool {
		if !isSuccessResponse(record, "messages") {
			return false
		}
		data, _ := record["data"].(map[string]any)
		messages, _ := data["messages"].([]any)
		var lastSystem map[string]any
		for _, value := range messages {
			message, _ := value.(map[string]any)
			if message["role"] == "system" {
				lastSystem = message
			}
		}
		if lastSystem == nil {
			t.Fatal("no system tool declaration in the real Session transcript")
		}
		if tools, _ := lastSystem["toolsAdded"].([]any); len(tools) != 0 {
			t.Fatalf("post-resume turn restored default tools: %v", tools)
		}
		return true
	})
	resumed.closeAndWait("after post-resume tool check")
}

// index.ts:session_start scans only messages after the last execute marker and joins each assistant's text blocks with newlines.
func TestRPCPlanModeResumeRebuildsOnlyWholeDoneMarkers(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home, cwd := t.TempDir(), t.TempDir()
	manager := codingagent.NewSessionManagerWithDir(cwd, filepath.Join(home, "sessions"))
	session, err := manager.Create("plan-resume", "")
	if err != nil {
		t.Fatal(err)
	}
	appendAssistant := func(texts ...string) {
		t.Helper()
		content := make([]ai.AssistantContentBlock, 0, len(texts))
		for _, text := range texts {
			content = append(content, ai.TextContent{Text: text})
		}
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: agent.RoleAssistant, Content: content, StopReason: "stop", Usage: &ai.Usage{},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	appendAssistant("[DONE:1]")
	if _, err := session.AppendCustomMessage("plan-mode-execute", "Execute the plan.", true, nil); err != nil {
		t.Fatal(err)
	}
	appendAssistant("[DONE:", "1]", "[DONE:2]")
	id, err := codingagent.GenerateEntryID()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendEntry(codingagent.CustomEntry{
		SessionEntryBase: codingagent.SessionEntryBase{Type: "custom", ID: id, ParentID: session.LeafID(), Timestamp: codingagent.RFC3339NowNano()},
		CustomType:       "plan-mode",
		Data: map[string]any{"enabled": false, "executing": true, "todos": []map[string]any{
			{"step": 1, "text": "Inspect the implementation", "completed": false},
			{"step": 2, "text": "Verify the result", "completed": false},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, cwd, []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1",
		"PIG_SDK_GO_ROOT=" + filepath.Join(root, "extensions", "sdk"),
	}, "--model", "test-faux/faux-1", "--session", session.Path(), "-e", filepath.Join(root, "examples", "extensions", "plan-mode"))
	awaitPlanModeCommand(t, p)
	p.send(`{"id":"todos","type":"prompt","message":"/todos"}`)
	var notification string
	p.await("resumed todo state", func(record rpcRecord) bool {
		if record["type"] == "extension_ui_request" && record["method"] == "notify" {
			notification, _ = record["message"].(string)
		}
		return isSuccessResponse(record, "todos")
	})
	want := "Plan Progress:\n1. ○ Inspect the implementation\n2. ✓ Verify the result"
	if notification != want {
		t.Fatalf("resumed todo UI = %q, want %q", notification, want)
	}
	p.closeAndWait("after checking resumed todo state")
}

func awaitPlanModeCommand(t *testing.T, p *rpcProcess) {
	t.Helper()
	p.send(`{"id":"plan-commands","type":"get_commands"}`)
	p.await("Go plan-mode factory loads", func(record rpcRecord) bool {
		if !isSuccessResponse(record, "plan-commands") {
			return false
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		for _, raw := range commands {
			command, _ := raw.(map[string]any)
			if command["name"] == "plan" {
				return true
			}
		}
		t.Fatalf("plan command not loaded: %v\n%s", record, p.stderr.String())
		return false
	})
}
