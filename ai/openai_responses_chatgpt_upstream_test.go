package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func chatGPTTestModel(baseURL string, compat *ModelCompat) *Model {
	return &Model{ID: "gpt-5-mini", DisplayName: "GPT-5 Mini", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 400000, MaxOutputTokens: 128000},
		ProviderMeta: ProviderMetadata{API: APIOpenAIResponses, ProviderID: "openai", BaseURL: baseURL, Reasoning: true, Compat: compat}}
}

func responsesFetch(respond func() *http.Response) *http.Client {
	return &http.Client{Transport: FetchFunction(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return respond(), nil
	})}
}

// .upstream/v0.99.1/packages/ai/test/openai-responses-chatgpt-sign-in.test.ts:26 (direct stream, context at :20)
func captureChatGPTPayload(t *testing.T, apiKey string, model *Model) map[string]any {
	t.Helper()
	var payload map[string]any
	provider, err := directAPIProvider(model, apiKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(t.Context(), responsesUpstreamContext(), StreamOptions{
		MaxTokens: 1000, Temperature: 0.5, TemperatureSet: true, CacheRetention: CacheRetentionLong,
		OnPayload: func(raw any, _ *Model) (any, error) {
			encoded, err := json.Marshal(raw)
			if err != nil {
				return nil, err
			}
			return nil, errors.Join(json.Unmarshal(encoded, &payload))
		},
		Fetch: responsesFetch(func() *http.Response {
			return &http.Response{StatusCode: 500, Header: http.Header{}, Body: http.NoBody}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	if payload == nil {
		t.Fatal("Request payload was not captured")
	}
	return payload
}

func TestOpenAIResponsesChatGPTSignInUpstream(t *testing.T) {
	openai := "https://api.openai.com/v1"
	t.Run("omits request fields that token sharing rejects", func(t *testing.T) {
		payload := captureChatGPTPayload(t, "chatgpt-access-token", chatGPTTestModel(openai, nil))
		for _, field := range []string{"max_output_tokens", "temperature", "prompt_cache_retention"} {
			if value, ok := payload[field]; ok {
				t.Errorf("%s = %#v, want it omitted", field, value)
			}
		}
	})
	t.Run("omits prompt_cache_options on models with explicit prompt cache mode", func(t *testing.T) {
		explicit := chatGPTTestModel(openai, &ModelCompat{SupportsExplicitPromptCacheMode: new(true)})
		signIn := captureChatGPTPayload(t, "chatgpt-access-token", explicit)
		apiKey := captureChatGPTPayload(t, "sk-proj-test", explicit)
		if value, ok := signIn["prompt_cache_options"]; ok {
			t.Errorf("sign-in prompt_cache_options = %#v, want it omitted", value)
		}
		if got, _ := apiKey["prompt_cache_options"].(map[string]any); got["ttl"] != "30m" || len(got) != 1 {
			t.Errorf("api key prompt_cache_options = %#v, want {ttl: 30m}", apiKey["prompt_cache_options"])
		}
	})
	for _, tc := range []struct {
		name, apiKey, baseURL string
	}{
		{"OpenAI API keys", "sk-proj-test", openai},
		{"other OpenAI-compatible endpoints", "gateway-key", "https://gateway.example.com/v1"},
	} {
		t.Run("keeps those fields for "+tc.name, func(t *testing.T) {
			payload := captureChatGPTPayload(t, tc.apiKey, chatGPTTestModel(tc.baseURL, nil))
			if payload["max_output_tokens"] != float64(1000) || payload["temperature"] != 0.5 || payload["prompt_cache_retention"] != "24h" {
				t.Errorf("payload = %#v", payload)
			}
		})
	}
}

// .upstream/v0.99.1/packages/ai/test/openai-responses-usage-limit.test.ts:37
func TestOpenAIResponsesChatGPTUsageLimitUpstream(t *testing.T) {
	const usageLimit = `{"code":"subscription_sharing_usage_limit_exceeded","message":"Usage limit reached."}`
	errorMessage := func(t *testing.T, response *http.Response) string {
		t.Helper()
		provider, err := directAPIProvider(chatGPTTestModel("https://api.openai.com/v1", nil), "test", nil)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := provider.Stream(t.Context(), responsesUpstreamContext(), StreamOptions{Fetch: responsesFetch(func() *http.Response { return response })})
		if err != nil {
			t.Fatal(err)
		}
		result := stream.Result()
		if result.StopReason != StopReasonError {
			t.Fatalf("stopReason = %q", result.StopReason)
		}
		return result.ErrorMessage
	}
	const usageURL = "Check your ChatGPT usage: https://chatgpt.com/settings/usage"
	t.Run("links to ChatGPT usage when the request is rejected", func(t *testing.T) {
		body := `{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"Usage limit reached.","type":"rate_limit_error"}}`
		message := errorMessage(t, &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))})
		if !strings.Contains(message, "subscription_sharing_usage_limit_exceeded") || !strings.Contains(message, usageURL) {
			t.Errorf("errorMessage = %q", message)
		}
	})
	t.Run("links to ChatGPT usage when the stream fails", func(t *testing.T) {
		event := `{"type":"response.failed","sequence_number":0,"response":{"id":"resp_failed","status":"failed","error":` + usageLimit + `}}`
		message := errorMessage(t, &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.failed\ndata: " + event + "\n\n"))})
		if !strings.Contains(message, "subscription_sharing_usage_limit_exceeded: Usage limit reached.") || !strings.Contains(message, usageURL) {
			t.Errorf("errorMessage = %q", message)
		}
	})
}
