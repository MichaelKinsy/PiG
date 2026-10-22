package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// agent-session.ts:3433-3436: pi.appendEntry emits entry_appended, and JSON mode prints every session event (print-mode.ts subscribes with
// JSON.stringify). The extension's append in every SDK, run by `--mode json -p "/robust-note first"` over the real binary, must print the
// entry_appended line with the entry's custom type and data (RPC mode already does: TestRPCAppendEntryIsVisibleToTheSameHandlerInEverySDK).
func TestJSONModePrintsEntryAppendedForEveryExtensionSDK(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "test", "parity", "testdata", "robust-notes")
	binary := buildPigBinaryForSignalTest(t)
	for _, sdk := range []string{"node", "go", "python", "rust"} {
		t.Run(sdk, func(t *testing.T) {
			home := t.TempDir()
			// The one-shot run loads the Go and Rust fixtures cold (Rust compiles its crate dependencies on first use, 130 s on the
			// Windows runner), so the whole run is bounded by the cold-build budget the RPC sibling gives its session_start wait.
			ctx, cancel := context.WithTimeout(t.Context(), probeWait(t, 10*time.Minute))
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--mode", "json", "--model", "test-faux/faux-1", "--no-session", "-e", filepath.Join(fixtures, sdk), "-p", "/robust-note first")
			cmd.Dir = t.TempDir()
			cmd.Env = append(cmd.Environ(), "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_SDK_GO_ROOT="+filepath.Join(root, "extensions", "sdk"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("pig: %v (context: %v)\nstderr: %s", err, ctx.Err(), stderr.String())
			}
			// Pi emits one entry_appended per pi.appendEntry (agent-session.ts appendEntry), so the line appears exactly once.
			found := 0
			for line := range bytes.SplitSeq(stdout.Bytes(), []byte("\n")) {
				var record struct {
					Type  string `json:"type"`
					Entry struct {
						CustomType string         `json:"customType"`
						Data       map[string]any `json:"data"`
					} `json:"entry"`
				}
				if json.Unmarshal(line, &record) == nil && record.Type == "entry_appended" && record.Entry.CustomType == "robust-note" && record.Entry.Data["text"] == "first" {
					found++
				}
			}
			if found != 1 {
				t.Fatalf("%d entry_appended lines for the extension's one appendEntry, want 1\nstdout: %s\nstderr: %s", found, stdout.String(), stderr.String())
			}
		})
	}
}
