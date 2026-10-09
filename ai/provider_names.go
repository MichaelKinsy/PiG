package ai

// builtinProviderNames mirrors the `name` field of every built-in provider in
// upstream packages/ai/src/providers/*.ts. Upstream renders this name in the
// login selector (interactive-mode.ts:getLoginProviderOptions) and in login
// status labels, so the port must use the same strings.
var builtinProviderNames = map[string]string{
	"amazon-bedrock":             "Amazon Bedrock",
	"ant-ling":                   "Ant Ling",
	"anthropic":                  "Anthropic",
	"azure":                      "Azure",
	"baseten":                    "Baseten",
	"cerebras":                   "Cerebras",
	"cloudflare-ai-gateway":      "Cloudflare AI Gateway",
	"cloudflare-workers-ai":      "Cloudflare Workers AI",
	"deepseek":                   "DeepSeek",
	"fireworks":                  "Fireworks",
	"github-copilot":             "GitHub Copilot",
	"google":                     "Google",
	"google-vertex":              "Google Vertex AI",
	"groq":                       "Groq",
	"huggingface":                "Hugging Face",
	"kimi-coding":                "Kimi For Coding",
	"meta":                       "Meta",
	"minimax":                    "MiniMax",
	"minimax-cn":                 "MiniMax CN",
	"mistral":                    "Mistral",
	"moonshotai":                 "Moonshot AI",
	"moonshotai-cn":              "Moonshot AI CN",
	"nvidia":                     "NVIDIA",
	"openai":                     "OpenAI",
	"openai-codex":               "OpenAI Codex (legacy)",
	"opencode":                   "OpenCode Zen",
	"opencode-go":                "OpenCode Go",
	"openrouter":                 "OpenRouter",
	"qwen-token-plan":            "Qwen Token Plan",
	"qwen-token-plan-cn":         "Qwen Token Plan CN",
	"qwen-token-plan-individual": "Qwen Token Plan Individual",
	"radius":                     "Radius",
	"together":                   "Together",
	"typesafe":                   "TypeSafe",
	"vercel-ai-gateway":          "Vercel AI Gateway",
	"xai":                        "xAI",
	"xiaomi":                     "Xiaomi",
	"xiaomi-token-plan-ams":      "Xiaomi Token Plan AMS",
	"xiaomi-token-plan-cn":       "Xiaomi Token Plan CN",
	"xiaomi-token-plan-sgp":      "Xiaomi Token Plan SGP",
	"zai":                        "Z.AI",
	"zai-coding-cn":              "Z.AI Coding CN",
}

// ProviderDisplayName returns the upstream provider.name for a built-in
// provider, or providerID when the provider is not in the catalog.
func ProviderDisplayName(providerID string) string {
	if name, ok := builtinProviderNames[providerID]; ok {
		return name
	}
	return providerID
}

// builtinProviderBaseURLs mirrors the `baseUrl` of each built-in provider in upstream packages/ai/src/providers/*.ts. It is the provider-level default that bug reports and provider composition read; model catalogs carry their own per-model base URLs. A provider with no entry declares none upstream.
var builtinProviderBaseURLs = map[string]string{
	"ant-ling":                   "https://api.ant-ling.com/v1",
	"anthropic":                  "https://api.anthropic.com",
	"baseten":                    "https://inference.baseten.co/v1",
	"cerebras":                   "https://api.cerebras.ai/v1",
	"deepseek":                   "https://api.deepseek.com",
	"fireworks":                  "https://api.fireworks.ai/inference",
	"github-copilot":             "https://api.individual.githubcopilot.com",
	"google":                     "https://generativelanguage.googleapis.com/v1beta",
	"groq":                       "https://api.groq.com/openai/v1",
	"huggingface":                "https://router.huggingface.co/v1",
	"kimi-coding":                "https://api.kimi.com/coding",
	"meta":                       "https://api.meta.ai/v1",
	"minimax":                    "https://api.minimax.io/anthropic",
	"minimax-cn":                 "https://api.minimaxi.com/anthropic",
	"mistral":                    "https://api.mistral.ai",
	"moonshotai":                 "https://api.moonshot.ai/v1",
	"moonshotai-cn":              "https://api.moonshot.cn/v1",
	"nvidia":                     "https://integrate.api.nvidia.com/v1",
	"openai":                     "https://api.openai.com/v1",
	"openai-codex":               "https://chatgpt.com/backend-api",
	"openrouter":                 "https://openrouter.ai/api/v1",
	"qwen-token-plan":            "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
	"qwen-token-plan-cn":         "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
	"qwen-token-plan-individual": "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
	"together":                   "https://api.together.ai/v1",
	"vercel-ai-gateway":          "https://ai-gateway.vercel.sh",
	"xai":                        "https://api.x.ai/v1",
	"xiaomi":                     "https://api.xiaomimimo.com/v1",
	"xiaomi-token-plan-ams":      "https://token-plan-ams.xiaomimimo.com/v1",
	"xiaomi-token-plan-cn":       "https://token-plan-cn.xiaomimimo.com/v1",
	"xiaomi-token-plan-sgp":      "https://token-plan-sgp.xiaomimimo.com/v1",
	"zai":                        "https://api.z.ai/api/coding/paas/v4",
	"zai-coding-cn":              "https://open.bigmodel.cn/api/coding/paas/v4",
}

// ProviderBaseURL returns the upstream provider.baseUrl of a built-in provider, or "" when the provider declares none.
func ProviderBaseURL(providerID string) string { return builtinProviderBaseURLs[providerID] }
