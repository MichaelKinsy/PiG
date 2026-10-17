package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// api/openai-completions.ts convertMessages(model, context, compat): the instruction message role is `model.reasoning && compat.supportsDeveloperRole ? "developer" :
// "system"` (:1233), and the compat is detected from the model's provider, base URL and id (detectCompat :1620-1650); user images are sent only for a model whose input includes
// "image" (:1257-ish `model.input.includes("image")`). ConvertCompletionsMessages must hand each of those model fields to the conversion.
func TestConvertCompletionsMessagesUsesTheModelsProviderBaseURLIDAndInput(t *testing.T) {
	transcript := NormalizeContext(Context{
		SystemPrompt: "be brief",
		Messages: []Message{
			UserMessage{Content: UserContentBlocks{TextContent{Text: "look"}, ImageContent{MimeType: "image/png", Data: "AAAA"}}, Timestamp: 1},
		},
	})
	role := func(providerID, baseURL, modelID string) string {
		t.Helper()
		model := &Model{ID: modelID, ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: providerID, BaseURL: baseURL, Reasoning: true}, Input: []string{"text"}}
		converted, err := ConvertCompletionsMessages(model, transcript, nil, ConvertCompletionsMessagesOptions{})
		if err != nil || len(converted) == 0 {
			t.Fatalf("convert: %v (%d messages)", err, len(converted))
		}
		return converted[0].Role
	}
	cases := []struct {
		name                    string
		providerID, baseURL, id string
		want                    string
	}{
		{"standard provider", "custom", "https://example.test/v1", "m", "developer"},
		{"non-standard base URL", "custom", "https://api.x.ai/v1", "m", "system"},
		{"non-standard provider", "cerebras", "https://example.test/v1", "m", "system"},
		{"openrouter anthropic model", "openrouter", "https://example.test/v1", "anthropic/claude", "developer"},
		{"openrouter other model", "openrouter", "https://example.test/v1", "meta/llama", "system"},
	}
	for _, tc := range cases {
		if got := role(tc.providerID, tc.baseURL, tc.id); got != tc.want {
			t.Errorf("%s: instruction role = %q, want %q", tc.name, got, tc.want)
		}
	}

	hasImage := func(input []string) bool {
		model := &Model{ID: "m", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom", BaseURL: "https://example.test/v1"}, Input: input}
		converted, err := ConvertCompletionsMessages(model, transcript, nil, ConvertCompletionsMessagesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(converted)
		return strings.Contains(string(data), "image_url")
	}
	if hasImage([]string{"text"}) {
		t.Error("an image reached a model whose input is text only")
	}
	if !hasImage([]string{"text", "image"}) {
		t.Error("an image was dropped for a model whose input includes image")
	}
}
