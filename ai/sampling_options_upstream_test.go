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
		// upstream: packages/ai/test/sampling-options.test.ts:97 (merges request sampling params into the request body)
		{name: "merges request sampling params into the request body", options: StreamOptions{SamplingParams: map[string]any{"top_p": 0.95, "top_k": 0, "min_p": 0}}, want: map[string]any{"top_p": 0.95, "top_k": float64(0), "min_p": float64(0)}},
		// upstream: packages/ai/test/sampling-options.test.ts:107
		{name: "omits sampling params when neither options nor model set them", absent: []string{"temperature", "top_p"}},
		// upstream: packages/ai/test/sampling-options.test.ts:217
		{name: "overrides named request fields", options: StreamOptions{Temperature: 0, TemperatureSet: true, SamplingParams: map[string]any{"temperature": 1}}, want: map[string]any{"temperature": float64(1)}},
		// upstream: packages/ai/test/sampling-options.test.ts:226
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

// upstream: packages/ai/test/sampling-options.test.ts:115 (#9506): model defaults apply to direct stream()/complete() calls, not only streamSimple(); request keys take precedence.
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

// upstream: packages/ai/test/sampling-options.test.ts:127: request sampling params reach the payload through streamSimple. Upstream asserts only the captured top_p.
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

// samplingThinkingModel is the 1.0.2 makeModel(api, samplingParams, overrides) fixture (sampling-options.test.ts:31-50).
func samplingThinkingModel(api API, samplingParams map[string]any, reasoning bool, levelMap ThinkingLevelMap, byLevel SamplingParamsByThinkingLevel) *Model {
	return &Model{ID: "custom-model", DisplayName: "Custom Model", Input: []string{"text"}, SamplingParams: samplingParams, ThinkingLevelMap: levelMap, SamplingParamsByThinkingLevel: byLevel,
		Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384},
		ProviderMeta: ProviderMetadata{API: api, ProviderID: "custom-provider", BaseURL: "http://127.0.0.1:9/v1", Reasoning: reasoning}}
}

// captureSimpleSamplingPayload mirrors captureSimplePayload (sampling-options.test.ts:79-94): streamSimple with the capturing onPayload.
func captureSimpleSamplingPayload(t *testing.T, model *Model, options StreamOptions) map[string]any {
	t.Helper()
	var captured map[string]any
	capturedError := errors.New("payload captured")
	options.APIKey = "fake-key"
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
	stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), options)
	if err == nil && stream != nil {
		_ = stream.Result()
	} else if !errors.Is(err, capturedError) {
		t.Fatalf("StreamSimple: %v", err)
	}
	if captured == nil {
		t.Fatal("Expected payload to be captured before request failure")
	}
	return captured
}

func assertSamplingPayload(t *testing.T, payload, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if !reflect.DeepEqual(payload[key], value) {
			t.Errorf("%s = %#v, want %#v", key, payload[key], value)
		}
	}
}

// Pi 1.0.2 (#9776) sampling params by thinking level through streamSimple (sampling-options.test.ts:135-174).
func TestSamplingParamsByThinkingLevelStreamSimpleUpstream(t *testing.T) {
	// :135 "applies sampling params for the effective thinking level over model defaults": low and medium are unsupported, so low clamps up to high.
	t.Run("applies sampling params for the effective thinking level over model defaults", func(t *testing.T) {
		// upstream: packages/ai/test/sampling-options.test.ts:135
		model := samplingThinkingModel(APIOpenAICompletions, map[string]any{"temperature": 1, "top_p": 0.95}, true,
			ThinkingLevelMap{ThinkingLow: nil, ThinkingMedium: nil},
			SamplingParamsByThinkingLevel{ThinkingHigh: {"temperature": 0.8, "top_k": 64}})
		payload := captureSimpleSamplingPayload(t, model, StreamOptions{Thinking: ThinkingLevelLow})
		assertSamplingPayload(t, payload, map[string]any{"temperature": 0.8, "top_p": 0.95, "top_k": float64(64)})
	})
	// :153 "applies off sampling params when reasoning is disabled"
	t.Run("applies off sampling params when reasoning is disabled", func(t *testing.T) {
		// upstream: packages/ai/test/sampling-options.test.ts:154
		model := samplingThinkingModel(APIOpenAICompletions, nil, false, nil, SamplingParamsByThinkingLevel{ThinkingOff: {"temperature": 0.7}})
		payload := captureSimpleSamplingPayload(t, model, StreamOptions{})
		assertSamplingPayload(t, payload, map[string]any{"temperature": 0.7})
	})
	// :163 "merges stream-option keys over thinking-level keys"
	t.Run("merges stream-option keys over thinking-level keys", func(t *testing.T) {
		// upstream: packages/ai/test/sampling-options.test.ts:164
		model := samplingThinkingModel(APIOpenAICompletions, nil, true, nil, SamplingParamsByThinkingLevel{ThinkingLow: {"temperature": 0.6, "top_p": 0.95}})
		payload := captureSimpleSamplingPayload(t, model, StreamOptions{Thinking: ThinkingLevelLow, SamplingParams: map[string]any{"top_p": 0.5}})
		assertSamplingPayload(t, payload, map[string]any{"temperature": 0.6, "top_p": 0.5})
	})
}

// Pi 1.0.2 (#9776) sampling params by thinking level on direct stream() requests (sampling-options.test.ts:177-215).
func TestSamplingParamsByThinkingLevelDirectStreamUpstream(t *testing.T) {
	// :177 "applies thinking-level params between model and request params for %s"
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAzureOpenAIResponses} {
		t.Run("applies thinking-level params between model and request params for "+string(api), func(t *testing.T) {
			// upstream: packages/ai/test/sampling-options.test.ts:177
			model := samplingThinkingModel(api, map[string]any{"temperature": 1, "top_p": 0.95}, true, nil, SamplingParamsByThinkingLevel{ThinkingLow: {"temperature": 0.6, "top_k": 64}})
			provider, err := directAPIProvider(model, "fake-key", nil)
			if err != nil {
				t.Fatal(err)
			}
			payload := captureSamplingPayload(t, provider, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}),
				StreamOptions{IsReasoning: true, ReasoningEffort: "low", SamplingParams: map[string]any{"top_p": 0.5}})
			assertSamplingPayload(t, payload, map[string]any{"temperature": 0.6, "top_p": 0.5, "top_k": float64(64)})
		})
	}
	// :198 "uses medium sampling params for summary-only %s requests"
	for _, api := range []API{APIOpenAIResponses, APIAzureOpenAIResponses} {
		t.Run("uses medium sampling params for summary-only "+string(api)+" requests", func(t *testing.T) {
			// upstream: packages/ai/test/sampling-options.test.ts:198
			model := samplingThinkingModel(api, nil, true, nil, SamplingParamsByThinkingLevel{ThinkingOff: {"temperature": 0.7}, ThinkingMedium: {"temperature": 0.8}})
			provider, err := directAPIProvider(model, "fake-key", nil)
			if err != nil {
				t.Fatal(err)
			}
			payload := captureSamplingPayload(t, provider, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}),
				StreamOptions{IsReasoning: true, ReasoningSummary: "auto"})
			reasoning, _ := payload["reasoning"].(map[string]any)
			if reasoning["effort"] != "medium" {
				t.Errorf("reasoning = %#v, want effort medium", payload["reasoning"])
			}
			assertSamplingPayload(t, payload, map[string]any{"temperature": 0.8})
		})
	}
}
