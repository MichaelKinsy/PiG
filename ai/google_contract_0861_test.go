package ai

import (
	"strings"
	"testing"
)

func TestSupportsMultimodalFunctionResponseMatchesGoogleMajorVersion(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"gemini-3-pro", true},
		{"gemini-live-3-flash", true},
		{"gemini-2.5-pro", false},
		{"claude-sonnet-via-google", true},
		{"prefix-gemini-2", true},
	}
	for _, test := range tests {
		if got := supportsMultimodalFunctionResponse(test.model); got != test.want {
			t.Errorf("supportsMultimodalFunctionResponse(%q) = %v, want %v", test.model, got, test.want)
		}
	}
}

func TestGeminiReplayKeepsSignaturesOnlyForSameProviderAndModel(t *testing.T) {
	const signature = "YWJjZA=="
	message := AssistantMessage{
		Provider: "google", Model: "gemini-3-pro",
		Content: []AssistantContentBlock{
			TextContent{TextSignature: signature},
			ThinkingContent{ThinkingSignature: signature},
			ToolCall{ID: "call", Name: "read", Arguments: JsonObject{}, ThoughtSignature: signature},
		},
	}
	same := geminiConvertMessages([]Message{message}, "google", "gemini-3-pro", true)
	if len(same) != 1 || len(same[0].Parts) != 3 {
		t.Fatalf("same-provider content = %#v", same)
	}
	for index, part := range same[0].Parts {
		if part.ThoughtSignature != signature {
			t.Errorf("same-provider part %d signature = %q", index, part.ThoughtSignature)
		}
	}

	foreign := geminiConvertMessages([]Message{message}, "google", "gemini-3-flash", true)
	if len(foreign) != 1 || len(foreign[0].Parts) != 1 {
		t.Fatalf("cross-model content = %#v", foreign)
	}
	if foreign[0].Parts[0].ThoughtSignature != "" || foreign[0].Parts[0].FunctionCall == nil {
		t.Fatalf("cross-model tool call retained signature: %#v", foreign[0].Parts[0])
	}
}

func TestGeminiToolResultImageOrderingMatchesUpstream(t *testing.T) {
	results := []Message{
		ToolResultMessage{ToolCallID: "one", ToolName: "first", Content: []ToolResultMessageContent{ImageContent{MimeType: "image/png", Data: "AAAA"}}},
		ToolResultMessage{ToolCallID: "two", ToolName: "second", Content: []ToolResultMessageContent{ImageContent{MimeType: "image/png", Data: "BBBB"}}},
	}
	gemini2 := geminiConvertMessages(results, "google", "gemini-2.5-pro", true)
	if len(gemini2) != 4 {
		t.Fatalf("Gemini 2 content count = %d, want 4: %#v", len(gemini2), gemini2)
	}
	if gemini2[0].Parts[0].FunctionResponse == nil || gemini2[1].Parts[0].Text == nil || *gemini2[1].Parts[0].Text != "Tool result image:" ||
		gemini2[2].Parts[0].FunctionResponse == nil || gemini2[3].Parts[0].Text == nil || *gemini2[3].Parts[0].Text != "Tool result image:" {
		t.Fatalf("Gemini 2 tool-result ordering = %#v", gemini2)
	}

	gemini3 := geminiConvertMessages(results, "google", "gemini-3-pro", true)
	if len(gemini3) != 1 || len(gemini3[0].Parts) != 2 {
		t.Fatalf("Gemini 3 content = %#v", gemini3)
	}
	for index, part := range gemini3[0].Parts {
		if part.FunctionResponse == nil || len(part.FunctionResponse.Parts) != 1 {
			t.Errorf("Gemini 3 response %d images = %#v", index, part.FunctionResponse)
		}
	}
}

// google-shared.ts:441-472 mapStopReason: STOP and MAX_TOKENS map to stop and length, every other FinishReason enum member maps to error, and a value outside
// the enum reaches the exhaustive switch's default, which throws `Unhandled stop reason: <reason>` (google-generative-ai.ts:226 and google-vertex.ts:234 call it
// inside the stream try, so the throw ends the stream with that message). google-generative-ai.ts:276-283 turns a pending stop reason into "Google stream ended
// without a finish reason" and an error stop into `Provider stopped with: <raw>`.
func TestMapGoogleFinishReasonFollowsMapStopReason(t *testing.T) {
	for reason, want := range map[string]StopReason{"STOP": StopReasonStop, "MAX_TOKENS": StopReasonLength} {
		if got, err := mapStopReason(reason); err != nil || got != want {
			t.Errorf("mapStopReason(%q) = (%q, %v), want %q", reason, got, err, want)
		}
		if got, message := mapGoogleFinishReason(reason); got != want || message != "" {
			t.Errorf("mapGoogleFinishReason(%q) = (%q, %q)", reason, got, message)
		}
	}
	for _, reason := range []string{"BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "SAFETY", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION", "IMAGE_OTHER",
		"RECITATION", "FINISH_REASON_UNSPECIFIED", "OTHER", "LANGUAGE", "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "NO_IMAGE"} {
		if got, err := mapStopReason(reason); err != nil || got != StopReasonError {
			t.Errorf("mapStopReason(%q) = (%q, %v), want an error stop without a throw", reason, got, err)
		}
		if got, message := mapGoogleFinishReason(reason); got != StopReasonError || message != "Provider stopped with: "+reason {
			t.Errorf("mapGoogleFinishReason(%q) = (%q, %q)", reason, got, message)
		}
	}
	if got, err := mapStopReason("future_reason"); err == nil || err.Error() != "Unhandled stop reason: future_reason" || got != StopReasonError {
		t.Errorf("mapStopReason(future_reason) = (%q, %v)", got, err)
	}
	if got, message := mapGoogleFinishReason("future_reason"); got != StopReasonError || message != "Unhandled stop reason: future_reason" {
		t.Errorf("mapGoogleFinishReason(future_reason) = (%q, %q)", got, message)
	}
	if got, message := mapGoogleFinishReason(""); got != StopReasonError || message != "Google stream ended without a finish reason" {
		t.Errorf("mapGoogleFinishReason(\"\") = (%q, %q)", got, message)
	}
}

// google-shared.ts:102-111 getDisabledGoogleThinkingConfig: a model that does not use thinking levels disables thinking with thinkingBudget 0; a level model whose
// "off" is supported also gets budget 0 (clampThinkingLevel keeps "off"); a level model whose map removes "off" gets the level "off" clamps to, resolved through
// its map and sent as the Google level; an unresolvable mapping throws, which Go returns as the error.
func TestGetDisabledGoogleThinkingConfig(t *testing.T) {
	budgetZero := func(t *testing.T, model *Model, label string) {
		t.Helper()
		got, err := getDisabledGoogleThinkingConfig(model)
		if err != nil || got == nil || got.ThinkingBudget == nil || *got.ThinkingBudget != 0 || got.ThinkingLevel != "" {
			t.Errorf("%s: config = %#v, err = %v, want thinkingBudget 0", label, got, err)
		}
	}
	budgetZero(t, reasoningModel("gemini-2.5-flash"), "budget model")
	budgetZero(t, reasoningModel("gemini-2.5-flash", ThinkingLevelMap{ThinkingOff: nil}), "budget model whose map removes off")
	budgetZero(t, reasoningModel("gemini-3-flash-preview"), "level model with off supported")

	removed := reasoningModel("gemini-3-flash-preview", ThinkingLevelMap{ThinkingOff: nil})
	got, err := getDisabledGoogleThinkingConfig(removed)
	if err != nil || got == nil || got.ThinkingBudget != nil || got.ThinkingLevel != "MINIMAL" {
		t.Errorf("level model without off: config = %#v, err = %v, want thinkingLevel MINIMAL", got, err)
	}

	mapped := reasoningModel("gemini-3-flash-preview", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: new("low")})
	got, err = getDisabledGoogleThinkingConfig(mapped)
	if err != nil || got == nil || got.ThinkingLevel != "LOW" {
		t.Errorf("level model with off mapped through minimal: config = %#v, err = %v, want thinkingLevel LOW", got, err)
	}

	bad := reasoningModel("gemini-3-flash-preview", ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: new("extreme")})
	if got, err := getDisabledGoogleThinkingConfig(bad); err == nil || got != nil {
		t.Errorf("unresolvable mapping: config = %#v, err = %v, want an error", got, err)
	}
}

func reasoningModel(id string, levels ...ThinkingLevelMap) *Model {
	model := &Model{ID: id, ProviderMeta: ProviderMetadata{Reasoning: true}}
	if len(levels) > 0 {
		model.ThinkingLevelMap = levels[0]
	}
	return model
}

// google-generative-ai.ts:224-226 and google-vertex.ts:232-234 call mapStopReason inside the chunk loop: a finishReason outside the enum throws there, so the
// stream fails at that chunk with `Unhandled stop reason: <reason>`, before that chunk's usageMetadata (:231) is applied and before any later chunk is read.
func TestGoogleStreamFailsAtUnhandledFinishReasonChunk(t *testing.T) {
	const sse = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"first"}]}}]}

data: {"candidates":[{"finishReason":"future_reason"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"later"}]}}]}

`
	check := func(t *testing.T, message *AssistantMessage) {
		t.Helper()
		if message.StopReason != StopReasonError || message.ErrorMessage != "Unhandled stop reason: future_reason" || message.RawStopReason != "future_reason" {
			t.Fatalf("message = stop %q, error %q, raw %q", message.StopReason, message.ErrorMessage, message.RawStopReason)
		}
		if message.Usage.TotalTokens != 0 || message.Usage.Input != 0 || message.Usage.Output != 0 {
			t.Errorf("usage = %+v, want the failing chunk's usage not applied", message.Usage)
		}
		var text string
		for _, block := range message.Content {
			if content, ok := block.(TextContent); ok {
				text += content.Text
			}
		}
		if text != "first" {
			t.Errorf("text = %q, want the later chunk unread", text)
		}
	}
	t.Run("observed continuation", func(t *testing.T) {
		check(t, googleTerminalMessage(t, runGoogleSSE(t, sse)))
	})
	t.Run("plain body", func(t *testing.T) {
		builder := newAssistantStreamBuilder(t.Context(), APIGoogleGenerativeAI, "google", "gemini-2.5-flash")
		(&googleProvider{}).parseGeminiSSE(t.Context(), strings.NewReader(sse), builder)
		check(t, builder.stream.Result())
	})
}
