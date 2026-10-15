package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agent-session.ts abort ends with waitForIdle, which returns at once when no agent run is active at that point (isIdle). A prompt read just before it is still in its preflight, so Pi 1.1.0 answers the abort first and lets the prompt run: for `prompt` then `abort` in one input batch it writes the abort response, then the prompt response (stdin open or closed). PiG joined every admitted prompt before answering, so the abort answered after the run, or not at all when stdin ended.
func TestRPCAbortWhileIdleAnswersBeforeALaterRun(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	input := `{"type":"prompt","id":"p","message":"hello"}` + "\n" + `{"type":"abort","id":"a"}` + "\n"
	stdinPath := filepath.Join(t.TempDir(), "commands.jsonl")
	if err := os.WriteFile(stdinPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin, err := os.Open(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	t.Setenv("PIG_TEST_FAUX", "1")
	stdout, stderr, code := runPigForModeTest(t, bin, stdin, nil, "--mode", "rpc", "--no-session", "--no-extensions", "--offline", "--model", "test-faux/echo")
	if code != 0 {
		t.Fatalf("pig --mode rpc exited %d\nstderr: %s", code, stderr)
	}
	var order []string
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		var record struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Type == "response" {
			order = append(order, record.ID)
		}
	}
	if len(order) < 2 || order[0] != "a" || order[1] != "p" {
		t.Fatalf("response order = %q, want the abort answered before the prompt [a p]\nstdout:\n%s", order, stdout)
	}
}
