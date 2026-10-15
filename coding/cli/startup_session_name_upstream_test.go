package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func startupNameSessionFixture(t *testing.T, projectDir, sessionFile string) {
	t.Helper()
	timestamp := time.Now().UTC()
	entries := []any{
		map[string]any{"type": "session", "version": 3, "id": "existing-session", "timestamp": timestamp.Format(time.RFC3339Nano), "cwd": projectDir},
		map[string]any{"type": "message", "id": "assistant-1", "parentId": nil, "timestamp": timestamp.Format(time.RFC3339Nano), "message": map[string]any{
			"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "hello"}}, "provider": "anthropic", "model": "claude-sonnet-4-5", "timestamp": timestamp.UnixMilli(),
		}},
	}
	var data bytes.Buffer
	for _, entry := range entries {
		if err := json.NewEncoder(&data).Encode(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sessionFile, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func startupSessionInfoNames(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for line := range bytes.SplitSeq(bytes.TrimSpace(data), []byte{'\n'}) {
		var entry struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Type == "session_info" {
			names = append(names, entry.Name)
		}
	}
	return names
}

func TestStartupNameSelection(t *testing.T) {
	for _, kind := range []string{"resume", "fork", "new", "unnamed"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "session.jsonl")
			startupNameSessionFixture(t, root, path)
			selection := startupSessionSelection{runtimeCWD: root, sessionDir: root}
			name := "CLI Named Session" // main normalizes the CLI argument before selection naming.
			switch kind {
			case "resume", "unnamed":
				selection.resumePath = path
			case "fork":
				selection.forkPath = path

			}
			if kind == "unnamed" {
				name = ""
			}
			applied, err := selection.applyName(name)

			if err != nil {
				t.Fatal(err)
			}
			wantApplied := kind == "resume" || kind == "fork"
			if applied != wantApplied {
				t.Fatalf("applied=%v want=%v", applied, wantApplied)
			}
			want := []string{}
			if wantApplied {
				want = append(want, "CLI Named Session")
			}
			if got := startupSessionInfoNames(t, path); !slices.Equal(got, want) {
				t.Fatalf("names=%q want=%q", got, want)
			}
		})
	}
}

func TestRPCStartupNameIsInitialMetadata(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.Mkdir(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--mode", "rpc", "--no-session", "--name", "  CLI Named Session  ", "--model", "test-faux/faux-1")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	cmd.Stdin = bytes.NewBufferString("{\"type\":\"get_state\",\"id\":\"state\"}\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("RPC failed: %v: %s", err, stderr.String())
	}
	responses := 0
	for line := range bytes.SplitSeq(bytes.TrimSpace(stdout.Bytes()), []byte{'\n'}) {
		var event struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Data struct {
				SessionName string `json:"sessionName"`
			} `json:"data"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("RPC JSON: %v: %s", err, line)
		}
		if event.Type == "session_info_changed" {
			t.Fatalf("startup name emitted a runtime name-change notification: %s", line)
		}
		if event.ID == "state" {
			responses++
			if event.Data.SessionName != "CLI Named Session" {
				t.Fatalf("initial name = %q", event.Data.SessionName)
			}
		}
	}
	if responses != 1 {
		t.Fatalf("get_state responses = %d, want one request's response: %s", responses, stdout.String())
	}
}

// Upstream 0.99.1 changes only how its harness starts the CLI: `--import` receives the source resolver as a file URL (pathToFileURL) instead of a path
// (.upstream/v0.99.1/packages/coding-agent/test/startup-session-name.test.ts:10-11). The Go test starts the compiled binary and has no Node loader flag, so that substitution has no Go
// counterpart: the inputs and expectations of every case are unchanged.
// .upstream/v0.99.1/packages/coding-agent/test/startup-session-name.test.ts:113 — sets --name on the selected session before runtime model validation.
func TestUpstreamStartupSessionName(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agentDir, projectDir, sessionFile := filepath.Join(root, "agent"), filepath.Join(root, "project"), filepath.Join(root, "session.jsonl")
	for _, dir := range []string{agentDir, projectDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	startupNameSessionFixture(t, projectDir, sessionFile)
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--session", sessionFile, "--name", "  CLI Named Session  ", "--model", "missing-model", "-p", "hi")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(), "PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_OFFLINE=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI did not exit without a signal: %v: %s", ctx.Err(), stderr.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("exit = %v, want code 1: %s", err, stderr.String())
	}
	// ExitCode is -1 for a signal, so code 1 also proves the upstream null signal assertion.
	code := exitErr.ExitCode()
	names := startupSessionInfoNames(t, sessionFile)
	raw, err := json.Marshal([]any{code, code < 0, names})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("STARTUP_SESSION_NAME %s\n", raw)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1: %s", code, stderr.String())
	}
	if !slices.Equal(names, []string{"CLI Named Session"}) {
		t.Fatalf("session_info names = %q, want [CLI Named Session]", names)
	}
}
