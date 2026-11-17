package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// print-mode.ts:44-52 disposeRuntime calls unsubscribe() and unsubscribeBackpressure() BEFORE `await runtimeHost.dispose()`, which emits session_shutdown
// (agent-session-runtime.ts:415-422). The JSON writer is gone when a session_shutdown handler appends an entry (agent-session.ts:3563-3566 emits
// entry_appended), names the session or sends a custom message, so the stream ends with agent_settled and nothing a shutdown handler does is written,
// while the session file keeps all of it. pig closed the runtime first and printed entry_appended, session_info_changed and the custom message's
// message_start / message_end after agent_settled.
func TestJSONModeShutdownHandlerOutputIsNotWrittenInEverySDK(t *testing.T) {
	t.Parallel()
	root, binary := shutdownWriterRoot(t)
	for _, sdk := range []string{"node", "go"} {
		for _, noSession := range []bool{false, true} {
			name := sdk
			if noSession {
				name += "/no-session"
			}
			t.Run(name, func(t *testing.T) {
				requireSDKFixtureToolchain(t, sdk)
				sessions := t.TempDir()
				args := []string{"--mode", "json", "--model", "test-faux/faux-1", "-e", filepath.Join(root, "test", "parity", "testdata", "shutdown-writer", sdk), "What is 20+22?"}
				if noSession {
					args = append([]string{"--no-session"}, args...)
				} else {
					args = append([]string{"--session-dir", sessions}, args...)
				}
				ctx, cancel := context.WithTimeout(t.Context(), probeWait(t, 10*time.Minute))
				defer cancel()
				cmd := shutdownWriterCommand(ctx, t, binary, root, args...)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("pig: %v (context: %v)\nstderr: %s", err, ctx.Err(), stderr.String())
				}
				assertJSONStreamEndsAtAgentSettled(t, stdout.Bytes())
				if noSession {
					return
				}
				// The handler's work is persisted exactly as Pi persists it: the entry, the name, the custom message and (Node) the label.
				entries := readShutdownSessionEntries(t, sessions)
				want := map[string]bool{"custom:shutdown-entry": false, "session_info:shutdown-name": false, "custom_message:shutdown-message": false}
				if sdk == "node" {
					want["label:shutdown-label"] = false
				}
				for _, entry := range entries {
					if _, ok := want[entry]; ok {
						want[entry] = true
					}
				}
				for entry, found := range want {
					if !found {
						t.Errorf("session file lacks %s; entries: %v", entry, entries)
					}
				}
			})
		}
	}
}

// A slow stdout reader keeps lines queued behind the pipe when the run ends. Detaching the writer at shutdown must not drop them: the last line Pi
// writes, agent_settled, arrives complete and newline-terminated after a large entry_appended an agent_end handler wrote, and nothing the
// session_shutdown handler did follows it.
func TestJSONModeShutdownDetachKeepsQueuedLinesForASlowReader(t *testing.T) {
	t.Parallel()
	root, binary := shutdownWriterRoot(t)
	sessions := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), probeWait(t, 10*time.Minute))
	defer cancel()
	cmd := shutdownWriterCommand(ctx, t, binary, root, "--mode", "json", "--model", "test-faux/faux-1", "--session-dir", sessions,
		"-e", filepath.Join(root, "test", "parity", "testdata", "shutdown-writer", "node-big"), "What is 20+22?")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The reader starts after pig has filled the pipe and finished its run: the large line and agent_settled wait in the queue.
	time.Sleep(testbudget.Wait(t) / 20)
	stdout, err := io.ReadAll(pipe)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
	}
	assertJSONStreamEndsAtAgentSettled(t, stdout)
	big := 0
	for line := range bytes.SplitSeq(stdout, []byte("\n")) {
		var record struct {
			Type  string `json:"type"`
			Entry struct {
				Data struct {
					Text string `json:"text"`
				} `json:"data"`
			} `json:"entry"`
		}
		if json.Unmarshal(line, &record) == nil && record.Type == "entry_appended" && len(record.Entry.Data.Text) == 300000 {
			big++
		}
	}
	if big != 1 {
		t.Fatalf("%d complete entry_appended lines for the agent_end entry, want 1", big)
	}
}

// Pi writes nothing for a session_shutdown handler that throws in JSON mode but the extension error on stderr (extension runner emitError): the
// cure detaches the stdout writer only, so the error still reaches stderr and stdout still ends at agent_settled.
func TestJSONModeShutdownHandlerErrorStillReportsOnStderr(t *testing.T) {
	t.Parallel()
	root, binary := shutdownWriterRoot(t)
	ctx, cancel := context.WithTimeout(t.Context(), probeWait(t, 10*time.Minute))
	defer cancel()
	cmd := shutdownWriterCommand(ctx, t, binary, root, "--mode", "json", "--model", "test-faux/faux-1", "--no-session",
		"-e", filepath.Join(root, "test", "parity", "testdata", "shutdown-writer", "node-throw"), "What is 20+22?")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
	}
	assertJSONStreamEndsAtAgentSettled(t, stdout.Bytes())
	if !strings.Contains(stderr.String(), "shutdown-boom") {
		t.Errorf("stderr = %q, want the extension error shutdown-boom", stderr.String())
	}
}

func shutdownWriterRoot(t *testing.T) (root, binary string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root, buildPigBinaryForSignalTest(t)
}

func shutdownWriterCommand(ctx context.Context, t *testing.T, binary, root string, args ...string) *exec.Cmd {
	t.Helper()
	home := t.TempDir()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(cmd.Environ(), "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_SDK_GO_ROOT="+filepath.Join(root, "extensions", "sdk"))
	return cmd
}

// assertJSONStreamEndsAtAgentSettled fails unless stdout's last line is a complete agent_settled record ended by a newline, and no line carries what a
// session_shutdown handler emits.
func assertJSONStreamEndsAtAgentSettled(t *testing.T, stdout []byte) {
	t.Helper()
	if !bytes.HasSuffix(stdout, []byte("}\n")) {
		t.Fatalf("stdout does not end with a newline-terminated record; tail: %q", lastBytes(stdout, 200))
	}
	lines := bytes.Split(bytes.TrimSuffix(stdout, []byte("\n")), []byte("\n"))
	var last struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil || last.Type != "agent_settled" {
		t.Errorf("last stdout line = %q, want agent_settled (Pi writes nothing after it)", lastBytes(lines[len(lines)-1], 300))
	}
	for _, line := range lines {
		if bytes.Contains(line, []byte("shutdown-entry")) || bytes.Contains(line, []byte("shutdown-name")) || bytes.Contains(line, []byte("shutdown-text")) || bytes.Contains(line, []byte("shutdown-label")) {
			t.Errorf("stdout carries what session_shutdown emitted: %q", lastBytes(line, 300))
		}
	}
}

func lastBytes(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}

// readShutdownSessionEntries returns "type[:customType|name|label]" for every record of the one session file under dir.
func readShutdownSessionEntries(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files under %s = %v (%v), want one", dir, files, err)
	}
	file, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var entries []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 4<<20)
	for scanner.Scan() {
		var record struct {
			Type       string  `json:"type"`
			CustomType string  `json:"customType"`
			Name       string  `json:"name"`
			Label      *string `json:"label"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("session record %q: %v", scanner.Text(), err)
		}
		entry := record.Type
		switch {
		case record.CustomType != "":
			entry += ":" + record.CustomType
		case record.Name != "":
			entry += ":" + record.Name
		case record.Label != nil:
			entry += ":" + *record.Label
		}
		entries = append(entries, entry)
	}
	return entries
}
