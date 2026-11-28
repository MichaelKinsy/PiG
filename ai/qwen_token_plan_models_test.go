package ai

// pi: packages/ai/src/providers/qwen-token-plan-individual.ts

// pi: packages/ai/src/providers/qwen-token-plan.ts
// pi: packages/ai/src/providers/qwen-token-plan-cn.ts
// pi: packages/ai/src/providers/qwen-token-plan-individual-cn.ts

// Ports packages/ai/test/qwen-token-plan-models.test.ts (Pi 1397070024).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var qwenTextModels = []string{
	"MiniMax-M2.5", "deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2",
	"kimi-k2.5", "kimi-k2.6", "kimi-k2.7-code", "qwen3.6-flash", "qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus",
	"qwen3.8-flash", "qwen3.8-max",
}

var qwenIndividualTextModels = []string{
	"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "deepseek-v4.1-flash", "deepseek-v4.1-flash-1017", "glm-5.2", "glm-5.3",
	"qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max", "qwen4-max",
}

var qwenImageModels = []string{"qwen-image-2.0", "qwen-image-2.0-pro", "wan2.7-image", "wan2.7-image-pro"}

var qwenThinkingModels = []string{
	"deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2",
	"kimi-k2.5", "kimi-k2.6", "kimi-k2.7-code", "qwen3.6-flash", "qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus",
	"qwen3.8-flash", "qwen3.8-max",
}

var qwenTokenPlanProviders = []string{"qwen-token-plan", "qwen-token-plan-cn", "qwen-token-plan-individual", "qwen-token-plan-individual-cn"}

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
		qwenCases(qwenTokenPlanProviders[:2], []string{"deepseek-v4-pro", "glm-5", "glm-5.1", "glm-5.2"}),
		qwenCases(qwenTokenPlanProviders[2:], []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "glm-5.3"})...,
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
func captureCatalogCompletionsPayload(t *testing.T, provider, modelID string, level ModelThinkingLevel) map[string]any {
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
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}), StreamOptions{IsReasoning: true, Thinking: level.ReasoningOption()})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	return payload
}

func TestQwenTokenPlanIndividualExposesExactlyDocumentedTextModels(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:122
	t.Run("exposes exactly the documented Individual text models", func(t *testing.T) {
		got := generatedModelIDs("qwen-token-plan-individual")
		slices.Sort(got)
		want := slices.Clone(qwenIndividualTextModels)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("qwen-token-plan-individual models = %v, want %v", got, want)
		}
	})
}

func TestQwenTokenPlanIndividualCnExposesExactlyDocumentedTextModels(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:146 (3e4f8726)
	t.Run("exposes exactly the documented Individual text models on the China endpoint", func(t *testing.T) {
		got := generatedModelIDs("qwen-token-plan-individual-cn")
		slices.Sort(got)
		want := slices.Clone(qwenIndividualTextModels)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("qwen-token-plan-individual-cn models = %v, want %v", got, want)
		}
	})
}

// #11773
func TestQwenTokenPlanPrefersThePerPlanKeyAndFallsBackToTheSharedOne(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:159
	for _, test := range []struct{ provider, planVar, sharedVar string }{
		{"qwen-token-plan", "QWEN_TOKEN_PLAN_TEAM_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
		{"qwen-token-plan-cn", "QWEN_TOKEN_PLAN_TEAM_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
		{"qwen-token-plan-individual", "QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY", "QWEN_TOKEN_PLAN_API_KEY"},
		{"qwen-token-plan-individual-cn", "QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY", "QWEN_TOKEN_PLAN_CN_API_KEY"},
	} {
		t.Run("prefers the per-plan key on "+test.provider+" and falls back to the shared one", func(t *testing.T) {
			clearEnvKeyVars(t)
			resolve := func(env map[string]string) string {
				t.Helper()
				models := CreateModels(CreateModelsOptions{AuthContext: &AuthContext{
					Env:        func(name string) (string, bool) { value, ok := env[name]; return value, ok },
					FileExists: func(string) bool { return false },
				}})
				models.SetProvider(builtinProviderConstructors[test.provider]())
				result, err := models.GetAuth(t.Context(), test.provider)
				if err != nil {
					t.Fatalf("GetAuth: %v", err)
				}
				if result == nil {
					return ""
				}
				return result.Auth.APIKey
			}
			both, shared := map[string]string{test.planVar: "plan", test.sharedVar: "shared"}, map[string]string{test.sharedVar: "shared"}
			if got := resolve(both); got != "plan" {
				t.Errorf("provider auth with %s and %s = %q, want plan", test.planVar, test.sharedVar, got)
			}
			if got := resolve(shared); got != "shared" {
				t.Errorf("provider auth with %s only = %q, want shared", test.sharedVar, got)
			}
			if got := GetEnvAPIKey(test.provider, both); got != "plan" {
				t.Errorf("GetEnvAPIKey with %s and %s = %q, want plan", test.planVar, test.sharedVar, got)
			}
			if got := GetEnvAPIKey(test.provider, shared); got != "shared" {
				t.Errorf("GetEnvAPIKey with %s only = %q, want shared", test.sharedVar, got)
			}
		})
	}
}

func TestQwenTokenPlanKeepsAPerPlanKeyAwayFromTheOtherPlan(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:182
	t.Run("keeps a per-plan key away from the other plan", func(t *testing.T) {
		clearEnvKeyVars(t)
		if got := FindEnvKeys("qwen-token-plan", map[string]string{"QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY": "test"}); got != nil {
			t.Errorf("FindEnvKeys(qwen-token-plan) = %v, want none", got)
		}
		if got := FindEnvKeys("qwen-token-plan-individual-cn", map[string]string{"QWEN_TOKEN_PLAN_TEAM_CN_API_KEY": "test"}); got != nil {
			t.Errorf("FindEnvKeys(qwen-token-plan-individual-cn) = %v, want none", got)
		}
	})
}

// The four providers are one catalog function over a models.dev source, a gateway and an optional allowlist (catalog/providers/qwen-token-plan.ts
// qwenTokenPlanModels), so every record sits on its provider's gateway and an Individual record is its region's Team record under another provider id.
func TestQwenTokenPlanProvidersShareOneCatalogPerRegion(t *testing.T) {
	for _, pair := range []struct{ team, individual string }{{"qwen-token-plan", "qwen-token-plan-individual"}, {"qwen-token-plan-cn", "qwen-token-plan-individual-cn"}} {
		gateway := builtinProviderConstructors[pair.team]().BaseURL
		if individual := builtinProviderConstructors[pair.individual]().BaseURL; gateway == "" || individual != gateway {
			t.Errorf("%s gateway = %q, %s gateway = %q, want one gateway per region", pair.team, gateway, pair.individual, individual)
		}
		for _, provider := range []string{pair.team, pair.individual} {
			for _, id := range generatedModelIDs(provider) {
				if model := mustGeneratedModel(t, provider, id); model.BaseURL != gateway {
					t.Errorf("%s/%s baseUrl = %q, want the provider's gateway %q", provider, id, model.BaseURL, gateway)
				}
			}
		}
		for _, id := range generatedModelIDs(pair.individual) {
			team, ok := LookupModelExact(pair.team + "/" + id)
			if !ok {
				t.Errorf("%s/%s has no %s record", pair.individual, id, pair.team)
				continue
			}
			// Record names the row's own stored JSON, which carries the provider id.
			got, want := *mustGeneratedModel(t, pair.individual, id), *team
			want.Provider, want.Record, got.Record = pair.individual, "", ""
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s/%s = %+v, want the %s record %+v", pair.individual, id, got, pair.team, want)
			}
		}
	}
}

func TestQwenTokenPlanExposesTextModelsAndOmitsImageModels(t *testing.T) {
	for _, provider := range qwenTokenPlanProviders[:2] {
		// upstream: packages/ai/test/qwen-token-plan-models.test.ts:136
		t.Run("exposes all text models on "+provider, func(t *testing.T) {
			ids := generatedModelIDs(provider)
			for _, expected := range qwenTextModels {
				if !slices.Contains(ids, expected) {
					t.Errorf("%s should include %s", provider, expected)
				}
			}
		})
		// upstream: packages/ai/test/qwen-token-plan-models.test.ts:143
		t.Run("omits image models from "+provider, func(t *testing.T) {
			ids := generatedModelIDs(provider)
			for _, excluded := range qwenImageModels {
				if slices.Contains(ids, excluded) {
					t.Errorf("%s should not include %s", provider, excluded)
				}
			}
		})
	}
}

func TestQwenTokenPlanOmitsRetiredModels(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:286
	for _, provider := range qwenTokenPlanProviders {
		t.Run("omits retired models on "+provider, func(t *testing.T) {
			ids := generatedModelIDs(provider)
			// #11773: deepseek-v3.2 and deepseek-v4-flash left the Token Plan model lists.
			for _, retired := range []string{"qwen3.8-max-preview", "deepseek-v3.2", "deepseek-v4-flash"} {
				if slices.Contains(ids, retired) {
					t.Errorf("%s still lists %s", provider, retired)
				}
			}
		})
	}
}

func assertThinkingLevelMap(t *testing.T, model *GeneratedModel, want map[ModelThinkingLevel]string) {
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
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:184
	for _, test := range qwenReasoningEffortCases() {
		t.Run("exposes Qwen reasoning_effort levels for "+test.provider+"/"+test.modelID, func(t *testing.T) {
			low := (*string)(nil)
			if test.modelID == "glm-5.3" {
				low = ptrString("low")
			}
			assertThinkingLevelPresence(t, mustGeneratedModel(t, test.provider, test.modelID), map[ModelThinkingLevel]*string{
				ThinkingMinimal: nil, ThinkingLow: low, ThinkingMedium: nil, ThinkingHigh: ptrString("high"), ThinkingXHigh: nil, ThinkingMax: ptrString("max"),
			})
		})
	}
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:203 (d53980fe)
	t.Run("exposes glm-5.3 reasoning_effort levels for qwen-token-plan-individual", func(t *testing.T) {
		model := mustGeneratedModel(t, "qwen-token-plan-individual", "glm-5.3")
		assertThinkingLevelPresence(t, model, map[ModelThinkingLevel]*string{
			ThinkingMinimal: nil, ThinkingLow: ptrString("low"), ThinkingMedium: nil, ThinkingHigh: ptrString("high"), ThinkingXHigh: nil, ThinkingMax: ptrString("max"),
		})
	})
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:202
	for _, test := range qwen38Cases() {
		t.Run("exposes qwen3.8 reasoning_effort levels for "+test.provider+"/"+test.modelID, func(t *testing.T) {
			assertThinkingLevelPresence(t, mustGeneratedModel(t, test.provider, test.modelID), map[ModelThinkingLevel]*string{
				ThinkingMinimal: nil, ThinkingLow: ptrString("low"), ThinkingMedium: ptrString("medium"), ThinkingHigh: nil, ThinkingXHigh: ptrString("xhigh"), ThinkingMax: nil,
			})
		})
	}
}

func TestQwenTokenPlanSendsQwenThinkingFields(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:151
	for _, test := range qwenThinkingCases() {
		t.Run("sends Qwen thinking fields for "+test.provider+"/"+test.modelID, func(t *testing.T) {
			payload := captureCatalogCompletionsPayload(t, test.provider, test.modelID, ThinkingHigh)
			if payload["enable_thinking"] != true {
				t.Errorf("%s/%s enable_thinking = %v, want true", test.provider, test.modelID, payload["enable_thinking"])
			}
			if _, exists := payload["thinking"]; exists {
				t.Errorf("%s/%s payload has thinking: %v", test.provider, test.modelID, payload["thinking"])
			}
		})
	}
}

func TestQwenTokenPlanSendsReasoningEffort(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:228
	for _, test := range qwenReasoningEffortCases() {
		t.Run("sends Qwen reasoning_effort for "+test.provider+"/"+test.modelID, func(t *testing.T) {
			payload := captureCatalogCompletionsPayload(t, test.provider, test.modelID, ThinkingHigh)
			if payload["reasoning_effort"] != "high" {
				t.Errorf("%s/%s reasoning_effort = %v, want high", test.provider, test.modelID, payload["reasoning_effort"])
			}
		})
	}
}

func TestQwenTokenPlanSendsQwen38XHighReasoningEffort(t *testing.T) {
	// upstream: packages/ai/test/qwen-token-plan-models.test.ts:260
	for _, test := range qwen38Cases() {
		t.Run("sends qwen3.8 xhigh reasoning_effort for "+test.provider+"/"+test.modelID, func(t *testing.T) {
			payload := captureCatalogCompletionsPayload(t, test.provider, test.modelID, ThinkingXHigh)
			if payload["enable_thinking"] != true || payload["reasoning_effort"] != "xhigh" {
				t.Errorf("%s/%s enable_thinking=%v reasoning_effort=%v, want true/xhigh", test.provider, test.modelID, payload["enable_thinking"], payload["reasoning_effort"])
			}
			if _, exists := payload["thinking"]; exists {
				t.Errorf("%s/%s payload has thinking", test.provider, test.modelID)
			}
		})
	}
}
