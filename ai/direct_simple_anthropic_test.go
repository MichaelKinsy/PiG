package ai

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func TestDirectSimpleAnthropicSelectedModel(t *testing.T) {
	t.Parallel()
	// packages/ai/test/anthropic-force-adaptive-thinking.test.ts:71,78,117: the supplied custom model, not a catalog lookup, owns reasoning and adaptive capability.
	for _, tc := range []struct {
		name     string
		adaptive bool
		level    ThinkingLevel
		thinking string
		output   string
	}{
		{"omitted", true, "", `{"type":"disabled"}`, ""},
		{"off", true, ThinkingOff, `{"type":"disabled"}`, ""},
		{"adaptive", true, ThinkingMedium, `{"type":"adaptive","display":"summarized"}`, `{"effort":"medium"}`},
		{"budget", false, ThinkingMedium, `{"type":"enabled","display":"summarized","budget_tokens":8192}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &Model{ID: "vendor--claude-opus-latest", DisplayName: "Vendor Proxy Opus Latest", Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh, ContextWindow: 200000, MaxOutputTokens: 32000}, Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: "vendor-proxy", API: APIAnthropicMessages, BaseURL: "http://127.0.0.1:9", Reasoning: true, Compat: &ModelCompat{ForceAdaptiveThinking: new(tc.adaptive)}}}
			captured := errors.New("payload captured")
			var payload map[string]json.RawMessage
			stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{APIKey: "fake-key", Thinking: tc.level, OnPayload: func(value any, _ *Model) (any, error) {
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(encoded, &payload); err != nil {
					return nil, err
				}
				return nil, captured
			}})
			// Pi catches onPayload rejection into the stream's terminal assistant result.
			if err != nil || stream == nil {
				t.Fatalf("stream setup=%v, %v", stream, err)
			}
			result := stream.Result()
			if result.StopReason != StopReasonError || result.ErrorMessage != captured.Error() || result.API != APIAnthropicMessages || result.Provider != model.ProviderMeta.ProviderID || result.Model != model.ID {
				t.Fatalf("capture terminal result=%+v", result)
			}
			var eventTypes []AssistantEventType
			for event := range stream.Events(t.Context()) {
				eventTypes = append(eventTypes, event.EventType())
			}
			if !slices.Equal(eventTypes, []AssistantEventType{EventError}) {
				t.Fatalf("capture events=%v, want only error", eventTypes)
			}
			assertShapeJSON(t, payload["thinking"], tc.thinking)
			if tc.output != "" {
				assertShapeJSON(t, payload["output_config"], tc.output)
			} else if value, present := payload["output_config"]; present {
				t.Fatalf("output_config=%s; want absent", value)
			}
		})
	}
}

func BenchmarkDirectSimpleAnthropicPayload(b *testing.B) {
	generated, ok := LookupModelExact("anthropic/claude-sonnet-4-5")
	if !ok {
		b.Fatal("missing model")
	}
	model := generated.ToModel()
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Summarize the change and identify its regression tests.")}}})
	captured := errors.New("payload captured")
	options := StreamOptions{APIKey: "fake-key", OnPayload: func(any, *Model) (any, error) { return nil, captured }}
	b.ReportAllocs()
	for b.Loop() {
		stream, err := StreamSimple(b.Context(), model, transcript, options)
		if err != nil || stream == nil {
			b.Fatalf("capture setup: stream=%v error=%v", stream, err)
		}
		if result := stream.Result(); result.StopReason != StopReasonError || result.ErrorMessage != captured.Error() {
			b.Fatalf("capture terminal result=%+v", result)
		}
	}
}
