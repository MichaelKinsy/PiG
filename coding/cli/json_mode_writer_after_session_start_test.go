package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// print-mode.ts:76-122: rebindSession awaits session.bindExtensions (every session_start handler) and only then subscribes the JSON writer, so the stream starts
// at the prompt: the entry_appended, session_info_changed and custom message_start/message_end events a session_start handler emits are not written, and
// the events of the command the prompt runs are. The writer-start fixture (every SDK) appends, names and sends in session_start and in /writer-after.
func TestJSONModeWritesNothingSessionStartEmittedBeforeTheWriterSubscribesInEverySDK(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "test", "parity", "testdata", "writer-start")
	binary := buildPigBinaryForSignalTest(t)
	for _, sdk := range []string{"node", "go", "python", "rust"} {
		t.Run(sdk, func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--mode", "json", "--model", "test-faux/faux-1", "--no-session", "-e", filepath.Join(fixtures, sdk), "-p", "/writer-after")
			cmd.Dir = t.TempDir()
			cmd.Env = append(cmd.Environ(), "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_SDK_GO_ROOT="+filepath.Join(root, "extensions", "sdk"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
			}
			var log []rpcRecord
			for line := range bytes.SplitSeq(stdout.Bytes(), []byte("\n")) {
				var record rpcRecord
				if json.Unmarshal(line, &record) != nil {
					continue
				}
				if record["type"] == "entry_appended" || record["type"] == "session_info_changed" || record["type"] == "message_start" || record["type"] == "message_end" {
					log = append(log, record)
				}
				if bytes.Contains(line, []byte("writer-start")) || bytes.Contains(line, []byte("start-name")) || bytes.Contains(line, []byte("start-message")) {
					t.Errorf("the stream carries what session_start emitted, before the writer subscribed: %s", line)
				}
			}
			if n := countEntryAppended(log, "writer-after"); n != 1 {
				t.Errorf("%d entry_appended for the command's appendEntry, want 1\nstdout: %s", n, stdout.String())
			}
			if n := countSessionInfoChanged(log, "after-name"); n != 1 {
				t.Errorf("%d session_info_changed for the command's setSessionName, want 1\nstdout: %s", n, stdout.String())
			}
			for _, phase := range []string{"message_start", "message_end"} {
				found := 0
				for _, record := range log {
					if message, _ := record["message"].(map[string]any); record["type"] == phase && message["customType"] == "writer-after" {
						found++
					}
				}
				if found != 1 {
					t.Errorf("%d %s for the command's custom message, want 1\nstdout: %s", found, phase, stdout.String())
				}
			}
		})
	}
}
