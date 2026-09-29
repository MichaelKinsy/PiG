package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi main.ts takes over stdout before resource loading; rpc-mode.ts:55-61 writes protocol records through writeRawStdout, not the redirected process.stdout.write.
func TestRPCProtocolUsesRawStdoutAfterStartupTakeover(t *testing.T) {
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), buildPigBinaryForSignalTest(t),
		"--mode", "rpc", "--no-session", "--no-extensions", "--no-skills", "--offline",
		"--model", "test-faux/faux-1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home,
		"PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1")
	cmd.Stdin = strings.NewReader("{\"id\":\"state\",\"type\":\"get_state\"}\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC command: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	var response struct {
		ID      string         `json:"id"`
		Type    string         `json:"type"`
		Command string         `json:"command"`
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("RPC response must be one stdout JSON record: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	if response.ID != "state" || response.Type != "response" || response.Command != "get_state" || !response.Success || response.Data == nil {
		t.Fatalf("RPC response = %+v", response)
	}
	if strings.Contains(stderr.String(), `"id":"state"`) {
		t.Fatalf("RPC response leaked to stderr: %s", &stderr)
	}
}
