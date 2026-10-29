package providers

// Ports packages/ai/catalog/providers/moonshotai.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func moonshotAlwaysThinkingModelID(id string) bool {
	return id == "kimi-k2.7-code" || id == "kimi-k2.7-code-highspeed"
}

// Kimi K2.6/K2.7 accept system text after the conversation starts but reject
// tool-bearing system messages.
func moonshotTextOnlyMidConvoModelID(id string) bool {
	return id == "kimi-k2.6" || moonshotAlwaysThinkingModelID(id)
}

func moonshotModels(ctx *ProviderContext, source string, baseURL string) ([]ChatModel, error) {
	return ModelsDevModels(ctx, source, ModelsDevOptions{
		API:     "openai-completions",
		BaseURL: baseURL,
		Map: func(model ChatModel, source *ModelsDevModel) (ChatModel, error) {
			id := model.Str("id")
			isKimiK3 := IsKimiK3FamilyID(id)
			// Moonshot does not bill cache writes; models.dev lists the input rate as cache_write for the K3 family.
			var cost *Obj
			if id == "kimi-k3" {
				cost = KimiK3Cost()
			} else {
				cacheWrite := 0.0
				if !isKimiK3 {
					cacheWrite = googleOr(source.Cost.CacheWrite, 0)
				}
				cost = O(
					"input", googleOr(source.Cost.Input, 0),
					"output", googleOr(source.Cost.Output, 0),
					"cacheRead", googleOr(source.Cost.CacheRead, 0),
					"cacheWrite", cacheWrite,
				)
			}
			var compat *Obj
			if isKimiK3 {
				compat = KimiK3OpenAICompat()
			} else {
				compat = KimiOpenAICompat()
			}
			return Spread(model, O("reasoning", isKimiK3 || source.Reasoning, "cost", cost, "compat", compat)), nil
		},
	})
}

func moonshotCompletionsCompat() *Obj {
	return O(
		"supportsStore", false,
		"supportsDeveloperRole", false,
		"supportsReasoningEffort", false,
		"maxTokensField", "max_tokens",
	)
}

func finalizeMoonshotModel(model ChatModel, _ *ProviderContext) {
	id := model.Str("id")
	if moonshotAlwaysThinkingModelID(id) {
		// Kimi K2.7 Code is always-thinking. Official docs say
		// `thinking: { type: "disabled" }` is rejected, and callers can omit
		// the thinking parameter to use the enabled default.
		MergeThinking(model, O("off", nil))
	}
	isKimiK3 := IsKimiK3FamilyID(id)
	if isKimiK3 || moonshotTextOnlyMidConvoModelID(id) {
		compat := O("supportsMidConvoSystemMessages", true)
		// The Kimi K3 family also accepts tool-bearing system messages.
		if isKimiK3 {
			compat.Set("supportsMidConvoToolAdditions", true)
		}
		MergeCompat(model, compat)
	}
}

func moonshotai() ProviderDefinition {
	return ProviderDefinition{
		ID: "moonshotai",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return moonshotModels(ctx, "moonshotai", "https://api.moonshot.ai/v1")
		},
		Compat:   ApiCompatDefaults{"openai-completions": FixedCompat(moonshotCompletionsCompat())},
		Finalize: finalizeMoonshotModel,
	}
}
