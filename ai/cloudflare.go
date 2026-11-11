package ai

const (
	CloudflareWorkersAIBaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	// CloudflareWorkersAIRESTBaseURL is the Workers AI REST endpoint root; model runs are posted to `run` (api/cloudflare.ts:5-7).
	CloudflareWorkersAIRESTBaseURL      = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai"
	CloudflareAIGatewayCompatBaseURL    = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat"
	CloudflareAIGatewayOpenAIBaseURL    = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"
	CloudflareAIGatewayAnthropicBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic"
)
