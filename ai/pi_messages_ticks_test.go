package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// D82 W5: the microtask cost of every await, yield and rejection on the pi-messages body path. ticks.mjs runs Pi's real stream() over
// a scripted Web Stream and counts microtasks with a self-rescheduling microtask; this test runs the Go provider loop over the same
// chunks on the executor with the same counter and requires every read call and every consumer delivery at the same tick.

type piMessagesTickScenario struct {
	Name         string          `json:"name"`
	Chunks       []string        `json:"chunks"`
	End          string          `json:"end"`
	Marks        [][2]any        `json:"marks"`
	StopReason   StopReason      `json:"stopReason"`
	ErrorMessage *string         `json:"errorMessage"`
	Result       json.RawMessage `json:"result"`
}

// scriptedChunkReader is the response body's reader over an already-buffered ReadableStream: each read is a settled promise.
type scriptedChunkReader struct {
	turn   *continuationTurn
	chunks [][]byte
	end    string
	reads  int
	onRead func(call int)
}

func (reader *scriptedChunkReader) ReadChunk() ([]byte, error) {
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

// runPiMessagesTickScenario returns the read and delivery marks in order, the tick of the terminal push, and the stream's result.
// The terminal push resolves the result under the stream lock, so the counter sees it at the next tick.
func runPiMessagesTickScenario(t *testing.T, scenario piMessagesTickScenario) (marks []string, terminalPush int, result *AssistantMessage) {
	t.Helper()
	provider := &piMessagesProvider{cfg: PiMessagesConfig{ProviderID: "p", Model: "strict"}}
	stream := NewAssistantMessageEventStream()
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
	reader := &scriptedChunkReader{end: scenario.End}
	for _, encoded := range scenario.Chunks {
		chunk, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		reader.chunks = append(reader.chunks, chunk)
	}
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
			provider.consumeBody(ctx, stream, newPiMessagesEventConverter("p", "strict"), reader, turn)
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

func TestPiMessagesBodyPathTicksMatchPi(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "coding", "testdata", "rpc33-observation", "providers", "pi-messages", "ticks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Scenarios []piMessagesTickScenario `json:"scenarios"`
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
				got, terminal, result := runPiMessagesTickScenario(t, scenario)
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
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if got, want := canonicalPiMessagesState(t, encoded), canonicalPiMessagesState(t, scenario.Result); got != want {
					t.Fatalf("terminal message\n got: %s\nwant: %s", got, want)
				}
			}
		})
	}
}
