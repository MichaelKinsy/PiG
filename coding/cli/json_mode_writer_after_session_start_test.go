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

// runWriterStartJSON runs `pig --mode json -p <command>` with the writer-start fixture of sdk and returns the records of the stdout stream.
func runWriterStartJSON(t *testing.T, binary, root, sdk, command string) (records []rpcRecord, stdout string) {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--mode", "json", "--model", "test-faux/faux-1", "--no-session", "-e", filepath.Join(root, "test", "parity", "testdata", "writer-start", sdk), "-p", command)
	cmd.Dir = t.TempDir()
	cmd.Env = append(cmd.Environ(), "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_SDK_GO_ROOT="+filepath.Join(root, "extensions", "sdk"))
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
	}
	for line := range bytes.SplitSeq(out.Bytes(), []byte("\n")) {
		var record rpcRecord
		if json.Unmarshal(line, &record) == nil {
			records = append(records, record)
		}
	}
	return records, out.String()
}

// countCustomMessage counts the message_start or message_end records (phase) of a custom message with customType.
func countCustomMessage(log []rpcRecord, phase, customType string) int {
	found := 0
	for _, record := range log {
		if message, _ := record["message"].(map[string]any); record["type"] == phase && message["customType"] == customType {
			found++
		}
	}
	return found
}

// print-mode.ts:84-98 (Pi 6aab551a79ba): rebindSession subscribes the JSON writer before `await session.bindExtensions(...)`, so the entry_appended, session_info_changed and
// custom message_start/message_end events a session_start handler emits are written, after the session header (written before the first bind) and before the events of the
// command the prompt runs. The writer-start fixture (every SDK) appends, names and sends in session_start and in /writer-after.
func TestJSONModeWritesWhatSessionStartEmitsInEverySDK(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, sdk := range []string{"node", "go", "python", "rust", "lisp"} {
		t.Run(sdk, func(t *testing.T) {
			requireSDKFixtureToolchain(t, sdk)
			log, stdout := runWriterStartJSON(t, binary, root, sdk, "/writer-after")
			if len(log) == 0 || log[0]["type"] != "session" {
				t.Fatalf("the stream does not start with the session header\nstdout: %s", stdout)
			}
			for _, want := range []struct {
				what string
				got  int
			}{
				{"entry_appended for the session_start appendEntry", countEntryAppended(log, "writer-start")},
				{"entry_appended for the command's appendEntry", countEntryAppended(log, "writer-after")},
				{"session_info_changed for the session_start setSessionName", countSessionInfoChanged(log, "start-name")},
				{"session_info_changed for the command's setSessionName", countSessionInfoChanged(log, "after-name")},
				{"message_start of the session_start custom message", countCustomMessage(log, "message_start", "writer-start")},
				{"message_end of the session_start custom message", countCustomMessage(log, "message_end", "writer-start")},
				{"message_start of the command's custom message", countCustomMessage(log, "message_start", "writer-after")},
				{"message_end of the command's custom message", countCustomMessage(log, "message_end", "writer-after")},
			} {
				if want.got != 1 {
					t.Errorf("%d %s, want 1\nstdout: %s", want.got, want.what, stdout)
				}
			}
			// The startup events come before the command's.
			startup, after := -1, -1
			for i, record := range log {
				if record["type"] != "entry_appended" {
					continue
				}
				if entry, _ := record["entry"].(map[string]any); entry["customType"] == "writer-start" && startup < 0 {
					startup = i
				} else if entry["customType"] == "writer-after" && after < 0 {
					after = i
				}
			}
			if startup < 1 || after < startup {
				t.Errorf("the session_start entry_appended is at %d and the command's at %d, want header < startup < command\nstdout: %s", startup, after, stdout)
			}
		})
	}
}

// Pi 6aab551a79ba, test/suite/print-mode-session-start-events.test.ts: a command that replaces the session (ctx.newSession) binds the new session's extensions, and the JSON writer,
// subscribed to the new session before it binds, writes the new session's session_start events: the session_start appendEntry of the startup and of the replacement are both in the
// output, as are both names. Every SDK has ctx.newSession (docs/extension-api-parity.md, the session-replacement rows: Lisp serves ctx-new-session).
func TestJSONModeWritesSessionStartEventsOfAReplacementInEverySDK(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, sdk := range []string{"node", "go", "python", "rust", "lisp"} {
		t.Run(sdk, func(t *testing.T) {
			requireSDKFixtureToolchain(t, sdk)
			log, stdout := runWriterStartJSON(t, binary, root, sdk, "/writer-renew")
			if len(log) == 0 || log[0]["type"] != "session" {
				t.Fatalf("the stream does not start with the session header\nstdout: %s", stdout)
			}
			if n := countEntryAppended(log, "writer-start"); n != 2 {
				t.Errorf("%d entry_appended for the session_start appendEntry (startup and replacement), want 2\nstdout: %s", n, stdout)
			}
			if n := countSessionInfoChanged(log, "start-name"); n != 2 {
				t.Errorf("%d session_info_changed for the session_start setSessionName (startup and replacement), want 2\nstdout: %s", n, stdout)
			}
		})
	}
}
