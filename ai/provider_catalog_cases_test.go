package ai

// providerCatalogCases lists every <ID>_MODELS, <ID>_IMAGE_MODELS and <ID>_CLASSIFIER_MODELS export of the pinned providers/*.models.ts.
type providerCatalogCase struct {
	provider   string
	chat       func() ChatModelCatalog
	image      func() ImageModelCatalog
	classifier func() ClassifierModelCatalog
}

func providerCatalogCases() []providerCatalogCase {
	return []providerCatalogCase{
		{"amazon-bedrock", AmazonBedrockModels, AmazonBedrockImageModels, AmazonBedrockClassifierModels},
		{"anthropic", AnthropicModels, AnthropicImageModels, AnthropicClassifierModels},
		{"ant-ling", AntLingModels, AntLingImageModels, AntLingClassifierModels},
		{"azure", AzureModels, AzureImageModels, AzureClassifierModels},
		{"baseten", BasetenModels, BasetenImageModels, BasetenClassifierModels},
		{"cerebras", CerebrasModels, CerebrasImageModels, CerebrasClassifierModels},
		{"cloudflare-ai-gateway", CloudflareAIGatewayModels, CloudflareAIGatewayImageModels, CloudflareAIGatewayClassifierModels},
		{"cloudflare-workers-ai", CloudflareWorkersAIModels, CloudflareWorkersAIImageModels, CloudflareWorkersAIClassifierModels},
		{"deepseek", DeepSeekModels, DeepSeekImageModels, DeepSeekClassifierModels},
		{"fireworks", FireworksModels, FireworksImageModels, FireworksClassifierModels},
		{"github-copilot", GitHubCopilotModels, GitHubCopilotImageModels, GitHubCopilotClassifierModels},
		{"google", GoogleModels, GoogleImageModels, GoogleClassifierModels},
		{"google-vertex", GoogleVertexModels, GoogleVertexImageModels, GoogleVertexClassifierModels},
		{"groq", GroqModels, GroqImageModels, GroqClassifierModels},
		{"huggingface", HuggingFaceModels, HuggingFaceImageModels, HuggingFaceClassifierModels},
		{"kimi-coding", KimiCodingModels, KimiCodingImageModels, KimiCodingClassifierModels},
		{"meta", MetaModels, MetaImageModels, MetaClassifierModels},
		{"minimax-cn", MiniMaxCNModels, MiniMaxCNImageModels, MiniMaxCNClassifierModels},
		{"minimax", MiniMaxModels, MiniMaxImageModels, MiniMaxClassifierModels},
		{"mistral", MistralModels, MistralImageModels, MistralClassifierModels},
		{"moonshotai-cn", MoonshotAICNModels, MoonshotAICNImageModels, MoonshotAICNClassifierModels},
		{"moonshotai", MoonshotAIModels, MoonshotAIImageModels, MoonshotAIClassifierModels},
		{"nvidia", NVIDIAModels, NVIDIAImageModels, NVIDIAClassifierModels},
		{"openai-codex", OpenAICodexModels, OpenAICodexImageModels, OpenAICodexClassifierModels},
		{"openai", OpenAIModels, OpenAIImageModels, OpenAIClassifierModels},
		{"opencode-go", OpenCodeGoModels, OpenCodeGoImageModels, OpenCodeGoClassifierModels},
		{"opencode", OpenCodeModels, OpenCodeImageModels, OpenCodeClassifierModels},
		{"openrouter", OpenRouterModels, OpenRouterImageModels, OpenRouterClassifierModels},
		{"qwen-token-plan-cn", QwenTokenPlanCNModels, QwenTokenPlanCNImageModels, QwenTokenPlanCNClassifierModels},
		{"qwen-token-plan-individual", QwenTokenPlanIndividualModels, QwenTokenPlanIndividualImageModels, QwenTokenPlanIndividualClassifierModels},
		{"qwen-token-plan", QwenTokenPlanModels, QwenTokenPlanImageModels, QwenTokenPlanClassifierModels},
		{"radius", RadiusModels, RadiusImageModels, RadiusClassifierModels},
		{"together", TogetherModels, TogetherImageModels, TogetherClassifierModels},
		{"typesafe", TypesafeModels, TypesafeImageModels, TypesafeClassifierModels},
		{"vercel-ai-gateway", VercelAIGatewayModels, VercelAIGatewayImageModels, VercelAIGatewayClassifierModels},
		{"xai", XAIModels, XAIImageModels, XAIClassifierModels},
		{"xiaomi", XiaomiModels, XiaomiImageModels, XiaomiClassifierModels},
		{"xiaomi-token-plan-ams", XiaomiTokenPlanAMSModels, XiaomiTokenPlanAMSImageModels, XiaomiTokenPlanAMSClassifierModels},
		{"xiaomi-token-plan-cn", XiaomiTokenPlanCNModels, XiaomiTokenPlanCNImageModels, XiaomiTokenPlanCNClassifierModels},
		{"xiaomi-token-plan-sgp", XiaomiTokenPlanSGPModels, XiaomiTokenPlanSGPImageModels, XiaomiTokenPlanSGPClassifierModels},
		{"zai-coding-cn", ZAICodingCNModels, ZAICodingCNImageModels, ZAICodingCNClassifierModels},
		{"zai", ZAIModels, ZAIImageModels, ZAIClassifierModels},
	}
}
