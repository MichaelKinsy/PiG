//go:build integration

// json_stream_test.go: integration coverage for `--mode json`, driven through
// the real pig binary and the deterministic faux provider.
//
// These assert the properties a unit test cannot: that events reach a consumer
// *while the child is alive*, that cancellation terminates promptly, and that
// --session-id resumes prior context. The wire shapes themselves are pinned by
// cmd/pig/json_mode_test.go and by parity scenario json/01-json-mode-streams-events.

package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// jsonModeCmd builds a `--mode json` invocation against the faux provider.
// pigHome is passed in rather than allocated per call: sessions live under
// PIG_HOME, so a resume test must run both turns against the same home.
func jsonModeCmd(t *testing.T, pigHome string, extra ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{
		"--no-extensions", "--mode", "json",
		"--model", "test-faux/echo",
	}, extra...)
	cmd := exec.Command(buildBinary(t), args...)
	cmd.Dir = pigHome
	cmd.Env = append(os.Environ(),
		"PIG_HOME="+pigHome,
		"PIG_CODING_AGENT_DIR="+filepath.Join(pigHome, "agent"),
		"PIG_CODING_AGENT_SESSION_DIR=",
		"PIG_OFFLINE=1",
		"PIG_QUIET_STARTUP=1",
		"PIG_TEST_FAUX=1",
		"PIG_TEST_FAUX_SCENARIO="+deterministicScenario,
	)
	return cmd
}

func TestJSONModeCmdIsolatesFauxRuntime(t *testing.T) {
	for _, name := range []string{"PIG_HOME", "PIG_CODING_AGENT_DIR", "PIG_CODING_AGENT_SESSION_DIR", "PIG_OFFLINE", "PIG_TEST_FAUX", "PIG_TEST_FAUX_SCENARIO"} {
		t.Setenv(name, "inherited-value-must-not-reach-child")
	}
	home := t.TempDir()
	cmd := jsonModeCmd(t, home)
	if cmd.Dir != home {
		t.Fatalf("working directory = %q, want private home %q", cmd.Dir, home)
	}
	env := map[string]string{}
	for _, entry := range cmd.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	for key, want := range map[string]string{
		"PIG_HOME":                     home,
		"PIG_CODING_AGENT_DIR":         filepath.Join(home, "agent"),
		"PIG_CODING_AGENT_SESSION_DIR": "",
		"PIG_OFFLINE":                  "1",
		"PIG_TEST_FAUX":                "1",
		"PIG_TEST_FAUX_SCENARIO":       deterministicScenario,
	} {
		if env[key] != want {
			t.Errorf("%s = %q, want %q", key, env[key], want)
		}
	}
}

// decodeTypes reads JSONL and returns each object's event type.
func decodeType(t *testing.T, line string) string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		t.Fatalf("stdout line is not JSON: %q (%v)", line, err)
	}
	kind, _ := obj["type"].(string)
	return kind
}

// The whole point of the streaming contract: a consumer must be able to render
// progress from stdout while the child runs. Reading an event and asserting the
// process has not exited proves the output is not collected and dumped at exit,
// which is what pig did before.
func TestJSONModeEmitsEventsBeforeExit(t *testing.T) {
	cmd := jsonModeCmd(t, t.TempDir(), "Run: sleep for a while")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// The faux "sleep" tool keeps the turn open, so the process is still
	// running while these early events arrive.
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)
	var seen []string
	deadline := time.Now().Add(45 * time.Second)
	for scanner.Scan() && time.Now().Before(deadline) {
		seen = append(seen, decodeType(t, scanner.Text()))
		if len(seen) >= 3 {
			break
		}
	}
	if len(seen) < 3 {
		t.Fatalf("read only %d events before the deadline: %v", len(seen), seen)
	}

	// Still alive: Signal(0) reports an error once the process is gone.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process already exited, so events were not streamed live: %v", err)
	}
	if seen[0] != "session" {
		t.Fatalf("first line = %q, want the session header", seen[0])
	}
}

// Cancellation must terminate both a backpressured JSON writer and an active
// tool with a draining consumer. Pi's signal path disposes the runtime without
// awaiting flushRawStdout (print-mode.ts), then exits with 128+signum.
func TestJSONModeCancellationTerminatesPromptly(t *testing.T) {
	for _, drain := range []bool{false, true} {
		name := "blocked-stdout"
		if drain {
			name = "draining-stdout"
		}
		t.Run(name, func(t *testing.T) {
			pigHome := t.TempDir()
			args := []string{}
			if !drain {
				promptPath := filepath.Join(pigHome, "system.txt")
				// Exceed pipe capacity so an unread consumer exercises backpressure.
				if err := os.WriteFile(promptPath, []byte(strings.Repeat("cancellation backpressure fixture\n", 1<<16)), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--system-prompt", promptPath)
			}
			cmd := jsonModeCmd(t, pigHome, append(args, "Run: sleep for a while")...)
			// Own the pipe separately from exec.Cmd: Wait must not close a pipe
			// that the consumer is still draining during process shutdown.
			stdout, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdout.Close() }()
			cmd.Stdout = writer
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				_ = writer.Close()
				t.Fatal(err)
			}
			_ = writer.Close()
			waitDone := make(chan struct{})
			var waitErr error
			go func() {
				waitErr = cmd.Wait()
				close(waitDone)
			}()
			readerDone := make(chan struct{})
			started := make(chan struct{})
			var readErr error
			go func() {
				defer close(readerDone)
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					var event struct {
						Type string `json:"type"`
					}
					if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
						readErr = err
						return
					}
					if event.Type == "agent_start" {
						close(started)
						if drain {
							_, readErr = io.Copy(io.Discard, stdout)
						}
						return
					}
				}
				readErr = scanner.Err()
				if readErr == nil {
					readErr = io.EOF
				}
				readErr = fmt.Errorf("stream ended before agent_start: %w", readErr)
			}()
			// One waiter owns the child. Cleanup always joins it and the reader,
			// including readiness failures; no child or goroutine escapes a test.
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				<-waitDone
				_ = stdout.Close()
				<-readerDone
			})
			select {
			case <-started:
			case <-readerDone:
				if readErr != nil {
					t.Fatal(readErr)
				}
			case <-time.After(45 * time.Second):
				t.Fatal("stream did not emit agent_start")
			}
			if drain {
				// agent_start alone does not prove a tool is in flight. The faux
				// bash command writes this marker immediately before sleeping.
				marker := filepath.Join(pigHome, ai.TestFauxToolStartedMarker)
				if !pollUntil(45*time.Second, func() bool {
					_, err := os.Stat(marker)
					return err == nil
				}) {
					t.Fatal("faux tool never started")
				}
			}
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case <-waitDone:
				var exitErr *exec.ExitError
				if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 128+int(syscall.SIGTERM) {
					t.Fatalf("wait = %v, want SIGTERM exit code %d; stderr: %s", waitErr, 128+int(syscall.SIGTERM), stderr.String())
				}
			case <-deadline.C:
				t.Fatal("child did not exit within 30s of SIGTERM")
			}
			select {
			case <-readerDone:
				if readErr != nil {
					t.Fatalf("stdout reader: %v", readErr)
				}
			case <-deadline.C:
				t.Fatal("stdout reader did not finish within 30s of SIGTERM")
			}
		})
	}
}

// Resuming by --session-id must carry prior context. The faux provider only
// answers the recall prompt when the earlier turn is present in history, so a
// correct answer is evidence of resume rather than a canned reply.
func TestJSONModeResumesSessionByID(t *testing.T) {
	pigHome := t.TempDir()
	const id = "json-resume-probe"

	first := jsonModeCmd(t, pigHome, "--session-id", id,
		"Remember this exact code: the letters A L P H A and the digits 7 7 4 9")
	if out, err := first.CombinedOutput(); err != nil {
		t.Fatalf("first turn failed: %v\n%s", err, out)
	}

	second := jsonModeCmd(t, pigHome, "--session-id", id,
		"What was the code I told you to remember?")
	out, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("resumed turn failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ALPHA-7749") {
		t.Fatalf("resumed turn lost prior context; want ALPHA-7749 in:\n%s", out)
	}
}
