package ai

// builtinProviderNames mirrors the `name` field of every built-in provider in
// upstream packages/ai/src/providers/*.ts. Upstream renders this name in the
// login selector (interactive-mode.ts:getLoginProviderOptions) and in login
// status labels, so the port must use the same strings.
var builtinProviderNames = map[string]string{
	"amazon-bedrock":             "Amazon Bedrock",
	"ant-ling":                   "Ant Ling",
	"anthropic":                  "Anthropic",
	"azure-openai-responses":     "Azure OpenAI",
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
	"openai-codex":               "OpenAI Codex",
	"opencode":                   "OpenCode Zen",
	"opencode-go":                "OpenCode Go",
	"openrouter":                 "OpenRouter",
	"qwen-token-plan":            "Qwen Token Plan",
	"qwen-token-plan-cn":         "Qwen Token Plan CN",
	"qwen-token-plan-individual": "Qwen Token Plan Individual",
	"radius":                     "Radius",
	"together":                   "Together",
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
