//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// openAIStub answers every chat completion with "stub answer N" for its Nth request and records each request's role/text pairs, so a test observes exactly what the Session worker sent to the provider.
type openAIStub struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests [][]stubMessage
}

type stubMessage struct{ Role, Text string }

func newOpenAIStub(t *testing.T) *openAIStub {
	t.Helper()
	stub := &openAIStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var messages []stubMessage
		for _, message := range body.Messages {
			text, _ := message.Content.(string)
			if blocks, ok := message.Content.([]any); ok {
				for _, block := range blocks {
					if fields, ok := block.(map[string]any); ok {
						if part, ok := fields["text"].(string); ok {
							text += part
						}
					}
				}
			}
			messages = append(messages, stubMessage{message.Role, text})
		}
		stub.mu.Lock()
		stub.requests = append(stub.requests, messages)
		count := len(stub.requests)
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta map[string]any, finish any, usage any) {
			payload := map[string]any{"id": "stub", "object": "chat.completion.chunk", "created": 1, "model": "stub-1", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
			if usage != nil {
				payload["usage"] = usage
			}
			encoded, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
		}
		chunk(map[string]any{"role": "assistant", "content": fmt.Sprintf("stub answer %d", count)}, nil, nil)
		chunk(map[string]any{}, "stop", map[string]any{"prompt_tokens": 1, "completion_tokens": 3, "total_tokens": 4})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (stub *openAIStub) snapshot() [][]stubMessage {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return slices.Clone(stub.requests)
}

// developmentEntryEnvironment isolates every user-owned location and selects experimental dispatch with one logical server directory. The root is short because Unix socket paths have a platform length limit.
type developmentEntryEnvironment struct {
	binary string
	env    []string
	server string
	agent  string
	cwd    string
}

func newDevelopmentEntryEnvironment(t *testing.T, stub *openAIStub) developmentEntryEnvironment {
	t.Helper()
	binary, buildEnv := buildDevelopmentEntry(t)
	root, err := os.MkdirTemp("", "pxe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// A failed run can leave detached internal processes whose arguments name this root.
		_ = exec.Command("pkill", "-KILL", "-f", root).Run()
		_ = os.RemoveAll(root)
	})
	agent, server, home, cwd := filepath.Join(root, "a"), filepath.Join(root, "s"), filepath.Join(root, "h"), filepath.Join(root, "w")
	for _, directory := range []string{agent, home, cwd} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	models := map[string]any{"providers": map[string]any{"stub": map[string]any{
		"baseUrl": stub.server.URL + "/v1", "api": "openai-completions", "apiKey": "stub-key",
		"models": []any{map[string]any{"id": "stub-1", "name": "Stub", "reasoning": false, "input": []string{"text"}, "contextWindow": 8000, "maxTokens": 1000, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}},
	}}}
	encoded, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "models.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	env := slices.Concat(buildEnv, []string{
		"HOME=" + home, "PIG_CODING_AGENT_DIR=" + agent, "PI_CODING_AGENT_DIR=" + agent,
		"PIG_SERVER_DIR=" + server, "PI_SERVER_DIR=" + server,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(home, "data"), "XDG_STATE_HOME=" + filepath.Join(home, "state"),
		"PI_OFFLINE=1", "PI_EXPERIMENTAL=1",
	})
	return developmentEntryEnvironment{binary: binary, env: env, server: server, agent: agent, cwd: cwd}
}

func (e developmentEntryEnvironment) command(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, e.binary, args...)
	command.Dir = e.cwd
	command.Env = e.env
	return command
}

// run executes one client command to completion and returns its trimmed stdout.
func (e developmentEntryEnvironment) run(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	command := e.command(ctx, args...)
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%v: %v\nstdout: %s\nstderr: %s", args, err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// waitForRetirement waits until the logical server's coordinator releases its public socket, which happens after the last client and Session worker leave an automatically activated generation.
func (e developmentEntryEnvironment) waitForRetirement(t *testing.T, serverID string) {
	t.Helper()
	socket := filepath.Join(e.server, serverID+".sock")
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Lstat(socket); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never retired: %s", socket)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestDevelopmentEntryServerClientSessionLifecycle drives the built development executable through the complete Pi 1.0.4 server/client path (src/experimental/cli.ts, commands.ts, client.ts, client-runtime.ts, server.ts, coordinator.ts, process.ts, session-worker-manager.ts): a cold prompt activates a detached server, coordinator and Session worker; the worker answers through the real coding Harness; the Session survives its server's retirement and resumes with its durable history; a foreground server serves an explicit --connect client and exits cleanly on SIGTERM.
func TestDevelopmentEntryServerClientSessionLifecycle(t *testing.T) {
	stub := newOpenAIStub(t)
	e := newDevelopmentEntryEnvironment(t, stub)

	// client.ts: no Session and a prompt creates one on the only discovered server; with none running, client-runtime.ts activates one.
	if got := e.run(t, "client", "--provider", "stub", "--model", "stub-1", "first"); got != "stub answer 1" {
		t.Fatalf("cold prompt = %q", got)
	}
	serverIDBytes, err := os.ReadFile(filepath.Join(e.server, "default-server-id"))
	if err != nil {
		t.Fatal(err)
	}
	serverID := strings.TrimSpace(string(serverIDBytes))
	e.waitForRetirement(t, serverID)

	// client.ts: no prompt and no Session lists every discovered server's Sessions as "<server>\t<session>"; the Session persists in the catalog after retirement.
	listed := strings.Split(e.run(t, "client"), "\n")
	if len(listed) != 1 || !strings.HasPrefix(listed[0], serverID+"\t") {
		t.Fatalf("list = %q", listed)
	}
	sessionID := strings.TrimPrefix(listed[0], serverID+"\t")
	if _, err := os.Stat(filepath.Join(e.agent, "experimental", "sessions", sessionID)); err != nil {
		t.Fatalf("the catalog holds the listed Session on disk: %v", err)
	}
	e.waitForRetirement(t, serverID)

	// A second prompt to the same Session reopens its durable Harness in a new worker and sends the retained turn.
	if got := e.run(t, "client", "--session-id", sessionID, "second"); got != "stub answer 2" {
		t.Fatalf("resumed prompt = %q", got)
	}
	requests := stub.snapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d", len(requests))
	}
	var turns []stubMessage
	for _, message := range requests[1] {
		if message.Role != "system" {
			turns = append(turns, message)
		}
	}
	want := []stubMessage{{"user", "first"}, {"assistant", "stub answer 1"}, {"user", "second"}}
	if !slices.Equal(turns, want) {
		t.Fatalf("the resumed Session sent %q, want %q", turns, want)
	}
	e.waitForRetirement(t, serverID)

	// commands.ts: the foreground server prints its ID and socket, serves an explicit --connect client and exits 0 on SIGTERM.
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	foreground := e.command(ctx, "server")
	var serverErr bytes.Buffer
	foreground.Stderr = &serverErr
	output, err := foreground.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := foreground.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(output)
	var lines []string
	for range 2 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("server output %q: %v\nstderr: %s", lines, err, serverErr.String())
		}
		lines = append(lines, strings.TrimSuffix(line, "\n"))
	}
	exited := make(chan error, 1)
	go func() { exited <- foreground.Wait() }()
	socket := filepath.Join(e.server, serverID+".sock")
	if lines[0] != "Server: "+serverID || lines[1] != "Socket: "+socket {
		t.Fatalf("server output = %q", lines)
	}
	if got := e.run(t, "client", "--connect", "unix://"+socket, "--session-id", sessionID, "third"); got != "stub answer 3" {
		t.Fatalf("explicit connect prompt = %q", got)
	}
	if err := foreground.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := <-exited; err != nil {
		t.Fatalf("foreground server did not exit cleanly: %v\nstderr: %s", err, serverErr.String())
	}
	e.waitForRetirement(t, serverID)
}
