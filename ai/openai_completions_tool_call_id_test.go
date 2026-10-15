package ai

import (
	"strings"
	"testing"
)

// openai-completions.ts convertMessages hands normalizeToolCallId to transformMessages (transform-messages.ts:136-139), which applies it to a tool call of
// another model only and maps the matching tool result to the new id. A call the same model produced, with its result, goes out as it is, even when its
// id is longer than 40 characters on provider "openai" or carries the `|` of a Responses id.
//
// mutation-checked: normalizing every id after the conversion, as a post-pass over the request messages does, rewrites the same-model ids and fails it.
func TestCompletionsNormalizesOnlyTheToolCallIDsOfOtherModels(t *testing.T) {
	longID := strings.Repeat("a", 60)
	pipeID := "call_same|item+/=" + strings.Repeat("b", 50)
	foreignPipe := "call_other|item+/=" + strings.Repeat("c", 50)
	call := func(id string) ToolCall { return ToolCall{ID: id, Name: "read", Arguments: JsonObject{}} }
	same := func(blocks ...AssistantContentBlock) AssistantMessage {
		return AssistantMessage{Provider: "openai", API: APIOpenAICompletions, Model: "m", StopReason: StopReasonToolUse, Content: blocks}
	}
	foreign := AssistantMessage{Provider: "github-copilot", API: APIOpenAIResponses, Model: "gpt-x", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{call(foreignPipe)}}
	result := func(id string) ToolResultMessage {
		return ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}}
	}
	transcript := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("go")},
		same(call(longID), call(pipeID)), result(longID), result(pipeID),
		foreign, result(foreignPipe),
		UserMessage{Content: UserText("more")},
	}})
	p := &openAIProvider{cfg: OpenAIConfig{Model: "m", ProviderID: "openai"}}
	conversion, err := p.convertTranscript(transcript, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var calls, results []string
	for _, message := range conversion.chat {
		for _, toolCall := range message.ToolCalls {
			calls = append(calls, toolCall.ID)
		}
		if message.ToolCallID != "" {
			results = append(results, message.ToolCallID)
		}
	}
	normalizedForeign := normalizeCompletionsToolCallID(foreignPipe, "openai")
	if normalizedForeign == foreignPipe || len(normalizedForeign) > 40 {
		t.Fatalf("the foreign id normalizes to %q", normalizedForeign)
	}
	wantCalls := []string{longID, pipeID, normalizedForeign}
	if strings.Join(calls, ",") != strings.Join(wantCalls, ",") || strings.Join(results, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("tool call ids %q, tool result ids %q, want both %q (same-model ids untouched, the foreign id and its result normalized)", calls, results, wantCalls)
	}
}

// normalizeToolCallId truncates a plain id to 40 characters only on provider "openai" (openai-completions.ts:1224); a foreign id on any other provider keeps its length.
//
// mutation-checked: a normalizer built for a fixed provider id fails one of the two rows.
func TestCompletionsTruncatesAForeignPlainToolCallIDOnlyOnTheOpenAIProvider(t *testing.T) {
	plain := strings.Repeat("d", 60)
	foreign := AssistantMessage{Provider: "anthropic", API: APIAnthropicMessages, Model: "claude-x", StopReason: StopReasonToolUse,
		Content: []AssistantContentBlock{ToolCall{ID: plain, Name: "read", Arguments: JsonObject{}}}}
	transcript := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("go")}, foreign,
		ToolResultMessage{ToolCallID: plain, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
		UserMessage{Content: UserText("more")},
	}})
	for provider, want := range map[string]string{"openai": plain[:40], "openrouter": plain} {
		p := &openAIProvider{cfg: OpenAIConfig{Model: "m", ProviderID: provider}}
		conversion, err := p.convertTranscript(transcript, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		var call, result string
		for _, message := range conversion.chat {
			for _, toolCall := range message.ToolCalls {
				call = toolCall.ID
			}
			if message.ToolCallID != "" {
				result = message.ToolCallID
			}
		}
		if call != want || result != want {
			t.Errorf("provider %q: call id %q, result id %q, want %q", provider, call, result, want)
		}
	}
}
