// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/profiling"
	"github.com/MichaelKinsy/PiG/test/longsession"
)

// readToolFiles are Pi-Bolt's benchmark fixtures (bench/fixtures at bolt-v0.8.5) by path and size: the headless scenario's fake model reads them in this order.
var readToolFiles = []struct {
	path string
	size int
}{
	{"fixture/agent-session.ts", 140613},
	{"fixture/interactive-mode.ts", 238599},
	{"fixture/tui.ts", 47462},
	{"fixture/openai-completions.ts", 62871},
}

// readToolsWords is the prose Pi-Bolt's fake model streams a word at a time before each tool call and the answer.
var readToolsWords = strings.Fields("Looking at the code structure the module wires the session state into the renderer and then streams tool results back through the agent loop before compaction runs ")

// readToolsServer answers like Pi-Bolt's bench/fake_model.py: every turn streams 120 words, the first four turns after the prompt then call read on the next fixture with arguments streamed 8 bytes per record, and the fifth answers "Done: read all four files".
func readToolsServer(t *testing.T) *httptest.Server {
	t.Helper()
	chunk := func(delta map[string]any, finish any, usage map[string]any) string {
		body := map[string]any{"id": "chatcmpl-bench", "object": "chat.completion.chunk", "created": 0, "model": "fake-model",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		if usage != nil {
			body["choices"] = []any{}
			body["usage"] = usage
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Error(err)
		}
		return "data: " + string(encoded) + "\n\n"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("request body: %v", err)
		}
		turn := 0
		for _, message := range request.Messages {
			switch message.Role {
			case "user":
				turn = 0
			case "tool":
				turn++
			}
		}
		var out strings.Builder
		out.WriteString(chunk(map[string]any{"role": "assistant", "content": ""}, nil, nil))
		for i := range 120 {
			out.WriteString(chunk(map[string]any{"content": readToolsWords[i%len(readToolsWords)] + " "}, nil, nil))
		}
		if turn < len(readToolFiles) {
			arguments, _ := json.Marshal(map[string]string{"path": readToolFiles[turn].path})
			out.WriteString(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call_%d", turn), "type": "function", "function": map[string]any{"name": "read", "arguments": ""}}}}, nil, nil))
			for i := 0; i < len(arguments); i += 8 {
				out.WriteString(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": string(arguments[i:min(i+8, len(arguments))])}}}}, nil, nil))
			}
			out.WriteString(chunk(map[string]any{}, "tool_calls", nil))
		} else {
			out.WriteString(chunk(map[string]any{"content": "\n\nDone: read all four files (prompt 1)."}, nil, nil))
			out.WriteString(chunk(map[string]any{}, "stop", nil))
		}
		out.WriteString(chunk(nil, nil, map[string]any{"prompt_tokens": 1000 * (turn + 1), "completion_tokens": 150, "total_tokens": 1000*(turn+1) + 150}))
		out.WriteString("data: [DONE]\n\n")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, out.String())
	}))
	t.Cleanup(server.Close)
	return server
}

// printReadTools sets up Pi-Bolt's headless scenario in a new home with env added, and returns the function that runs it once and returns its exit record. The first start in a new home materializes caches, so it runs once before returning.
func printReadTools(t *testing.T, env ...string) func() profiling.ProcessMetrics {
	t.Helper()
	home := newPigHome(t)
	home.env = append(home.env, "PI_OFFLINE=1", "PIG_OFFLINE=1") // As Pi-Bolt's harness runs it.
	home.env = append(home.env, env...)
	server := readToolsServer(t)
	agent := strings.TrimPrefix(envValue(home.env, "PIG_CODING_AGENT_DIR"), "PIG_CODING_AGENT_DIR=")
	models := `{"providers":{"fake":{"baseUrl":"` + server.URL + `/v1","api":"openai-completions","apiKey":"fake","models":[{"id":"fake-model","contextWindow":200000,"maxTokens":8192}]}}}`
	files := map[string]string{
		filepath.Join(agent, "models.json"):   models,
		filepath.Join(agent, "settings.json"): `{"lastChangelogVersion":"9999.0.0","theme":"dark"}`,
		filepath.Join(agent, "auth.json"):     "{}",
	}
	for _, file := range readToolFiles {
		files[filepath.Join(home.cwd, file.path)] = writeSource(file.size)
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-p", "--no-session", "--model", "fake/fake-model", "Read the four fixture files"}
	run := func() profiling.ProcessMetrics {
		t.Helper()
		var stdout string
		record := home.run(args, func(_ int, stdin io.Writer, out *bufio.Reader) {
			_ = stdin.(io.Closer).Close() // Pi-Bolt runs it with no input; print mode reads piped stdin to its end.
			data, _ := io.ReadAll(out)
			stdout = string(data)
		})
		if !strings.Contains(stdout, "Done: read all four files") {
			t.Fatalf("pig -p did not answer after the four reads:\n%s", stdout)
		}
		return record
	}
	run()
	return run
}

// Pi-Bolt's headless scenario in print mode: one prompt, four read tool calls on 47-239 KB TypeScript files, then the answer, from process start to exit. The goroutines it starts and its auth.json stats are gated beside the allocations: each tool turn re-reads the credentials and hands the stream across goroutines.
func TestPrintReadToolsProcessGate(t *testing.T) {
	record := printReadTools(t)()
	t.Logf("perfgate: process.print_tools record %+v", record)
	perfgate.Check(t, "process.print_tools.allocs", float64(record.AllocObjects))
	perfgate.Check(t, "process.print_tools.bytes", float64(record.AllocBytes))
	perfgate.Check(t, "process.print_tools.gc_cycles", float64(record.GCCycles))
	perfgate.Check(t, "process.print_tools.goroutines_created", float64(record.GoroutinesCreated))
	perfgate.Check(t, "process.print_tools.auth_stats", float64(record.AuthFileStats))
}

// D175: with GOGC unset, a print run collects at GOGC 400, so the headless scenario's ~9.5 MB of allocations finish without a collection. At fb18cdb01b it ran 2-3 GC cycles.
func TestPrintModeRaisesGCTarget(t *testing.T) {
	if record := printReadTools(t)(); record.GCCycles != 0 || record.GCPercent != 400 {
		t.Errorf("print run with GOGC unset: %d GC cycles at GC percent %d, want 0 at 400 (%+v)", record.GCCycles, record.GCPercent, record)
	}
}

// D175: a user's GOGC wins over the print-mode target: at GOGC=100 the headless scenario runs the 2-3 GC cycles it ran before the policy.
func TestPrintModeKeepsUsersGOGC(t *testing.T) {
	if record := printReadTools(t, "GOGC=100")(); record.GCCycles < 2 || record.GCPercent != 100 {
		t.Errorf("print run with GOGC=100: %d GC cycles at GC percent %d, want at least 2 at 100 (%+v)", record.GCCycles, record.GCPercent, record)
	}
}

// D175: a print run over a large session goes back to GOGC 100 once a collection finds more than 64 MiB live. The process reports the percent it exits at and the heap goal its last cycle set; without the fallback the percent stays 400 and the goal is about five times the live heap instead of two (and peak RSS 1.17-1.20x GOGC=100's at fb18cdb01b + F1b). The session is the E12 resume fixture (longsession, 15M tokens, about 66 MB).
func TestPrintModeGCTargetFallsBackOnLargeHeap(t *testing.T) {
	home := newPigHome(t)
	// The loaders run at four Ps whatever the host, so the two arms' peaks compare at a fixed width.
	home.env = append(home.env, "PI_OFFLINE=1", "PIG_OFFLINE=1", "GOMAXPROCS=4")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"c","object":"chat.completion.chunk","created":0,"model":"scripted","choices":[{"index":0,"delta":{"role":"assistant","content":"Done"},"finish_reason":null}]}`,
			`{"id":"c","object":"chat.completion.chunk","created":0,"model":"scripted","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`{"id":"c","object":"chat.completion.chunk","created":0,"model":"scripted","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
		}
	}))
	t.Cleanup(server.Close)
	const window = 32_000_000 // Above the session's context, so the run does not compact.
	agent := strings.TrimPrefix(envValue(home.env, "PIG_CODING_AGENT_DIR"), "PIG_CODING_AGENT_DIR=")
	if err := longsession.WriteAgentDir(agent, window, server.URL+"/v1"); err != nil {
		t.Fatal(err)
	}
	var session bytes.Buffer
	meta, err := longsession.Generate(&session, longsession.Options{Tokens: 15_000_000, Window: window, Seed: 1, Cwd: home.cwd})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session: %d entries, %d tokens, %d bytes", meta.Entries, meta.Tokens, session.Len())
	dir := t.TempDir()
	run := func(round int, env ...string) profiling.ProcessMetrics {
		t.Helper()
		path := filepath.Join(dir, fmt.Sprintf("session-%d.jsonl", round)) // A run appends its turn, so each opens its own copy.
		if err := os.WriteFile(path, session.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		saved := home.env
		home.env = append(slices.Clip(home.env), env...)
		defer func() { home.env = saved }()
		var stdout string
		record := home.run([]string{"-p", "--session", path, "Say done"}, func(_ int, stdin io.Writer, out *bufio.Reader) {
			_ = stdin.(io.Closer).Close()
			data, _ := io.ReadAll(out)
			stdout = string(data)
		})
		if !strings.Contains(stdout, "Done") {
			t.Fatalf("pig -p --session did not answer:\n%s", stdout)
		}
		_ = os.Remove(path)
		return record
	}
	run(-1, "GOGC=100") // The first start in a new home materializes caches; measure the runs after it.
	policy, user := run(0), run(1, "GOGC=100")
	t.Logf("GOGC unset: GC percent %d, heap goal %d B, peak RSS %d B; GOGC=100: heap goal %d B, peak RSS %d B", policy.GCPercent, policy.HeapGoalBytes, policy.MaxRSSBytes, user.HeapGoalBytes, user.MaxRSSBytes)
	if policy.GCPercent != 100 {
		t.Errorf("print run over a %d-byte session exited at GC percent %d, want 100 (the fallback did not fire)", session.Len(), policy.GCPercent)
	}
	// The goal follows the live heap of the last cycle times (1 + percent/100). After the fallback it was 0.85-1.55x GOGC=100's over 20 runs (the last cycle lands at a different point of the load); without it 3.4x.
	if goal := float64(policy.HeapGoalBytes) / float64(user.HeapGoalBytes); goal > 2.0 {
		t.Errorf("heap goal at exit %d B with GOGC unset, %.2fx the %d B of GOGC=100 (at most 2x)", policy.HeapGoalBytes, goal, user.HeapGoalBytes)
	}
	// A gross-memory guard above any noise seen (one GOGC=100 run's peak varies by about 10%: 473-569 MB on the Linux lab host); the percent and goal checks catch a missing fallback.
	if rss := float64(policy.MaxRSSBytes) / float64(user.MaxRSSBytes); rss > 1.35 {
		t.Errorf("peak RSS %d B with GOGC unset, %.2fx the %d B of GOGC=100 (at most 1.35x)", policy.MaxRSSBytes, rss, user.MaxRSSBytes)
	}
}
