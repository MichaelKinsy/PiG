package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

const fauxOraclePath = "../coding/testdata/rpc33-observation/providers/faux/pi.json"

type fauxOracleFixture struct {
	Name            string  `json:"name"`
	TokensPerSecond float64 `json:"tokensPerSecond"`
	TokenSize       int     `json:"tokenSize"`
	NoResponse      bool    `json:"noResponse"`
	Factory         string  `json:"factory"`
	Content         []struct {
		Type      string         `json:"type"`
		Text      string         `json:"text"`
		Thinking  string         `json:"thinking"`
		ID        string         `json:"id"`
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"content"`
	Message struct {
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
		ResponseID   string `json:"responseId"`
		Timestamp    *int64 `json:"timestamp"`
	} `json:"message"`
}

type fauxOracle struct {
	Pi       string `json:"pi"`
	Deferred struct {
		Records [][]int           `json:"records"`
		Results []json.RawMessage `json:"results"`
	} `json:"deferred"`
	Results []struct {
		Fixture fauxOracleFixture `json:"fixture"`
		Direct  []struct {
			Layers  int               `json:"layers"`
			Mode    string            `json:"mode"`
			Records []json.RawMessage `json:"records"`
		} `json:"direct"`
	} `json:"results"`
}

func readFauxOracle(t testing.TB) fauxOracle {
	t.Helper()
	data, err := os.ReadFile(fauxOraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var oracle fauxOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// provider builds the Go faux provider for one oracle fixture. Pi's fixture is packages/ai/src/providers/faux.ts with tokenSize {min,max} = 1.
func (fixture fauxOracleFixture) provider() *fauxProvider {
	provider := NewFauxProvider(FauxConfig{API: "faux-probe", ProviderID: "faux-probe", MinTokenSize: fixture.TokenSize, MaxTokenSize: fixture.TokenSize, TokensPerSecond: int(fixture.TokensPerSecond)})
	if fixture.NoResponse {
		return provider
	}
	response := FauxResponse{StopReason: fixture.Message.StopReason, ErrorMessage: fixture.Message.ErrorMessage, ResponseID: fixture.Message.ResponseID}
	one := int64(1)
	response.Timestamp = &one
	for _, block := range fixture.Content {
		switch block.Type {
		case "text":
			response.Content = append(response.Content, FauxText(block.Text))
		case "thinking":
			response.Content = append(response.Content, FauxThinking(block.Thinking))
		default:
			response.Content = append(response.Content, FauxToolCall(block.Name, block.Arguments, block.ID))
		}
	}
	switch fixture.Factory {
	case "value":
		provider.SetResponses([]FauxResponseStep{FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (FauxResponse, error) {
			return response, nil
		})})
	case "reject":
		provider.SetResponses([]FauxResponseStep{FauxFactoryStep(func(TranscriptContext, StreamOptions, *FauxProviderState, *Model) (FauxResponse, error) {
			return FauxResponse{}, errors.New("scripted factory failure")
		})})
	default:
		provider.SetResponses([]FauxResponseStep{FauxStaticStep(response)})
	}
	return provider
}

// fauxOracleComparable removes only clocks: an assistant message's timestamp. Everything else, including usage and key presence, must match.
func fauxOracleComparable(t testing.TB, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	var canonicalize func(any)
	canonicalize = func(value any) {
		switch value := value.(type) {
		case []any:
			for _, item := range value {
				canonicalize(item)
			}
		case map[string]any:
			if handle, ok := value["deferred"].(map[string]any); ok {
				// The handle id embeds a clock and a random draw.
				handle["id"] = "ID"
			}
			if value["role"] == "assistant" {
				if _, ok := value["timestamp"].(json.Number); ok {
					value["timestamp"] = json.Number("0")
				}
			}
			for key, item := range value {
				if key != "arguments" {
					canonicalize(item)
				}
			}
		}
	}
	canonicalize(decoded)
	out, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// fauxSyncSegment runs body as the synchronous segment of the probe's main script: it owns the executor from stream creation until its first await, so the provider's queued body cannot start earlier, as in a single JavaScript thread.
func fauxSyncSegment(ctx context.Context, body func(ctx context.Context, turn *continuationTurn)) {
	ctx, executor := withContinuationExecutor(ctx)
	executor.run(func(turn *continuationTurn) { body(ctx, turn) })
}

// fauxDirectObservation replays probe.mjs `direct`: a `for await` consumer behind `layers` lazyStream layers. Each record is a copy at the delivery tick plus the number of faux pushes so far.
func fauxDirectObservation(t testing.TB, fixture fauxOracleFixture, layers int) []json.RawMessage {
	t.Helper()
	provider := fixture.provider()
	var pushed atomic.Int64
	provider.afterPush = func() { pushed.Add(1) }
	model := provider.GetModel()
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "probe"}}, Timestamp: 1}}})
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
		// `for await (const event of stream)` runs in the segment that created the stream.
		for event := range response.Events(context.WithValue(context.WithoutCancel(ctx), continuationTurnKey{}, turn)) {
			record("event", event)
		}
		record("result", awaitContinuation(turn, response.resultContinuation(turn.executor)))
	})
	return records
}

// fauxTickObservation replays probe.mjs `ticks`: a loop that starts in the same synchronous segment as stream creation and records the push count after each microtask.
func fauxTickObservation(t testing.TB, fixture fauxOracleFixture) []json.RawMessage {
	t.Helper()
	provider := fixture.provider()
	var pushed atomic.Int64
	provider.afterPush = func() { pushed.Add(1) }
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "probe"}}, Timestamp: 1}}})
	var records []json.RawMessage
	fauxSyncSegment(t.Context(), func(ctx context.Context, turn *continuationTurn) {
		if _, err := provider.Stream(ctx, transcript, StreamOptions{}); err != nil {
			t.Error(err)
			return
		}
		for range 80 {
			suspendContinuation(turn)
			records = append(records, json.RawMessage(fmt.Sprint(pushed.Load())))
		}
	})
	return records
}

// fauxDeferredObservation replays probe.mjs `deferredTicks`: submission, a pending fetch, the final fetch and an unknown handle, each measured like `ticks`.
func fauxDeferredObservation(t testing.TB) (records [][]int, results []*AssistantMessage) {
	t.Helper()
	provider := NewFauxProvider(FauxConfig{API: "faux-probe", ProviderID: "faux-probe", MinTokenSize: 1, MaxTokenSize: 1, Deferred: &FauxDeferredConfig{PendingFetches: 1, PollAfterMS: new(int64(5))}})
	one := int64(1)
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{FauxText("one two three")}, Timestamp: &one})})
	var pushed atomic.Int64
	provider.afterPush = func() { pushed.Add(1) }
	model := provider.GetModel()
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "probe"}}, Timestamp: 1}}})
	fauxSyncSegment(t.Context(), func(ctx context.Context, turn *continuationTurn) {
		phase := func(create func() (*AssistantMessageEventStream, error)) *AssistantMessage {
			pushed.Store(0)
			stream, err := create()
			if err != nil {
				t.Error(err)
				return nil
			}
			seen := make([]int, 0, 40)
			for range 40 {
				suspendContinuation(turn)
				seen = append(seen, int(pushed.Load()))
			}
			records = append(records, seen)
			return awaitContinuation(turn, stream.resultContinuation(turn.executor))
		}
		submit := phase(func() (*AssistantMessageEventStream, error) {
			return provider.Stream(ctx, transcript, StreamOptions{Deferred: &DeferredOption{Enabled: true}})
		})
		if submit == nil || submit.Deferred == nil {
			t.Errorf("submission produced no handle: %#v", submit)
			return
		}
		handle := *submit.Deferred
		fetch := func(handle DeferredHandle) func() (*AssistantMessageEventStream, error) {
			return func() (*AssistantMessageEventStream, error) {
				return provider.fetchDeferred(ctx, model, handle, DeferredFetchOptions{})
			}
		}
		unknown := handle
		unknown.ID = "unknown"
		results = []*AssistantMessage{submit, phase(fetch(handle)), phase(fetch(handle)), phase(fetch(unknown))}
	})
	return records, results
}

func TestFauxDeferredObservationOracle(t *testing.T) {
	oracle := readFauxOracle(t)
	records, results := fauxDeferredObservation(t)
	if want, have := fauxOracleComparable(t, oracle.Deferred.Records), fauxOracleComparable(t, records); !bytes.Equal(want, have) {
		t.Errorf("deferred tick schedule differs from Pi:\n%s", gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath(t.Name()), string(want), string(have))))
	}
	if want, have := fauxOracleComparable(t, oracle.Deferred.Results), fauxOracleComparable(t, results); !bytes.Equal(want, have) {
		t.Errorf("deferred results differ from Pi:\n%s", gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath(t.Name()), string(want), string(have))))
	}
}

// TestFauxObservationOracle replays Pi's faux provider through 0-2 lazyStream layers and a for-await consumer.
// upstream: packages/ai/src/providers/faux.ts:streamWithDeltas, scheduleChunk; packages/ai/src/api/lazy.ts:31-61.
// The oracle is coding/testdata/rpc33-observation/providers/faux/probe.mjs; each record carries the push count at delivery, so it fixes the tick order.
func TestFauxObservationOracle(t *testing.T) {
	oracle := readFauxOracle(t)
	if oracle.Pi != "0.87.1" {
		t.Fatalf("oracle pins Pi %s", oracle.Pi)
	}
	for _, result := range oracle.Results {
		for _, direct := range result.Direct {
			if direct.Mode != "direct" && direct.Mode != "ticks" {
				continue
			}
			t.Run(fmt.Sprintf("%s/%s%d", result.Fixture.Name, direct.Mode, direct.Layers), func(t *testing.T) {
				t.Parallel()
				var got []json.RawMessage
				if direct.Mode == "ticks" {
					got = fauxTickObservation(t, result.Fixture)
				} else {
					got = fauxDirectObservation(t, result.Fixture, direct.Layers)
				}
				want, have := fauxOracleComparable(t, direct.Records), fauxOracleComparable(t, got)
				if !bytes.Equal(want, have) {
					t.Errorf("observation differs from Pi:\n%s", gotextdiff.ToUnified("Pi", "Go", string(want), myers.ComputeEdits(span.URIFromPath(t.Name()), string(want), string(have))))
				}
			})
		}
	}
}
