package ai

import (
	"encoding/json"
	"testing"
)

// Ports openai-responses-message-id.test.ts. Multiple text blocks in one
// assistant turn must get unique fallback message ids (msg_pi_<n>, then
// msg_pi_<n>_<k>); duplicate ids are rejected by the Responses API. In
// production a foreign thinking block is converted to a text block by
// NormalizeMessages (agent/transform.go), so a foreign assistant turn
// replayed on openai-codex reaches convertMessages as two text blocks: the
// exact reachable trigger the upstream test exercises via transformMessages.
func TestResponsesConvertMessages_MultipleTextBlocksGetUniqueMessageIDs(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai-codex", Model: "gpt-5.5"}}
	messages := []Message{
		UserMessage{Content: UserText("hello")},
		AssistantMessage{Content: []AssistantContentBlock{
			TextContent{Text: "private reasoning"},
			TextContent{Text: "visible answer"},
		}},
	}

	items, _ := p.convertMessages(messages, nil)

	var ids []string
	for i := range items {
		if items[i].Type == "message" && items[i].Role == "assistant" {
			ids = append(ids, items[i].ID)
		}
	}
	want := []string{"msg_pi_1", "msg_pi_1_1"}
	if len(ids) != len(want) {
		t.Fatalf("message ids = %v, want %v", ids, want)
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if id != want[i] {
			t.Errorf("message id[%d] = %q, want %q", i, id, want[i])
		}
		if seen[id] {
			t.Errorf("duplicate message id %q (Responses API rejects duplicates)", id)
		}
		seen[id] = true
	}
}

// A single-text-block turn keeps the un-suffixed msg_pi_<n> fallback id.
func TestResponsesConvertMessages_SingleTextBlockMessageID(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai-codex", Model: "gpt-5.5"}}
	messages := []Message{
		UserMessage{Content: UserText("hello")},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "answer"}}},
	}
	items, _ := p.convertMessages(messages, nil)
	for i := range items {
		if items[i].Type == "message" && items[i].Role == "assistant" {
			if items[i].ID != "msg_pi_1" {
				t.Errorf("single-block message id = %q, want msg_pi_1", items[i].ID)
			}
			var parts []map[string]any
			if err := json.Unmarshal(items[i].Content, &parts); err != nil || len(parts) == 0 {
				t.Fatalf("message content not an output_text array: %v", err)
			}
		}
	}
}
