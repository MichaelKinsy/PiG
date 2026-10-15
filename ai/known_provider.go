package ai

// KnownProvider is a built-in provider id. Ports packages/ai/src/types.ts (KnownProvider).
type KnownProvider string

const (
	KnownProviderAmazonBedrock           KnownProvider = "amazon-bedrock"
	KnownProviderAntLing                 KnownProvider = "ant-ling"
	KnownProviderAnthropic               KnownProvider = "anthropic"
	KnownProviderGoogle                  KnownProvider = "google"
	KnownProviderGoogleVertex            KnownProvider = "google-vertex"
	KnownProviderOpenai                  KnownProvider = "openai"
	KnownProviderAzure                   KnownProvider = "azure"
	KnownProviderOpenaiCodex             KnownProvider = "openai-codex"
	KnownProviderRadius                  KnownProvider = "radius"
	KnownProviderTypesafe                KnownProvider = "typesafe"
	KnownProviderNvidia                  KnownProvider = "nvidia"
	KnownProviderDeepseek                KnownProvider = "deepseek"
	KnownProviderGithubCopilot           KnownProvider = "github-copilot"
	KnownProviderXai                     KnownProvider = "xai"
	KnownProviderGroq                    KnownProvider = "groq"
	KnownProviderCerebras                KnownProvider = "cerebras"
	KnownProviderOpenrouter              KnownProvider = "openrouter"
	KnownProviderVercelAiGateway         KnownProvider = "vercel-ai-gateway"
	KnownProviderZai                     KnownProvider = "zai"
	KnownProviderZaiCodingCn             KnownProvider = "zai-coding-cn"
	KnownProviderMistral                 KnownProvider = "mistral"
	KnownProviderMinimax                 KnownProvider = "minimax"
	KnownProviderMinimaxCn               KnownProvider = "minimax-cn"
	KnownProviderMoonshotai              KnownProvider = "moonshotai"
	KnownProviderMoonshotaiCn            KnownProvider = "moonshotai-cn"
	KnownProviderHuggingface             KnownProvider = "huggingface"
	KnownProviderFireworks               KnownProvider = "fireworks"
	KnownProviderTogether                KnownProvider = "together"
	KnownProviderBaseten                 KnownProvider = "baseten"
	KnownProviderOpencode                KnownProvider = "opencode"
	KnownProviderOpencodeGo              KnownProvider = "opencode-go"
	KnownProviderKimiCoding              KnownProvider = "kimi-coding"
	KnownProviderMeta                    KnownProvider = "meta"
	KnownProviderCloudflareWorkersAi     KnownProvider = "cloudflare-workers-ai"
	KnownProviderCloudflareAiGateway     KnownProvider = "cloudflare-ai-gateway"
	KnownProviderQwenTokenPlan           KnownProvider = "qwen-token-plan"
	KnownProviderQwenTokenPlanCn         KnownProvider = "qwen-token-plan-cn"
	KnownProviderQwenTokenPlanIndividual KnownProvider = "qwen-token-plan-individual"
	KnownProviderXiaomi                  KnownProvider = "xiaomi"
	KnownProviderXiaomiTokenPlanCn       KnownProvider = "xiaomi-token-plan-cn"
	KnownProviderXiaomiTokenPlanAms      KnownProvider = "xiaomi-token-plan-ams"
	KnownProviderXiaomiTokenPlanSgp      KnownProvider = "xiaomi-token-plan-sgp"
)

// ProviderID is a provider id: a KnownProvider or any other string (types.ts ProviderId = KnownProvider | string).
type ProviderID = string
