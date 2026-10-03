package ai

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// captureSamplingPayload mirrors the upstream onPayload exception: no request is sent.
func captureSamplingPayload(t *testing.T, provider Provider, transcript TranscriptContext, options StreamOptions) map[string]any {
	t.Helper()
	var captured map[string]any
	capturedError := errors.New("payload captured")
	options.OnPayload = func(payload any, _ *Model) (any, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &captured); err != nil {
			return nil, err
		}
		return nil, capturedError
	}
	stream, err := provider.Stream(t.Context(), transcript, options)
	if err == nil && stream != nil {
		_ = stream.Result()
	} else if !errors.Is(err, capturedError) {
		t.Fatalf("Stream: %v", err)
	}
	if captured == nil {
		t.Fatal("Expected payload to be captured before request failure")
	}
	return captured
}

func TestSamplingOptionsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name      string
		options   StreamOptions
		anthropic bool
		want      map[string]any
		absent    []string
	}{
		// .upstream/v0.99.1/packages/ai/test/sampling-options.test.ts:69 (merges request sampling params into the request body)
		{name: "merges request sampling params into the request body", options: StreamOptions{SamplingParams: map[string]any{"top_p": 0.95, "top_k": 0, "min_p": 0}}, want: map[string]any{"top_p": 0.95, "top_k": float64(0), "min_p": float64(0)}},
		// .upstream/v0.99.1/packages/ai/test/sampling-options.test.ts:79
		{name: "omits sampling params when neither options nor model set them", absent: []string{"temperature", "top_p"}},
		// .upstream/v0.99.1/packages/ai/test/sampling-options.test.ts:112
		{name: "overrides named request fields", options: StreamOptions{Temperature: 0, TemperatureSet: true, SamplingParams: map[string]any{"temperature": 1}}, want: map[string]any{"temperature": float64(1)}},
		// .upstream/v0.99.1/packages/ai/test/sampling-options.test.ts:121
		{name: "is ignored by non-OpenAI-compatible APIs", anthropic: true, options: StreamOptions{SamplingParams: map[string]any{"top_p": 0.9, "top_k": 40}}, absent: []string{"top_p", "top_k"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var provider Provider = NewOpenAIProvider(OpenAIConfig{APIKey: "fake-key", Model: "custom-model", ProviderID: "custom-provider", BaseURL: "http://127.0.0.1:9/v1"})
			if tc.anthropic {
				// 0.99.1 builds this model with the shared makeModel fixture (sampling-options.test.ts:25-39).
				provider = NewAnthropicProvider(AnthropicConfig{APIKey: "fake-key", Model: "custom-model", ProviderID: "custom-provider", BaseURL: "http://127.0.0.1:9/v1"})
			}
			payload := captureSamplingPayload(t, provider, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), tc.options)
			for key, want := range tc.want {
				if !reflect.DeepEqual(payload[key], want) {
					t.Errorf("%s = %#v, want %#v", key, payload[key], want)
				}
			}
			for _, key := range tc.absent {
				if value, ok := payload[key]; ok {
					t.Errorf("%s must be absent, got %#v", key, value)
				}
			}
		})
	}
}

// .upstream/v0.99.1/packages/ai/test/sampling-options.test.ts:87 (#9506): model defaults apply to direct stream()/complete() calls, not only streamSimple(); request keys take precedence.
func TestSamplingOptionsModelDefaultsOnDirectStreamUpstream(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses} {
		t.Run("applies model-level sampling params with request keys taking precedence for "+string(api), func(t *testing.T) {
			model := &Model{ID: "custom-model", DisplayName: "Custom Model", Input: []string{"text"}, SamplingParams: map[string]any{"top_p": 0.95, "min_p": 0.05},
				Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384},
				ProviderMeta: ProviderMetadata{API: api, ProviderID: "custom-provider", BaseURL: "http://127.0.0.1:9/v1"}}
			provider, err := directAPIProvider(model, "fake-key", nil)
			if err != nil {
				t.Fatal(err)
			}
			payload := captureSamplingPayload(t, provider, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{SamplingParams: map[string]any{"top_p": 0.5}})
			if payload["top_p"] != 0.5 || payload["min_p"] != 0.05 {
				t.Errorf("top_p=%#v min_p=%#v, want 0.5 and 0.05", payload["top_p"], payload["min_p"])
			}
		})
	}
}

// .upstream/v0.99.1/packages/ai/test/sampling-options.test.ts:99: request sampling params reach the payload through streamSimple. Upstream asserts only the captured top_p.
func TestSamplingOptionsPassThroughStreamSimpleUpstream(t *testing.T) {
	model := &Model{ID: "custom-model", DisplayName: "Custom Model", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384},
		ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "custom-provider", BaseURL: "http://127.0.0.1:9/v1"}}
	var captured map[string]any
	stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{APIKey: "fake-key", SamplingParams: map[string]any{"top_p": 0.5}, OnPayload: func(payload any, _ *Model) (any, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return nil, errors.Join(errors.New("payload captured"), json.Unmarshal(raw, &captured))
	}})
	if err == nil {
		stream.Result()
	}
	if captured["top_p"] != 0.5 {
		t.Fatalf("err=%v payload=%#v", err, captured)
	}
}
