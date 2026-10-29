package providers

// Ports packages/ai/catalog/providers/zai-coding-cn.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen"

func zaiCodingCn() ProviderDefinition {
	return ProviderDefinition{
		ID: "zai-coding-cn",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return zaiCodingPlanModels(ctx, "zhipuai-coding-plan", "https://open.bigmodel.cn/api/coding/paas/v4")
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(zaiCompletionsCompat())},
	}
}
