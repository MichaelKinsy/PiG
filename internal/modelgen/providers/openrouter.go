package providers

// Ports packages/ai/catalog/providers/openrouter.ts

import (
	"regexp"
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts
)

const openrouterBaseURL = "https://openrouter.ai/api/v1"

// OpenRouter rejects `configuration_update` system messages on Opus 5 ("Mid-conversation
// reasoning effort (configuration_update) is not supported on anthropic/claude-opus-5-20260723")
// while accepting them on Fable 5.1.
var openrouterMidConvoEffortUnsupportedModelIDs = map[string]bool{"anthropic/claude-opus-5": true}

var (
	openrouterDeveloperRoleProviders = regexp.MustCompile(`^(anthropic|openai)/`)
	openrouterAnthropicModels        = regexp.MustCompile(`^~?anthropic/`)
)

// openrouterCorrectUpstream keeps selected model metadata stable until upstream settles.
func openrouterCorrectUpstream(model ChatModel) ChatModel {
	id := model.Str("id")
	// Keep Kimi K3's canonical output limit when gateway metadata is missing or incorrect.
	if id == "~moonshotai/kimi-latest" ||
		(strings.HasPrefix(id, "moonshotai/") && IsKimiK3FamilyID(strings.TrimPrefix(id, "moonshotai/"))) {
		model.Set("maxTokens", KimiK3MaxTokens)
	}
	if id == "moonshotai/kimi-k2.5" {
		model.Set("cost", Spread(model.Object("cost"), O("input", 0.41, "output", 2.06, "cacheRead", 0.07)))
		model.Set("maxTokens", 4096)
	}
	if strings.HasPrefix(id, "moonshotai/kimi-k2.6") {
		MergeCompat(model, O("supportsDeveloperRole", false, "requiresReasoningContentOnAssistantMessages", true))
	}
	if id == "z-ai/glm-5" {
		model.Set("cost", Spread(model.Object("cost"), O("input", 0.6, "output", 1.9, "cacheRead", 0.119)))
	}
	return model
}

// openrouterRouterModels are router aliases. Their price depends on the model they route to, so it is unknown here.
func openrouterRouterModels() []ChatModel {
	return []ChatModel{
		O(
			"id", "auto",
			"name", "Auto",
			"api", "openai-completions",
			"provider", "openrouter",
			"baseUrl", openrouterBaseURL,
			"reasoning", true,
			"input", Strings("text", "image"),
			"cost", O("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
			"contextWindow", 2000000,
			"maxTokens", 30000,
		),
		// OpenRouter exposes Fusion as a router alias/plugin entry point; its model
		// metadata does not advertise tools, but the alias resolves to a concrete model
		// that can invoke caller tools and has the openrouter:fusion server tool auto-injected.
		O(
			"id", "openrouter/fusion",
			"name", "OpenRouter: Fusion",
			"api", "openai-completions",
			"provider", "openrouter",
			"baseUrl", openrouterBaseURL,
			"reasoning", true,
			"input", Strings("text"),
			"cost", O("input", 0, "output", 0, "cacheRead", 0, "cacheWrite", 0),
			"contextWindow", 1000000,
			"maxTokens", 30000,
		),
	}
}

func openrouter() ProviderDefinition {
	return ProviderDefinition{
		ID: "openrouter",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			models := CloneAll(ctx.Sources.OpenRouter.Chat)
			for i, model := range models {
				models[i] = openrouterCorrectUpstream(model)
			}
			return AddMissing(models, openrouterRouterModels()), nil
		},
		Images: func(ctx *ProviderContext) ([]*Obj, error) {
			return CloneAll(ctx.Sources.OpenRouter.Images), nil
		},
		Classifiers: func(ctx *ProviderContext) ([]*Obj, error) {
			return CloneAll(ctx.Sources.OpenRouter.Classifiers), nil
		},
		Compat: ApiCompatDefaults{"openai-completions": func(model *Obj) *Obj {
			id := model.Str("id")
			compat := NewObj()
			// OpenRouter passes developer messages through to Anthropic and OpenAI models only.
			if !openrouterDeveloperRoleProviders.MatchString(id) {
				compat.Set("supportsDeveloperRole", false)
			}
			compat.Set("thinkingFormat", "openrouter")
			compat.Set("supportsStrictMode", true)
			if openrouterAnthropicModels.MatchString(id) {
				compat.Set("cacheControlFormat", "anthropic")
			}
			compat.Set("sendSessionAffinityHeaders", true)
			return compat
		}},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			id := model.Str("id")
			api := model.Str("api")
			if api == "anthropic-messages" && SupportsClaudeMidConvoEffort(id) && !openrouterMidConvoEffortUnsupportedModelIDs[id] {
				EnableClaudeMidConvoEffort(model)
			}
			if strings.HasPrefix(id, "inception/mercury-2") {
				// Mercury 2 in instant mode (reasoning_effort: "none") disables tool calling.
				// Mark "off" unsupported so the openai-completions provider omits the reasoning param
				// instead of defaulting to {reasoning:{effort:"none"}}.
				// Pi's low/medium/high pass through verbatim; OpenRouter normalizes to Mercury's vocabulary.
				MergeThinking(model, O("off", nil))
			}
			if id == "z-ai/glm-5.2" {
				MergeThinking(model, O("xhigh", "xhigh"))
			}
			// OpenAI models behind OpenRouter accept plain system text in place.
			if api == "openai-completions" && strings.HasPrefix(id, "openai/") &&
				OpenAIMidConvoModelIDs[strings.TrimPrefix(id, "openai/")] {
				MergeCompat(model, O("supportsMidConvoSystemMessages", true))
			}
		},
	}
}
