package providers

// pi: packages/ai/catalog/providers/azure.ts
// pi: packages/ai/test/azure-models.test.ts

import (
	"reflect"
	"testing"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

func azureTestOpenAIModel() ChatModel {
	return O(
		"id", "gpt-6-sol",
		"name", "GPT-6 Sol",
		"api", "openai-responses",
		"provider", "openai",
		"baseUrl", "https://api.openai.com/v1",
		"reasoning", true,
		"input", []any{"text", "image"},
		"cost", O(
			"input", 2.0,
			"output", 10.0,
			"cacheRead", 0.2,
			"cacheWrite", 2.5,
			"tiers", []any{O("inputTokensAbove", 272000.0, "input", 4.0, "output", 15.0, "cacheRead", 0.4, "cacheWrite", 5.0)},
		),
		"contextWindow", 272000.0,
		"maxTokens", 128000.0,
	)
}

// Pi ea1cfcc4c (#11949): Azure's copy of an OpenAI model keeps OpenAI's long-context price tier.
func TestAzureKeepsOpenAILongContextPriceTier(t *testing.T) {
	models := azureOpenAIModels([]ChatModel{azureTestOpenAIModel()})
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}
	model := models[0]
	if model.Str("provider") != "azure" || model.Str("api") != "azure-openai-responses" {
		t.Fatalf("provider = %q, api = %q", model.Str("provider"), model.Str("api"))
	}
	if model.Num("contextWindow") != 1050000 {
		t.Fatalf("contextWindow = %v, want 1050000", model.Num("contextWindow"))
	}
	if want := azureTestOpenAIModel().Object("cost"); !reflect.DeepEqual(model.Object("cost"), want) {
		t.Fatalf("cost = %#v, want %#v", model.Object("cost"), want)
	}
}
