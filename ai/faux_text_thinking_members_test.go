package ai

import "testing"

// upstream: packages/ai/src/types.ts:TextContent.textSignature,ThinkingContent.thinkingSignature,ThinkingContent.redacted;
// packages/ai/src/providers/faux.ts:streamWithDeltas (the partial message holds {type, text|thinking}, the final message holds the response blocks).
func TestFauxTextAndThinkingOptionalMembersReachTheFinalMessage(t *testing.T) {
	t.Parallel()
	provider := NewFauxProvider(FauxConfig{})
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	thinking := FauxThinking("secret")
	thinking.ThinkingSignature, thinking.Redacted = "enc", true
	text := FauxText("hello")
	text.TextSignature = `{"v":1,"id":"msg_1"}`
	call := FauxToolCall("lookup", map[string]any{"q": "x"}, &FauxToolCallOptions{ID: "call-1"})
	provider.SetResponses([]FauxResponseStep{FauxStaticStep(FauxResponse{Content: []FauxContentBlock{thinking, text, call}, StopReason: "toolUse"})})
	stream, err := provider.Stream(t.Context(), emptyTranscript(), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for event := range stream.Events(t.Context()) {
		switch end := event.(type) {
		case ThinkingEndEvent:
			if partial := end.Partial.Content[end.ContentIndex].(ThinkingContent); partial.ThinkingSignature != "" || partial.Redacted {
				t.Errorf("partial thinking block = %+v, want only the thinking text", partial)
			}
		case TextEndEvent:
			if partial := end.Partial.Content[end.ContentIndex].(TextContent); partial.TextSignature != "" {
				t.Errorf("partial text block = %+v, want only the text", partial)
			}
		}
	}
	final := stream.Result().Content
	if len(final) != 3 {
		t.Fatalf("final content = %+v", final)
	}
	gotThinking := final[0].(ThinkingContent)
	if gotThinking.Thinking != "secret" || gotThinking.ThinkingSignature != "enc" || !gotThinking.Redacted {
		t.Errorf("final thinking block = %+v, want signature enc and redacted", gotThinking)
	}
	gotText := final[1].(TextContent)
	if gotText.Text != "hello" || gotText.TextSignature != text.TextSignature {
		t.Errorf("final text block = %+v, want signature %q", gotText, text.TextSignature)
	}
}
