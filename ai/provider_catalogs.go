package ai

// Ports the per-provider catalog exports of packages/ai/src/providers/<id>.models.ts: <ID>_MODELS, <ID>_IMAGE_MODELS and <ID>_CLASSIFIER_MODELS.
// Each function returns the provider's models in catalog order, keyed by model id, as a fresh copy.

// AmazonBedrockModels is AMAZON_BEDROCK_MODELS.
func AmazonBedrockModels() ChatModelCatalog { return chatModelCatalog("amazon-bedrock") }

// AmazonBedrockImageModels is AMAZON_BEDROCK_IMAGE_MODELS.
func AmazonBedrockImageModels() ImageModelCatalog { return imageModelCatalog("amazon-bedrock") }

// AmazonBedrockClassifierModels is AMAZON_BEDROCK_CLASSIFIER_MODELS.
func AmazonBedrockClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("amazon-bedrock")
}

// AnthropicModels is ANTHROPIC_MODELS.
func AnthropicModels() ChatModelCatalog { return chatModelCatalog("anthropic") }

// AnthropicImageModels is ANTHROPIC_IMAGE_MODELS.
func AnthropicImageModels() ImageModelCatalog { return imageModelCatalog("anthropic") }

// AnthropicClassifierModels is ANTHROPIC_CLASSIFIER_MODELS.
func AnthropicClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("anthropic") }

// AntLingModels is ANT_LING_MODELS.
func AntLingModels() ChatModelCatalog { return chatModelCatalog("ant-ling") }

// AntLingImageModels is ANT_LING_IMAGE_MODELS.
func AntLingImageModels() ImageModelCatalog { return imageModelCatalog("ant-ling") }

// AntLingClassifierModels is ANT_LING_CLASSIFIER_MODELS.
func AntLingClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("ant-ling") }

// AzureModels is AZURE_MODELS.
func AzureModels() ChatModelCatalog { return chatModelCatalog("azure") }

// AzureImageModels is AZURE_IMAGE_MODELS.
func AzureImageModels() ImageModelCatalog { return imageModelCatalog("azure") }

// AzureClassifierModels is AZURE_CLASSIFIER_MODELS.
func AzureClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("azure") }

// BasetenModels is BASETEN_MODELS.
func BasetenModels() ChatModelCatalog { return chatModelCatalog("baseten") }

// BasetenImageModels is BASETEN_IMAGE_MODELS.
func BasetenImageModels() ImageModelCatalog { return imageModelCatalog("baseten") }

// BasetenClassifierModels is BASETEN_CLASSIFIER_MODELS.
func BasetenClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("baseten") }

// CerebrasModels is CEREBRAS_MODELS.
func CerebrasModels() ChatModelCatalog { return chatModelCatalog("cerebras") }

// CerebrasImageModels is CEREBRAS_IMAGE_MODELS.
func CerebrasImageModels() ImageModelCatalog { return imageModelCatalog("cerebras") }

// CerebrasClassifierModels is CEREBRAS_CLASSIFIER_MODELS.
func CerebrasClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("cerebras") }

// CloudflareAIGatewayModels is CLOUDFLARE_AI_GATEWAY_MODELS.
func CloudflareAIGatewayModels() ChatModelCatalog { return chatModelCatalog("cloudflare-ai-gateway") }

// CloudflareAIGatewayImageModels is CLOUDFLARE_AI_GATEWAY_IMAGE_MODELS.
func CloudflareAIGatewayImageModels() ImageModelCatalog {
	return imageModelCatalog("cloudflare-ai-gateway")
}

// CloudflareAIGatewayClassifierModels is CLOUDFLARE_AI_GATEWAY_CLASSIFIER_MODELS.
func CloudflareAIGatewayClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("cloudflare-ai-gateway")
}

// CloudflareWorkersAIModels is CLOUDFLARE_WORKERS_AI_MODELS.
func CloudflareWorkersAIModels() ChatModelCatalog { return chatModelCatalog("cloudflare-workers-ai") }

// CloudflareWorkersAIImageModels is CLOUDFLARE_WORKERS_AI_IMAGE_MODELS.
func CloudflareWorkersAIImageModels() ImageModelCatalog {
	return imageModelCatalog("cloudflare-workers-ai")
}

// CloudflareWorkersAIClassifierModels is CLOUDFLARE_WORKERS_AI_CLASSIFIER_MODELS.
func CloudflareWorkersAIClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("cloudflare-workers-ai")
}

// DeepSeekModels is DEEPSEEK_MODELS.
func DeepSeekModels() ChatModelCatalog { return chatModelCatalog("deepseek") }

// DeepSeekImageModels is DEEPSEEK_IMAGE_MODELS.
func DeepSeekImageModels() ImageModelCatalog { return imageModelCatalog("deepseek") }

// DeepSeekClassifierModels is DEEPSEEK_CLASSIFIER_MODELS.
func DeepSeekClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("deepseek") }

// FireworksModels is FIREWORKS_MODELS.
func FireworksModels() ChatModelCatalog { return chatModelCatalog("fireworks") }

// FireworksImageModels is FIREWORKS_IMAGE_MODELS.
func FireworksImageModels() ImageModelCatalog { return imageModelCatalog("fireworks") }

// FireworksClassifierModels is FIREWORKS_CLASSIFIER_MODELS.
func FireworksClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("fireworks") }

// GitHubCopilotModels is GITHUB_COPILOT_MODELS.
func GitHubCopilotModels() ChatModelCatalog { return chatModelCatalog("github-copilot") }

// GitHubCopilotImageModels is GITHUB_COPILOT_IMAGE_MODELS.
func GitHubCopilotImageModels() ImageModelCatalog { return imageModelCatalog("github-copilot") }

// GitHubCopilotClassifierModels is GITHUB_COPILOT_CLASSIFIER_MODELS.
func GitHubCopilotClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("github-copilot")
}

// GoogleModels is GOOGLE_MODELS.
func GoogleModels() ChatModelCatalog { return chatModelCatalog("google") }

// GoogleImageModels is GOOGLE_IMAGE_MODELS.
func GoogleImageModels() ImageModelCatalog { return imageModelCatalog("google") }

// GoogleClassifierModels is GOOGLE_CLASSIFIER_MODELS.
func GoogleClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("google") }

// GoogleVertexModels is GOOGLE_VERTEX_MODELS.
func GoogleVertexModels() ChatModelCatalog { return chatModelCatalog("google-vertex") }

// GoogleVertexImageModels is GOOGLE_VERTEX_IMAGE_MODELS.
func GoogleVertexImageModels() ImageModelCatalog { return imageModelCatalog("google-vertex") }

// GoogleVertexClassifierModels is GOOGLE_VERTEX_CLASSIFIER_MODELS.
func GoogleVertexClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("google-vertex")
}

// GroqModels is GROQ_MODELS.
func GroqModels() ChatModelCatalog { return chatModelCatalog("groq") }

// GroqImageModels is GROQ_IMAGE_MODELS.
func GroqImageModels() ImageModelCatalog { return imageModelCatalog("groq") }

// GroqClassifierModels is GROQ_CLASSIFIER_MODELS.
func GroqClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("groq") }

// HuggingFaceModels is HUGGINGFACE_MODELS.
func HuggingFaceModels() ChatModelCatalog { return chatModelCatalog("huggingface") }

// HuggingFaceImageModels is HUGGINGFACE_IMAGE_MODELS.
func HuggingFaceImageModels() ImageModelCatalog { return imageModelCatalog("huggingface") }

// HuggingFaceClassifierModels is HUGGINGFACE_CLASSIFIER_MODELS.
func HuggingFaceClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("huggingface")
}

// KimiCodingModels is KIMI_CODING_MODELS.
func KimiCodingModels() ChatModelCatalog { return chatModelCatalog("kimi-coding") }

// KimiCodingImageModels is KIMI_CODING_IMAGE_MODELS.
func KimiCodingImageModels() ImageModelCatalog { return imageModelCatalog("kimi-coding") }

// KimiCodingClassifierModels is KIMI_CODING_CLASSIFIER_MODELS.
func KimiCodingClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("kimi-coding")
}

// MetaModels is META_MODELS.
func MetaModels() ChatModelCatalog { return chatModelCatalog("meta") }

// MetaImageModels is META_IMAGE_MODELS.
func MetaImageModels() ImageModelCatalog { return imageModelCatalog("meta") }

// MetaClassifierModels is META_CLASSIFIER_MODELS.
func MetaClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("meta") }

// MiniMaxCNModels is MINIMAX_CN_MODELS.
func MiniMaxCNModels() ChatModelCatalog { return chatModelCatalog("minimax-cn") }

// MiniMaxCNImageModels is MINIMAX_CN_IMAGE_MODELS.
func MiniMaxCNImageModels() ImageModelCatalog { return imageModelCatalog("minimax-cn") }

// MiniMaxCNClassifierModels is MINIMAX_CN_CLASSIFIER_MODELS.
func MiniMaxCNClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("minimax-cn") }

// MiniMaxModels is MINIMAX_MODELS.
func MiniMaxModels() ChatModelCatalog { return chatModelCatalog("minimax") }

// MiniMaxImageModels is MINIMAX_IMAGE_MODELS.
func MiniMaxImageModels() ImageModelCatalog { return imageModelCatalog("minimax") }

// MiniMaxClassifierModels is MINIMAX_CLASSIFIER_MODELS.
func MiniMaxClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("minimax") }

// MistralModels is MISTRAL_MODELS.
func MistralModels() ChatModelCatalog { return chatModelCatalog("mistral") }

// MistralImageModels is MISTRAL_IMAGE_MODELS.
func MistralImageModels() ImageModelCatalog { return imageModelCatalog("mistral") }

// MistralClassifierModels is MISTRAL_CLASSIFIER_MODELS.
func MistralClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("mistral") }

// MoonshotAICNModels is MOONSHOTAI_CN_MODELS.
func MoonshotAICNModels() ChatModelCatalog { return chatModelCatalog("moonshotai-cn") }

// MoonshotAICNImageModels is MOONSHOTAI_CN_IMAGE_MODELS.
func MoonshotAICNImageModels() ImageModelCatalog { return imageModelCatalog("moonshotai-cn") }

// MoonshotAICNClassifierModels is MOONSHOTAI_CN_CLASSIFIER_MODELS.
func MoonshotAICNClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("moonshotai-cn")
}

// MoonshotAIModels is MOONSHOTAI_MODELS.
func MoonshotAIModels() ChatModelCatalog { return chatModelCatalog("moonshotai") }

// MoonshotAIImageModels is MOONSHOTAI_IMAGE_MODELS.
func MoonshotAIImageModels() ImageModelCatalog { return imageModelCatalog("moonshotai") }

// MoonshotAIClassifierModels is MOONSHOTAI_CLASSIFIER_MODELS.
func MoonshotAIClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("moonshotai") }

// NVIDIAModels is NVIDIA_MODELS.
func NVIDIAModels() ChatModelCatalog { return chatModelCatalog("nvidia") }

// NVIDIAImageModels is NVIDIA_IMAGE_MODELS.
func NVIDIAImageModels() ImageModelCatalog { return imageModelCatalog("nvidia") }

// NVIDIAClassifierModels is NVIDIA_CLASSIFIER_MODELS.
func NVIDIAClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("nvidia") }

// OpenAICodexModels is OPENAI_CODEX_MODELS.
func OpenAICodexModels() ChatModelCatalog { return chatModelCatalog("openai-codex") }

// OpenAICodexImageModels is OPENAI_CODEX_IMAGE_MODELS.
func OpenAICodexImageModels() ImageModelCatalog { return imageModelCatalog("openai-codex") }

// OpenAICodexClassifierModels is OPENAI_CODEX_CLASSIFIER_MODELS.
func OpenAICodexClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("openai-codex")
}

// OpenAIModels is OPENAI_MODELS.
func OpenAIModels() ChatModelCatalog { return chatModelCatalog("openai") }

// OpenAIImageModels is OPENAI_IMAGE_MODELS.
func OpenAIImageModels() ImageModelCatalog { return imageModelCatalog("openai") }

// OpenAIClassifierModels is OPENAI_CLASSIFIER_MODELS.
func OpenAIClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("openai") }

// OpenCodeGoModels is OPENCODE_GO_MODELS.
func OpenCodeGoModels() ChatModelCatalog { return chatModelCatalog("opencode-go") }

// OpenCodeGoImageModels is OPENCODE_GO_IMAGE_MODELS.
func OpenCodeGoImageModels() ImageModelCatalog { return imageModelCatalog("opencode-go") }

// OpenCodeGoClassifierModels is OPENCODE_GO_CLASSIFIER_MODELS.
func OpenCodeGoClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("opencode-go")
}

// OpenCodeModels is OPENCODE_MODELS.
func OpenCodeModels() ChatModelCatalog { return chatModelCatalog("opencode") }

// OpenCodeImageModels is OPENCODE_IMAGE_MODELS.
func OpenCodeImageModels() ImageModelCatalog { return imageModelCatalog("opencode") }

// OpenCodeClassifierModels is OPENCODE_CLASSIFIER_MODELS.
func OpenCodeClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("opencode") }

// OpenRouterModels is OPENROUTER_MODELS.
func OpenRouterModels() ChatModelCatalog { return chatModelCatalog("openrouter") }

// OpenRouterImageModels is OPENROUTER_IMAGE_MODELS.
func OpenRouterImageModels() ImageModelCatalog { return imageModelCatalog("openrouter") }

// OpenRouterClassifierModels is OPENROUTER_CLASSIFIER_MODELS.
func OpenRouterClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("openrouter") }

// QwenTokenPlanCNModels is QWEN_TOKEN_PLAN_CN_MODELS.
func QwenTokenPlanCNModels() ChatModelCatalog { return chatModelCatalog("qwen-token-plan-cn") }

// QwenTokenPlanCNImageModels is QWEN_TOKEN_PLAN_CN_IMAGE_MODELS.
func QwenTokenPlanCNImageModels() ImageModelCatalog { return imageModelCatalog("qwen-token-plan-cn") }

// QwenTokenPlanCNClassifierModels is QWEN_TOKEN_PLAN_CN_CLASSIFIER_MODELS.
func QwenTokenPlanCNClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("qwen-token-plan-cn")
}

// QwenTokenPlanIndividualModels is QWEN_TOKEN_PLAN_INDIVIDUAL_MODELS.
func QwenTokenPlanIndividualModels() ChatModelCatalog {
	return chatModelCatalog("qwen-token-plan-individual")
}

// QwenTokenPlanIndividualImageModels is QWEN_TOKEN_PLAN_INDIVIDUAL_IMAGE_MODELS.
func QwenTokenPlanIndividualImageModels() ImageModelCatalog {
	return imageModelCatalog("qwen-token-plan-individual")
}

// QwenTokenPlanIndividualClassifierModels is QWEN_TOKEN_PLAN_INDIVIDUAL_CLASSIFIER_MODELS.
func QwenTokenPlanIndividualClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("qwen-token-plan-individual")
}

// QwenTokenPlanModels is QWEN_TOKEN_PLAN_MODELS.
func QwenTokenPlanModels() ChatModelCatalog { return chatModelCatalog("qwen-token-plan") }

// QwenTokenPlanImageModels is QWEN_TOKEN_PLAN_IMAGE_MODELS.
func QwenTokenPlanImageModels() ImageModelCatalog { return imageModelCatalog("qwen-token-plan") }

// QwenTokenPlanClassifierModels is QWEN_TOKEN_PLAN_CLASSIFIER_MODELS.
func QwenTokenPlanClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("qwen-token-plan")
}

// RadiusModels is RADIUS_MODELS.
func RadiusModels() ChatModelCatalog { return chatModelCatalog("radius") }

// RadiusImageModels is RADIUS_IMAGE_MODELS.
func RadiusImageModels() ImageModelCatalog { return imageModelCatalog("radius") }

// RadiusClassifierModels is RADIUS_CLASSIFIER_MODELS.
func RadiusClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("radius") }

// TogetherModels is TOGETHER_MODELS.
func TogetherModels() ChatModelCatalog { return chatModelCatalog("together") }

// TogetherImageModels is TOGETHER_IMAGE_MODELS.
func TogetherImageModels() ImageModelCatalog { return imageModelCatalog("together") }

// TogetherClassifierModels is TOGETHER_CLASSIFIER_MODELS.
func TogetherClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("together") }

// TypesafeModels is TYPESAFE_MODELS.
func TypesafeModels() ChatModelCatalog { return chatModelCatalog("typesafe") }

// TypesafeImageModels is TYPESAFE_IMAGE_MODELS.
func TypesafeImageModels() ImageModelCatalog { return imageModelCatalog("typesafe") }

// TypesafeClassifierModels is TYPESAFE_CLASSIFIER_MODELS.
func TypesafeClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("typesafe") }

// VercelAIGatewayModels is VERCEL_AI_GATEWAY_MODELS.
func VercelAIGatewayModels() ChatModelCatalog { return chatModelCatalog("vercel-ai-gateway") }

// VercelAIGatewayImageModels is VERCEL_AI_GATEWAY_IMAGE_MODELS.
func VercelAIGatewayImageModels() ImageModelCatalog { return imageModelCatalog("vercel-ai-gateway") }

// VercelAIGatewayClassifierModels is VERCEL_AI_GATEWAY_CLASSIFIER_MODELS.
func VercelAIGatewayClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("vercel-ai-gateway")
}

// XAIModels is XAI_MODELS.
func XAIModels() ChatModelCatalog { return chatModelCatalog("xai") }

// XAIImageModels is XAI_IMAGE_MODELS.
func XAIImageModels() ImageModelCatalog { return imageModelCatalog("xai") }

// XAIClassifierModels is XAI_CLASSIFIER_MODELS.
func XAIClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("xai") }

// XiaomiModels is XIAOMI_MODELS.
func XiaomiModels() ChatModelCatalog { return chatModelCatalog("xiaomi") }

// XiaomiImageModels is XIAOMI_IMAGE_MODELS.
func XiaomiImageModels() ImageModelCatalog { return imageModelCatalog("xiaomi") }

// XiaomiClassifierModels is XIAOMI_CLASSIFIER_MODELS.
func XiaomiClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("xiaomi") }

// XiaomiTokenPlanAMSModels is XIAOMI_TOKEN_PLAN_AMS_MODELS.
func XiaomiTokenPlanAMSModels() ChatModelCatalog { return chatModelCatalog("xiaomi-token-plan-ams") }

// XiaomiTokenPlanAMSImageModels is XIAOMI_TOKEN_PLAN_AMS_IMAGE_MODELS.
func XiaomiTokenPlanAMSImageModels() ImageModelCatalog {
	return imageModelCatalog("xiaomi-token-plan-ams")
}

// XiaomiTokenPlanAMSClassifierModels is XIAOMI_TOKEN_PLAN_AMS_CLASSIFIER_MODELS.
func XiaomiTokenPlanAMSClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("xiaomi-token-plan-ams")
}

// XiaomiTokenPlanCNModels is XIAOMI_TOKEN_PLAN_CN_MODELS.
func XiaomiTokenPlanCNModels() ChatModelCatalog { return chatModelCatalog("xiaomi-token-plan-cn") }

// XiaomiTokenPlanCNImageModels is XIAOMI_TOKEN_PLAN_CN_IMAGE_MODELS.
func XiaomiTokenPlanCNImageModels() ImageModelCatalog {
	return imageModelCatalog("xiaomi-token-plan-cn")
}

// XiaomiTokenPlanCNClassifierModels is XIAOMI_TOKEN_PLAN_CN_CLASSIFIER_MODELS.
func XiaomiTokenPlanCNClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("xiaomi-token-plan-cn")
}

// XiaomiTokenPlanSGPModels is XIAOMI_TOKEN_PLAN_SGP_MODELS.
func XiaomiTokenPlanSGPModels() ChatModelCatalog { return chatModelCatalog("xiaomi-token-plan-sgp") }

// XiaomiTokenPlanSGPImageModels is XIAOMI_TOKEN_PLAN_SGP_IMAGE_MODELS.
func XiaomiTokenPlanSGPImageModels() ImageModelCatalog {
	return imageModelCatalog("xiaomi-token-plan-sgp")
}

// XiaomiTokenPlanSGPClassifierModels is XIAOMI_TOKEN_PLAN_SGP_CLASSIFIER_MODELS.
func XiaomiTokenPlanSGPClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("xiaomi-token-plan-sgp")
}

// ZAICodingCNModels is ZAI_CODING_CN_MODELS.
func ZAICodingCNModels() ChatModelCatalog { return chatModelCatalog("zai-coding-cn") }

// ZAICodingCNImageModels is ZAI_CODING_CN_IMAGE_MODELS.
func ZAICodingCNImageModels() ImageModelCatalog { return imageModelCatalog("zai-coding-cn") }

// ZAICodingCNClassifierModels is ZAI_CODING_CN_CLASSIFIER_MODELS.
func ZAICodingCNClassifierModels() ClassifierModelCatalog {
	return classifierModelCatalog("zai-coding-cn")
}

// ZAIModels is ZAI_MODELS.
func ZAIModels() ChatModelCatalog { return chatModelCatalog("zai") }

// ZAIImageModels is ZAI_IMAGE_MODELS.
func ZAIImageModels() ImageModelCatalog { return imageModelCatalog("zai") }

// ZAIClassifierModels is ZAI_CLASSIFIER_MODELS.
func ZAIClassifierModels() ClassifierModelCatalog { return classifierModelCatalog("zai") }
