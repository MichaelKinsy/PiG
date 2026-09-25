package main

import (
	"os"
	"path/filepath"
	"testing"
)

// RPC calls setModel/setThinkingLevel without Persist, exactly as Pi's rpc-mode.ts does.
func TestRPCModelMutationsDoNotRewriteDefaults(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(agentDir, "settings.json")
	const original = `{"defaultProvider":"saved","defaultModel":"saved-model","defaultThinkingLevel":"high"}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + home}, "--model", "test-faux/echo", "--no-session", "--no-extensions")
	for _, command := range []string{
		`{"id":"set-model","type":"set_model","provider":"test-faux","modelId":"echo"}`,
		`{"id":"set-thinking","type":"set_thinking_level","level":"low"}`,
		`{"id":"cycle-model","type":"cycle_model"}`,
		`{"id":"cycle-thinking","type":"cycle_thinking_level"}`,
	} {
		p.send(command)
		p.await("mutation response", func(record rpcRecord) bool {
			if record["type"] != "response" {
				return false
			}
			if record["success"] != true {
				t.Fatalf("mutation failed: %v", record)
			}
			return true
		})
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Fatalf("RPC %s rewrote defaults: %s", command, got)
		}
	}
}
