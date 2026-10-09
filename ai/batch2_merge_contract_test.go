package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// The selected model must outrank a same-named catalog entry and the legacy map-only constructor input, without mutating that selected descriptor.
func TestBatch2SelectedModelDataSurvivesCustomFetchConstruction(t *testing.T) {
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses} {
		t.Run(string(api), func(t *testing.T) {
			selected := &Model{ID: "gpt-4o-mini", Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: "openai", API: api, Reasoning: true}, ThinkingLevelMap: ThinkingLevelMap{ThinkingHigh: new("selected-high")}, Capabilities: ModelCapabilities{SupportsImages: true}}
			original := *selected
			legacy := ThinkingLevelMap{ThinkingHigh: new("ignored-legacy")}
			var provider Provider
			if api == APIOpenAICompletions {
				provider = NewOpenAIProvider(OpenAIConfig{APIKey: "key", Model: selected.ID, ProviderID: "openai", ModelMetadata: selected, ThinkingLevelMap: legacy})
			} else {
				provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "key", Model: selected.ID, ProviderID: "openai", ModelMetadata: selected, ThinkingLevelMap: legacy, IsReasoning: true})
			}
			payload := captureSamplingPayload(t, provider, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "describe"}, ImageContent{Data: "ZmFrZQ==", MimeType: "image/png"}}}}}), StreamOptions{Thinking: ThinkingLevelHigh, IsReasoning: true})
			if api == APIOpenAICompletions {
				if payload["reasoning_effort"] != "selected-high" {
					t.Fatalf("selected effort lost: %#v", payload)
				}
			} else {
				reasoning, ok := payload["reasoning"].(map[string]any)
				if !ok || reasoning["effort"] != "selected-high" {
					t.Fatalf("selected effort lost: %#v", payload)
				}
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"image_url"`) || strings.Contains(string(raw), `"input_image"`) {
				t.Fatalf("selected text-only model received an image: %s", raw)
			}
			if !reflect.DeepEqual(*selected, original) {
				t.Fatal("selected model was mutated")
			}
		})
	}
}

// Codex owns retries/response observation, while the caller still owns HTTP execution.
func TestBatch2CodexCustomFetchRetainsNativeRetryAndObservation(t *testing.T) {
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: codexTestToken(t, "merge-account"), Model: "gpt-5.5", ProviderID: "openai-codex"}).(*openAIResponsesProvider)
	fallback := 0
	provider.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		fallback++
		return nil, errors.New("unexpected fallback transport")
	})}
	calls := 0
	fetch := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After-Ms": []string{"0"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"overloaded"}}`))}, nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(responseIDWireFixture(APIOpenAICodexResponses)))}, nil
	})}
	observed := []int{}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{Fetch: fetch, Transport: TransportSSE, MaxRetries: new(1), OnResponse: func(_ context.Context, response ProviderResponse, _ *Model) error {
		observed = append(observed, response.Status)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop || calls != 2 || fallback != 0 || !reflect.DeepEqual(observed, []int{429, 200}) || result.ResponseID != "response-id-fixture" {
		t.Fatalf("result=%#v calls=%d fallback=%d observed=%v", result, calls, fallback, observed)
	}
}
