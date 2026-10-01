package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

const testFauxOraclePath = "../coding/testdata/rpc33-observation/providers/test-faux/pi.json"

type testFauxOracle struct {
	Pi      string `json:"pi"`
	Results []struct {
		Scenario struct {
			Name   string `json:"name"`
			Prompt string `json:"prompt"`
		} `json:"scenario"`
		Direct []struct {
			Layers  int               `json:"layers"`
			Mode    string            `json:"mode"`
			Records []json.RawMessage `json:"records"`
		} `json:"direct"`
	} `json:"results"`
}

func readTestFauxOracle(t testing.TB) testFauxOracle {
	t.Helper()
	data, err := os.ReadFile(testFauxOraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle testFauxOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

func testFauxTranscript(prompt string) TranscriptContext {
	return NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: prompt}}, Timestamp: 1}}})
}

// testFauxDirectObservation replays providers/test-faux/probe.mjs `direct`: a `for await` consumer behind `layers` lazyStream layers.
func testFauxDirectObservation(t testing.TB, prompt string, layers int) []json.RawMessage {
	t.Helper()
	provider := &TestFauxProvider{}
	var pushed atomic.Int64
	provider.afterPush = func() { pushed.Add(1) }
	model := &Model{ID: "faux-1", ProviderMeta: ProviderMetadata{ProviderID: "test-faux", API: "test-faux"}, Provider: provider}
	transcript := testFauxTranscript(prompt)
	var records []json.RawMessage
	record := func(field string, value any) {
		data, err := json.Marshal(map[string]any{"pushed": pushed.Load(), field: value})
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, data)
	}
	fauxSyncSegment(t.Context(), func(ctx context.Context, turn *continuationTurn) {
		make := func(ctx context.Context) (*AssistantMessageEventStream, error) {
			return provider.Stream(ctx, transcript, StreamOptions{})
		}
		for range layers {
			inner := make
			make = func(ctx context.Context) (*AssistantMessageEventStream, error) {
				return LazyStream(ctx, model, inner), nil
			}
		}
		response, err := make(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		for event := range response.Events(context.WithValue(context.WithoutCancel(ctx), continuationTurnKey{}, turn)) {
			record("event", event)
		}
		record("result", awaitContinuation(turn, response.resultContinuation(turn.executor)))
	})
	return records
}

// testFauxTickObservation replays `ticks`: an independent microtask loop that starts in the stream-creating segment.
func testFauxTickObservation(t testing.TB, prompt string) []json.RawMessage {
	t.Helper()
	provider := &TestFauxProvider{}
	var pushed atomic.Int64
	provider.afterPush = func() { pushed.Add(1) }
	var records []json.RawMessage
	fauxSyncSegment(t.Context(), func(ctx context.Context, turn *continuationTurn) {
		stream, err := provider.Stream(ctx, testFauxTranscript(prompt), StreamOptions{})
		if err != nil {
			t.Error(err)
			return
		}
		for range 40 {
			suspendContinuation(turn)
			records = append(records, json.RawMessage(fmt.Sprint(pushed.Load())))
		}
		awaitContinuation(turn, stream.resultContinuation(turn.executor))
	})
	return records
}

// TestTestFauxObservationOracle replays Pi's paired test-faux fixture through 0-2 lazyStream layers and a for-await consumer.
// upstream: test/parity/testdata/test-faux-provider.ts:streamTestFaux (emitPlan runs from one queueMicrotask; TUI_LIVE_STREAM awaits a timer between deltas); packages/ai/src/api/lazy.ts:31-61.
// The oracle is providers/test-faux/probe.mjs; each record carries the push count at delivery.
func TestTestFauxObservationOracle(t *testing.T) {
	oracle := readTestFauxOracle(t)
	if oracle.Pi != "0.99.2" {
		t.Fatalf("oracle pins Pi %s", oracle.Pi)
	}
	for _, result := range oracle.Results {
		for _, direct := range result.Direct {
			if direct.Mode != "direct" && direct.Mode != "ticks" {
				continue
			}
			t.Run(fmt.Sprintf("%s/%s%d", result.Scenario.Name, direct.Mode, direct.Layers), func(t *testing.T) {
				t.Parallel()
				var got []json.RawMessage
				if direct.Mode == "ticks" {
					got = testFauxTickObservation(t, result.Scenario.Prompt)
				} else {
					got = testFauxDirectObservation(t, result.Scenario.Prompt, direct.Layers)
				}
				want, have := fauxOracleComparable(t, direct.Records), fauxOracleComparable(t, got)
				if !bytes.Equal(want, have) {
					t.Errorf("observation differs from Pi:\n%s", gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath(t.Name()), string(want), string(have))))
				}
			})
		}
	}
}
