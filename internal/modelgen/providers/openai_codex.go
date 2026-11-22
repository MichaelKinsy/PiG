package providers

// Ports packages/ai/catalog/providers/openai-codex.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// ChatGPT OAuth models. models.dev does not list them; keep a small, explicit
// list without aliases. Only list models the Codex backend accepts for ChatGPT
// accounts: GPT-5.5 and GPT-5.3 Codex Spark are rejected with "not supported
// when using Codex with a ChatGPT account".
var openaiCodexModelList = []struct {
	id, name      string
	contextWindow float64
}{
	// Context windows are the defaults /backend-api/codex/models returns (checked November 2026).
	{"gpt-6.2-astra", "GPT-6.2 Astra", 272000},
	{"gpt-6.2-sol", "GPT-6.2 Sol", 500000},
	{"gpt-6.1-sol", "GPT-6.1 Sol", 500000},
	{"gpt-6.1-terra", "GPT-6.1 Terra", 500000},
	{"gpt-6-astra", "GPT-6 Astra", 272000},
	{"gpt-6-sol", "GPT-6 Sol", 272000},
	{"gpt-6-luna", "GPT-6 Luna", 272000},
	{"gpt-5.6-luna", "GPT-5.6 Luna", 272000},
	{"gpt-5.6-sol", "GPT-5.6 Sol", 272000},
	{"gpt-5.6-terra", "GPT-5.6 Terra", 272000},
}

// Codex CLI keeps additional_tools behind its own model list.
var openaiCodexAdditionalToolsModelIDs = map[string]bool{
	"gpt-5.6-sol":   true,
	"gpt-5.6-terra": true,
	"gpt-5.6-luna":  true,
	"gpt-6-astra":   true,
	"gpt-6-sol":     true,
	"gpt-6-luna":    true,
	"gpt-6.1-sol":   true,
	"gpt-6.1-terra": true,
	"gpt-6.2-astra": true,
	"gpt-6.2-sol":   true,
}

func openaiCodex() ProviderDefinition {
	return ProviderDefinition{
		ID: "openai-codex",
		Models: func(*ProviderContext) ([]ChatModel, error) {
			models := make([]ChatModel, 0, len(openaiCodexModelList))
			for _, entry := range openaiCodexModelList {
				models = append(models, O(
					"id", entry.id,
					"name", entry.name,
					"api", "openai-codex-responses",
					"provider", "openai-codex",
					"baseUrl", "https://chatgpt.com/backend-api",
					"reasoning", true,
					"input", Strings("text", "image"),
					"cost", OpenAIStandardCost(entry.id),
					"contextWindow", entry.contextWindow,
					"maxTokens", 128000,
				))
			}
			return models, nil
		},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			id := model.Str("id")
			if SupportsOpenAIXhigh(id) {
				MergeThinking(model, O("minimal", "low"))
			}
			EnableOpenAIGrammarTools(model)
			if OpenAIToolSearchModelIDs[id] {
				compat := NewObj()
				if openaiCodexAdditionalToolsModelIDs[id] {
					compat.Set("supportsAdditionalTools", true)
				}
				compat.Set("supportsToolSearch", true)
				MergeCompat(model, compat)
			}
			EnableOpenAIMidConvoSystemMessages(model, false)
			if OpenAIMidConvoEffortModelIDs[id] {
				MergeCompat(model, O("supportsMidConvoEffort", true))
			}
		},
	}
}
