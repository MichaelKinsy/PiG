package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// These cases port .upstream/v0.87.1/packages/ai/test/google-thinking-signature.test.ts through the production SSE parser instead of testing SDK Part helpers in isolation.
func TestGoogleThinkingSignatureUpstream(t *testing.T) {
	for _, tc := range []struct {
		name      string
		thought   *bool
		signature string
		thinking  bool
	}{
		// .upstream/v0.87.1/packages/ai/test/google-thinking-signature.test.ts:5
		{"treats part.thought === true as thinking/undefined", new(true), "", true},
		{"treats part.thought === true as thinking/opaque-signature", new(true), "opaque-signature", true},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-signature.test.ts:10
		{"does not treat thoughtSignature alone as thinking/undefined", nil, "opaque-signature", false},
		{"does not treat thoughtSignature alone as thinking/false", new(false), "opaque-signature", false},
		// .upstream/v0.87.1/packages/ai/test/google-thinking-signature.test.ts:18
		{"does not treat empty/missing signatures as thinking if thought is not set/undefined", nil, "", false},
		{"does not treat empty/missing signatures as thinking if thought is not set/false", new(false), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			part, err := json.Marshal(geminiPart{Text: new("text"), Thought: tc.thought, ThoughtSignature: tc.signature})
			if err != nil {
				t.Fatal(err)
			}
			message := googleTerminalMessage(t, runGoogleSSE(t, fmt.Sprintf("data: {\"candidates\":[{\"content\":{\"parts\":[%s]},\"finishReason\":\"STOP\"}]}\n\n", part)))
			if len(message.Content) != 1 {
				t.Fatalf("content = %#v", message.Content)
			}
			switch block := message.Content[0].(type) {
			case ThinkingContent:
				if !tc.thinking || block.ThinkingSignature != tc.signature {
					t.Fatalf("thinking = %#v", block)
				}
			case TextContent:
				if tc.thinking || block.TextSignature != tc.signature {
					t.Fatalf("text = %#v", block)
				}
			default:
				t.Fatalf("unexpected block: %#v", block)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/google-thinking-signature.test.ts:23
	t.Run("preserves the existing signature when subsequent deltas omit thoughtSignature", func(t *testing.T) {
		for _, signatures := range [][]string{{"sig-1"}, {"sig-1", ""}, {"sig-1", "", ""}} {
			assertGoogleSignatureSequence(t, signatures, "sig-1")
		}
	})
	// .upstream/v0.87.1/packages/ai/test/google-thinking-signature.test.ts:34
	t.Run("updates the signature when a new non-empty signature arrives", func(t *testing.T) { assertGoogleSignatureSequence(t, []string{"sig-1", "sig-2"}, "sig-2") })
}

func assertGoogleSignatureSequence(t *testing.T, signatures []string, want string) {
	t.Helper()
	var sse strings.Builder
	for _, signature := range signatures {
		fmt.Fprintf(&sse, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\",\"thought\":true,\"thoughtSignature\":%q}]}}]}\n\n", signature)
	}
	sse.WriteString("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
	message := googleTerminalMessage(t, runGoogleSSE(t, sse.String()))
	if len(message.Content) != 1 {
		t.Fatalf("content = %#v", message.Content)
	}
	block, ok := message.Content[0].(ThinkingContent)
	if !ok || block.ThinkingSignature != want {
		t.Fatalf("thinking = %#v; want signature %q", message.Content[0], want)
	}
}

func TestGoogleSignedEmptyBlocksUpstream(t *testing.T) {
	const sig = "AAAAAAAAAAAAAAAAAAAAAA=="
	const model = "gemini-3-pro-preview"
	for _, tc := range []struct {
		name, sourceModel string
		blocks            []AssistantContentBlock
		signed            bool
		thought           bool
	}{
		// .upstream/v0.87.1/packages/ai/test/google-shared-signed-empty-blocks.test.ts:59
		{"keeps a signed empty thinking block so its signature is echoed back", model, []AssistantContentBlock{ThinkingContent{ThinkingSignature: sig}}, true, true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-signed-empty-blocks.test.ts:76
		{"keeps a signed empty text block the same way", model, []AssistantContentBlock{TextContent{TextSignature: sig}}, true, false},
		// .upstream/v0.87.1/packages/ai/test/google-shared-signed-empty-blocks.test.ts:92
		{"still drops unsigned empty blocks", model, []AssistantContentBlock{ThinkingContent{}, TextContent{Text: "   "}}, false, false},
		// .upstream/v0.87.1/packages/ai/test/google-shared-signed-empty-blocks.test.ts:109
		{"still drops signed empty blocks from a different provider/model (signature unusable)", "other-model", []AssistantContentBlock{ThinkingContent{ThinkingSignature: sig}, TextContent{TextSignature: sig}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks := append(tc.blocks, ToolCall{ID: "call_1", Name: "bash", Arguments: map[string]any{"command": "ls"}})
			contents := geminiConvertMessages([]Message{UserMessage{Content: UserText("Hi")}, AssistantMessage{API: APIGoogleGenerativeAI, Provider: "google", Model: tc.sourceModel, Content: blocks, StopReason: StopReasonToolUse}}, "google", model, false)
			if len(contents) != 2 || contents[1].Role != "model" {
				t.Fatalf("contents = %#v", contents)
			}
			parts := contents[1].Parts
			if tc.signed {
				if len(parts) != 2 || parts[0].ThoughtSignature != sig || parts[0].Text == nil || *parts[0].Text != "" {
					t.Fatalf("parts = %#v", parts)
				}
				if tc.thought && (parts[0].Thought == nil || !*parts[0].Thought) {
					t.Fatalf("thought = %#v", parts[0])
				}
			} else {
				if len(parts) != 1 || parts[0].FunctionCall == nil {
					t.Fatalf("parts = %#v", parts)
				}
				data, err := json.Marshal(parts)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), sig) {
					t.Fatalf("signature leaked: %s", data)
				}
			}
		})
	}
}

func TestGoogleUnsignedToolCallsUpstream(t *testing.T) {
	const sig = "AAAAAAAAAAAAAAAAAAAAAA=="
	for _, tc := range []struct {
		name, provider, model, sourceModel, signature string
		ids                                           bool
	}{
		// .upstream/v0.87.1/packages/ai/test/google-shared-gemini3-unsigned-tool-call.test.ts:81
		{"preserves tool call IDs/google gemini-3-pro-preview", "google", "gemini-3-pro-preview", "gemini-3-pro-preview", "", true},
		{"preserves tool call IDs/google gemini-3.6-flash", "google", "gemini-3.6-flash", "gemini-3.6-flash", "", true},
		{"preserves tool call IDs/google-vertex", "google-vertex", "gemini-3-pro-preview", "gemini-3-pro-preview", "", true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-gemini3-unsigned-tool-call.test.ts:100
		{"does not add skip_thought_signature_validator for unsigned Google Gen AI tool calls", "google", "gemini-3-pro-preview", "other-model", "", true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-gemini3-unsigned-tool-call.test.ts:118
		{"does not add skip_thought_signature_validator for unsigned Vertex tool calls", "google-vertex", "gemini-3-pro-preview", "gemini-3-pro-preview", "", true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-gemini3-unsigned-tool-call.test.ts:130
		{"preserves valid thoughtSignature when present for the same provider and model", "google", "gemini-3-pro-preview", "gemini-3-pro-preview", sig, true},
		// .upstream/v0.87.1/packages/ai/test/google-shared-gemini3-unsigned-tool-call.test.ts:142
		{"does not add a thoughtSignature for non-Gemini-3 models", "google", "gemini-2.5-flash", "other-model", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []Message{
				UserMessage{Content: UserText("Hi")},
				AssistantMessage{Provider: tc.provider, Model: tc.sourceModel, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{
					ToolCall{ID: "call_1", Name: "bash", Arguments: map[string]any{"command": "echo hi"}, ThoughtSignature: tc.signature},
					ToolCall{ID: "call_2", Name: "bash", Arguments: map[string]any{"command": "ls -la"}},
				}},
				ToolResultMessage{ToolCallID: "call_1", ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: "hi"}}},
				ToolResultMessage{ToolCallID: "call_2", ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: "files"}}},
			}
			contents := geminiConvertMessages(messages, tc.provider, tc.model, false)
			if len(contents) != 3 || len(contents[1].Parts) != 2 || len(contents[2].Parts) != 2 {
				t.Fatalf("contents = %#v", contents)
			}
			var calls, responses []string
			for i, part := range contents[1].Parts {
				if part.FunctionCall == nil {
					t.Fatalf("non-call part: %#v", part)
				}
				calls = append(calls, part.FunctionCall.ID)
				want := ""
				if i == 0 {
					want = tc.signature
				}
				if part.ThoughtSignature != want {
					t.Fatalf("signature = %q; want %q", part.ThoughtSignature, want)
				}
			}
			for _, part := range contents[2].Parts {
				if part.FunctionResponse == nil {
					t.Fatalf("non-response part: %#v", part)
				}
				responses = append(responses, part.FunctionResponse.ID)
			}
			want := []string{"", ""}
			if tc.ids {
				want = []string{"call_1", "call_2"}
			}
			if !reflect.DeepEqual(calls, want) || !reflect.DeepEqual(responses, want) {
				t.Fatalf("calls=%v responses=%v want=%v", calls, responses, want)
			}
			data, err := json.Marshal(contents)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "skip_thought_signature_validator") || strings.Contains(string(data), "Historical context") {
				t.Fatalf("fabricated history: %s", data)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/google-shared-gemini3-unsigned-tool-call.test.ts:162
	for _, tc := range []struct {
		model string
		want  bool
	}{{"gemini-2.5-flash", false}, {"gemini-3.6-flash", true}, {"claude-sonnet-4-5", true}, {"gpt-oss-120b", true}} {
		t.Run("requiresToolCallId/"+tc.model, func(t *testing.T) {
			if got := requiresToolCallId(tc.model); got != tc.want {
				t.Fatalf("got %v; want %v", got, tc.want)
			}
		})
	}
}
