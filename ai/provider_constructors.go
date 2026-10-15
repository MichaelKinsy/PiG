package ai

// Ports packages/ai/src/providers/<id>.ts: one constructor per built-in provider, each assembled from the shared catalog by builtinProvider.

// BuiltinProvider is the built-in "all" provider.
func BuiltinProvider() *ModelsProvider { return builtinProvider("all") }

// AmazonBedrockProvider is the built-in "amazon-bedrock" provider.
func AmazonBedrockProvider() *ModelsProvider { return builtinProvider("amazon-bedrock") }

// AntLingProvider is the built-in "ant-ling" provider.
func AntLingProvider() *ModelsProvider { return builtinProvider("ant-ling") }

// AnthropicProvider is the built-in "anthropic" provider.
func AnthropicProvider() *ModelsProvider { return builtinProvider("anthropic") }

// AzureProvider is the built-in "azure" provider.
func AzureProvider() *ModelsProvider { return builtinProvider("azure") }

// BasetenProvider is the built-in "baseten" provider.
func BasetenProvider() *ModelsProvider { return builtinProvider("baseten") }

// CerebrasProvider is the built-in "cerebras" provider.
func CerebrasProvider() *ModelsProvider { return builtinProvider("cerebras") }

// CloudflareAIGatewayProvider is the built-in "cloudflare-ai-gateway" provider.
func CloudflareAIGatewayProvider() *ModelsProvider { return builtinProvider("cloudflare-ai-gateway") }

// DeepseekProvider is the built-in "deepseek" provider.
func DeepseekProvider() *ModelsProvider { return builtinProvider("deepseek") }

// FireworksProvider is the built-in "fireworks" provider.
func FireworksProvider() *ModelsProvider { return builtinProvider("fireworks") }

// GithubCopilotProvider is the built-in "github-copilot" provider.
func GithubCopilotProvider() *ModelsProvider { return builtinProvider("github-copilot") }

// GoogleVertexProvider is the built-in "google-vertex" provider.
func GoogleVertexProvider() *ModelsProvider { return builtinProvider("google-vertex") }

// GroqProvider is the built-in "groq" provider.
func GroqProvider() *ModelsProvider { return builtinProvider("groq") }

// KimiCodingProvider is the built-in "kimi-coding" provider.
func KimiCodingProvider() *ModelsProvider { return builtinProvider("kimi-coding") }

// MetaProvider is the built-in "meta" provider.
func MetaProvider() *ModelsProvider { return builtinProvider("meta") }

// MinimaxProvider is the built-in "minimax" provider.
func MinimaxProvider() *ModelsProvider { return builtinProvider("minimax") }

// MinimaxCnProvider is the built-in "minimax-cn" provider.
func MinimaxCnProvider() *ModelsProvider { return builtinProvider("minimax-cn") }

// MistralProvider is the built-in "mistral" provider.
func MistralProvider() *ModelsProvider { return builtinProvider("mistral") }

// MoonshotaiProvider is the built-in "moonshotai" provider.
func MoonshotaiProvider() *ModelsProvider { return builtinProvider("moonshotai") }

// MoonshotaiCnProvider is the built-in "moonshotai-cn" provider.
func MoonshotaiCnProvider() *ModelsProvider { return builtinProvider("moonshotai-cn") }

// NvidiaProvider is the built-in "nvidia" provider.
func NvidiaProvider() *ModelsProvider { return builtinProvider("nvidia") }

// OpenaiProvider is the built-in "openai" provider.
func OpenaiProvider() *ModelsProvider { return builtinProvider("openai") }

// OpenaiCodexProvider is the built-in "openai-codex" provider.
func OpenaiCodexProvider() *ModelsProvider { return builtinProvider("openai-codex") }

// OpencodeProvider is the built-in "opencode" provider; its API streams add the OpenCode session header (providers/opencode.ts).
func OpencodeProvider() *ModelsProvider { return builtinProvider("opencode") }

// OpenrouterProvider is the built-in "openrouter" provider.
func OpenrouterProvider() *ModelsProvider { return builtinProvider("openrouter") }

// QwenTokenPlanProvider is the built-in "qwen-token-plan" provider.
func QwenTokenPlanProvider() *ModelsProvider { return builtinProvider("qwen-token-plan") }

// QwenTokenPlanCnProvider is the built-in "qwen-token-plan-cn" provider.
func QwenTokenPlanCnProvider() *ModelsProvider { return builtinProvider("qwen-token-plan-cn") }

// QwenTokenPlanIndividualProvider is the built-in "qwen-token-plan-individual" provider.
func QwenTokenPlanIndividualProvider() *ModelsProvider {
	return builtinProvider("qwen-token-plan-individual")
}

// VercelAIGatewayProvider is the built-in "vercel-ai-gateway" provider.
func VercelAIGatewayProvider() *ModelsProvider { return builtinProvider("vercel-ai-gateway") }

// XaiProvider is the built-in "xai" provider.
func XaiProvider() *ModelsProvider { return builtinProvider("xai") }

// XiaomiTokenPlanAmsProvider is the built-in "xiaomi-token-plan-ams" provider.
func XiaomiTokenPlanAmsProvider() *ModelsProvider { return builtinProvider("xiaomi-token-plan-ams") }

// XiaomiTokenPlanCnProvider is the built-in "xiaomi-token-plan-cn" provider.
func XiaomiTokenPlanCnProvider() *ModelsProvider { return builtinProvider("xiaomi-token-plan-cn") }

// XiaomiTokenPlanSgpProvider is the built-in "xiaomi-token-plan-sgp" provider.
func XiaomiTokenPlanSgpProvider() *ModelsProvider { return builtinProvider("xiaomi-token-plan-sgp") }

// ZaiProvider is the built-in "zai" provider.
func ZaiProvider() *ModelsProvider { return builtinProvider("zai") }

// ZaiCodingCnProvider is the built-in "zai-coding-cn" provider.
func ZaiCodingCnProvider() *ModelsProvider { return builtinProvider("zai-coding-cn") }
