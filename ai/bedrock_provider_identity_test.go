package ai

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

func TestBedrockSelectedProviderIdentity(t *testing.T) {
	// packages/ai/src/api/bedrock-converse-stream.ts:134-143: output.provider is model.provider, not the canonical API provider name.
	for _, id := range []string{"amazon-bedrock", "custom-bedrock-proxy", ""} {
		model := Model{ID: "custom-model", ProviderMeta: ProviderMetadata{ProviderID: id, API: APIBedrockConverseStream}}
		if got := NewBedrockProviderWithModel(model).ID(); got != id {
			t.Errorf("selected provider ID=%q, want %q", got, id)
		}
	}
	for _, provider := range []*BedrockProvider{NewBedrockProvider("model", ""), NewBedrockProviderWithName("model", "name", "")} {
		if got := provider.ID(); got != "amazon-bedrock" {
			t.Errorf("default provider ID=%q", got)
		}
	}
}

func TestBedrockStreamSimplePreservesSelectedProviderIdentity(t *testing.T) {
	for _, key := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_BEARER_TOKEN_BEDROCK"} {
		t.Setenv(key, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	for _, providerID := range []string{"amazon-bedrock", "custom-bedrock-proxy", ""} {
		for _, stop := range []string{"end_turn", "guardrail_intervened"} {
			t.Run(providerID+"/"+stop, func(t *testing.T) {
				var response bytes.Buffer
				encoder := eventstream.NewEncoder()
				for _, event := range []struct{ kind, payload string }{
					{"messageStart", `{"role":"assistant"}`},
					{"contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"answer"}}`},
					{"contentBlockStop", `{"contentBlockIndex":0}`},
					{"messageStop", `{"stopReason":"` + stop + `"}`},
				} {
					var headers eventstream.Headers
					headers.Set(":message-type", eventstream.StringValue("event"))
					headers.Set(":event-type", eventstream.StringValue(event.kind))
					headers.Set(":content-type", eventstream.StringValue("application/json"))
					if err := encoder.Encode(&response, eventstream.Message{Headers: headers, Payload: []byte(event.payload)}); err != nil {
						t.Fatal(err)
					}
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
					_, _ = w.Write(response.Bytes())
				}))
				t.Cleanup(server.Close)
				model := &Model{ID: "custom-model", ProviderMeta: ProviderMetadata{ProviderID: providerID, API: APIBedrockConverseStream, BaseURL: server.URL}, Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
				stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
				if err != nil {
					t.Fatal(err)
				}
				result := stream.Result()
				wantReason := StopReasonStop
				if stop != "end_turn" {
					wantReason = StopReasonError
				}
				if result.Provider != providerID || result.Model != model.ID || result.API != APIBedrockConverseStream || result.StopReason != wantReason {
					t.Fatalf("result=%+v; want provider=%q reason=%s", result, providerID, wantReason)
				}
				for event := range stream.Events(t.Context()) {
					if start, ok := event.(StartEvent); ok && start.Partial.Provider != providerID {
						t.Errorf("start provider=%q, want %q", start.Partial.Provider, providerID)
					}
				}
			})
		}
	}
}
