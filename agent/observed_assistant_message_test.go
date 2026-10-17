package agent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func observedMessageCases() map[string]*ai.AssistantMessage {
	endTurn := true
	duration := int64(12)
	reasoning := 3
	return map[string]*ai.AssistantMessage{
		"nil content":   {API: "api", Provider: "p", Model: "m", Timestamp: 7},
		"empty content": {Content: []ai.AssistantContentBlock{}, Diagnostics: []ai.AssistantMessageDiagnostic{}, StopReason: ai.StopReasonPending},
		"text and thinking": {
			Content: []ai.AssistantContentBlock{
				ai.ThinkingContent{Thinking: "a", ThinkingSignature: "s1"},
				ai.TextContent{Text: "hello"},
				ai.ThinkingContent{Thinking: "b", ThinkingSignature: "s2"},
			},
			Usage:      ai.Usage{Input: 1, Output: 2, Reasoning: &reasoning},
			EndTurn:    &endTurn,
			DurationMs: &duration,
		},
		"tool call and diagnostics": {
			Content:     []ai.AssistantContentBlock{ai.ToolCall{ID: "c", Name: "read", Arguments: ai.JsonObject{"path": "x", "n": []any{1.0, "y"}}}},
			Diagnostics: []ai.AssistantMessageDiagnostic{{Type: "d", Details: map[string]any{"k": "v"}}},
			StopReason:  ai.StopReasonToolUse, ErrorMessage: "e", RawStopReason: "raw",
		},
	}
}

// observedAgentAssistantMessage takes an owned observation's slices instead of copying them; the agent message it builds equals agentAssistantMessage's for the same observation, and changing it does not reach the source message.
func TestObservedAgentAssistantMessageEqualsTheCopyingConversion(t *testing.T) {
	for name, source := range observedMessageCases() {
		t.Run(name, func(t *testing.T) {
			want := agentAssistantMessage(source.Observe())
			got := observedAgentAssistantMessage(source.Observe())
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("observed conversion\n%#v\nwant\n%#v", got, want)
			}
			before := source.Observe()
			if len(got.Content) > 0 {
				got.Content[0] = ai.TextContent{Text: "changed"}
			}
			got.Usage.Input = 99
			if after := source.Observe(); !reflect.DeepEqual(after, before) {
				t.Fatalf("changing the agent message changed its source: %#v, was %#v", after, before)
			}
		})
	}
	if got, want := observedAgentAssistantMessage(nil), agentAssistantMessage(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("nil: %#v, want %#v", got, want)
	}
}
