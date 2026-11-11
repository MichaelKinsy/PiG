package providers

// Ports packages/ai/catalog/providers/zai-coding-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

func zaiCodingCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "zai-coding-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return zaiCodingPlanModels(ctx, "zhipuai-coding-plan", "https://open.bigmodel.cn/api/coding/paas/v4")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(zaiCompletionsCompat())},
	}
}
