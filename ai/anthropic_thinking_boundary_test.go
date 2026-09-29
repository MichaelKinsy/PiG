package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// upstream: packages/ai/src/api/anthropic-messages.ts:889-902,1175. Only zero takes the raw-options fallback; a positive budget below 1024 is retained.
func TestAnthropicThinkingBudgetCapBoundaryUpstream(t *testing.T) {
	for _, tc := range []struct{ cap, wantMax, wantBudget int }{
		{512, 512, 1024}, {1024, 1024, 1024}, {1025, 1025, 1},
		{2048, 2048, 1024}, {20000, 16896, 16384},
	} {
		t.Run(fmt.Sprint(tc.cap), func(t *testing.T) {
			model := &Model{ProviderMeta: ProviderMetadata{Reasoning: true}, Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh, MaxOutputTokens: tc.cap}}
			result := thinkingToAnthropicConfig(model, 512, ThinkingHigh)
			if result == nil || result.MaxTokens != tc.wantMax || result.Thinking == nil || result.Thinking.BudgetTokens != tc.wantBudget {
				t.Fatalf("thinking = %#v; want max=%d budget=%d", result, tc.wantMax, tc.wantBudget)
			}
		})
	}
}

// upstream: packages/ai/src/api/anthropic-messages.ts:633-658,683-717. Text and thinking start fields seed the block, and deltas append rather than replace it.
func TestAnthropicContentBlockStartUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, start, delta, want         string
		startEvent, deltaEvent, endEvent AssistantEventType
	}{
		{"text", `{"type":"text","text":"initial text"}`, `{"type":"text_delta","text":" plus delta"}`, `[{"type":"text","text":"initial text plus delta"}]`, EventTextStart, EventTextDelta, EventTextEnd},
		{"thinking", `{"type":"thinking","thinking":"initial reasoning","signature":"initial-signature"}`, `{"type":"thinking_delta","thinking":" plus delta"}`, `[{"type":"thinking","thinking":"initial reasoning plus delta","thinkingSignature":"initial-signature"}]`, EventThinkingStart, EventThinkingDelta, EventThinkingEnd},
		{"missing signature", `{"type":"thinking"}`, `{"type":"thinking_delta","thinking":"reasoning"}`, `[{"type":"thinking","thinking":"reasoning","thinkingSignature":""}]`, EventThinkingStart, EventThinkingDelta, EventThinkingEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sse := fmt.Sprintf("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":%s}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":%s}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", tc.start, tc.delta)
			events := runAnthropicSSE(t, sse)
			message := anthropicTerminal(t, events)
			if message.StopReason != StopReasonStop {
				t.Fatalf("terminal = %#v", message)
			}
			encoded, err := json.Marshal(message.Content)
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, encoded, tc.want)
			wantEvents := []AssistantEventType{EventStart, tc.startEvent, tc.deltaEvent, tc.endEvent, EventDone}
			if got := anthropicEventTypes(events); !reflect.DeepEqual(got, wantEvents) {
				t.Fatalf("events = %v, want %v", got, wantEvents)
			}
		})
	}
}

// The shared builder must not invent Anthropic's explicit signature for other providers' unsigned reasoning.
func TestThinkingBuilderKeepsOmittedSignature(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIGoogleGenerativeAI, "google", "test-model")
	builder.thinkingDelta("reasoning", false)
	builder.done(StopReasonStop, nil, "")
	encoded, err := json.Marshal(builder.stream.Result().Content)
	if err != nil {
		t.Fatal(err)
	}
	assertShapeJSON(t, encoded, `[{"type":"thinking","thinking":"reasoning"}]`)
}

// The content union, message clone, and persisted JSON round trip must all retain the signature's presence, including on failed historical turns.
func TestThinkingSignatureMessageRoundTrip(t *testing.T) {
	for _, signature := range []string{"", `,"thinkingSignature":""`, `,"thinkingSignature":"signed"`} {
		for _, stop := range []StopReason{StopReasonStop, StopReasonError, StopReasonAborted} {
			t.Run(string(stop)+signature, func(t *testing.T) {
				content := `{"type":"thinking","thinking":"reasoning"` + signature + `}`
				input := `{"role":"assistant","api":"anthropic-messages","provider":"custom-anthropic","model":"test-model","content":[` + content + `],"usage":{},"stopReason":"` + string(stop) + `","timestamp":1}`
				var message AssistantMessage
				if err := json.Unmarshal([]byte(input), &message); err != nil {
					t.Fatal(err)
				}
				cloned := message.cloneMessage().(AssistantMessage)
				encoded, err := json.Marshal(cloned)
				if err != nil {
					t.Fatal(err)
				}
				var reopened AssistantMessage
				if err := json.Unmarshal(encoded, &reopened); err != nil {
					t.Fatal(err)
				}
				encoded, err = json.Marshal(reopened.Content)
				if err != nil {
					t.Fatal(err)
				}
				assertShapeJSON(t, encoded, "["+content+"]")
			})
		}
	}
}

func TestThinkingSignatureDecodeReplacesPresence(t *testing.T) {
	var content ThinkingContent
	for _, input := range []string{
		`{"type":"thinking","thinking":"reasoning","thinkingSignature":""}`,
		`{"type":"thinking","thinking":"reasoning"}`,
		`{"type":"thinking","thinking":"reasoning","thinkingSignature":"signature"}`,
	} {
		if err := json.Unmarshal([]byte(input), &content); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		assertShapeJSON(t, encoded, input)
	}
}

// Anthropic requires both fields on a thinking block, including signed empty thinking and unsigned thinking accepted by model compat.
func TestAnthropicThinkingReplayWireFieldsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, thinking, signature, want string
		allowEmpty                      bool
	}{
		{"unsigned allowed", "reasoning", "", `[{"type":"thinking","thinking":"reasoning","signature":""}]`, true},
		{"signed empty thinking", "", "signed", `[{"type":"thinking","thinking":"","signature":"signed"}]`, false},
		{"unsigned converted", "reasoning", "", `[{"type":"text","text":"reasoning"}]`, false},
	} {
		for _, oauth := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/oauth=%t", tc.name, oauth), func(t *testing.T) {
				converted := anthConvertMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: tc.thinking, ThinkingSignature: tc.signature}}, StopReason: StopReasonStop}}, oauth, tc.allowEmpty, false)
				if len(converted) != 1 {
					t.Fatalf("converted = %#v", converted)
				}
				encoded, err := json.Marshal(converted[0].Content)
				if err != nil {
					t.Fatal(err)
				}
				assertShapeJSON(t, encoded, tc.want)
			})
		}
	}
}

func BenchmarkThinkingSignatureJSON(b *testing.B) {
	for _, signature := range []string{"", `,"thinkingSignature":""`, `,"thinkingSignature":"signed"`} {
		b.Run(signature, func(b *testing.B) {
			data := []byte(`{"type":"thinking","thinking":"` + strings.Repeat("reasoning ", 100) + `"` + signature + `}`)
			b.ReportAllocs()
			for b.Loop() {
				var content ThinkingContent
				if err := json.Unmarshal(data, &content); err != nil {
					b.Fatal(err)
				}
				if _, err := json.Marshal(content); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
