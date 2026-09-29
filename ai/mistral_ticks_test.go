package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// D82 W5: the microtask cost of every await, yield and rejection on the Mistral body path. ticks.mjs runs Pi's real stream() over a
// scripted Web Stream and counts microtasks with a self-rescheduling microtask; this test runs the Go provider loop over the same
// chunks on the executor with the same counter and requires every read call and every consumer delivery at the same tick.

type mistralTickScenario struct {
	Name         string          `json:"name"`
	Chunks       []string        `json:"chunks"`
	End          string          `json:"end"`
	Marks        [][2]any        `json:"marks"`
	StopReason   StopReason      `json:"stopReason"`
	ErrorMessage *string         `json:"errorMessage"`
	Result       json.RawMessage `json:"result"`
}

// scriptedMistralReader is the response body's reader over a ReadableStream whose pull enqueues one scripted chunk: the stream is
// already closed (or errored) once the last chunk has been dequeued, because the pull that follows it closes the controller.
type scriptedMistralReader struct {
	turn   *continuationTurn
	chunks [][]byte
	total  int
	end    string
	reads  int
	onRead func(call int)
}

func (reader *scriptedMistralReader) ReadChunk() ([]byte, error) {
	reader.reads++
	reader.onRead(reader.reads)
	suspendContinuation(reader.turn) // `await reader.read()` of a settled read promise.
	if len(reader.chunks) > 0 {
		chunk := reader.chunks[0]
		reader.chunks = reader.chunks[1:]
		return chunk, nil
	}
	if reader.end == "error" {
		return nil, errors.New("body failed")
	}
	return nil, io.EOF
}

func (reader *scriptedMistralReader) streamSettled() bool { return reader.reads >= reader.total }

// runMistralTickScenario returns the read and delivery marks in order, the tick of the terminal push, and the stream's result.
// The terminal push resolves the result under the stream lock, so the counter sees it at the next tick.
func runMistralTickScenario(t *testing.T, scenario mistralTickScenario) (marks []string, terminalPush int, result *AssistantMessage) {
	t.Helper()
	provider := &mistralProvider{cfg: MistralConfig{ProviderID: "p", Model: "strict"}}
	builder := newObservedProviderBuilder(t.Context(), APIMistralConversations, "p", "strict")
	builder.managed = true
	stream := builder.stream
	ctx := stream.ObservationContext(t.Context())
	_, executor := withContinuationExecutor(ctx)
	var ticks int
	terminalPush = -1
	spinning := false
	var spin func()
	spin = func() {
		stream.mu.Lock()
		resolved := stream.resolved
		stream.mu.Unlock()
		if resolved && terminalPush < 0 {
			terminalPush = ticks
		}
		ticks++
		if spinning {
			executor.post(spin)
		}
	}
	reader := &scriptedMistralReader{end: scenario.End}
	for _, encoded := range scenario.Chunks {
		chunk, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		reader.chunks = append(reader.chunks, chunk)
	}
	reader.total = len(reader.chunks)
	reader.onRead = func(call int) {
		if call == 1 {
			ticks, spinning = 0, true
			executor.post(spin) // Queued before the read's own continuation, as the probe's read() hook queues it.
		}
		marks = append(marks, fmt.Sprintf("read:%d@%d", call, ticks))
	}

	// The consumer's for-await starts synchronously after stream() returns, before the provider's continuation runs.
	consumer := executor.newTurn()
	var provided sync.WaitGroup
	provided.Go(func() {
		_ = stream.responseContinuation(func(turn *continuationTurn) error {
			reader.turn = turn
			builder.turn = turn
			provider.consumeChunks(t.Context(), reader, builder)
			return nil
		})
	})
	consumer.run(func(turn *continuationTurn) {
		for event := range stream.Events(context.WithValue(ctx, continuationTurnKey{}, turn)) {
			marks = append(marks, fmt.Sprintf("deliver:%s@%d", event.EventType(), ticks))
		}
		spinning = false
	})
	provided.Wait()
	return marks, terminalPush, stream.Result()
}

func TestMistralBodyPathTicksMatchPi(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "coding", "testdata", "rpc33-observation", "providers", "mistral-conversations", "ticks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Scenarios []mistralTickScenario `json:"scenarios"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Scenarios) == 0 {
		t.Fatal("oracle has no scenarios")
	}
	for _, scenario := range oracle.Scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			var want []string
			wantTerminal := -1
			for _, mark := range scenario.Marks {
				label := mark[0].(string)
				switch {
				case label == "push:done" || label == "push:error":
					wantTerminal = int(mark[1].(float64))
				case strings.HasPrefix(label, "read:") || strings.HasPrefix(label, "deliver:"):
					want = append(want, fmt.Sprintf("%s@%v", label, mark[1]))
				}
			}
			for run := range 20 {
				got, terminal, result := runMistralTickScenario(t, scenario)
				if strings.Join(got, " ") != strings.Join(want, " ") {
					t.Fatalf("run %d tick order differs\n got: %s\nwant: %s", run, strings.Join(got, " "), strings.Join(want, " "))
				}
				if terminal != wantTerminal {
					t.Fatalf("run %d terminal event pushed at tick %d, want %d", run, terminal, wantTerminal)
				}
				if result.StopReason != scenario.StopReason {
					t.Fatalf("stop reason %s, want %s", result.StopReason, scenario.StopReason)
				}
				if scenario.ErrorMessage != nil && result.ErrorMessage != *scenario.ErrorMessage {
					t.Fatalf("error message %q, want %q", result.ErrorMessage, *scenario.ErrorMessage)
				}
				encoded, err := json.Marshal(result.Observe())
				if err != nil {
					t.Fatal(err)
				}
				if got, want := canonicalMistralState(t, encoded), canonicalMistralState(t, scenario.Result); got != want {
					t.Fatalf("terminal message\n got: %s\nwant: %s", got, want)
				}
			}
		})
	}
}

// TestMistralRealBodyTicksMatchPi replays realticks.json: Pi's real stream() against a loopback server that writes headers and the
// whole body in one write. Unlike the scripted bodies of ticks.json, the first read here goes through undici, so this pins the
// microtask cost of a real fetch body: every push of the provider and every delivery to the consumer at the tick Pi produced it.
func TestMistralRealBodyTicksMatchPi(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "coding", "testdata", "rpc33-observation", "providers", "mistral-conversations", "realticks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Fixture string   `json:"fixture"`
			Body    string   `json:"body"`
			Marks   [][2]any `json:"marks"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Cases) == 0 {
		t.Fatal("oracle has no cases")
	}
	for _, c := range oracle.Cases {
		t.Run(c.Fixture, func(t *testing.T) {
			var wantPush, wantDeliver []string
			for _, mark := range c.Marks {
				label := mark[0].(string)
				switch {
				case strings.HasPrefix(label, "push:"):
					wantPush = append(wantPush, fmt.Sprint(mark[1]))
				case label == "deliver:start":
					// The counter is armed from the push hook, which runs before the push resolves the consumer's waiter; Pi arms it after. The start delivery therefore reads one tick high here and every later mark is unaffected.
					wantDeliver = append(wantDeliver, fmt.Sprintf("%s@%v", label, mark[1].(float64)+1))
				default:
					wantDeliver = append(wantDeliver, fmt.Sprintf("%s@%v", label, mark[1]))
				}
			}
			for run := range 20 {
				gotPush, gotDeliver := runMistralRealBodyScenario(t, c.Body)
				if strings.Join(gotPush, " ") != strings.Join(wantPush, " ") {
					t.Fatalf("run %d push ticks differ\n got: %s\nwant: %s", run, strings.Join(gotPush, " "), strings.Join(wantPush, " "))
				}
				if strings.Join(gotDeliver, " ") != strings.Join(wantDeliver, " ") {
					t.Fatalf("run %d delivery ticks differ\n got: %s\nwant: %s", run, strings.Join(gotDeliver, " "), strings.Join(wantDeliver, " "))
				}
			}
		})
	}
}

// runMistralRealBodyScenario streams body from a loopback server through the provider's own transport and response turn, counting
// microtask generations from the start push (the first body read is issued in the same generation). It returns the tick of every
// push after the start and the tick of every delivery.
func runMistralRealBodyScenario(t *testing.T, body string) (pushes, deliveries []string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	ctx, executor := withContinuationExecutor(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	client := streamingHTTPClientNoRetry()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	provider := &mistralProvider{cfg: MistralConfig{ProviderID: "p", Model: "strict"}}
	builder := newObservedProviderBuilder(ctx, APIMistralConversations, "p", "strict")
	_, builder.managed = response.Body.(*observedResponseBody)
	if !builder.managed {
		t.Fatal("the transport did not observe the response body")
	}
	stream := builder.stream

	var ticks int
	counting := false
	var spin func()
	spin = func() {
		ticks++
		if counting {
			executor.post(spin)
		}
	}
	first := true
	builder.partialCopy = func(view *AssistantMessage) *AssistantMessage {
		if first { // The start push: Pi's counter starts at the first body read, in this generation.
			first = false
			ticks, counting = 0, true
			executor.post(spin)
		} else {
			pushes = append(pushes, fmt.Sprint(ticks))
		}
		return view
	}

	consumer := executor.newTurn()
	var provided sync.WaitGroup
	provided.Go(func() {
		_ = builder.responseTurn(func() error {
			provider.streamResponse(ctx, response.Body, &mistralResponseBody{reader: response.Body, closeBody: func() {}, request: ctx}, builder)
			return nil
		})
	})
	consumer.run(func(turn *continuationTurn) {
		for event := range stream.Events(context.WithValue(builder.ctx, continuationTurnKey{}, turn)) {
			deliveries = append(deliveries, fmt.Sprintf("deliver:%s@%d", event.EventType(), ticks))
		}
		counting = false
	})
	provided.Wait()
	return pushes, deliveries
}
