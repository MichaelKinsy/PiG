package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var bedrockRPCRuns = flag.Int("rpc33-bedrock-runs", 200, "runs per Bedrock ConverseStream RPC observation case")

// The Agent, Session and RPC subscriber hops between the provider's Event Stream and the RPC writer decide which message_update records observe which provider state. Those hops belong to the executor-turn work for the Agent and Session (gap-d82 W3 and W7), so the complete-record comparison is opt-in until that lands.
var bedrockRPCFull = flag.Bool("rpc33-bedrock-full", false, "compare every message_update and message_end record, not only the assistant message_start")

const bedrockRPCOracleDir = "../../coding/testdata/rpc33-observation/providers/bedrock-converse-stream"

type bedrockRPCOracleFile struct {
	PiVersion string `json:"piVersion"`
	RPC       []struct {
		Shape    string            `json:"shape"`
		Delivery string            `json:"delivery"`
		HTTP     string            `json:"http"`
		Events   []json.RawMessage `json:"events"`
	} `json:"rpc"`
}

type bedrockRPCInputs struct {
	Bodies     map[string][]string `json:"bodies"`
	LaterBody  string              `json:"laterBody"`
	Deliveries []string
}

// TestRPCBedrockConverseStreamObservation runs the real pig binary in --mode rpc against one loopback Bedrock ConverseStream server and compares the first assistant message_start (the start state) with Pi 0.87.1's for every shape and delivery of the oracle, run after run. With -rpc33-bedrock-full it compares every record of that message.
// The oracle's HTTP/1 rows apply: PiG's loopback connection is HTTP/1.1, and Pi's h1 and h2 rows are identical.
// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:354-363; packages/ai/src/api/bedrock-converse-stream.ts;
// oracle: coding/testdata/rpc33-observation/providers/bedrock-converse-stream/probe.mjs.
func TestRPCBedrockConverseStreamObservation(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pig binary")
	}
	var oracle bedrockRPCOracleFile
	var inputs bedrockRPCInputs
	for name, target := range map[string]any{"pi.json": &oracle, "inputs.json": &inputs} {
		data, err := os.ReadFile(filepath.Join(bedrockRPCOracleDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
	}
	bin := buildPigBinaryForSignalTest(t)
	cases := 0
	for _, c := range oracle.RPC {
		if c.HTTP != "h1" {
			continue
		}
		cases++
		t.Run(c.Shape+"/"+c.Delivery, func(t *testing.T) {
			records := firstBedrockAssistantMessage(t, c.Events)
			if !*bedrockRPCFull {
				records = records[:1]
			}
			want := canonicalBedrockRPC(t, records)
			var failures atomic.Int32
			var wg sync.WaitGroup
			jobs := make(chan int)
			for range 8 {
				wg.Go(func() {
					for run := range jobs {
						got := runBedrockRPCOnce(t, bin, &inputs, c.Shape, c.Delivery, len(records))
						if !reflect.DeepEqual(got, want) && failures.Add(1) == 1 {
							wantJSON, _ := json.MarshalIndent(want, "", "  ")
							gotJSON, _ := json.MarshalIndent(got, "", "  ")
							t.Errorf("run %d differs from Pi\nPi:\n%s\nGo:\n%s", run, wantJSON, gotJSON)
						}
					}
				})
			}
			for run := range *bedrockRPCRuns {
				jobs <- run
			}
			close(jobs)
			wg.Wait()
			if n := failures.Load(); n != 0 {
				t.Errorf("%d of %d runs differ from Pi", n, *bedrockRPCRuns)
			}
		})
	}
	if cases != 12 {
		t.Fatalf("oracle has %d HTTP/1 cases; 4 shapes and 3 deliveries require 12", cases)
	}
}

// firstBedrockAssistantMessage keeps the oracle's records up to the first assistant message_end: the oracle also holds the follow-up turn.
func firstBedrockAssistantMessage(t *testing.T, events []json.RawMessage) []json.RawMessage {
	t.Helper()
	for i, event := range events {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event, &probe); err != nil {
			t.Fatal(err)
		}
		if probe.Type == "message_end" {
			return events[:i+1]
		}
	}
	t.Fatal("the oracle has no assistant message_end")
	return nil
}

// canonicalBedrockRPC decodes records and replaces wall clocks: a numeric message timestamp above 1e9 is a clock.
func canonicalBedrockRPC(t *testing.T, records []json.RawMessage) any {
	t.Helper()
	out := make([]any, len(records))
	for i, record := range records {
		if err := json.Unmarshal(record, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return canonicalizeBedrockClocks(out)
}

func canonicalizeBedrockClocks(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = canonicalizeBedrockClocks(v[i])
		}
	case map[string]any:
		for k, item := range v {
			if number, ok := item.(float64); ok && k == "timestamp" && number > 1e9 {
				v[k] = float64(0)
				continue
			}
			v[k] = canonicalizeBedrockClocks(item)
		}
	}
	return value
}

func bedrockFrames(t *testing.T, inputs *bedrockRPCInputs, shape string) (frames [][]byte) {
	t.Helper()
	for _, encoded := range inputs.Bodies[shape] {
		frame, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func runBedrockRPCOnce(t *testing.T, bin string, inputs *bedrockRPCInputs, shape, delivery string, keep int) any {
	t.Helper()
	frames := bedrockFrames(t, inputs, shape)
	later, err := base64.StdEncoding.DecodeString(inputs.LaterBody)
	if err != nil {
		t.Fatal(err)
	}
	first, rest := bytes.Join(frames, nil), []byte(nil)
	switch delivery {
	case "pending":
		first, rest = frames[0], bytes.Join(frames[1:], nil)
	case "split":
		cut := len(frames[1]) / 2
		first, rest = append(append([]byte(nil), frames[0]...), frames[1][:cut]...), append(append([]byte(nil), frames[1][cut:]...), bytes.Join(frames[2:], nil)...)
	}
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Header().Set("X-Amzn-Requestid", "req-"+strconv.Itoa(int(requests.Load())))
		if requests.Add(1) != 1 {
			_, _ = w.Write(later)
			return
		}
		if delivery == "buffered" {
			w.Header().Set("Content-Length", strconv.Itoa(len(first)))
			_, _ = w.Write(first)
			return
		}
		_, _ = w.Write(first)
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write(rest)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"providers":{"p":{"api":"bedrock-converse-stream","baseUrl":%q,"apiKey":"k","models":[{"id":"strict","name":"strict","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":1000}]}}}`, server.URL)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--mode", "rpc", "--offline", "--no-extensions", "--model", "p/strict", "--no-context-files", "--no-skills", "--session-dir", filepath.Join(dir, "s"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_HOME="+filepath.Join(dir, "pighome"), "AWS_BEDROCK_SKIP_AUTH=1")
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
		return canonicalBedrockRPC(t, r.records[:min(keep, len(r.records))])
	case <-time.After(30 * time.Second):
		t.Errorf("timeout; stderr: %s", stderr.String())
		return nil
	}
}
