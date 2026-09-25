package ai

import "testing"

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

func TestMapGoogleFinishReasonRejectsUnknownValues(t *testing.T) {
	for _, reason := range []string{"IMAGE_OTHER", "LANGUAGE", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "FINISH_REASON_UNSPECIFIED", "future_reason"} {
		stopReason, message := mapGoogleFinishReason(reason)
		if stopReason != StopReasonError || message != "Provider stopped with: "+reason {
			t.Errorf("mapGoogleFinishReason(%q) = (%q, %q)", reason, stopReason, message)
		}
	}
}
