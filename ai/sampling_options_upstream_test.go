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
		name        string
		modelParams map[string]any
		options     StreamOptions
		anthropic   bool
		want        map[string]any
		absent      []string
	}{
		// .upstream/v0.87.1/packages/ai/test/sampling-options.test.ts:78
		{name: "merges stream-option sampling params into the request body", options: StreamOptions{SamplingParams: map[string]any{"top_p": 0.95, "top_k": 0, "min_p": 0}}, want: map[string]any{"top_p": 0.95, "top_k": float64(0), "min_p": float64(0)}},
		// .upstream/v0.87.1/packages/ai/test/sampling-options.test.ts:88
		{name: "omits sampling params when neither options nor model set them", absent: []string{"temperature", "top_p"}},
		// .upstream/v0.87.1/packages/ai/test/sampling-options.test.ts:95
		{name: "applies model-level sampling params", modelParams: map[string]any{"temperature": 1, "top_p": 0.95}, want: map[string]any{"temperature": float64(1), "top_p": 0.95}},
		// .upstream/v0.87.1/packages/ai/test/sampling-options.test.ts:102
		{name: "merges stream-option keys over model-level keys", modelParams: map[string]any{"top_p": 0.95, "min_p": 0.05}, options: StreamOptions{SamplingParams: map[string]any{"top_p": 0.5}}, want: map[string]any{"top_p": 0.5, "min_p": 0.05}},
		// .upstream/v0.87.1/packages/ai/test/sampling-options.test.ts:111
		{name: "overrides named request fields", options: StreamOptions{Temperature: 0, TemperatureSet: true, SamplingParams: map[string]any{"temperature": 1}}, want: map[string]any{"temperature": float64(1)}},
		// .upstream/v0.87.1/packages/ai/test/sampling-options.test.ts:120
		{name: "is ignored by non-OpenAI-compatible APIs", anthropic: true, options: StreamOptions{SamplingParams: map[string]any{"top_p": 0.9, "top_k": 40}}, absent: []string{"top_p", "top_k"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var provider Provider = NewOpenAIProvider(OpenAIConfig{APIKey: "fake-key", Model: "custom-model", ProviderID: "custom-provider", BaseURL: "http://127.0.0.1:9/v1", SamplingParams: tc.modelParams})
			if tc.anthropic {
				provider = NewAnthropicProvider(AnthropicConfig{APIKey: "fake-key", Model: "vendor--claude", ProviderID: "vendor-proxy", BaseURL: "http://127.0.0.1:9"})
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
