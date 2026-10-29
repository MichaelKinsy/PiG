package providers

// Ports packages/ai/catalog/providers/nvidia.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Listed by NIM but unusable through pi (no tool calls, broken streaming, or retired).
var nvidiaUnsupportedModelIDs = map[string]bool{
	"abacusai/dracarys-llama-3.1-70b-instruct": true,
	"bytedance/seed-oss-36b-instruct":          true,
	"deepseek-ai/deepseek-v4-flash":            true,
	"deepseek-ai/deepseek-v4-pro":              true,
	"google/gemma-2-2b-it":                     true,
	"google/gemma-3n-e2b-it":                   true,
	"google/gemma-3n-e4b-it":                   true,
	"google/gemma-4-31b-it":                    true,
	"meta/llama-3.2-1b-instruct":               true,
	"meta/llama-4-maverick-17b-128e-instruct":  true,
	"microsoft/phi-4-mini-instruct":            true,
	"minimaxai/minimax-m2.7":                   true,
	"mistralai/mistral-nemotron":               true,
	"nvidia/nemotron-mini-4b-instruct":         true,
	"qwen/qwen3-next-80b-a3b-instruct":         true,
	"qwen/qwen3.5-397b-a17b":                   true,
	"sarvamai/sarvam-m":                        true,
	"upstage/solar-10.7b-instruct":             true,
}

func nvidia() ProviderDefinition {
	return ProviderDefinition{
		ID: "nvidia",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			liveIds := ctx.Sources.NvidiaModelIDs
			liveID := func(id string) (string, bool) {
				if live, ok := liveIds[id]; ok {
					return live, true
				}
				live, ok := liveIds[NormalizeNvidiaModelID(id)]
				return live, ok
			}
			return ModelsDevModels(ctx, "nvidia", ModelsDevOptions{
				API:     "openai-completions",
				BaseURL: NvidiaBaseURL,
				Headers: O("NVCF-POLL-SECONDS", "3600"),
				Compat: O(
					"supportsStore", false,
					"supportsDeveloperRole", false,
					"supportsReasoningEffort", false,
					"maxTokensField", "max_tokens",
					"supportsStrictMode", false,
					"supportsLongCacheRetention", false,
				),
				// Only models the live NIM API serves, under the id it uses.
				Include: func(source *ModelsDevModel) bool {
					if !source.TextInput || !source.TextOutput {
						return false
					}
					id, ok := liveID(source.ID)
					return ok && !nvidiaUnsupportedModelIDs[id]
				},
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					id, ok := liveID(model.Str("id"))
					if !ok {
						id = model.Str("id")
					}
					name, _ := model.Get("name")
					if model.Str("name") == model.Str("id") {
						name = id
					}
					return Spread(model, O("id", id, "name", name)), nil
				},
			})
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsDeveloperRole", false,
			"supportsReasoningEffort", false,
			"maxTokensField", "max_tokens",
			"supportsLongCacheRetention", false,
		))},
	}
}
