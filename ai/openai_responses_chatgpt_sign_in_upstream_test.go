package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func chatgptSignInPayload(t *testing.T, apiKey, baseURL string, compat *OpenAIResponsesCompat) map[string]json.RawMessage {
	t.Helper()
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: "gpt-5-mini", ProviderID: "openai", BaseURL: baseURL, APIKey: apiKey, IsReasoning: true, Compat: compat}).(*openAIResponsesProvider)
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	var payload map[string]json.RawMessage
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{MaxTokens: 1000, Temperature: 0.5, TemperatureSet: true, CacheRetention: CacheRetentionLong,
		OnPayload: func(next any, _ *Model) (any, error) {
			data, err := json.Marshal(next)
			if err != nil {
				return nil, err
			}
			return nil, json.Unmarshal(data, &payload)
		}})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	if payload == nil {
		t.Fatal("Request payload was not captured")
	}
	return payload
}

// Ports packages/ai/test/openai-responses-chatgpt-sign-in.test.ts.
func TestOpenAIResponsesChatGPTSignInRequestFieldsUpstream(t *testing.T) {
	const openAIBaseURL = "https://api.openai.com/v1"
	// .upstream/v0.99.1/packages/ai/test/openai-responses-chatgpt-sign-in.test.ts:45
	t.Run("omits request fields that token sharing rejects", func(t *testing.T) {
		payload := chatgptSignInPayload(t, "chatgpt-access-token", openAIBaseURL, nil)

		for _, field := range []string{"max_output_tokens", "temperature", "prompt_cache_retention"} {
			if _, ok := payload[field]; ok {
				t.Errorf("payload has %s: %s", field, payload[field])
			}
		}
	})
	// .upstream/v0.99.1/packages/ai/test/openai-responses-chatgpt-sign-in.test.ts:53
	t.Run("omits prompt_cache_options on models with explicit prompt cache mode", func(t *testing.T) {
		explicit := &OpenAIResponsesCompat{SupportsExplicitPromptCacheMode: new(true)}

		signIn := chatgptSignInPayload(t, "chatgpt-access-token", openAIBaseURL, explicit)
		apiKey := chatgptSignInPayload(t, "sk-proj-test", openAIBaseURL, explicit)

		if _, ok := signIn["prompt_cache_options"]; ok {
			t.Errorf("sign-in payload has prompt_cache_options: %s", signIn["prompt_cache_options"])
		}
		if string(apiKey["prompt_cache_options"]) != `{"ttl":"30m"}` {
			t.Errorf("api key prompt_cache_options=%s", apiKey["prompt_cache_options"])
		}
	})
	for _, tc := range []struct{ name, apiKey, baseURL string }{
		{"OpenAI API keys", "sk-proj-test", openAIBaseURL},
		{"other OpenAI-compatible endpoints", "gateway-key", "https://gateway.example.com/v1"},
	} {
		// .upstream/v0.99.1/packages/ai/test/openai-responses-chatgpt-sign-in.test.ts:63
		t.Run("keeps those fields for "+tc.name, func(t *testing.T) {
			payload := chatgptSignInPayload(t, tc.apiKey, tc.baseURL, nil)

			if string(payload["max_output_tokens"]) != "1000" || string(payload["temperature"]) != "0.5" || string(payload["prompt_cache_retention"]) != `"24h"` {
				t.Errorf("payload=%v", payload)
			}
		})
	}
}
