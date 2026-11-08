package providers

// Ports packages/ai/catalog/providers/qwen-token-plan-individual-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// QwenCloud Token Plan Individual on the China endpoint: the Individual allowlist served from the China catalog.
func qwenTokenPlanIndividualCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "qwen-token-plan-individual-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return qwenTokenPlanIndividualModels(ctx, "alibaba-token-plan-cn", cnBaseURL)
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(qwenCompletionsCompat())},
	}
}
