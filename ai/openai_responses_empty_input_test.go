package ai

import (
	"encoding/json"
	"testing"
)

// convertResponsesMessages starts from `const messages: ResponseInput = []` (openai-responses-shared.ts), so a context
// with no messages sends `"input": []`. A nil Go slice marshals to null, which the Responses API rejects.
func TestConvertAnchoredMessagesEncodesAnEmptyInputAsAnArray(t *testing.T) {
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-test"}}
	for name, messages := range map[string][]Message{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			items, err := provider.convertAnchoredMessages(messages, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != "[]" {
				t.Fatalf("input = %s, want []", encoded)
			}
		})
	}
}

// buildParams sends the converted messages as `input` (openai-responses.ts:316-332), so a context with no system
// prompt and no messages sends `input: []`, for an official endpoint and an OpenAI-compatible endpoint with a custom
// base URL alike.
func TestOpenAIResponsesRequestSendsAnEmptyInputArray(t *testing.T) {
	for name, cfg := range map[string]OpenAIResponsesConfig{
		"official":   responsesCompatConfig(t, "openai", "gpt-5.4"),
		"custom URL": {Model: "local-model", ProviderID: "proxy", BaseURL: "https://proxy.example.com/v1", APIKey: "sk-test-key"},
	} {
		t.Run(name, func(t *testing.T) {
			payload, _, _ := captureResponsesCompat(t, cfg, Context{}, StreamOptions{}, "data: [DONE]\n\n")
			if got := string(payload["input"]); got != "[]" {
				t.Fatalf("input = %s, want []", got)
			}
		})
	}
}
