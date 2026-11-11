package providers

// Ports packages/ai/catalog/providers/openai.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

const openaiBaseURL = "https://api.openai.com/v1"

// models.dev lists this alias, but it is not accepted by OpenAI APIs.
var openaiUnsupportedModelIDs = map[string]bool{"gpt-5.6": true}

// Keep direct OpenAI requests in the short-context pricing tier by default. Users can opt into the
// larger context through model overrides, so retain long-context cost metadata on the capped models.
var openaiShortContextCappedModelIDs = map[string]bool{
	"gpt-5.4":       true,
	"gpt-5.5":       true,
	"gpt-5.6-sol":   true,
	"gpt-5.6-terra": true,
	"gpt-5.6-luna":  true,
	"gpt-6-astra":   true,
	"gpt-6-sol":     true,
	"gpt-6-luna":    true,
	"gpt-6.1-sol":   true,
	"gpt-6.1-terra": true,
}

var openaiLongContextPricingModelIDs = func() map[string]bool {
	ids := map[string]bool{"gpt-5.4-pro": true, "gpt-5.5-pro": true}
	for id := range openaiShortContextCappedModelIDs {
		ids[id] = true
	}
	return ids
}()

var openaiNoneReasoningModelIDs = map[string]bool{
	"gpt-5.1":       true,
	"gpt-5.2":       true,
	"gpt-5.3-codex": true,
	"gpt-5.4":       true,
	"gpt-5.4-mini":  true,
	"gpt-5.4-nano":  true,
	"gpt-5.5":       true,
	"gpt-5.6-sol":   true,
	"gpt-5.6-terra": true,
	"gpt-5.6-luna":  true,
	"gpt-6-sol":     true,
	"gpt-6-luna":    true,
}

// OpenAI documents configuration_update for the GPT-6 family.
// https://developers.openai.com/api/docs/guides/reasoning
var openaiMidConvoEffortModelIDs = map[string]bool{
	"gpt-6-astra": true, "gpt-6-sol": true, "gpt-6-luna": true, "gpt-6.1-sol": true, "gpt-6.1-terra": true,
}

// Not listed by models.dev.
func openaiGPT5ChatLatest() ChatModel {
	return O(
		"id", "gpt-5-chat-latest",
		"name", "GPT-5 Chat Latest",
		"api", "openai-responses",
		"baseUrl", openaiBaseURL,
		"provider", "openai",
		"reasoning", false,
		"input", Strings("text", "image"),
		"cost", O("input", 1.25, "output", 10, "cacheRead", 0.125, "cacheWrite", 0),
		"contextWindow", 128000,
		"maxTokens", 16384,
	)
}

// OpenAI Decisions API (public beta): gpt-6-luna is its only model. It bills input tokens only,
// with the same long-context multiplier as chat requests. Only API keys work: Sign in with ChatGPT
// tokens are rejected on /v1/decisions, and the Codex backend has no Decisions route.
// The endpoint rejects inputs above 922K tokens (the model's documented maximum input), but
// requests running longer than about five seconds, currently above roughly 600K input tokens,
// fail with a gateway timeout.
// https://developers.openai.com/api/docs/guides/decisions
func openaiClassifierModels() []*Obj {
	return []*Obj{
		O(
			"type", "classifier",
			"id", "gpt-6-luna",
			"name", "GPT-6 Luna",
			"api", "openai-decisions",
			"provider", "openai",
			"baseUrl", openaiBaseURL,
			"input", Strings("text", "image"),
			"cost", WithOpenAILongContextPricing(O("input", 0.1, "output", 0, "cacheRead", 0, "cacheWrite", 0)),
			"contextWindow", 922000,
		),
	}
}

func openaiCorrectUpstream(model ChatModel) ChatModel {
	id := model.Str("id")
	if openaiShortContextCappedModelIDs[id] {
		model.Set("contextWindow", OpenAILongContextInputThreshold)
		model.Set("maxTokens", 128000)
	}
	if openaiLongContextPricingModelIDs[id] {
		cost := OpenAIStandardCosts()[id]
		if cost == nil {
			cost = model.Object("cost")
		}
		model.Set("cost", WithOpenAILongContextPricing(cost))
	}
	// models.dev reports gpt-5-pro output as 272000 (a duplicate of the input sub-limit);
	// the actual max output is 128000. Also propagates to the derived Azure clone.
	if id == "gpt-5-pro" {
		model.Set("maxTokens", 128000)
	}
	return model
}

func openai() ProviderDefinition {
	return ProviderDefinition{
		ID: "openai",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			models, err := ModelsDevModels(ctx, "openai", ModelsDevOptions{
				API:     "openai-responses",
				BaseURL: openaiBaseURL,
				// Audio models are only served on Chat Completions.
				Protocol: "responses",
				Include:  func(source *ModelsDevModel) bool { return !openaiUnsupportedModelIDs[source.ID] },
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					return openaiCorrectUpstream(model), nil
				},
			})
			if err != nil {
				return nil, err
			}
			return AddMissing(models, []ChatModel{openaiGPT5ChatLatest()}), nil
		},
		Classifiers: func(*ProviderContext) ([]*Obj, error) { return openaiClassifierModels(), nil },
		Finalize: func(model ChatModel, _ *ProviderContext) {
			if model.Str("api") != "openai-responses" {
				return
			}
			id := model.Str("id")
			if openaiNoneReasoningModelIDs[id] {
				MergeThinking(model, O("off", "none"))
			}
			if id == "gpt-5.5" {
				MergeThinking(model, O("minimal", nil))
			}
			MergeCompat(model, O("supportsStrictMode", true))
			EnableOpenAIGrammarTools(model)
			if OpenAIToolSearchModelIDs[id] {
				MergeCompat(model, O("supportsAdditionalTools", true, "supportsToolSearch", true))
			}
			EnableOpenAIMidConvoSystemMessages(model, false)
			// OpenAI charges prompt-cache writes starting with the GPT-5.6 family, and exactly
			// those models accept `prompt_cache_options`; older models reject the parameter.
			// https://developers.openai.com/api/docs/guides/prompt-caching
			if model.Object("cost").Num("cacheWrite") > 0 {
				MergeCompat(model, O("supportsExplicitPromptCacheMode", true))
			}
			if openaiMidConvoEffortModelIDs[id] {
				MergeCompat(model, O("supportsMidConvoEffort", true))
			}
			WithInputLimits(model, O("maxRequestBytes", 512*1024*1024, "images", O("maxPerRequest", 1500)))
		},
	}
}
