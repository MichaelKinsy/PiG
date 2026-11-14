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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/profiling"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
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

// readToolsServer answers like Pi-Bolt's bench/fake_model.py: every turn streams 120 words, the first four turns after the prompt then call read on the next fixture with arguments streamed 8 bytes per record, and the fifth answers "Done: read all four files". onPrompt, when set, runs when a run's first request arrives, before its answer: pig is then past its startup, where cli.Main chooses the GC policy, and waits for the model.
func readToolsServer(t *testing.T, onPrompt func()) *httptest.Server {
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
		if turn == 0 && onPrompt != nil {
			onPrompt()
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

// printReadToolsHome sets up Pi-Bolt's headless scenario in a new home with env added, and returns the home and the arguments of a run. onPrompt is readToolsServer's.
func printReadToolsHome(t *testing.T, onPrompt func(), env ...string) (*pigHome, []string) {
	t.Helper()
	home := newPigHome(t)
	home.env = append(home.env, "PI_OFFLINE=1", "PIG_OFFLINE=1") // As Pi-Bolt's harness runs it.
	home.env = append(home.env, env...)
	server := readToolsServer(t, onPrompt)
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
	return home, []string{"-p", "--no-session", "--model", "fake/fake-model", "Read the four fixture files"}
}

// printReadToolsOnce runs the scenario of home once in binary with env added and returns its exit record. started, when set, receives the process id once the process runs.
func printReadToolsOnce(t *testing.T, home *pigHome, binary string, env, args []string, started func(pid int)) profiling.ProcessMetrics {
	t.Helper()
	var stdout string
	record := home.runBinary(binary, env, args, func(pid int, stdin io.Writer, out *bufio.Reader) {
		if started != nil {
			started(pid)
		}
		_ = stdin.(io.Closer).Close() // Pi-Bolt runs it with no input; print mode reads piped stdin to its end.
		data, _ := io.ReadAll(out)
		stdout = string(data)
	})
	if !strings.Contains(stdout, "Done: read all four files") {
		t.Fatalf("pig -p did not answer after the four reads:\n%s", stdout)
	}
	return record
}

// printReadTools sets up Pi-Bolt's headless scenario in a new home with env added, and returns the function that runs it once and returns its exit record. The first start in a new home materializes caches, so it runs once before returning.
func printReadTools(t *testing.T, env ...string) func() profiling.ProcessMetrics {
	t.Helper()
	home, args := printReadToolsHome(t, nil, env...)
	binary, err := pigBinary()
	if err != nil {
		t.Fatal(err)
	}
	run := func() profiling.ProcessMetrics {
		t.Helper()
		return printReadToolsOnce(t, home, binary, nil, args, nil)
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

// gcChurnBytes is the garbage the GC probe allocates in a print run: several times the heap growth one collection allows at the print-mode target (16 MiB less what is marked) and tens of times the growth at GOGC=50, so the collections the probe counts follow from the GC percent and not from what the run itself allocates.
const gcChurnBytes = 64 << 20

// gcProbeSource is added to cmd/pig's package main (go build -overlay) for TestPrintModeKeepsUsersGOGC: with PIG_PERFGATE_CHURN_REPORT set, the first SIGUSR1 allocates PIG_PERFGATE_CHURN_BYTES of garbage in 64 KiB pieces and writes to that path the GC percent (/gc/gogc:percent) before and after the garbage and the collections that finished between the two reads. Without it the binary is cmd/pig.
const gcProbeSource = `// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"runtime/metrics"
	"strconv"
	"syscall"
)

// perfgateGarbage holds the piece allocated last, so every piece is a heap allocation and the one before it garbage.
var perfgateGarbage []byte

func init() {
	report := os.Getenv("PIG_PERFGATE_CHURN_REPORT")
	if report == "" {
		return
	}
	total, _ := strconv.ParseUint(os.Getenv("PIG_PERFGATE_CHURN_BYTES"), 10, 64)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		<-signals
		samples := []metrics.Sample{{Name: "/gc/gogc:percent"}, {Name: "/gc/cycles/total:gc-cycles"}}
		metrics.Read(samples)
		percent, cycles := samples[0].Value.Uint64(), samples[1].Value.Uint64()
		const piece = 64 << 10
		for done := uint64(0); done < total; done += piece {
			perfgateGarbage = make([]byte, piece)
		}
		perfgateGarbage = nil
		metrics.Read(samples)
		data, _ := json.Marshal(map[string]uint64{"percent_before": percent, "percent_after": samples[0].Value.Uint64(), "cycles": samples[1].Value.Uint64() - cycles})
		_ = os.WriteFile(report+".tmp", data, 0o600)
		_ = os.Rename(report+".tmp", report)
	}()
}
`

// gcProbeBinary is cmd/pig with gcProbeSource.
var gcProbeBinary = sync.OnceValues(func() (string, error) { return probeBinary("gcprobe", gcProbeSource) })

// gcChurn is the GC probe's report: the GC percent before and after its garbage, and the collections that finished between the two reads.
type gcChurn struct {
	PercentBefore uint64 `json:"percent_before"`
	PercentAfter  uint64 `json:"percent_after"`
	Cycles        uint64 `json:"cycles"`
}

// printChurn runs the headless scenario once in a new home with env added, in cmd/pig with the GC probe, and returns the probe's report and the run's exit record. The probe allocates its garbage when the run's first model request arrives, and the model answers once the probe has reported, so the garbage falls after cli.Main chose the GC policy and before the run's own tool turns.
func printChurn(t *testing.T, env ...string) (gcChurn, profiling.ProcessMetrics) {
	t.Helper()
	probe, err := gcProbeBinary()
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "churn.json")
	pids := make(chan int, 1)
	var prompt sync.Once
	home, args := printReadToolsHome(t, func() {
		prompt.Do(func() {
			if err := syscall.Kill(<-pids, syscall.SIGUSR1); err != nil {
				t.Error(err)
				return
			}
			deadline := time.Now().Add(testbudget.Wait(t))
			for time.Now().Before(deadline) {
				if _, err := os.Stat(report); err == nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Errorf("the probe wrote no %s", report)
		})
	}, env...)
	probeEnv := []string{"PIG_PERFGATE_CHURN_REPORT=" + report, fmt.Sprintf("PIG_PERFGATE_CHURN_BYTES=%d", gcChurnBytes)}
	record := printReadToolsOnce(t, home, probe, probeEnv, args, func(pid int) { pids <- pid })
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var churn gcChurn
	if err := json.Unmarshal(data, &churn); err != nil {
		t.Fatalf("%s: %v", report, err)
	}
	return churn, record
}

// D175: a user's GOGC wins over the print-mode target. The run sets GOGC=50, which is neither the target's 400 nor the 100 that the runtime starts at and the policy falls back to, so no percent the product could set reads as the user's.
//
// The GC percent is 50 when the run's first model request arrives, after the probe's garbage, and in the exit record.
//
// The collector runs at that percent while the garbage is allocated: the garbage runs at least 4 times the collections it runs with GOGC unset. After a collection that marked M bytes, with S bytes of stacks and globals to scan, the heap may grow by max(4 MiB x P/100 - M, (M + S) x P/100) before its goal at percent P (the rule internal/gcpolicy's goal floor works with), so G bytes of garbage run about G divided by that growth collections. For one M both terms are at least 8 times larger at 400 than at 50, so the expected ratio is 8. The two runs do not share M or the points where the count starts and stops: a collection starts 45/64 to 61/64 of the way to its goal (runtime/mgcpacer.go), and the garbage allocated while it marks counts as marked, which raises the growth at 50 by at most 128/109. That leaves 8 x 45/64 x 109/128 = 4.8, and the bound is 4, half the ratio of the two percents.
//
// The check was the collections of the scenario alone, at least 2 at GOGC=100, as it ran before the policy. The scenario's allocations have since fallen from 9.5 MB to 5.3 MB: its second collection starts 6 to 37 ms before the exit record, which counts finished collections, and 13 of 50 runs at 62b1a69c3f counted 1.
func TestPrintModeKeepsUsersGOGC(t *testing.T) {
	const percent, target = 50, 400
	user, userRecord := printChurn(t, fmt.Sprintf("GOGC=%d", percent))
	policy, policyRecord := printChurn(t)
	t.Logf("GOGC=%d: GC percent %d at the first model request, %d after %d B of garbage and %d collections, %d at exit; GOGC unset: %d, %d after %d collections, %d at exit; exit records %+v and %+v",
		percent, user.PercentBefore, user.PercentAfter, gcChurnBytes, user.Cycles, userRecord.GCPercent, policy.PercentBefore, policy.PercentAfter, policy.Cycles, policyRecord.GCPercent, userRecord, policyRecord)
	if user.PercentBefore != percent || user.PercentAfter != percent || userRecord.GCPercent != percent {
		t.Errorf("print run with GOGC=%d: GC percent %d at the first model request, %d after %d B of garbage and %d at exit, want %d at all three", percent, user.PercentBefore, user.PercentAfter, gcChurnBytes, userRecord.GCPercent, percent)
	}
	if policy.PercentBefore != target || policy.PercentAfter != target || policy.Cycles == 0 {
		t.Fatalf("print run with GOGC unset: GC percent %d at the first model request and %d after %d B of garbage in %d collections, want %d at both and a collection: the run does not measure the print-mode target", policy.PercentBefore, policy.PercentAfter, gcChurnBytes, policy.Cycles, target)
	}
	if least := policy.Cycles * (target / percent) / 2; user.Cycles < least {
		t.Errorf("print run with GOGC=%d: %d collections of %d B of garbage, want at least %d: %d times the %d of GOGC unset, half the ratio of the two percents", percent, user.Cycles, gcChurnBytes, least, target/percent/2, policy.Cycles)
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
