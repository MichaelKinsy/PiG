package coding

// pi: packages/ai/src/api/azure-openai-responses.ts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// azure-openai-responses.ts buildParams converts with convertResponsesMessages, which sends the system prompt in the developer role for a model with
// reasoning (model.reasoning) and the system role otherwise. The Session builds the provider from the model entry, so the entry's reasoning flag must
// reach the provider; a stream option alone does not choose the role.
func TestAzureModelEntryReasoningChoosesTheDeveloperRole(t *testing.T) {
	for _, reasoning := range []bool{true, false} {
		provider, err := buildProviderForEntry("azure-openai-responses", "gpt-5", ai.APIAzureOpenAIResponses, icodingagent.ModelEntry{
			ProviderID: "azure-openai-responses", ModelID: "gpt-5", BaseURL: "https://example.openai.azure.com/openai/v1", APIKey: "key", API: string(ai.APIAzureOpenAIResponses),
			Reasoning: reasoning, Input: []string{"text"},
		}, newRuntimeTestServices(t), "", false, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = provider.Close() })
		var payload string
		sentinel := errors.New("payload captured")
		options := ai.StreamOptions{OnPayload: func(body any, _ *ai.Model) (any, error) {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			payload = string(encoded)
			return nil, sentinel
		}}
		stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{SystemPrompt: "be brief", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), options)
		if err == nil {
			stream.Result()
		}
		role := `"role":"system"`
		if reasoning {
			role = `"role":"developer"`
		}
		if !strings.Contains(payload, role) {
			t.Errorf("reasoning=%v: payload %s lacks %s", reasoning, payload, role)
		}
		if testing.Verbose() {
			t.Logf("reasoning=%v: %s", reasoning, payload)
		}
	}
}
