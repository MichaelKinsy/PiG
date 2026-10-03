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

// The pi-messages row of the D82 multi-provider oracle. coding/testdata/rpc33-observation/providers/pi-messages/pi.json is the raw
// output of the real `pi --mode rpc` binary of the Pi release its piVersion field names (probe.mjs in that directory). This test
// serves the same wire bytes to the real cmd/pig binary and requires the first assistant turn's RPC records to equal Pi's.

const piMessagesOracleDir = "coding/testdata/rpc33-observation/providers/pi-messages"

type piMessagesOracle struct {
	Bodies map[string]string `json:"bodies"`
	Reply  string            `json:"reply"`
	Cases  []struct {
		Fixture  string            `json:"fixture"`
		Delivery string            `json:"delivery"`
		RPC      []json.RawMessage `json:"rpc"`
	} `json:"cases"`
}

func loadPiMessagesOracle(t *testing.T) piMessagesOracle {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureSourceRoot, filepath.FromSlash(piMessagesOracleDir), "pi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle piMessagesOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// servePiMessages answers POST /messages the way probe.mjs's serve() does: odd requests get the fixture, even requests the follow-up reply.
//
// The fixture's writes are paced by the client, not only by the clock. Pi's recorded message_update.usage depends on how many SSE
// records one body read delivers: when a record and the terminal record that sets the usage share a read, the earlier record's update
// already carries the final usage (the `tail` rows). The 25ms gap between writes is what keeps a write from joining its predecessor
// in one read; a stalled client process (a loaded machine) lets two writes pile up and turns a `chunked` run into a different,
// equally faithful delivery. After each write the server therefore also waits until the client has recorded one RPC record for every
// SSE record the writes so far completed. recorded receives one token per record runPiMessagesRPC reports. Every fixture record
// yields exactly one RPC record (agent-loop.ts emits message_start on `start`, one message_update per content event and
// message_end on the terminal or failing record), so a PiG run that drops a record stalls here until testbudget's hang bound
// ends the run and reports its partial transcript.
func servePiMessages(t *testing.T, body, reply, delivery string) (baseURL string, recorded chan struct{}) {
	t.Helper()
	recorded = make(chan struct{}, 1024)
	var count atomic.Int64
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		payload, paced := body, true
		if count.Add(1)%2 == 0 {
			payload, paced = reply, false
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
		var written strings.Builder
		var awaited int
		for _, piece := range piMessagesPieces(payload, delivery) {
			_, _ = io.WriteString(w, piece)
			flusher.Flush()
			written.WriteString(piece)
			completed := strings.Count(written.String(), "\n\n")
			for ; paced && awaited < completed; awaited++ {
				select {
				case <-recorded:
				case <-r.Context().Done():
					return
				}
			}
			select {
			case <-time.After(gap):
			case <-r.Context().Done():
				return
			}
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return "http://" + listener.Addr().String(), recorded
}

// piMessagesPieces splits a body into the writes of probe.mjs's chunked (one SSE record per write), split (40-byte pieces) and tail
// (the first two records, then the rest) deliveries.
func piMessagesPieces(body, delivery string) []string {
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
		return []string{strings.Join(records[:2], ""), strings.Join(records[2:], "")}
	}
	return records
}

// The RPC transcript is compared record for record, including message_update.usage, except one open defect outside this API's stream loop:
//   - assistantMessageEvent.contentSignature and .redacted: Pi's pi-messages converter forwards the backend event's own fields
//     (`{ ...event, partial }`, pi-messages.ts:283), while ai.ThinkingEndEvent and ai.TextEndEvent carry no such fields.
//
// The first message_start (the D82 row) and every message_end are compared whole.
func withoutKnownGaps(record map[string]any) map[string]any {
	if record["type"] != "message_update" {
		return record
	}
	if event, ok := record["assistantMessageEvent"].(map[string]any); ok {
		delete(event, "contentSignature")
		delete(event, "redacted")
	}
	return record
}

func withoutTimestamps(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			if key != "timestamp" {
				out[key] = withoutTimestamps(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = withoutTimestamps(item)
		}
		return out
	}
	return value
}

// runPiMessagesRPC runs one pig RPC session in dir against baseURL and returns the first assistant turn's records in probe.mjs's
// runRPC() shape: message_start/message_end carry the message, message_update its cumulative usage and assistantMessageEvent
// (toJsonEvent drops the message). Each record it collects is announced on recorded, which servePiMessages paces its writes by.
func runPiMessagesRPC(ctx context.Context, bin, dir, baseURL string, recorded chan<- struct{}) ([]string, error) {
	agent := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agent, 0o700); err != nil {
		return nil, err
	}
	models := fmt.Sprintf(`{"providers":{"p":{"api":"pi-messages","baseUrl":%q,"apiKey":"k","models":[{"id":"strict","name":"strict","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":1000}]}}}`, baseURL)
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
		encoded, err := json.Marshal(withoutKnownGaps(withoutTimestamps(record).(map[string]any)))
		if err != nil {
			return nil, err
		}
		records = append(records, string(encoded))
		recorded <- struct{}{}
		if kind == "message_end" {
			return records, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return records, fmt.Errorf("read RPC output: %w\nstderr: %s", err, stderr.String())
	}
	return records, fmt.Errorf("no completed first assistant turn\nstderr: %s", stderr.String())
}

// TestRPCPiMessagesMatchesPi is the D82 pi-messages row: the first assistant turn's RPC records equal Pi's for every fixture and
// delivery shape, in every one of the runs. -pi-messages-runs raises the run count (the D82 evidence uses 200).
func TestRPCPiMessagesMatchesPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pig binary")
	}
	t.Parallel()
	runs := 3
	if value := os.Getenv("PIG_D82_RUNS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			t.Fatalf("PIG_D82_RUNS=%q", value)
		}
		runs = parsed
	}
	bin := buildPigBinaryForSignalTest(t)
	oracle := loadPiMessagesOracle(t)
	for _, c := range oracle.Cases {
		t.Run(c.Fixture+"/"+c.Delivery, func(t *testing.T) {
			var want []string
			for _, raw := range c.RPC {
				var value any
				if err := json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(withoutKnownGaps(withoutTimestamps(value).(map[string]any)))
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
					baseURL, recorded := servePiMessages(t, oracle.Bodies[c.Fixture], oracle.Reply, c.Delivery)
					got, err := runPiMessagesRPC(ctx, bin, dirs[run], baseURL, recorded)
					if err == nil && strings.Join(got, "\n") == strings.Join(want, "\n") {
						return
					}
					failures.Add(1)
					mu.Lock()
					if first == "" {
						first = fmt.Sprintf("run %d (error %v):\n%s", run, err, firstRecordDifference(got, want))
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

// firstRecordDifference names the first record where the two RPC transcripts differ.
func firstRecordDifference(got, want []string) string {
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
