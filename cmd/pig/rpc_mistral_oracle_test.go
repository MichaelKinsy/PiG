package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The mistral-conversations row of the D82 multi-provider oracle. coding/testdata/rpc33-observation/providers/mistral-conversations/pi.json is the raw
// output of Pi 0.87.1's real `pi --mode rpc` binary (probe.mjs in that directory). This test serves the same wire bytes to the real
// cmd/pig binary and requires the first assistant turn's RPC records to equal Pi's.

const mistralOracleDir = "coding/testdata/rpc33-observation/providers/mistral-conversations"

type mistralOracle struct {
	Bodies map[string]string `json:"bodies"`
	Reply  string            `json:"reply"`
	Cases  []struct {
		Fixture  string            `json:"fixture"`
		Delivery string            `json:"delivery"`
		RPC      []json.RawMessage `json:"rpc"`
	} `json:"cases"`
}

func loadMistralOracle(t *testing.T) mistralOracle {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureSourceRoot, filepath.FromSlash(mistralOracleDir), "pi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle mistralOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// serveMistral answers POST /messages the way probe.mjs's serve() does: odd requests get the fixture, even requests the follow-up reply.
func serveMistral(t *testing.T, body, reply, delivery string) string {
	t.Helper()
	var count atomic.Int64
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		payload := body
		if count.Add(1)%2 == 0 {
			payload = reply
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if delivery == "buffered" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = io.WriteString(w, payload)
			return
		}
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		select {
		case <-time.After(150 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		if delivery == "pending" {
			_, _ = io.WriteString(w, payload)
			return
		}
		gap := 25 * time.Millisecond
		if delivery == "tail" {
			gap = 150 * time.Millisecond
		}
		for _, piece := range mistralPieces(payload, delivery) {
			_, _ = io.WriteString(w, piece)
			flusher.Flush()
			select {
			case <-time.After(gap):
			case <-r.Context().Done():
				return
			}
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return "http://" + listener.Addr().String()
}

// mistralPieces splits a body into the writes of probe.mjs's chunked (one SSE record per write), split (40-byte pieces) and tail
// (the first two records, then the rest) deliveries.
func mistralPieces(body, delivery string) []string {
	var records []string
	for record := range strings.SplitAfterSeq(body, "\n\n") {
		if record != "" {
			records = append(records, record)
		}
	}
	switch delivery {
	case "split":
		var pieces []string
		for len(body) > 0 {
			n := min(40, len(body))
			pieces, body = append(pieces, body[:n]), body[n:]
		}
		return pieces
	case "tail":
		return []string{strings.Join(records[:min(2, len(records))], ""), strings.Join(records[min(2, len(records)):], "")}
	}
	return records
}

func mistralWithoutTimestamps(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			if key != "timestamp" {
				out[key] = mistralWithoutTimestamps(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = mistralWithoutTimestamps(item)
		}
		return out
	}
	return value
}

// runMistralRPC runs one pig RPC session in dir against baseURL and returns the first assistant turn's records in probe.mjs's
// runRPC() shape: message_start/message_end carry the message, message_update its cumulative usage and assistantMessageEvent
// (toJsonEvent drops the message).
func runMistralRPC(ctx context.Context, bin, dir, baseURL string) ([]string, error) {
	agent := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		return nil, err
	}
	models := fmt.Sprintf(`{"providers":{"p":{"api":"mistral-conversations","baseUrl":%q,"apiKey":"k","models":[{"id":"strict","name":"strict","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":1000}]}}}`, baseURL)
	for name, data := range map[string]string{filepath.Join(agent, "models.json"): models, filepath.Join(dir, "parity-read-target.txt"): "x\n"} {
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--mode", "rpc", "--offline", "--no-extensions", "--model", "p/strict", "--no-context-files", "--no-skills", "--session-dir", filepath.Join(dir, "s"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "PIG_HOME="+filepath.Join(dir, "pighome"), "PIG_CODING_AGENT_DIR="+agent)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	if _, err := io.WriteString(stdin, `{"id":"read","type":"prompt","message":"READ"}`+"\n"); err != nil {
		return nil, err
	}
	var records []string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		var event map[string]any
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		kind, _ := event["type"].(string)
		message, _ := event["message"].(map[string]any)
		var record map[string]any
		switch {
		case kind == "message_update":
			record = map[string]any{"type": kind, "usage": event["usage"], "assistantMessageEvent": event["assistantMessageEvent"]}
		case message["role"] == "assistant" && strings.HasPrefix(kind, "message_"):
			record = map[string]any{"type": kind, "message": message}
		case kind == "agent_settled":
			return records, fmt.Errorf("settled before the first assistant message ended\nstderr: %s", stderr.String())
		}
		if record == nil {
			continue
		}
		encoded, err := json.Marshal(mistralWithoutTimestamps(record)) // Every record is compared whole, including message_update.usage (rpc-mode.ts:361-363 backpressure wait).
		if err != nil {
			return nil, err
		}
		records = append(records, string(encoded))
		if kind == "message_end" {
			return records, nil
		}
	}
	scanErr := scanner.Err()
	if scanErr == nil {
		scanErr = io.EOF
	}
	return records, fmt.Errorf("no completed first assistant turn (scan: %w)\nstderr: %s", scanErr, stderr.String())
}

// TestRPCMistralMatchesPi is the D82 mistral-conversations row: the first assistant turn's RPC records equal Pi's for every fixture and
// delivery shape, in every one of the runs. -mistral-conversations-runs raises the run count (the D82 evidence uses 200).
func TestRPCMistralMatchesPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pig binary")
	}
	runs := 3
	if value := os.Getenv("PIG_D82_RUNS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("PIG_D82_RUNS=%q", value)
		}
		runs = parsed
	}
	bin := buildPigBinaryForSignalTest(t)
	oracle := loadMistralOracle(t)
	for _, c := range oracle.Cases {
		t.Run(c.Fixture+"/"+c.Delivery, func(t *testing.T) {
			var want []string
			for _, raw := range c.RPC {
				var value any
				if err := json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(mistralWithoutTimestamps(value))
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, string(encoded))
			}
			ctx := testbudget.Context(t)
			dirs := make([]string, runs)
			for i := range dirs {
				dirs[i] = t.TempDir()
			}
			var failures atomic.Int64
			var mu sync.Mutex
			var first string
			sem := make(chan struct{}, 4)
			var group sync.WaitGroup
			for run := range runs {
				sem <- struct{}{}
				group.Go(func() {
					defer func() { <-sem }()
					got, err := runMistralRPC(ctx, bin, dirs[run], serveMistral(t, oracle.Bodies[c.Fixture], oracle.Reply, c.Delivery))
					if err == nil && strings.Join(got, "\n") == strings.Join(want, "\n") {
						return
					}
					failures.Add(1)
					mu.Lock()
					if first == "" {
						first = fmt.Sprintf("run %d (error %v):\n%s", run, err, mistralFirstRecordDifference(got, want))
					}
					mu.Unlock()
				})
			}
			group.Wait()
			if failures.Load() != 0 {
				t.Fatalf("%d/%d runs differ from Pi\n%s", failures.Load(), runs, first)
			}
		})
	}
}

// mistralFirstRecordDifference names the first record where the two RPC transcripts differ.
func mistralFirstRecordDifference(got, want []string) string {
	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			return fmt.Sprintf("record %d of %d (want %d)\n got: %s\nwant: %s", i, len(got), len(want), g, w)
		}
	}
	return "identical"
}
