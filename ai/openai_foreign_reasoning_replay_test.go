package ai

import "testing"

// pi-mono #6167: transformMessages turns another model's plain-text thinking into assistant text, so a target with
// compat.requiresReasoningContentOnAssistantMessages received reasoning_content "" and the reasoning inlined in content.
// pig divergence (D126): the target keeps that reasoning as reasoning_content.
func TestForeignThinkingReplaysAsReasoningContentWhenTheTargetRequiresIt(t *testing.T) {
	foreign := AssistantMessage{
		Provider: "anthropic", API: APIAnthropicMessages, Model: "claude-x", StopReason: StopReasonStop,
		Content: []AssistantContentBlock{ThinkingContent{Thinking: "plan A", ThinkingSignature: "EqQBsig"}, TextContent{Text: "done"}},
	}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("go")}, foreign, UserMessage{Content: UserText("more")}}})
	for _, tc := range []struct {
		name                          string
		requires, asText, isReasoning bool
		wantReasoning                 *string
		wantContent                   any
	}{
		{"required", true, false, true, new("plan A"), "done"},
		{"not required keeps Pi's text replay", false, false, true, nil, nil},
		{"non-reasoning target keeps Pi's text replay", true, false, false, nil, nil},
		{"thinking-as-text target keeps Pi's text replay", true, true, true, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &openAIProvider{cfg: OpenAIConfig{Model: "m", ProviderID: "p", Compat: &OpenAICompat{RequiresReasoningContentOnAssistantMessages: &tc.requires, RequiresThinkingAsText: &tc.asText}}}
			conversion, err := p.convertTranscript(transcript, nil, tc.isReasoning)
			if err != nil {
				t.Fatal(err)
			}
			var assistant *oaiMessage
			for i := range conversion.chat {
				if conversion.chat[i].Role == "assistant" {
					assistant = &conversion.chat[i]
				}
			}
			if assistant == nil {
				t.Fatalf("no assistant message: %+v", conversion.chat)
			}
			if tc.wantReasoning != nil {
				if assistant.ReasoningContent == nil || *assistant.ReasoningContent != *tc.wantReasoning || assistant.Content != tc.wantContent {
					t.Fatalf("reasoning_content=%v content=%v, want %q and %v", assistant.ReasoningContent, assistant.Content, *tc.wantReasoning, tc.wantContent)
				}
				return
			}
			if assistant.ReasoningContent != nil && *assistant.ReasoningContent != "" {
				t.Fatalf("reasoning_content = %q, want none", *assistant.ReasoningContent)
			}
		})
	}
}

// A thinking-only foreign turn keeps Pi's text replay: as reasoning_content alone it would no longer be a request message.
func TestForeignThinkingOnlyTurnKeepsTextReplay(t *testing.T) {
	foreign := AssistantMessage{Provider: "anthropic", API: APIAnthropicMessages, Model: "claude-x", StopReason: StopReasonStop,
		Content: []AssistantContentBlock{ThinkingContent{Thinking: "plan A", ThinkingSignature: "EqQBsig"}}}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("go")}, foreign, UserMessage{Content: UserText("more")}}})
	p := &openAIProvider{cfg: OpenAIConfig{Model: "m", ProviderID: "p", Compat: &OpenAICompat{RequiresReasoningContentOnAssistantMessages: new(true)}}}
	conversion, err := p.convertTranscript(transcript, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversion.chat) != 3 || conversion.chat[1].Role != "assistant" || conversion.chat[1].Content != "plan A" {
		t.Fatalf("conversation = %+v", conversion.chat)
	}
}
