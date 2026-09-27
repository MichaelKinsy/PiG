package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRPCToolDetailsPersistAndReplay(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "parity-read-target.txt"), []byte("READ-PROBE\nsecond line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}
	args := []string{"--offline", "--no-extensions", "--no-context-files", "--no-skills", "--system-prompt", "Test", "--model", "test-faux/faux-1"}
	f := &upstreamRPC{p: startRPCProcessAt(t, cwd, env, args...), agentDir: filepath.Join(home, "agent")}
	f.prompt("Run: read parity-read-target.txt")
	f.prompt("Run: write parity-write-output.txt")
	check := func(messages []any) {
		t.Helper()
		n := 0
		for _, value := range messages {
			m := value.(map[string]any)
			if m["role"] != "toolResult" {
				continue
			}
			n++
			if _, ok := m["details"]; ok {
				t.Fatalf("persisted render details: %#v", m)
			}
			if m["isError"] != false {
				t.Fatalf("lost toolResult isError: %#v", m)
			}
		}
		if n != 2 {
			t.Fatalf("toolResult messages=%d, want read and write", n)
		}
	}
	check(f.command("get_messages", nil)["messages"].([]any))
	var disk []any
	for _, e := range f.diskEntries() {
		if e["type"] == "message" {
			disk = append(disk, e["message"])
		}
	}
	check(disk)
	path := f.command("get_state", nil)["sessionFile"].(string)
	f.finish()
	resumed := &upstreamRPC{p: startRPCProcessAt(t, cwd, env, append(args, "--session", path)...)}
	check(resumed.command("get_messages", nil)["messages"].([]any))
	resumed.finish()
}

func BenchmarkRPCJSONLToolResult(b *testing.B) {
	payload := rpcToolResultPayload("read output\nsecond line\n", nil, nil)
	b.ReportAllocs()
	for b.Loop() {
		writeJSONLine(io.Discard, payload)
	}
}
