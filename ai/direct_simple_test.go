package ai

import (
	"errors"
	"slices"
	"testing"
)

// Pi streamSimple dispatches every built-in api, including google-vertex (packages/ai/src/api/google-vertex.ts:313 streamSimple).
func TestDirectSimpleAuthenticationReachesRealConverters(t *testing.T) {
	for _, api := range []API{APIAnthropicMessages, APIAzureOpenAIResponses, APIGoogleGenerativeAI, APIGoogleVertex, APIMistralConversations, APIOpenAICodexResponses, APIOpenAICompletions, APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			model := &Model{ID: "test-model", ProviderMeta: ProviderMetadata{API: api, ProviderID: "test-provider", BaseURL: "https://example.invalid"}, Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
			sentinel := errors.New("captured")
			called := false
			stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}), StreamOptions{APIKey: "request-key", OnPayload: func(any, *Model) (any, error) { called = true; return nil, sentinel }})
			if api == APIAnthropicMessages {
				// packages/ai/src/api/anthropic-messages.ts:521-539,817-830: setup rejection returns the initialized assistant through one terminal error event, before start.
				if err != nil || stream == nil {
					t.Fatalf("stream=%v error=%v, want an error-terminated stream", stream != nil, err)
				}
				result := stream.Result()
				events := slices.Collect(stream.Events(t.Context()))
				if !called || len(events) != 1 {
					t.Fatalf("called=%v events=%v, want only the terminal error event", called, events)
				}
				failure, ok := events[0].(ErrorEvent)
				if !ok || failure.Reason != StopReasonError || failure.Error != result {
					t.Fatalf("event=%#v result=%#v, want the terminal ErrorEvent result", events[0], result)
				}
				if result.StopReason != StopReasonError || result.ErrorMessage != sentinel.Error() || result.API != api || result.Provider != model.ProviderMeta.ProviderID || result.Model != model.ID || result.Timestamp <= 0 {
					t.Fatalf("setup failure result=%#v", result)
				}
				return
			}
			if !called || !errors.Is(err, sentinel) || stream != nil {
				t.Fatalf("called=%v stream=%v error=%v", called, stream != nil, err)
			}
		})
	}
}

func TestDirectSimpleAuthHeadersMatchAPIRequirements(t *testing.T) {
	for _, api := range []API{APIAnthropicMessages, APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses, APIGoogleGenerativeAI, APIMistralConversations, APIOpenAICodexResponses} {
		for _, name := range []string{"Authorization", "Cf-Aig-Authorization", "X-Api-Key", "Other"} {
			for _, value := range []*string{nil, new(""), new(" \t"), new("credential")} {
				want := value != nil && *value == "credential" && ((api == APIAnthropicMessages && name != "Other") || ((api == APIOpenAICompletions || api == APIOpenAIResponses) && (name == "Authorization" || name == "Cf-Aig-Authorization")))
				_, err := directSimpleAPIKey(ProviderMetadata{API: api, ProviderID: "test-provider"}, StreamOptions{Headers: ProviderHeaders{name: value}})
				if (err == nil) != want {
					t.Fatalf("api=%s header=%s value=%v error=%v", api, name, value, err)
				}
			}
		}
	}
	// google-vertex.ts:430-434 resolveApiKey: without a key Vertex uses Application Default Credentials, so no key is not a direct-boundary error.
	if _, err := directSimpleAPIKey(ProviderMetadata{API: APIGoogleVertex, ProviderID: "test-provider"}, StreamOptions{}); err != nil {
		t.Fatalf("google-vertex without a key: %v", err)
	}
	// Model headers and ambient credentials are not options.apiKey/options.headers at the direct boundary.
	t.Setenv("OPENAI_API_KEY", "ambient")
	_, err := directSimpleAPIKey(ProviderMetadata{API: APIOpenAICompletions, ProviderID: "test-provider", Headers: map[string]string{"Authorization": "model-only"}}, StreamOptions{})
	if err == nil {
		t.Fatal("ambient/model auth crossed the direct request boundary")
	}
}
