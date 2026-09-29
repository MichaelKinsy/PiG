package ai

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The oracle is Pi 0.87.1's real anthropic-messages pipeline on Node 24.19.0 and 26.7.0 (identical), traced by coding/testdata/rpc33-observation/providers/anthropic-messages/probe.mjs. Every record carries the macrotask ordinal ("epoch") and the microtask round ("tick") in which Pi's consumer received an event or its result, and the message state visible at that instant.
const anthropicOracleDir = "../coding/testdata/rpc33-observation/providers/anthropic-messages/"

type anthropicOracleRecord struct {
	Seq     int            `json:"seq"`
	Epoch   int            `json:"epoch"`
	Tick    int            `json:"tick"`
	Side    string         `json:"side"`
	Type    string         `json:"type"`
	Message map[string]any `json:"message"`
}

type anthropicOracleCase struct {
	Shape        string                  `json:"shape"`
	Delivery     string                  `json:"delivery"`
	Layers       any                     `json:"layers"`
	AbortAtStart bool                    `json:"abortAtStart"`
	Trace        []anthropicOracleRecord `json:"trace"`
}

type anthropicOraclePlan struct {
	Headers string `json:"headers"`
	Hold    bool   `json:"hold"`
	Chunks  []struct {
		Wait   int    `json:"wait"`
		Data   string `json:"data"`
		Base64 string `json:"base64"`
	} `json:"chunks"`
}

// bytes is the chunk's exact bytes: text when they are valid UTF-8, otherwise base64.
func (plan anthropicOraclePlan) chunk(i int) string {
	chunk := plan.Chunks[i]
	if chunk.Base64 == "" {
		return chunk.Data
	}
	decoded, err := base64.StdEncoding.DecodeString(chunk.Base64)
	if err != nil {
		panic(err)
	}
	return string(decoded)
}

func loadAnthropicOracle(t *testing.T) (cases []anthropicOracleCase, plans map[string]map[string]anthropicOraclePlan) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(anthropicOracleDir, "pi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		PiVersion string                `json:"piVersion"`
		Cases     []anthropicOracleCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.PiVersion != "0.87.1" {
		t.Fatalf("oracle pins Pi %s", oracle.PiVersion)
	}
	raw, err = os.ReadFile(filepath.Join(anthropicOracleDir, "inputs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inputs struct {
		Plans map[string]map[string]anthropicOraclePlan `json:"plans"`
	}
	if err := json.Unmarshal(raw, &inputs); err != nil {
		t.Fatal(err)
	}
	return oracle.Cases, inputs.Plans
}

// microtaskClock numbers macrotasks and microtask rounds on the executor the way probe.mjs does: a macrotask is an external reaction; a tick is one round of a self-rescheduling reaction chain that restarts at every macrotask. The chain ends when no other reaction is ready, which is the moment Node would return to its event loop; onQuiet then lets the fixture server deliver its next bytes.
type microtaskClock struct {
	executor *continuationExecutor
	onQuiet  func()

	mu         sync.Mutex
	epoch      int
	tick       int
	generation int
}

func (clock *microtaskClock) install() {
	clock.executor.trace = &executorTrace{external: clock.startMacrotask}
}

// startMacrotask numbers a macrotask and restarts the tick chain.
func (clock *microtaskClock) startMacrotask() {
	clock.mu.Lock()
	clock.epoch++
	clock.tick = 0
	clock.generation++
	generation := clock.generation
	clock.mu.Unlock()
	var step func()
	step = func() {
		clock.mu.Lock()
		if generation != clock.generation {
			clock.mu.Unlock()
			return
		}
		clock.tick++
		clock.mu.Unlock()
		clock.executor.mu.Lock()
		quiet := len(clock.executor.ready) == 0
		clock.executor.mu.Unlock()
		if quiet {
			clock.onQuiet()
			return
		}
		clock.executor.post(step)
	}
	clock.executor.post(step)
}

func (clock *microtaskClock) now() (epoch, tick int) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.epoch, clock.tick
}

// serveAnthropicPlan writes one response the way probe.mjs's server does: each chunk is its own socket write, and a chunk after the first waits for the client to reach quiescence.
func serveAnthropicPlan(t *testing.T, plan anthropicOraclePlan, gate <-chan struct{}) (address string, done <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		request, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		_, _ = request.Body.Read(make([]byte, 1<<20))
		frame := func(data string) string { return fmt.Sprintf("%x\r\n%s\r\n", len(data), data) }
		waitGate := func() bool {
			select {
			case <-gate:
				return true
			case <-time.After(20 * time.Second):
				return false
			}
		}
		write := func(data string) { _, _ = conn.Write([]byte(data)) }
		const head = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"
		switch {
		case plan.Hold:
			write(head + "Transfer-Encoding: chunked\r\n\r\n")
			waitGate()
			// The consumer aborts on start; wait for the connection to close.
			_, _ = conn.Read(make([]byte, 1))
		case plan.Headers == "length":
			var body strings.Builder
			for i := range plan.Chunks {
				body.WriteString(plan.chunk(i))
			}
			write(fmt.Sprintf("%sContent-Length: %d\r\n\r\n%s", head, body.Len(), body.String()))
		default:
			response := head + "Transfer-Encoding: chunked\r\nConnection: close\r\n\r\n"
			pending, first := plan.Chunks, 0
			if plan.Headers == "chunked" {
				write(response + frame(plan.chunk(0)))
				pending = pending[1:]
				first++
			} else {
				write(response)
			}
			for i := range pending {
				if !waitGate() {
					return
				}
				suffix := ""
				if i == len(pending)-1 {
					suffix = "0\r\n\r\n"
				}
				write(frame(plan.chunk(first+i)) + suffix)
			}
		}
	}()
	return listener.Addr().String(), finished
}

// runAnthropicTrace drives the real provider against a fixture and records what Pi's probe records at the consumer: every delivered event and the awaited result, with the clock and the message state at that instant.
func runAnthropicTrace(t *testing.T, plan anthropicOraclePlan) []anthropicOracleRecord {
	t.Helper()
	return runAnthropicTraceObserved(t, plan, nil)
}

// runAnthropicTraceObserved additionally calls quiet at the end of every macrotask with the start event's partial, the object a Pi consumer holds while the producer keeps mutating it.
func runAnthropicTraceObserved(t *testing.T, plan anthropicOraclePlan, quiet func(epoch int, start *AssistantMessage)) []anthropicOracleRecord {
	t.Helper()
	gate := make(chan struct{}, 64)
	address, served := serveAnthropicPlan(t, plan, gate)
	provider := NewAnthropicProvider(AnthropicConfig{Model: "probe", ProviderID: "probe-provider", APIKey: "test", BaseURL: "http://" + address})
	t.Cleanup(func() { _ = provider.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ctx, executor := withContinuationExecutor(ctx)
	var start atomic.Pointer[AssistantMessage]
	var clock *microtaskClock
	clock = &microtaskClock{executor: executor, onQuiet: func() {
		if quiet != nil {
			epoch, _ := clock.now()
			quiet(epoch, start.Load())
		}
		select {
		case gate <- struct{}{}:
		default:
		}
	}}
	clock.install()

	var records []anthropicOracleRecord
	record := func(side, eventType string, message *AssistantMessage) {
		epoch, tick := clock.now()
		encoded := deliveredJSON(t, message)
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		decoded["timestamp"] = float64(0)
		records = append(records, anthropicOracleRecord{Epoch: epoch, Tick: tick, Side: side, Type: eventType, Message: decoded})
	}
	err := RunStreamContinuation(ctx, func(observation *StreamObservation) error {
		scoped := observation.Context(ctx)
		stream, err := provider.Stream(scoped, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe"), Timestamp: 1}}}), StreamOptions{})
		if err != nil {
			return err
		}
		for event := range stream.Events(scoped) {
			message := eventPartial(event)
			switch event := event.(type) {
			case StartEvent:
				message = event.Partial
				start.Store(event.Partial)
			case DoneEvent:
				message = event.Message
			case ErrorEvent:
				message = event.Error
			}
			record("deliver", string(event.EventType()), message)
		}
		result := awaitContinuation(observation.turn, stream.resultContinuation(executor))
		record("result", "", result)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture server did not finish")
	}
	return normalizeAnthropicTrace(records)
}

// normalizeAnthropicTrace renumbers epochs over the epochs that hold a record, exactly as probe.mjs's normalize does.
func normalizeAnthropicTrace(records []anthropicOracleRecord) []anthropicOracleRecord {
	ordinals := map[int]int{}
	for i := range records {
		if _, ok := ordinals[records[i].Epoch]; !ok {
			ordinals[records[i].Epoch] = len(ordinals) + 1
		}
		records[i].Epoch = ordinals[records[i].Epoch]
		records[i].Seq = i
	}
	return records
}

// oracleConsumerRecords keeps what a Go consumer can observe: deliveries and the awaited result.
func oracleConsumerRecords(records []anthropicOracleRecord) []anthropicOracleRecord {
	var kept []anthropicOracleRecord
	for _, record := range records {
		if record.Side == "push" {
			continue
		}
		kept = append(kept, record)
	}
	// Renumber epochs over the epochs that still hold a record.
	ordinals := map[int]int{}
	for i := range kept {
		if _, ok := ordinals[kept[i].Epoch]; !ok {
			ordinals[kept[i].Epoch] = len(ordinals) + 1
		}
		kept[i].Epoch = ordinals[kept[i].Epoch]
		kept[i].Seq = i
	}
	return kept
}

func describeAnthropicTrace(records []anthropicOracleRecord) string {
	var lines []string
	for _, record := range records {
		message, _ := json.Marshal(record.Message)
		lines = append(lines, fmt.Sprintf("e%d t%d %s %s %s", record.Epoch, record.Tick, record.Side, record.Type, message))
	}
	return strings.Join(lines, "\n")
}

// TestAnthropicMicrotaskTrace replays every layer-0 fixture through the real provider and compares each consumer delivery, its macrotask and microtask position, and the message state visible there with Pi's. Abort fixtures are excluded: a Go cancellation is an external completion, not a synchronous rejection (see the plan's cancellation gap).
func TestAnthropicMicrotaskTrace(t *testing.T) {
	cases, plans := loadAnthropicOracle(t)
	ran := 0
	for _, oracle := range cases {
		if oracle.AbortAtStart || !reflect.DeepEqual(oracle.Layers, float64(0)) {
			continue
		}
		plan := plans[oracle.Shape][oracle.Delivery]
		name := oracle.Shape + "/" + oracle.Delivery
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := runAnthropicTrace(t, plan)
			want := oracleConsumerRecords(append([]anthropicOracleRecord(nil), oracle.Trace...))
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Go trace differs from Pi\n--- Go\n%s\n--- Pi\n%s", describeAnthropicTrace(got), describeAnthropicTrace(want))
			}
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("no oracle cases ran")
	}
}

// deliveredJSON serializes the exported fields of a delivered partial. It does not re-observe the producer, so it checks that the fields themselves equal the stream state at delivery.
func deliveredJSON(t *testing.T, message *AssistantMessage) []byte {
	t.Helper()
	detached := *message
	detached.observation = nil
	encoded, err := json.Marshal(detached)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
