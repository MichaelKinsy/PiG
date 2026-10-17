package ai

import (
	"encoding/json"
	"testing"
)

// openai-completions.ts convertMessages, driven through ConvertCompletionsMessages. These cases pin branches that no
// other test distinguishes: each is named for the Pi line it mirrors and fails when that branch changes.

func completionsProofModel(reasoning bool) *Model {
	model := &Model{ID: "model", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom", BaseURL: "https://example.test/v1", Reasoning: reasoning}, Input: []string{"text", "image"}}
	return model
}

func completionsProofMessages(t *testing.T, model *Model, compat *OpenAICompat, messages ...Message) []map[string]any {
	t.Helper()
	converted, err := ConvertCompletionsMessages(model, NormalizeContext(Context{Messages: messages}), compat, ConvertCompletionsMessagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("%v in %s", err, encoded)
	}
	return out
}

func firstAssistant(t *testing.T, out []map[string]any) map[string]any {
	t.Helper()
	for _, message := range out {
		if message["role"] == "assistant" {
			return message
		}
	}
	t.Fatalf("no assistant message in %v", out)
	return nil
}

func TestConvertCompletionsMessagesSanitizesStringUserTextAndToolResultText(t *testing.T) {
	// openai-completions.ts convertMessages: string user content and tool result text go through sanitizeSurrogates.
	out := completionsProofMessages(t, completionsProofModel(false), nil,
		UserMessage{Content: UserText("u" + loneHigh), Timestamp: 1},
		AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonToolUse, Timestamp: 2, Content: []AssistantContentBlock{ToolCall{ID: "c1", Name: "ls", Arguments: JsonObject{}}}},
		ToolResultMessage{ToolCallID: "c1", ToolName: "ls", Content: []ToolResultMessageContent{TextContent{Text: "t" + loneHigh}}, Timestamp: 3},
	)
	if out[0]["content"] != "u" {
		t.Errorf("user content = %#v, want %q", out[0]["content"], "u")
	}
	if tool := out[len(out)-1]; tool["role"] != "tool" || tool["content"] != "t" {
		t.Errorf("tool result = %#v, want content %q", tool, "t")
	}
}

func TestConvertCompletionsMessagesJoinsAssistantThinkingByTheReasoningFieldRules(t *testing.T) {
	// openai-completions.ts convertMessages: thinking blocks join with "\n" into the reasoning field named by the first non-empty block's signature; blank blocks are skipped.
	assistant := AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonStop, Timestamp: 2, Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "  ", ThinkingSignature: "reasoning_text"},
		ThinkingContent{Thinking: "one", ThinkingSignature: "reasoning_content"},
		ThinkingContent{Thinking: "two", ThinkingSignature: "reasoning_content"},
		TextContent{Text: "answer"},
	}}
	out := completionsProofMessages(t, completionsProofModel(true), nil, UserMessage{Content: UserText("q"), Timestamp: 1}, assistant)
	got := out[len(out)-1]
	if got["reasoning_content"] != "one\ntwo" || got["reasoning_text"] != nil {
		t.Errorf("assistant = %#v, want reasoning_content %q and no reasoning_text", got, "one\ntwo")
	}
}

func TestConvertCompletionsMessagesSendsThinkingAsTextJoinedByBlankLines(t *testing.T) {
	// openai-completions.ts convertMessages with compat.requiresThinkingAsText: non-empty thinking joins with "\n\n" into one leading text part.
	assistant := AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonStop, Timestamp: 2, Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "one"}, ThinkingContent{Thinking: "two"}, TextContent{Text: "answer"},
	}}
	yes := true
	out := completionsProofMessages(t, completionsProofModel(true), &OpenAICompat{RequiresThinkingAsText: &yes}, UserMessage{Content: UserText("q"), Timestamp: 1}, assistant)
	parts, _ := out[len(out)-1]["content"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["text"] != "one\n\ntwo" || parts[1].(map[string]any)["text"] != "answer" {
		t.Errorf("content = %#v", out[len(out)-1]["content"])
	}
	// Only blank thinking leaves the answer as a plain string, not a parts list.
	blank := AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonStop, Timestamp: 2, Content: []AssistantContentBlock{ThinkingContent{Thinking: " "}, TextContent{Text: "answer"}}}
	out = completionsProofMessages(t, completionsProofModel(true), &OpenAICompat{RequiresThinkingAsText: &yes}, UserMessage{Content: UserText("q"), Timestamp: 1}, blank)
	if out[len(out)-1]["content"] != "answer" {
		t.Errorf("content with only blank thinking = %#v, want the plain string", out[len(out)-1]["content"])
	}
}

func TestConvertCompletionsMessagesKeepsTheFirstReasoningDetailsAndFallsBackToLegacyOnes(t *testing.T) {
	// openai-completions.ts convertMessages: the first thinking block that carries reasoning_details wins; tool-call thought signatures are the fallback only when none did.
	details := func(id string) string {
		return `[{"type":"reasoning.encrypted","id":"` + id + `","data":"x"}]`
	}
	legacy := `{"type":"reasoning.encrypted","id":"legacy","data":"y"}`
	assistant := func(blocks ...AssistantContentBlock) AssistantMessage {
		return AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonToolUse, Timestamp: 2, Content: blocks}
	}
	ids := func(message map[string]any) []string {
		var ids []string
		list, _ := message["reasoning_details"].([]any)
		for _, item := range list {
			ids = append(ids, item.(map[string]any)["id"].(string))
		}
		return ids
	}
	user := UserMessage{Content: UserText("q"), Timestamp: 1}
	out := completionsProofMessages(t, completionsProofModel(true), nil, user, assistant(
		ThinkingContent{Thinking: "a", ThinkingSignature: details("first")},
		ThinkingContent{Thinking: "b", ThinkingSignature: details("second")},
		ToolCall{ID: "c", Name: "ls", Arguments: JsonObject{}, ThoughtSignature: legacy},
	))
	if got := ids(firstAssistant(t, out)); len(got) != 1 || got[0] != "first" {
		t.Errorf("thinking details = %v, want [first]", got)
	}
	out = completionsProofMessages(t, completionsProofModel(true), nil, user, assistant(
		ThinkingContent{Thinking: "a", ThinkingSignature: "reasoning_content"},
		ToolCall{ID: "c", Name: "ls", Arguments: JsonObject{}, ThoughtSignature: legacy},
	))
	if got := ids(firstAssistant(t, out)); len(got) != 1 || got[0] != "legacy" {
		t.Errorf("legacy details = %v, want [legacy]", got)
	}
	if firstAssistant(t, out)["reasoning_content"] != nil {
		t.Errorf("reasoning_content %#v must not accompany reasoning_details", firstAssistant(t, out)["reasoning_content"])
	}
}

func TestConvertCompletionsMessagesAddsEmptyReasoningContentOnlyWhenNoneWasCopied(t *testing.T) {
	// openai-completions.ts convertMessages with compat.requiresReasoningContentOnAssistantMessages on a reasoning model: assistants without reasoning_content get "", ones that have it keep theirs.
	yes := true
	compat := &OpenAICompat{RequiresReasoningContentOnAssistantMessages: &yes}
	user := UserMessage{Content: UserText("q"), Timestamp: 1}
	plain := AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonStop, Timestamp: 2, Content: []AssistantContentBlock{TextContent{Text: "a"}}}
	thought := AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonStop, Timestamp: 2, Content: []AssistantContentBlock{ThinkingContent{Thinking: "kept", ThinkingSignature: "reasoning_content"}, TextContent{Text: "a"}}}
	out := completionsProofMessages(t, completionsProofModel(true), compat, user, plain)
	if got, ok := out[len(out)-1]["reasoning_content"]; !ok || got != "" {
		t.Errorf("plain assistant reasoning_content = %#v, want an empty string", got)
	}
	out = completionsProofMessages(t, completionsProofModel(true), compat, user, thought)
	if out[len(out)-1]["reasoning_content"] != "kept" {
		t.Errorf("thinking assistant reasoning_content = %#v, want %q", out[len(out)-1]["reasoning_content"], "kept")
	}
	out = completionsProofMessages(t, completionsProofModel(false), compat, user, plain)
	if _, ok := out[len(out)-1]["reasoning_content"]; ok {
		t.Errorf("a non-reasoning model must not get reasoning_content, got %#v", out[len(out)-1])
	}
}

func TestConvertCompletionsMessagesNeedsAnAssistantBridgeAfterToolResultsOnlyWhenTheLastKeptMessageWasOne(t *testing.T) {
	// openai-completions.ts convertMessages: lastRole is the role of the previous message that reached the end of the loop body; an empty user message is skipped and does not reset it.
	yes := true
	compat := &OpenAICompat{RequiresAssistantAfterToolResult: &yes}
	out := completionsProofMessages(t, completionsProofModel(false), compat,
		UserMessage{Content: UserText("q"), Timestamp: 1},
		AssistantMessage{API: APIOpenAICompletions, Provider: "custom", Model: "model", StopReason: StopReasonToolUse, Timestamp: 2, Content: []AssistantContentBlock{ToolCall{ID: "c1", Name: "ls", Arguments: JsonObject{}}}},
		ToolResultMessage{ToolCallID: "c1", ToolName: "ls", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Timestamp: 3},
		UserMessage{Content: UserContentBlocks{TextContent{Text: ""}}, Timestamp: 4},
		UserMessage{Content: UserText("next"), Timestamp: 5},
	)
	var roles []string
	for _, message := range out {
		roles = append(roles, message["role"].(string))
	}
	// Pi inserts the bridge before every user message that follows a tool result, the skipped empty one included, and the skip leaves lastRole at "toolResult" for the next.
	want := []string{"user", "assistant", "tool", "assistant", "assistant", "user"}
	if len(roles) != len(want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
}

func TestConvertCompletionsMessagesSendsASanitizedMidConversationInstructionInTheInstructionRole(t *testing.T) {
	// openai-completions.ts convertMessages: a mid-conversation system message becomes an instruction message in the developer role on a reasoning model that supports it (the system role otherwise), with sanitized text.
	yes := true
	messages := []Message{
		UserMessage{Content: UserText("q"), Timestamp: 1},
		SystemMessage{Content: SystemText("rule" + loneHigh), Timestamp: 2},
		UserMessage{Content: UserText("next"), Timestamp: 3},
	}
	for _, tc := range []struct {
		name      string
		reasoning bool
		want      string
	}{{"reasoning model", true, "developer"}, {"other model", false, "system"}} {
		out := completionsProofMessages(t, completionsProofModel(tc.reasoning), &OpenAICompat{SupportsMidConvoSystemMessages: &yes, SupportsDeveloperRole: &yes}, messages...)
		found := false
		for _, message := range out {
			if message["content"] == "rule" {
				found = true
				if message["role"] != tc.want {
					t.Errorf("%s: instruction role = %v, want %s", tc.name, message["role"], tc.want)
				}
			}
		}
		if !found {
			t.Errorf("%s: no sanitized instruction message in %v", tc.name, out)
		}
	}
}
