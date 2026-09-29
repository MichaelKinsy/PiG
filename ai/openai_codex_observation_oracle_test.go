package ai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Node oracle (coding/testdata/rpc33-observation/providers/openai-codex-responses/probe.mjs) drives Pi 0.87.1's
// real Codex SSE pipeline and logs every client-socket data event, every AssistantMessageEventStream push and every
// consumer delivery with the microtask tick at which it happened. These tests replay the same wire fixtures and the
// same counter chain on the Go executor and require the identical log.

const codexOraclePath = "../coding/testdata/rpc33-observation/providers/openai-codex-responses/pi.json"

type codexOracle struct {
	Fixtures map[string]string             `json:"fixtures"`
	Inproc   map[string][]codexOracleEntry `json:"inproc"`
	RPC      map[string]json.RawMessage    `json:"rpc"`
}

type codexOracleEntry struct {
	At      string          `json:"at"`
	Type    string          `json:"type,omitempty"`
	Segment int             `json:"segment"`
	Tick    int             `json:"tick"`
	State   *codexOracleMsg `json:"state,omitempty"`
}

type codexOracleMsg struct {
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ResponseID   *string         `json:"responseId"`
	Usage        int             `json:"usage"`
	ErrorMessage *string         `json:"errorMessage"`
}

func loadCodexOracle(t *testing.T) codexOracle {
	t.Helper()
	data, err := os.ReadFile(codexOraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle codexOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// codexTickLog is the Go analogue of the probe's counter chain. Every external completion starts a segment and restarts
// the chain; each observation extends it. The chain posts ordinary FIFO reactions, so it cannot reorder modeled ones.
type codexTickLog struct {
	t        *testing.T
	executor *continuationExecutor
	entries  []codexOracleEntry
	segment  int
	ticks    int
	allow    int
	running  bool

	mu         sync.Mutex
	wake       *sync.Cond
	externals  int
	idleExtern int
}

const codexTickWindow = 1024

func newCodexTickLog(t *testing.T, executor *continuationExecutor) *codexTickLog {
	log := &codexTickLog{t: t, executor: executor}
	log.wake = sync.NewCond(&log.mu)
	executor.trace = &executorTrace{
		external: log.external,
		idle:     log.idle,
		push: func(_ *AssistantMessageEventStream, event AssistantMessageEvent) {
			log.observe("push", event)
		},
	}
	return log
}

func (log *codexTickLog) startChain() {
	log.running = true
	var step func()
	step = func() {
		log.ticks++
		if log.ticks < log.allow {
			log.executor.post(step)
		} else {
			log.running = false
		}
	}
	log.executor.post(step)
}

func (log *codexTickLog) external() {
	if log.running {
		log.t.Error("an external event interrupted a running chain")
	}
	log.segment++
	log.ticks = 0
	log.allow = codexTickWindow
	log.startChain()
	log.entries = append(log.entries, codexOracleEntry{At: "external", Segment: log.segment})
	log.mu.Lock()
	log.externals++
	log.mu.Unlock()
}

func (log *codexTickLog) idle() {
	log.mu.Lock()
	log.idleExtern = log.externals
	log.wake.Broadcast()
	log.mu.Unlock()
}

// waitIdle blocks until the executor was idle after at least n external completions: the previous wire write has been
// fully consumed and the provider's next body read is pending.
func (log *codexTickLog) waitIdle(n int) bool {
	deadline := time.AfterFunc(20*time.Second, func() {
		log.mu.Lock()
		log.idleExtern = -1
		log.wake.Broadcast()
		log.mu.Unlock()
	})
	defer deadline.Stop()
	log.mu.Lock()
	defer log.mu.Unlock()
	for log.idleExtern < n {
		if log.idleExtern == -1 {
			return false
		}
		log.wake.Wait()
	}
	return true
}

func (log *codexTickLog) observe(at string, event AssistantMessageEvent) {
	if !log.running {
		log.t.Errorf("%s %s after the chain lapsed", at, event.EventType())
		return
	}
	log.allow = log.ticks + codexTickWindow
	entry := codexOracleEntry{At: at, Type: string(event.EventType()), Segment: log.segment, Tick: log.ticks}
	entry.State = codexOracleState(log.t, oracleEventMessage(event))
	log.entries = append(log.entries, entry)
}

func oracleEventMessage(event AssistantMessageEvent) *AssistantMessage {
	switch value := event.(type) {
	case StartEvent:
		return value.Partial
	case DoneEvent:
		return value.Message
	case ErrorEvent:
		return value.Error
	}
	return eventPartial(event)
}

func codexOracleState(t *testing.T, message *AssistantMessage) *codexOracleMsg {
	t.Helper()
	observed := message.Observe()
	encoded, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Content      json.RawMessage           `json:"content"`
		StopReason   string                    `json:"stopReason"`
		ResponseID   *string                   `json:"responseId"`
		Usage        struct{ TotalTokens int } `json:"usage"`
		ErrorMessage *string                   `json:"errorMessage"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire.Content) == "null" {
		// Observe clones an empty slice to nil; every consumer encodes it as Pi's empty content array.
		wire.Content = json.RawMessage("[]")
	}
	return &codexOracleMsg{Content: wire.Content, StopReason: wire.StopReason, ResponseID: wire.ResponseID, Usage: wire.Usage.TotalTokens, ErrorMessage: wire.ErrorMessage}
}

func (entry codexOracleEntry) describe() string {
	state := ""
	if entry.State != nil {
		id := "<nil>"
		if entry.State.ResponseID != nil {
			id = *entry.State.ResponseID
		}
		state = fmt.Sprintf(" %s %s id=%s usage=%d", entry.State.Content, entry.State.StopReason, id, entry.State.Usage)
	}
	return fmt.Sprintf("%s %s seg=%d tick=%d%s", entry.At, entry.Type, entry.Segment, entry.Tick, state)
}

func equalCodexEntries(got, want []codexOracleEntry) (string, bool) {
	for i := 0; i < max(len(got), len(want)); i++ {
		switch {
		case i >= len(got):
			return fmt.Sprintf("entry %d missing; want %s", i, want[i].describe()), false
		case i >= len(want):
			return fmt.Sprintf("entry %d unexpected: %s", i, got[i].describe()), false
		}
		g, w := got[i], want[i]
		same := g.At == w.At && g.Type == w.Type && g.Segment == w.Segment && g.Tick == w.Tick && (g.State == nil) == (w.State == nil)
		if same && g.State != nil {
			same = codexOracleStatesEqual(g.State, w.State)
		}
		if !same {
			return fmt.Sprintf("entry %d differs:\n  got  %s\n  want %s", i, g.describe(), w.describe()), false
		}
	}
	return "", true
}

func codexOracleStatesEqual(got, want *codexOracleMsg) bool {
	var gotContent, wantContent any
	if json.Unmarshal(got.Content, &gotContent) != nil || json.Unmarshal(want.Content, &wantContent) != nil {
		return false
	}
	return reflect.DeepEqual(gotContent, wantContent) && got.StopReason == want.StopReason && got.Usage == want.Usage &&
		reflect.DeepEqual(got.ResponseID, want.ResponseID) && reflect.DeepEqual(got.ErrorMessage, want.ErrorMessage)
}

// codexOracleServer answers one request with the probe's wire shapes: buffered is headers and body in one write,
// pending is headers and then the whole body in one write, chunked is headers and then one write per SSE record.
// Later writes wait until the Go executor is idle after the previous one, the deterministic analogue of the probe's timers.
func codexOracleServer(t *testing.T, log *codexTickLog, mode, body string) *httptest.Server {
	t.Helper()
	var records []string
	for rest := body; rest != ""; {
		end := strings.Index(rest, "\n\n") + 2
		records = append(records, rest[:end])
		rest = rest[end:]
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = bufio.NewReader(r.Body).WriteTo(discardWriter{})
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		write := func(data string) bool {
			_, err := conn.Write([]byte(data))
			return err == nil
		}
		chunk := func(data string) string { return fmt.Sprintf("%x\r\n%s\r\n", len(data), data) }
		const head = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"
		if mode == "buffered" {
			write(fmt.Sprintf("%sContent-Length: %d\r\n\r\n%s", head, len(body), body))
			return
		}
		if !write(head + "Transfer-Encoding: chunked\r\n\r\n") {
			return
		}
		writes := 1
		if mode == "pending" {
			if log.waitIdle(writes) {
				write(chunk(body) + "0\r\n\r\n")
			}
			return
		}
		for _, record := range records {
			if !log.waitIdle(writes) {
				return
			}
			if !write(chunk(record)) {
				return
			}
			writes++
		}
		if log.waitIdle(writes) {
			write("0\r\n\r\n")
		}
	}))
	server.Start()
	t.Cleanup(server.Close)
	return server
}

type discardWriter struct{}

func (discardWriter) Write(data []byte) (int, error) { return len(data), nil }

func runCodexOracleCase(t *testing.T, oracle codexOracle, mode, shape string) []codexOracleEntry {
	t.Helper()
	ctx, executor := withContinuationExecutor(t.Context())
	log := newCodexTickLog(t, executor)
	server := codexOracleServer(t, log, mode, oracle.Fixtures[shape])
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{Model: "strict", APIKey: codexTestToken(t, "acct"), BaseURL: server.URL})
	defer func() { _ = provider.Close() }()
	// Pi's probe starts its for-await in the same job that called stream(), so no socket completion can run between them. The consumer holds its continuation across Stream and the first iterator read, as the Agent does (agent-loop.ts:402-411).
	err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		scoped := observation.Context(ctx)
		stream, err := provider.Stream(scoped, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("READ")}}}), StreamOptions{Transport: TransportSSE, MaxRetries: new(0)})
		if err != nil {
			return err
		}
		for event := range stream.Events(scoped) {
			log.observe("deliver", event)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return log.entries
}

func TestCodexSSEObservationMatchesNodeOracle(t *testing.T) {
	oracle := loadCodexOracle(t)
	for _, mode := range []string{"buffered", "pending", "chunked"} {
		for _, shape := range []string{"tool", "text", "invalid", "apierror"} {
			t.Run(mode+"/"+shape, func(t *testing.T) {
				want := oracle.Inproc[mode+"/"+shape]
				if len(want) == 0 {
					t.Fatal("oracle has no entries")
				}
				for run := range 20 {
					got := runCodexOracleCase(t, oracle, mode, shape)
					if diff, ok := equalCodexEntries(got, want); !ok {
						t.Fatalf("run %d: %s", run, diff)
					}
				}
			})
		}
	}
}

// undici.mjs measures the fetch body latencies the model's constants encode.
func TestCodexUndiciLatenciesMatchNodeProbe(t *testing.T) {
	data, err := os.ReadFile("../coding/testdata/rpc33-observation/providers/openai-codex-responses/undici.json")
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		FetchResolution int `json:"fetchResolution"`
		BufferedRead    int `json:"bufferedRead"`
		PendingRead     int `json:"pendingRead"`
		CancelOpen      int `json:"cancelOpen"`
		CancelClosed    int `json:"cancelClosed"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatal(err)
	}
	// A read's await resumes one generation after the promise resolves.
	for name, pair := range map[string][2]int{
		"fetchResolution": {codexFetchResolutionTicks, probe.FetchResolution},
		"bufferedRead":    {codexBufferedReadHops + 1, probe.BufferedRead},
		"pendingRead":     {codexPendingReadHops + 1, probe.PendingRead},
		"cancelOpen":      {codexCancelOpenTicks, probe.CancelOpen},
		"cancelClosed":    {codexCancelClosedTicks, probe.CancelClosed},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: model %d ticks, Node probe %d", name, pair[0], pair[1])
		}
	}
}
