package ai

// Ports .upstream/current/packages/ai/test/qwen-token-plan-models.test.ts.
// The findEnvKeys case is not here: env API-key resolution lives in
// ai/auth.go and internal/codingagent/model_registry.go.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

var qwenTextModels = []string{
	"MiniMax-M2.5", "deepseek-v3.2", "deepseek-v4-flash", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2",
	"kimi-k2.5", "kimi-k2.6", "kimi-k2.7-code", "qwen3.6-flash", "qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus",
	"qwen3.8-flash", "qwen3.8-max",
}

var qwenIndividualTextModels = []string{
	"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2",
	"qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max",
}

var qwenImageModels = []string{"qwen-image-2.0", "qwen-image-2.0-pro", "wan2.7-image", "wan2.7-image-pro"}

var qwenThinkingModels = []string{
	"deepseek-v3.2", "deepseek-v4-flash", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2",
	"kimi-k2.5", "kimi-k2.6", "kimi-k2.7-code", "qwen3.6-flash", "qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus",
	"qwen3.8-flash", "qwen3.8-max",
}

var qwenTokenPlanProviders = []string{"qwen-token-plan", "qwen-token-plan-cn", "qwen-token-plan-individual"}

type qwenModelCase struct{ provider, modelID string }

func qwenCases(providers, models []string) []qwenModelCase {
	var cases []qwenModelCase
	for _, provider := range providers {
		for _, modelID := range models {
			cases = append(cases, qwenModelCase{provider, modelID})
		}
	}
	return cases
}

func qwenThinkingCases() []qwenModelCase {
	return append(qwenCases(qwenTokenPlanProviders[:2], qwenThinkingModels), qwenCases(qwenTokenPlanProviders[2:], qwenIndividualTextModels)...)
}

func qwenReasoningEffortCases() []qwenModelCase {
	return append(
		qwenCases(qwenTokenPlanProviders[:2], []string{"deepseek-v4-flash", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2"}),
		qwenCases(qwenTokenPlanProviders[2:], []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2"})...,
	)
}

func qwen38Cases() []qwenModelCase {
	return qwenCases(qwenTokenPlanProviders, []string{"qwen3.8-flash", "qwen3.8-max"})
}

func generatedModelIDs(provider string) []string {
	var ids []string
	for _, model := range GeneratedModels {
		if model.Provider == provider {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

func mustGeneratedModel(t *testing.T, provider, modelID string) *GeneratedModel {
	t.Helper()
	model, ok := LookupModelExact(provider + "/" + modelID)
	if !ok {
		t.Fatalf("missing model %s/%s", provider, modelID)
	}
	return model
}

// captureCatalogCompletionsPayload streams one request for a catalog model
// through the OpenAI Completions provider, configured from the catalog entry
// as the model runtime does, and returns the serialized request body.
func captureCatalogCompletionsPayload(t *testing.T, provider, modelID string, level ThinkingLevel) map[string]any {
	t.Helper()
	model := mustGeneratedModel(t, provider, modelID)
	var payload map[string]any
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: model.BaseURL, APIKey: "test", Model: model.ID, ProviderID: model.Provider, Compat: cloneCompat(model.Compat)}}
	p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")),
		}, nil
	})}
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}), StreamOptions{IsReasoning: true, Thinking: level})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	return payload
}

func TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels(t *testing.T) {
	got := generatedModelIDs("qwen-token-plan-individual")
	slices.Sort(got)
	want := slices.Clone(qwenIndividualTextModels)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("qwen-token-plan-individual models = %v, want %v", got, want)
	}
}

func TestQwenTokenPlanExposesTextModelsAndOmitsImageModels(t *testing.T) {
	for _, provider := range qwenTokenPlanProviders[:2] {
		ids := generatedModelIDs(provider)
		for _, expected := range qwenTextModels {
			if !slices.Contains(ids, expected) {
				t.Errorf("%s should include %s", provider, expected)
			}
		}
		for _, excluded := range qwenImageModels {
			if slices.Contains(ids, excluded) {
				t.Errorf("%s should not include %s", provider, excluded)
			}
		}
	}
}

func TestQwenTokenPlanOmitsRetiredQwen38MaxPreview(t *testing.T) {
	for _, provider := range qwenTokenPlanProviders {
		if slices.Contains(generatedModelIDs(provider), "qwen3.8-max-preview") {
			t.Errorf("%s still lists qwen3.8-max-preview", provider)
		}
	}
}

func assertThinkingLevelMap(t *testing.T, model *GeneratedModel, want map[ThinkingLevel]string) {
	t.Helper()
	for level, value := range want {
		mapped, ok := model.ThinkingLevelMap[level]
		if !ok {
			t.Errorf("%s/%s thinkingLevelMap lacks %s", model.Provider, model.ID, level)
			continue
		}
		got := ""
		if mapped != nil {
			got = *mapped
		}
		if got != value {
			t.Errorf("%s/%s thinkingLevelMap[%s] = %q, want %q (empty means null)", model.Provider, model.ID, level, got, value)
		}
	}
}

func TestQwenTokenPlanExposesReasoningEffortLevels(t *testing.T) {
	for _, test := range qwenReasoningEffortCases() {
		assertThinkingLevelMap(t, mustGeneratedModel(t, test.provider, test.modelID), map[ThinkingLevel]string{
			ThinkingMinimal: "", ThinkingLow: "", ThinkingMedium: "", ThinkingHigh: "high", ThinkingXHigh: "", ThinkingMax: "max",
		})
	}
	for _, test := range qwen38Cases() {
		assertThinkingLevelMap(t, mustGeneratedModel(t, test.provider, test.modelID), map[ThinkingLevel]string{
			ThinkingMinimal: "", ThinkingLow: "low", ThinkingMedium: "medium", ThinkingHigh: "", ThinkingXHigh: "xhigh", ThinkingMax: "",
		})
	}
}

func TestQwenTokenPlanSendsQwenThinkingFields(t *testing.T) {
	for _, test := range qwenThinkingCases() {
		payload := captureCatalogCompletionsPayload(t, test.provider, test.modelID, ThinkingHigh)
		if payload["enable_thinking"] != true {
			t.Errorf("%s/%s enable_thinking = %v, want true", test.provider, test.modelID, payload["enable_thinking"])
		}
		if _, exists := payload["thinking"]; exists {
			t.Errorf("%s/%s payload has thinking: %v", test.provider, test.modelID, payload["thinking"])
		}
	}
}

func TestQwenTokenPlanSendsReasoningEffort(t *testing.T) {
	for _, test := range qwenReasoningEffortCases() {
		payload := captureCatalogCompletionsPayload(t, test.provider, test.modelID, ThinkingHigh)
		if payload["reasoning_effort"] != "high" {
			t.Errorf("%s/%s reasoning_effort = %v, want high", test.provider, test.modelID, payload["reasoning_effort"])
		}
	}
}

func TestQwenTokenPlanSendsQwen38XHighReasoningEffort(t *testing.T) {
	for _, test := range qwen38Cases() {
		payload := captureCatalogCompletionsPayload(t, test.provider, test.modelID, ThinkingXHigh)
		if payload["enable_thinking"] != true || payload["reasoning_effort"] != "xhigh" {
			t.Errorf("%s/%s enable_thinking=%v reasoning_effort=%v, want true/xhigh", test.provider, test.modelID, payload["enable_thinking"], payload["reasoning_effort"])
		}
		if _, exists := payload["thinking"]; exists {
			t.Errorf("%s/%s payload has thinking", test.provider, test.modelID)
		}
	}
}
