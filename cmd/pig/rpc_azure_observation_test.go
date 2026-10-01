package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

var azureRPCRuns = flag.Int("rpc33-azure-runs", 200, "runs per Azure OpenAI Responses RPC observation case")

// The Agent, Session and RPC subscriber hops between the provider's Event Stream and the RPC writer determine which
// message_update records observe which provider state. Those hops belong to the executor-turn work for the Agent and Session
// (gap-d82 W3 and W7), so the complete-record comparison is opt-in until that lands.
var azureRPCFull = flag.Bool("rpc33-azure-full", false, "compare every message_update and message_end record, not only the assistant message_start")

const azureRPCOracle = "../../coding/testdata/rpc33-observation/providers/azure-openai-responses/rpc.json"

type azureRPCOracleFile struct {
	PiVersion string            `json:"piVersion"`
	Bodies    map[string]string `json:"bodies"`
	Cases     []struct {
		Shape   string            `json:"shape"`
		Fixture string            `json:"fixture"`
		Records []json.RawMessage `json:"records"`
	} `json:"cases"`
}

// TestRPCAzureOpenAIResponsesObservation runs the real pig binary in --mode rpc against one loopback Azure OpenAI Responses server and compares the
// first assistant message_start (the start state) with Pi 0.87.1's, run after run. With -rpc33-azure-full it compares every record of that message
// (message_start, each message_update, message_end).
// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363; packages/ai/src/api/azure-openai-responses.ts;
// oracle: coding/testdata/rpc33-observation/providers/azure-openai-responses/rpc.mjs.
func TestRPCAzureOpenAIResponsesObservation(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pig binary")
	}
	data, err := os.ReadFile(azureRPCOracle)
	if err != nil {
		t.Fatal(err)
	}
	var oracle azureRPCOracleFile
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Cases) != 4 {
		t.Fatalf("oracle has %d cases; buffered and pending for tool and text require 4", len(oracle.Cases))
	}
	bin := buildPigBinaryForSignalTest(t)
	for _, c := range oracle.Cases {
		t.Run(c.Shape+"/"+c.Fixture, func(t *testing.T) {
			records := c.Records
			if !*azureRPCFull {
				records = records[:1]
			}
			want := canonicalRPCRecords(t, records)
			var failures atomic.Int32
			var wg sync.WaitGroup
			jobs := make(chan int)
			for range 8 {
				wg.Go(func() {
					for run := range jobs {
						got := runAzureRPCOnce(t, bin, oracle.Bodies, c.Shape, c.Fixture, len(records))
						if !bytes.Equal(got, want) && failures.Add(1) == 1 {
							diff := gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath("rpc"), string(want), string(got)))
							t.Errorf("run %d differs from Pi\n%v", run, diff)
						}
					}
				})
			}
			for run := range *azureRPCRuns {
				jobs <- run
			}
			close(jobs)
			wg.Wait()
			if n := failures.Load(); n != 0 {
				t.Errorf("%d of %d runs differ from Pi", n, *azureRPCRuns)
			}
		})
	}
}

func canonicalRPCRecords(t *testing.T, records []json.RawMessage) []byte {
	t.Helper()
	out := make([]any, len(records))
	for i, record := range records {
		if err := json.Unmarshal(record, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.MarshalIndent(canonicalizeRPCClocks(out), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// canonicalizeRPCClocks mirrors rpc.mjs: a numeric message timestamp above 1e9 is a wall clock.
func canonicalizeRPCClocks(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = canonicalizeRPCClocks(v[i])
		}
	case map[string]any:
		for k, item := range v {
			if number, ok := item.(float64); ok && k == "timestamp" && number > 1e9 {
				v[k] = float64(0)
				continue
			}
			v[k] = canonicalizeRPCClocks(item)
		}
	}
	return value
}

func runAzureRPCOnce(t *testing.T, bin string, bodies map[string]string, shape, fixture string, keep int) []byte {
	t.Helper()
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		// Only the model's Responses request takes a response slot. A foreign client that reaches this reused loopback port must not consume the first body, or PiG's first request would receive the follow-up body.
		if r.Method != http.MethodPost || r.URL.Path != "/openai/v1/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) != 1 {
			_, _ = io.WriteString(w, bodies["followup"])
			return
		}
		if fixture == "buffered" {
			_, _ = io.WriteString(w, bodies[shape])
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, bodies[shape])
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"providers":{"p":{"api":"azure-openai-responses","baseUrl":%q,"apiKey":"k","models":[{"id":"strict","name":"strict","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":1000}]}}}`, server.URL+"/openai/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "parity-read-target.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--mode", "rpc", "--offline", "--no-extensions", "--model", "p/strict", "--no-context-files", "--no-skills", "--session-dir", filepath.Join(dir, "s"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_HOME="+filepath.Join(dir, "pighome"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if _, err := io.WriteString(stdin, `{"id":"read","type":"prompt","message":"READ"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		records []json.RawMessage
		err     error
	}
	done := make(chan result, 1)
	go func() {
		var records []json.RawMessage
		open := false
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(nil, 16<<20)
		for scanner.Scan() {
			var e struct {
				Type    string `json:"type"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			line := append([]byte(nil), scanner.Bytes()...)
			if json.Unmarshal(line, &e) != nil {
				continue
			}
			assistant := e.Message.Role == "assistant"
			if e.Type == "message_start" && assistant && !open {
				open = true
				releaseOnce()
			}
			if open && (e.Type == "message_update" || (assistant && (e.Type == "message_start" || e.Type == "message_end"))) {
				records = append(records, line)
			}
			if open && assistant && e.Type == "message_end" {
				done <- result{records: records}
				return
			}
		}
		done <- result{err: fmt.Errorf("stdout closed before assistant message_end: %w", scanner.Err())}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Errorf("%v; stderr: %s", r.err, stderr.String())
			return nil
		}
		return canonicalRPCRecords(t, r.records[:min(keep, len(r.records))])
	case <-time.After(30 * time.Second):
		t.Errorf("timeout; stderr: %s", stderr.String())
		return nil
	}
}
