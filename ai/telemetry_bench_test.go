package ai

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// benchmarkModelsStream drives one Models.Stream of a text, thinking and tool-call response through the lazy forwarding
// path; withTelemetry adds a recording parent span.
func benchmarkModelsStream(b *testing.B, withTelemetry bool) {
	model := &Model{ID: "model", DisplayName: "Model", ProviderMeta: ProviderMetadata{API: "telemetry-test", ProviderID: "test", BaseURL: "https://example.test"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
	result := &AssistantMessage{API: "telemetry-test", Provider: "test", Model: "model", Content: []AssistantContentBlock{TextContent{Text: "text-secret"}, ThinkingContent{Thinking: "thinking-secret"}, ToolCall{ID: "call-secret", Name: "lookup", Arguments: JsonObject{"value": "argument-secret"}}}, Usage: Usage{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, TotalTokens: 110, Cost: UsageCost{Total: 1}}, ResponseID: "response", StopReason: StopReasonStop}
	events := []AssistantMessageEvent{
		StartEvent{Partial: result},
		TextDeltaEvent{ContentIndex: 0, Delta: "text-secret", Partial: result},
		TextEndEvent{ContentIndex: 0, Content: "text-secret", Partial: result},
		ThinkingEndEvent{ContentIndex: 1, Content: "thinking-secret", Partial: result},
		ToolCallDeltaEvent{ContentIndex: 2, Delta: `{"value":`, Partial: result},
		ToolCallDeltaEvent{ContentIndex: 2, Delta: `"argument-secret"}`, Partial: result},
		ToolCallEndEvent{ContentIndex: 2, ToolCall: result.Content[2].(ToolCall), Partial: result},
		DoneEvent{Reason: StopReasonStop, Message: result},
	}
	models := CreateModels(CreateModelsOptions{})
	defer models.Close()
	respond := func(context.Context, *Model, TranscriptContext, StreamOptions) (*AssistantMessageEventStream, error) {
		stream := NewAssistantMessageEventStream()
		for _, event := range events {
			_ = stream.Push(event)
		}
		stream.End(result)
		return stream, nil
	}
	models.SetProvider(CreateProvider(CreateProviderOptions{ID: "test", Models: []AnyModel{model}, Auth: configuredTestAuth(), API: &ProviderStreams{Stream: respond, StreamSimple: respond}}))
	var options StreamOptions
	var recorder *telemetry.InMemoryTelemetryContext
	if withTelemetry {
		recorder = telemetry.NewInMemoryTelemetryContext()
	}
	request := Context{Messages: []Message{}}
	b.ReportAllocs()
	for b.Loop() {
		if withTelemetry {
			_ = recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) error {
				options.Telemetry = span
				models.Stream(b.Context(), model, request, options).Result()
				return nil
			})
			continue
		}
		models.Stream(b.Context(), model, request, options).Result()
	}
}

func BenchmarkModelsStreamNoTelemetry(b *testing.B)   { benchmarkModelsStream(b, false) }
func BenchmarkModelsStreamWithTelemetry(b *testing.B) { benchmarkModelsStream(b, true) }
