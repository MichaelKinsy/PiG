package ai

import (
	"reflect"
	"testing"
)

func TestPortWave12ZAICodingPlanModels(t *testing.T) {
	t.Parallel()
	// packages/ai/test/zai-coding-plan-models.test.ts:4
	t.Run("exposes GLM-4.6V on the China Coding Plan catalog", func(t *testing.T) {
		model := mustGeneratedModel(t, "zai-coding-cn", "glm-4.6v")
		if model.ID != "glm-4.6v" || model.Provider != "zai-coding-cn" || model.API != APIOpenAICompletions || model.BaseURL != "https://open.bigmodel.cn/api/coding/paas/v4" || !model.Reasoning || model.ContextWindow != 128000 || model.MaxOutputTokens != 32768 {
			t.Errorf("model metadata = %+v", model)
		}
		if !reflect.DeepEqual(model.Capabilities, []string{"text", "image"}) {
			t.Errorf("input = %v, want [text image]", model.Capabilities)
		}
		assertGeneratedModelCost(t, model, ModelCost{Input: 0.3, Output: 0.9})
		if model.Compat == nil || model.Compat.MaxTokensField != "max_tokens" || model.Compat.ThinkingFormat != "zai" || model.Compat.ZaiToolStream == nil || !*model.Compat.ZaiToolStream {
			t.Errorf("compat = %+v, want max_tokens/zai/zaiToolStream=true", model.Compat)
		}
	})

	// packages/ai/test/zai-coding-plan-models.test.ts:25
	t.Run("uses API-equivalent reference costs for Coding Plan models", func(t *testing.T) {
		want := ModelCost{Input: 1.4, Output: 4.4, CacheRead: 0.26, CacheWrite: 0}
		assertGeneratedModelCost(t, mustGeneratedModel(t, "zai", "glm-5.2"), want)
		for _, provider := range []string{"zai", "zai-coding-cn"} {
			t.Run(provider+"/glm-5.3", func(t *testing.T) {
				assertGeneratedModelCost(t, mustGeneratedModel(t, provider, "glm-5.3"), want)
			})
		}
	})

	// packages/ai/test/zai-coding-plan-models.test.ts:42
	t.Run("keeps zero costs for Coding Plan models without a matching API price", func(t *testing.T) {
		assertGeneratedModelCost(t, mustGeneratedModel(t, "zai", "glm-5.2-highspeed"), ModelCost{})
		for _, provider := range []string{"zai", "zai-coding-cn"} {
			t.Run(provider+"/glm-5.3-highspeed", func(t *testing.T) {
				assertGeneratedModelCost(t, mustGeneratedModel(t, provider, "glm-5.3-highspeed"), ModelCost{})
			})
		}
	})
}

func assertGeneratedModelCost(t *testing.T, model *GeneratedModel, want ModelCost) {
	t.Helper()
	got := model.ToModel().CostRates()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s/%s cost = %+v, want %+v", model.Provider, model.ID, got, want)
	}
}
