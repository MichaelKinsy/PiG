package ai

// Covers .upstream/current/packages/ai/src/providers/meta.ts and meta.models.ts:
// the generated catalog shard and the openai-responses request a Meta model
// sends. Upstream has no dedicated provider test file.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestMetaCatalogMatchesPinnedProviderDefinition(t *testing.T) {
	ids := generatedModelIDs("meta")
	slices.Sort(ids)
	want := []string{"muse-spark-1.1", "muse-spark-1.2", "muse-spark-1.2-contributor", "muse-spark-1.3", "muse-spark-1.3-contributor"}
	if !slices.Equal(ids, want) {
		t.Fatalf("meta models = %v, want %v", ids, want)
	}
	for _, id := range ids {
		model := mustGeneratedModel(t, "meta", id)
		if model.API != APIOpenAIResponses || model.BaseURL != "https://api.meta.ai/v1" || !model.Reasoning {
			t.Errorf("meta/%s = api %q base %q reasoning %v", id, model.API, model.BaseURL, model.Reasoning)
		}
	}
	assertThinkingLevelMap(t, mustGeneratedModel(t, "meta", "muse-spark-1.3"), map[ThinkingLevel]string{
		ThinkingOff: "", ThinkingMinimal: "minimal", ThinkingLow: "low", ThinkingMedium: "medium", ThinkingHigh: "high", ThinkingXHigh: "xhigh", ThinkingMax: "max",
	})
	assertThinkingLevelMap(t, mustGeneratedModel(t, "meta", "muse-spark-1.2"), map[ThinkingLevel]string{ThinkingMax: ""})
}

// captureMetaResponsesRequest streams one request for a Meta catalog model as
// the model runtime configures it and returns the request URL, headers, and body.
func captureMetaResponsesRequest(t *testing.T, modelID string, level ThinkingLevel) (string, http.Header, map[string]any) {
	t.Helper()
	model := mustGeneratedModel(t, "meta", modelID)
	var requestURL string
	var headers http.Header
	var payload map[string]any
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{
		BaseURL: model.BaseURL, APIKey: "LLM|minted-key", Model: model.ID, ProviderID: model.Provider,
		IsReasoning: model.Reasoning, Compat: cloneCompat(model.Compat),
	}).(*openAIResponsesProvider)
	provider.client = &http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requestURL = request.URL.String()
		headers = request.Header.Clone()
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))}, nil
	})}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{SystemPrompt: "be brief", Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{Thinking: level})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	return requestURL, headers, payload
}

func TestMetaResponsesRequestShape(t *testing.T) {
	requestURL, headers, payload := captureMetaResponsesRequest(t, "muse-spark-1.3", ThinkingMax)
	if requestURL != "https://api.meta.ai/v1/responses" {
		t.Fatalf("url = %s", requestURL)
	}
	if got := headers.Get("Authorization"); got != "Bearer LLM|minted-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if payload["model"] != "muse-spark-1.3" || payload["stream"] != true {
		t.Fatalf("payload = %#v", payload)
	}
	if reasoning, _ := payload["reasoning"].(map[string]any); reasoning["effort"] != "max" {
		t.Fatalf("reasoning = %#v, want effort max", payload["reasoning"])
	}
	input, _ := payload["input"].([]any)
	if first, _ := input[0].(map[string]any); len(input) != 2 || first["role"] != "developer" || first["content"] != "be brief" {
		t.Fatalf("input = %#v", payload["input"])
	}
}

func TestMetaResponsesClampsUnsupportedMaxThinking(t *testing.T) {
	_, _, payload := captureMetaResponsesRequest(t, "muse-spark-1.2", ThinkingMax)
	if reasoning, _ := payload["reasoning"].(map[string]any); reasoning["effort"] != "xhigh" {
		t.Fatalf("reasoning = %#v, want max clamped to xhigh", payload["reasoning"])
	}
}
