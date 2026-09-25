package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestRPCModeSessionReplacementAndExport(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions")
	manager := codingagent.NewSessionManagerWithDir(home, sessionDir)
	session, err := manager.Create("resume-rpc", "")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{
		Role:      agent.RoleUser,
		Content:   []ai.UserContentBlock{ai.TextContent{Text: "resume marker"}},
		Timestamp: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "restored"}},
		Usage:      &ai.Usage{Input: 10, Output: 2},
		StopReason: "stop",
		Timestamp:  2,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.Path()); err != nil {
		t.Fatalf("seed session was not persisted: %v", err)
	}

	exportPath := filepath.Join(home, "session.html")
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte(`{"compaction":{"enabled":true,"reserveTokens":100,"keepRecentTokens":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := []map[string]any{
		{"id": "messages", "type": "get_messages"},
		{"id": "stats", "type": "get_session_stats"},
		{"id": "models", "type": "get_available_models"},
		{"id": "set-model", "type": "set_model", "provider": "test-faux", "modelId": "echo"},
		{"id": "cycle-model", "type": "cycle_model"},
		{"id": "levels", "type": "get_available_thinking_levels"},
		{"id": "abort-retry", "type": "abort_retry"},
		{"id": "clone", "type": "clone"},
		{"id": "switch", "type": "switch_session", "sessionPath": session.Path()},
		{"id": "export", "type": "export_html", "outputPath": exportPath},
		{"id": "compact", "type": "compact", "customInstructions": "Keep the resume marker"},
		{"id": "fork", "type": "fork", "entryId": userID},
		{"id": "state", "type": "get_state"},
		{"id": "new", "type": "new_session", "parentSession": session.Path()},
		{"id": "new-state", "type": "get_state"},
	}
	var input strings.Builder
	for _, command := range commands {
		data, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(data)
		input.WriteByte('\n')
	}

	binary := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(binary,
		"--mode", "rpc",
		"--model", "test-faux/echo",
		"--session", session.Path(),
		"--session-dir", sessionDir,
	)
	cmd.Env = append(os.Environ(), "PIG_TEST_FAUX=1", "PIG_HOME="+home)
	cmd.Stdin = strings.NewReader(input.String())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC process: %v\n%s", err, stderr.String())
	}

	responses := make(map[string]map[string]any)
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	for scanner.Scan() {
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("decode RPC output: %v\n%s", err, stdout.String())
		}
		if id, _ := response["id"].(string); id != "" {
			responses[id] = response
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"messages", "stats", "models", "set-model", "cycle-model", "levels", "abort-retry", "clone", "switch", "export", "compact", "fork", "state", "new", "new-state"} {
		if response := responses[id]; response == nil || response["success"] != true {
			t.Fatalf("response %s = %#v\n%s", id, response, stdout.String())
		}
	}
	messages := responses["messages"]["data"].(map[string]any)["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "user" || messages[1].(map[string]any)["role"] != "assistant" {
		t.Fatalf("resume should preserve exactly the two stored conversation messages: %#v", messages)
	}
	stats := responses["stats"]["data"].(map[string]any)
	if stats["totalMessages"] != float64(2) || stats["tokens"].(map[string]any)["total"] != float64(12) || stats["contextUsage"] == nil {
		t.Fatalf("Session stats = %#v", stats)
	}
	models := responses["models"]["data"].(map[string]any)["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["api"] != "test-faux" {
		t.Fatalf("available models = %#v", models)
	}
	if data, exists := responses["cycle-model"]["data"]; !exists || data != nil {
		t.Fatalf("single-model cycle data = %#v, exists=%v", data, exists)
	}
	levels := responses["levels"]["data"].(map[string]any)["levels"].([]any)
	if len(levels) != 1 || levels[0] != "off" {
		t.Fatalf("available thinking levels = %#v", levels)
	}
	compactData := responses["compact"]["data"].(map[string]any)
	if compactData["summary"] == "" || compactData["firstKeptEntryId"] == "" || compactData["estimatedTokensAfter"] == nil {
		t.Fatalf("compact data = %#v", compactData)
	}
	forkData := responses["fork"]["data"].(map[string]any)
	if forkData["text"] != "resume marker" || forkData["cancelled"] != false {
		t.Fatalf("fork data = %#v", forkData)
	}
	stateData := responses["state"]["data"].(map[string]any)
	if stateData["messageCount"] != float64(0) {
		t.Fatalf("fork before first user message should be empty, got %v", stateData["messageCount"])
	}
	newState := responses["new-state"]["data"].(map[string]any)
	if newState["messageCount"] != float64(0) || newState["sessionId"] == stateData["sessionId"] {
		t.Fatalf("new Session state = %#v after fork state %#v", newState, stateData)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatalf("RPC export was not written: %v", err)
	}
}
