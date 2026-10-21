package ai

import "testing"

// openai-completions.ts convertMessages (:1321-1326): with compat.requiresThinkingAsText the non-empty thinking blocks become one leading plain text part ahead of the answer's text parts;
// without it the answer stays a plain string and the thinking goes to the reasoning field. compat.requiresThinkingAsText defaults to false (:1654) and a model's value overrides the detected one (:1710).
// mutation-checked: `thinkingAsText: requiresThinkingAsText && false` in convertMessagesWithCompat fails it.
func TestConvertMessagesWithCompatSendsThinkingAsTextOnlyWhenTheCompatAsksFor(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserText("q"), Timestamp: 1},
		AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "m", StopReason: StopReasonStop, Timestamp: 2, Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "first", ThinkingSignature: "reasoning_content"},
			ThinkingContent{Thinking: "second", ThinkingSignature: "reasoning_content"},
			TextContent{Text: "answer"},
		}},
	}
	yes, no := true, false
	for _, tc := range []struct {
		name   string
		compat *OpenAICompat
		asText bool
	}{
		{"required", &OpenAICompat{RequiresThinkingAsText: &yes}, true},
		{"explicitly not required", &OpenAICompat{RequiresThinkingAsText: &no}, false},
		{"unset", &OpenAICompat{}, false},
		{"no compat", nil, false},
	} {
		p := &openAIProvider{cfg: OpenAIConfig{Model: "m", ProviderID: "custom", Compat: tc.compat}}
		out, err := p.convertMessagesWithCompat(messages, nil, "system", toolAnchors{}, true)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		assistant := out[len(out)-1]
		if assistant.Role != "assistant" {
			t.Fatalf("%s: last message is %q", tc.name, assistant.Role)
		}
		if tc.asText {
			parts, ok := assistant.Content.([]oaiContentPart)
			if !ok || len(parts) != 2 || parts[0].Text == nil || *parts[0].Text != "first\n\nsecond" || parts[1].Text == nil || *parts[1].Text != "answer" || assistant.ReasoningContent != nil {
				t.Errorf("%s: content = %#v reasoning_content = %v, want [thinking text, answer] and no reasoning field", tc.name, assistant.Content, assistant.ReasoningContent)
			}
			continue
		}
		if assistant.Content != "answer" || assistant.ReasoningContent == nil || *assistant.ReasoningContent != "first\nsecond" {
			t.Errorf("%s: content = %#v reasoning_content = %v, want the plain answer and reasoning_content %q", tc.name, assistant.Content, assistant.ReasoningContent, "first\nsecond")
		}
	}
}
