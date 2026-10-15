package ai

import (
	"bytes"
	"encoding/json"
	"testing"
)

// azure.ts:44-55 wraps only the provider's Chat Completions API in azureStreams, which resolves the endpoint inside lazyStream's
// setup: an unset endpoint is a setup rejection (lazy.ts createSetupErrorMessage: errorMessage before timestamp). Its Responses
// models use azureOpenAIResponsesApi() directly, where createClient → resolveAzureConfig throws inside the module's try
// (azure-openai-responses.ts:65-71, :115-125): the module's message, timestamp first. Probed end to end on Pi 1.1.0 (gpt-4o:
// timestamp first; the stack runs createClient ← stream ← lazyApi).
func TestAzureMissingEndpointOrderFollowsPisAPIBoundary(t *testing.T) {
	for _, name := range []string{"AZURE_OPENAI_BASE_URL", "AZURE_OPENAI_RESOURCE_NAME"} {
		t.Setenv(name, "")
	}
	for id, wantErrorFirst := range map[string]bool{"deepseek-v4-pro": true, "gpt-4o": false} {
		t.Run(id, func(t *testing.T) {
			model := upstreamCatalogModel(t, "azure", id)
			result := BuiltinModels().Stream(t.Context(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}, StreamOptions{APIKey: "k"}).Result()
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte(`"errorMessage":"Azure OpenAI base URL is required.`)) {
				t.Fatalf("result = %s, want the bare base URL error", data)
			}
			if errorFirst := bytes.Index(data, []byte(`"errorMessage"`)) < bytes.Index(data, []byte(`"timestamp"`)); errorFirst != wantErrorFirst {
				t.Fatalf("errorMessage first = %v, want %v: %s", errorFirst, wantErrorFirst, data)
			}
		})
	}
}
