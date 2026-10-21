package ai

// Ports packages/ai/test/telemetry.test.ts.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

func aiTelemetryModel() *Model {
	return &Model{
		ID: "model", DisplayName: "Model",
		ProviderMeta: ProviderMetadata{API: "telemetry-test", ProviderID: "test", BaseURL: "https://example.test"},
		Input:        []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100},
	}
}

func aiTelemetryHandle(model *Model) DeferredHandle {
	return DeferredHandle{Provider: model.ProviderMeta.ProviderID, ModelID: model.ID, API: model.ProviderMeta.API, ID: "deferred-secret"}
}

func aiTelemetryMessage(stopReason StopReason) *AssistantMessage {
	return &AssistantMessage{
		API: "telemetry-test", Provider: "test", Model: "model",
		Content: []AssistantContentBlock{
			TextContent{Text: "text-secret"},
			ThinkingContent{Thinking: "thinking-secret"},
			ToolCall{ID: "call-secret", Name: "lookup", Arguments: JsonObject{"value": "argument-secret"}},
		},
		Usage:      Usage{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, TotalTokens: 110, Cost: UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1}},
		ResponseID: "response", StopReason: stopReason, Timestamp: time.Now().UnixMilli(),
	}
}

func aiTelemetryEvents(result *AssistantMessage) []AssistantMessageEvent {
	toolCall := result.Content[2].(ToolCall)
	var terminal AssistantMessageEvent
	switch result.StopReason {
	case StopReasonError, StopReasonAborted:
		terminal = ErrorEvent{Reason: result.StopReason, Error: result}
	case StopReasonPending:
		terminal = DoneEvent{Reason: StopReasonDeferred, Message: result}
	default:
		terminal = DoneEvent{Reason: result.StopReason, Message: result}
	}
	return []AssistantMessageEvent{
		StartEvent{Partial: result},
		TextDeltaEvent{ContentIndex: 0, Delta: "text-secret", Partial: result},
		TextEndEvent{ContentIndex: 0, Content: "text-secret", Partial: result},
		ThinkingEndEvent{ContentIndex: 1, Content: "thinking-secret", Partial: result},
		ToolCallDeltaEvent{ContentIndex: 2, Delta: `{"value":`, Partial: result},
		ToolCallDeltaEvent{ContentIndex: 2, Delta: `"argument-secret"}`, Partial: result},
		ToolCallEndEvent{ContentIndex: 2, ToolCall: toolCall, Partial: result},
		terminal,
	}
}

func aiTelemetrySetup(t *testing.T, result *AssistantMessage) *Models {
	t.Helper()
	model := aiTelemetryModel()
	respond := func() (*AssistantMessageEventStream, error) {
		stream := NewAssistantMessageEventStream()
		for _, event := range aiTelemetryEvents(result) {
			if err := stream.Push(event); err != nil {
				return nil, err
			}
		}
		stream.End(result)
		return stream, nil
	}
	models := CreateModels(CreateModelsOptions{})
	t.Cleanup(models.Close)
	models.SetProvider(CreateProvider(CreateProviderOptions{
		ID: model.ProviderMeta.ProviderID, Models: []AnyModel{model}, Auth: configuredTestAuth(),
		API: &ProviderStreams{
			Stream: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
				return respond()
			},
			StreamSimple: func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
				return respond()
			},
			FetchDeferred: func(context.Context, *Model, DeferredHandle, DeferredFetchOptions) (*AssistantMessageEventStream, error) {
				return respond()
			},
		},
	}))
	return models
}

// aiTelemetrySettled is vi.waitFor(() => every span settled).
func aiTelemetrySettled(t *testing.T, recorder *telemetry.InMemoryTelemetryContext) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		settled := true
		for _, span := range recorder.GetSpans() {
			settled = settled && span.Settled
		}
		if settled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("spans did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

func aiTelemetryCaller[T any](t *testing.T, recorder *telemetry.InMemoryTelemetryContext, body func(span telemetry.TelemetrySpan) T) T {
	t.Helper()
	result, err := telemetry.StartSpan(recorder, telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) (T, error) { return body(span), nil })
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func aiTelemetryJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Ports telemetry.test.ts "observes %s without changing delivered events or results".
func TestAITelemetryObservesWithoutChangingDeliveredEventsOrResultsUpstream(t *testing.T) {
	for _, method := range []string{"stream", "streamSimple", "streamDeferred"} {
		t.Run(method, func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			result := aiTelemetryMessage(StopReasonStop)
			models := aiTelemetrySetup(t, result)
			model := aiTelemetryModel()
			streamEvents := aiTelemetryEvents(result)
			var received []AssistantMessageEvent
			var final *AssistantMessage
			aiTelemetryCaller(t, recorder, func(parent telemetry.TelemetrySpan) struct{} {
				var stream *AssistantMessageEventStream
				switch method {
				case "streamDeferred":
					stream = models.StreamDeferred(t.Context(), model, aiTelemetryHandle(model), DeferredFetchOptions{StreamOptions: StreamOptions{Telemetry: parent}})
				case "stream":
					stream = models.Stream(t.Context(), model, Context{Messages: []Message{}}, StreamOptions{Telemetry: parent, Thinking: "high"})
				default:
					stream = models.StreamSimple(t.Context(), model, Context{Messages: []Message{}}, StreamOptions{Telemetry: parent, Thinking: "high"})
				}
				for event := range stream.Events(t.Context()) {
					received = append(received, event)
				}
				final = stream.Result()
				return struct{}{}
			})
			if len(received) != len(streamEvents) {
				t.Fatalf("received %d events, want %d", len(received), len(streamEvents))
			}
			for i := range received {
				if received[i].EventType() != streamEvents[i].EventType() {
					t.Fatalf("event %d = %s, want %s", i, received[i].EventType(), streamEvents[i].EventType())
				}
			}
			if final.StopReason != StopReasonStop || final.ResponseID != "response" {
				t.Fatalf("result = %+v", final)
			}
			aiTelemetrySettled(t, recorder)
			spans := recorder.GetSpans()
			parent, span := spans[0], spans[1]
			wantName := "pi.ai.request"
			if method == "streamDeferred" {
				wantName = "pi.ai.poll"
			}
			if span.Name != wantName || span.ParentID == nil || *span.ParentID != parent.ID {
				t.Fatalf("span = %q parent %v, want %q under %d", span.Name, span.ParentID, wantName, parent.ID)
			}
			// Pi's order: the start attributes (provider, model, api, then the scalar reasoning), then the end attributes.
			want := telemetry.SpanAttributes{telemetry.Attr("provider", "test"), telemetry.Attr("model", "model"), telemetry.Attr("api", "telemetry-test")}
			if method != "streamDeferred" {
				want = append(want, telemetry.Attr("reasoning", "high"))
			}
			want = append(want, telemetry.Attr("stop", "stop"), telemetry.Attr("usage.input", 11), telemetry.Attr("usage.output", 22), telemetry.Attr("usage.cache_read", 33), telemetry.Attr("usage.cache_write", 44), telemetry.Attr("cost.total", 1.0), telemetry.Attr("response.id", "response"))
			if !reflect.DeepEqual(span.Attributes, want) {
				t.Fatalf("attributes = %#v, want %#v", span.Attributes, want)
			}
			wantEvents := []telemetry.RecordedTelemetryEvent{
				{Name: "pi.ai.first_chunk", Attributes: telemetry.SpanAttributes{}},
				{Name: "pi.ai.block", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "text"), telemetry.Attr("index", 0), telemetry.Attr("chars", 11)}},
				{Name: "pi.ai.block", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "thinking"), telemetry.Attr("index", 1), telemetry.Attr("chars", 15)}},
				{Name: "pi.ai.block", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "tool_call"), telemetry.Attr("index", 2), telemetry.Attr("chars", 27), telemetry.Attr("tool", "lookup")}},
			}
			if !reflect.DeepEqual(span.Events, wantEvents) {
				t.Fatalf("events = %#v, want %#v", span.Events, wantEvents)
			}
			for _, secret := range []string{"text-secret", "thinking-secret", "argument-secret", "call-secret", "deferred-secret"} {
				if strings.Contains(aiTelemetryJSON(t, span), secret) {
					t.Fatalf("span records %q", secret)
				}
			}
			measurements := recorder.GetMetrics()
			if len(measurements) != 7 {
				t.Fatalf("measurements = %d, want 7", len(measurements))
			}
			var counters []float64
			for _, measurement := range measurements {
				if measurement.Kind == telemetry.TelemetryMetricKindCounter {
					counters = append(counters, measurement.Value)
				}
				var keys []string
				for _, attribute := range measurement.Attributes {
					keys = append(keys, attribute.Name)
				}
				wantKeys := map[string][]string{"pi.ai.tokens": {"provider", "model", "kind"}, "pi.ai.request.duration": {"provider", "model", "stop"}}[measurement.Name]
				if wantKeys == nil {
					wantKeys = []string{"provider", "model"}
				}
				if !reflect.DeepEqual(keys, wantKeys) {
					t.Fatalf("%s attribute order = %v, want %v", measurement.Name, keys, wantKeys)
				}
				if measurement.Kind == telemetry.TelemetryMetricKindHistogram && !reflect.DeepEqual(measurement.Context, span.Context) {
					t.Fatalf("%s exemplar = %v, want %v", measurement.Name, measurement.Context, span.Context)
				}
			}
			if !reflect.DeepEqual(counters, []float64{11, 22, 33, 44, 1}) {
				t.Fatalf("counters = %v", counters)
			}
		})
	}
}

// Ports telemetry.test.ts "reports %s status without error attributes or events".
func TestAITelemetryReportsErrorAndAbortedStatusUpstream(t *testing.T) {
	for _, reason := range []StopReason{StopReasonError, StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			result := aiTelemetryMessage(reason)
			result.ErrorMessage = "error-secret"
			models := aiTelemetrySetup(t, result)
			got := aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) *AssistantMessage {
				return models.CompleteSimple(t.Context(), aiTelemetryModel(), Context{Messages: []Message{}}, StreamOptions{Telemetry: span})
			})
			if got.StopReason != reason || got.ErrorMessage != "error-secret" {
				t.Fatalf("result = %+v", got)
			}
			aiTelemetrySettled(t, recorder)
			span := recorder.GetSpans()[1]
			want := telemetry.SpanStatus{Status: telemetry.SpanStatusCodeOK}
			if reason == StopReasonError {
				want = telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "provider_error", Message: "error-secret"}}
			}
			if !reflect.DeepEqual(span.Status, want) {
				t.Fatalf("status = %+v, want %+v", span.Status, want)
			}
			if span.Attributes.Value("stop") != string(reason) {
				t.Fatalf("stop = %v", span.Attributes.Value("stop"))
			}
			if strings.Contains(aiTelemetryJSON(t, span.Attributes), "error-secret") || strings.Contains(aiTelemetryJSON(t, span.Events), "error-secret") {
				t.Fatal("error message leaked into attributes or events")
			}
			if reason == StopReasonAborted && strings.Contains(aiTelemetryJSON(t, span), "error-secret") {
				t.Fatal("aborted span records the error message")
			}
		})
	}
}

// Ports telemetry.test.ts "bounds poll error messages, including missing messages (missing=%s)".
func TestAITelemetryBoundsPollErrorMessagesUpstream(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%v", missing), func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			result := aiTelemetryMessage(StopReasonError)
			if !missing {
				result.ErrorMessage = strings.Repeat("x", 500) + "discarded-tail"
			}
			models := aiTelemetrySetup(t, result)
			model := aiTelemetryModel()
			got := aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) *AssistantMessage {
				return models.FetchDeferred(t.Context(), model, aiTelemetryHandle(model), DeferredFetchOptions{StreamOptions: StreamOptions{Telemetry: span}})
			})
			if got.StopReason != StopReasonError {
				t.Fatalf("result = %+v", got)
			}
			aiTelemetrySettled(t, recorder)
			span := recorder.GetSpans()[1]
			wantMessage := ""
			if !missing {
				wantMessage = strings.Repeat("x", 500)
			}
			want := telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "provider_error", Message: wantMessage}}
			if !reflect.DeepEqual(span.Status, want) {
				t.Fatalf("status = %+v", span.Status)
			}
			if strings.Contains(aiTelemetryJSON(t, span), "discarded-tail") || strings.Contains(aiTelemetryJSON(t, span.Attributes), "xxx") || strings.Contains(aiTelemetryJSON(t, span.Events), "xxx") {
				t.Fatal("error message leaked beyond the status bound")
			}
		})
	}
}

// Ports telemetry.test.ts "includes setup failures and omits non-scalar native reasoning". Pi's native reasoning object
// has no Go field of the string option's name (StreamOptions.Thinking is the scalar level and GoogleThinking carries the
// provider-native object), so the object is passed through GoogleThinking and the empty scalar omits the attribute.
func TestAITelemetryIncludesSetupFailuresAndOmitsNonScalarReasoningUpstream(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	result := aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) *AssistantMessage {
		return models.Complete(t.Context(), aiTelemetryModel(), Context{Messages: []Message{}}, StreamOptions{Telemetry: span, GoogleThinking: &GoogleThinkingOptions{}})
	})
	if result.StopReason != StopReasonError {
		t.Fatalf("stop = %s", result.StopReason)
	}
	aiTelemetrySettled(t, recorder)
	span := recorder.GetSpans()[1]
	if _, ok := span.Attributes.Get("reasoning"); ok {
		t.Fatalf("attributes = %#v", span.Attributes)
	}
	if !reflect.DeepEqual(span.Events, []telemetry.RecordedTelemetryEvent{{Name: "pi.ai.first_chunk", Attributes: telemetry.SpanAttributes{}}}) {
		t.Fatalf("events = %#v", span.Events)
	}
	want := telemetry.SpanStatus{Status: telemetry.SpanStatusCodeError, Error: &telemetry.SpanStatusError{Name: "provider_error", Message: "Unknown provider: test"}}
	if !reflect.DeepEqual(span.Status, want) {
		t.Fatalf("status = %+v", span.Status)
	}
	if strings.Contains(aiTelemetryJSON(t, span.Attributes), "Unknown provider") {
		t.Fatal("setup error leaked into attributes")
	}
}

func aiTelemetryLaunch(body func()) { go body() }

// Ports telemetry.test.ts "counts interleaved tool deltas independently without reading arguments". Go's ToolCall has
// no accessor that can throw, so the call carries no arguments at all; observeModelRequest reads only its name.
func TestAITelemetryCountsInterleavedToolDeltasIndependentlyUpstream(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	result := aiTelemetryMessage(StopReasonStop)
	aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) struct{} {
		observer := observeModelRequest(aiTelemetryModel(), &StreamOptions{Telemetry: span}, false, aiTelemetryLaunch)
		toolCall := ToolCall{ID: "call", Name: "lookup"}
		for _, delta := range []struct {
			index int
			text  string
		}{{2, "ab"}, {3, "123"}, {2, "c"}} {
			observer.OnEvent(ToolCallDeltaEvent{ContentIndex: delta.index, Delta: delta.text, Partial: result})
		}
		for _, index := range []int{3, 2, 4} {
			observer.OnEvent(ToolCallEndEvent{ContentIndex: index, ToolCall: toolCall, Partial: result})
		}
		observer.OnResult(result)
		return struct{}{}
	})
	aiTelemetrySettled(t, recorder)
	want := []telemetry.RecordedTelemetryEvent{
		{Name: "pi.ai.block", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "tool_call"), telemetry.Attr("index", 3), telemetry.Attr("chars", 3), telemetry.Attr("tool", "lookup")}},
		{Name: "pi.ai.block", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "tool_call"), telemetry.Attr("index", 2), telemetry.Attr("chars", 3), telemetry.Attr("tool", "lookup")}},
		{Name: "pi.ai.block", Attributes: telemetry.SpanAttributes{telemetry.Attr("type", "tool_call"), telemetry.Attr("index", 4), telemetry.Attr("chars", 0), telemetry.Attr("tool", "lookup")}},
	}
	if got := recorder.GetSpans()[1].Events[1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v", got)
	}
}

// metricsLessSpan is `{ ...span, metrics: undefined }`.
type metricsLessSpan struct{ telemetry.TelemetrySpan }

func (metricsLessSpan) Metrics() telemetry.TelemetryMetrics { return nil }

// Ports telemetry.test.ts "supports a tracing-only span without metric capabilities".
func TestAITelemetrySupportsTracingOnlySpanUpstream(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	models := aiTelemetrySetup(t, aiTelemetryMessage(StopReasonStop))
	aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) struct{} {
		models.CompleteSimple(t.Context(), aiTelemetryModel(), Context{Messages: []Message{}}, StreamOptions{Telemetry: metricsLessSpan{span}})
		return struct{}{}
	})
	aiTelemetrySettled(t, recorder)
	if len(recorder.GetSpans()) != 2 {
		t.Fatalf("spans = %d", len(recorder.GetSpans()))
	}
	if metrics := recorder.GetMetrics(); len(metrics) != 0 {
		t.Fatalf("metrics = %+v", metrics)
	}
}

// Ports telemetry.test.ts "defaults to no-op telemetry".
func TestAITelemetryDefaultsToNoopUpstream(t *testing.T) {
	models := aiTelemetrySetup(t, aiTelemetryMessage(StopReasonStop))
	if got := models.CompleteSimple(t.Context(), aiTelemetryModel(), Context{Messages: []Message{}}); got.StopReason != StopReasonStop {
		t.Fatalf("stop = %s", got.StopReason)
	}
	if !AITelemetrySchema().Spans["pi.ai.request"].StartAttributes["provider"].Required {
		t.Fatal("provider is not a required start attribute")
	}
}

// Ports telemetry.test.ts "records low-cardinality metrics with monotonic durations".
func TestAITelemetryRecordsLowCardinalityMetricsWithMonotonicDurationsUpstream(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	result := aiTelemetryMessage(StopReasonStop)
	clock, restore := jsclock.MockPerformanceNow(100)
	defer restore()
	aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) struct{} {
		observer := observeModelRequest(aiTelemetryModel(), &StreamOptions{Telemetry: span}, false, aiTelemetryLaunch)
		clock.Set(125)
		observer.OnEvent(StartEvent{Partial: result})
		clock.Set(175)
		observer.OnResult(result)
		return struct{}{}
	})
	aiTelemetrySettled(t, recorder)
	type measured struct {
		Name       string
		Value      float64
		Attributes telemetry.SpanAttributes
		Unit       string
	}
	var got []measured
	metrics := recorder.GetMetrics()
	for _, m := range metrics {
		got = append(got, measured{m.Name, m.Value, m.Attributes, *m.Unit})
	}
	base := func(extra ...string) telemetry.SpanAttributes {
		attributes := telemetry.SpanAttributes{telemetry.Attr("provider", "test"), telemetry.Attr("model", "model")}
		if len(extra) == 2 {
			attributes = append(attributes, telemetry.Attr(extra[0], extra[1]))
		}
		return attributes
	}
	want := []measured{
		{"pi.ai.first_chunk.duration", 25, base(), "ms"},
		{"pi.ai.request.duration", 75, base("stop", "stop"), "ms"},
		{"pi.ai.tokens", 11, base("kind", "input"), "token"},
		{"pi.ai.tokens", 22, base("kind", "output"), "token"},
		{"pi.ai.tokens", 33, base("kind", "cache_read"), "token"},
		{"pi.ai.tokens", 44, base("kind", "cache_write"), "token"},
		{"pi.ai.cost", 1, base(), "USD"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metrics = %#v, want %#v", got, want)
	}
	for _, m := range metrics[:2] {
		if m.Context == nil {
			t.Fatalf("%s has no exemplar", m.Name)
		}
	}
}

// Ports telemetry.test.ts "records usage for a %s poll only when final".
func TestAITelemetryRecordsUsageForPollOnlyWhenFinalUpstream(t *testing.T) {
	for _, reason := range []StopReason{StopReasonPending, StopReasonDeferred, StopReasonStop, StopReasonError} {
		t.Run(string(reason), func(t *testing.T) {
			recorder := telemetry.NewInMemoryTelemetryContext()
			models := aiTelemetrySetup(t, aiTelemetryMessage(reason))
			model := aiTelemetryModel()
			aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) *AssistantMessage {
				return models.FetchDeferred(t.Context(), model, aiTelemetryHandle(model), DeferredFetchOptions{StreamOptions: StreamOptions{Telemetry: span}})
			})
			aiTelemetrySettled(t, recorder)
			counters := 0
			for _, m := range recorder.GetMetrics() {
				if m.Kind == telemetry.TelemetryMetricKindCounter {
					counters++
				}
			}
			want := 5
			if reason == StopReasonPending || reason == StopReasonDeferred {
				want = 0
			}
			if counters != want {
				t.Fatalf("counters = %d, want %d", counters, want)
			}
		})
	}
}

// countingMetrics is vi.spyOn(telemetry, "histogram"/"counter"): it records instrument creation and delegates.
type countingMetrics struct {
	telemetry.TelemetryMetrics
	mu                   sync.Mutex
	histograms, counters []string
}

func (c *countingMetrics) Counter(name string, options telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	c.mu.Lock()
	c.counters = append(c.counters, name)
	c.mu.Unlock()
	return c.TelemetryMetrics.Counter(name, options)
}

func (c *countingMetrics) Histogram(name string, options telemetry.HistogramOptions) telemetry.TelemetryHistogram {
	c.mu.Lock()
	c.histograms = append(c.histograms, name)
	c.mu.Unlock()
	return c.TelemetryMetrics.Histogram(name, options)
}

type countingSpan struct {
	telemetry.TelemetrySpan
	metrics *countingMetrics
}

func (s countingSpan) Metrics() telemetry.TelemetryMetrics { return s.metrics }

// Ports telemetry.test.ts "creates instruments once per metrics object across root and child requests".
func TestAITelemetryCreatesInstrumentsOncePerMetricsObjectUpstream(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	metrics := &countingMetrics{TelemetryMetrics: recorder}
	models := aiTelemetrySetup(t, aiTelemetryMessage(StopReasonStop))
	model := aiTelemetryModel()
	aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) struct{} {
		models.CompleteSimple(t.Context(), model, Context{Messages: []Message{}}, StreamOptions{Telemetry: countingSpan{span, metrics}})
		return struct{}{}
	})
	aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) struct{} {
		models.FetchDeferred(t.Context(), model, aiTelemetryHandle(model), DeferredFetchOptions{StreamOptions: StreamOptions{Telemetry: countingSpan{span, metrics}}})
		return struct{}{}
	})
	aiTelemetryCaller(t, recorder, func(span telemetry.TelemetrySpan) struct{} {
		models.Complete(t.Context(), model, Context{Messages: []Message{}}, StreamOptions{Telemetry: countingSpan{span, metrics}})
		return struct{}{}
	})
	aiTelemetrySettled(t, recorder)
	if want := []string{"pi.ai.request.duration", "pi.ai.first_chunk.duration"}; !reflect.DeepEqual(metrics.histograms, want) {
		t.Fatalf("histograms = %v, want %v", metrics.histograms, want)
	}
	if want := []string{"pi.ai.tokens", "pi.ai.cost"}; !reflect.DeepEqual(metrics.counters, want) {
		t.Fatalf("counters = %v, want %v", metrics.counters, want)
	}
	if got := len(recorder.GetMetrics()); got != 21 {
		t.Fatalf("measurements = %d, want 21", got)
	}
}

func lazyObserverSource(result *AssistantMessage) *AssistantMessageEventStream {
	source := NewAssistantMessageEventStream()
	_ = source.Push(DoneEvent{Reason: StopReasonStop, Message: result})
	return source
}

// Ports telemetry.test.ts "uses one source iterator and observes after push, once per terminal result". Pi's source is
// an arbitrary async iterable whose iterators are counted; Go's source is one FIFO stream that only the forwarder
// consumes, so the single-iterator assertion has no Go form. The `.then` registered inside onEvent is a result
// continuation on the outer stream's executor.
func TestLazyStreamObservationOrdersEventResolvedResultUpstream(t *testing.T) {
	result := aiTelemetryMessage(StopReasonStop)
	var mu sync.Mutex
	var order []string
	record := func(entry string) {
		mu.Lock()
		order = append(order, entry)
		mu.Unlock()
	}
	var stream *AssistantMessageEventStream
	stream = LazyStream(t.Context(), aiTelemetryModel(), func(context.Context) (*AssistantMessageEventStream, error) {
		return lazyObserverSource(result), nil
	}, LazyStreamObserver{
		OnEvent: func(AssistantMessageEvent) {
			record("event")
			stream.resultContinuation(stream.executor).onResolved(func(*AssistantMessage) { record("resolved") })
		},
		OnResult: func(received *AssistantMessage) {
			if received == nil || received.ResponseID != "response" {
				t.Errorf("OnResult = %+v", received)
			}
			record("result")
		},
	})
	if got := stream.Result(); got.ResponseID != "response" {
		t.Fatalf("result = %+v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), order...)
		mu.Unlock()
		if reflect.DeepEqual(got, []string{"event", "resolved", "result"}) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("order = %v, want [event resolved result]", got)
		}
		time.Sleep(time.Millisecond)
	}
}

// Ports telemetry.test.ts "reports a terminal result from an iterable without a result method" and "observes a
// result-only source once". Go's source is always a stream with a Result: the terminal event is the first and a result
// without events the second; each is observed once.
func TestLazyStreamObservationReportsTerminalResultOnceUpstream(t *testing.T) {
	for name, source := range map[string]func(*AssistantMessage) *AssistantMessageEventStream{
		"terminal event": lazyObserverSource,
		"result only": func(result *AssistantMessage) *AssistantMessageEventStream {
			stream := NewAssistantMessageEventStream()
			stream.End(result)
			return stream
		},
	} {
		t.Run(name, func(t *testing.T) {
			result := aiTelemetryMessage(StopReasonStop)
			observed := make(chan *AssistantMessage, 4)
			stream := LazyStream(t.Context(), aiTelemetryModel(), func(context.Context) (*AssistantMessageEventStream, error) {
				return source(result), nil
			}, LazyStreamObserver{OnResult: func(value *AssistantMessage) { observed <- value }})
			if got := stream.Result(); got.ResponseID != "response" {
				t.Fatalf("result = %+v", got)
			}
			select {
			case got := <-observed:
				if got == nil || got.ResponseID != "response" {
					t.Fatalf("observed %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("result not observed")
			}
			select {
			case extra := <-observed:
				t.Fatalf("result observed twice: %+v", extra)
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

// Ports telemetry.test.ts "reports setup failure events and the result once".
func TestLazyStreamObservationReportsSetupFailureOnceUpstream(t *testing.T) {
	var mu sync.Mutex
	var observed []string
	stream := LazyStream(t.Context(), aiTelemetryModel(), func(context.Context) (*AssistantMessageEventStream, error) {
		return nil, fmt.Errorf("setup-secret")
	}, LazyStreamObserver{
		OnEvent: func(event AssistantMessageEvent) {
			mu.Lock()
			observed = append(observed, string(event.EventType()))
			mu.Unlock()
		},
		OnResult: func(result *AssistantMessage) {
			mu.Lock()
			observed = append(observed, string(result.StopReason))
			mu.Unlock()
		},
	})
	if got := stream.Result(); got.ErrorMessage != "setup-secret" {
		t.Fatalf("result = %+v", got)
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(observed, []string{"error", "error"}) {
		t.Fatalf("observed = %v", observed)
	}
}
