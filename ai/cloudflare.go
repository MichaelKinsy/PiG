package ai

import "strings"

const (
	CloudflareWorkersAIBaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	// CloudflareWorkersAIRESTBaseURL is the Workers AI REST endpoint root; model runs are posted to `run` (api/cloudflare.ts:5-7).
	CloudflareWorkersAIRESTBaseURL      = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai"
	CloudflareAIGatewayCompatBaseURL    = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat"
	CloudflareAIGatewayOpenAIBaseURL    = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"
	CloudflareAIGatewayAnthropicBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic"
)

// resolveCloudflarePlaceholders is the substitution of resolveCloudflareModel (cloudflare-stream.ts:10-13): the account placeholder is replaced first, then the gateway placeholder in that result.
func resolveCloudflarePlaceholders(baseURL string, env ProviderEnv) string {
	if env == nil {
		return baseURL
	}
	for _, name := range []string{cloudflareAccountID, cloudflareGatewayID} {
		if value, exists := env[name]; exists {
			baseURL = replaceCloudflarePlaceholder(baseURL, "{"+name+"}", value)
		}
	}
	return baseURL
}

// The placeholder is nonempty. JavaScript string-search replacements expand $$, $&, $`, and $' against the original input at each match; capture-group tokens stay literal.
func replaceCloudflarePlaceholder(input, placeholder, replacement string) string {
	if !strings.Contains(replacement, "$") {
		return strings.ReplaceAll(input, placeholder, replacement)
	}
	var result strings.Builder
	offset := 0
	for {
		index := strings.Index(input[offset:], placeholder)
		if index < 0 {
			result.WriteString(input[offset:])
			return result.String()
		}
		start := offset + index
		end := start + len(placeholder)
		result.WriteString(input[offset:start])
		for i := 0; i < len(replacement); i++ {
			if replacement[i] == '$' && i+1 < len(replacement) {
				switch replacement[i+1] {
				case '$':
					result.WriteByte('$')
				case '&':
					result.WriteString(placeholder)
				case '`':
					result.WriteString(input[:start])
				case '\'':
					result.WriteString(input[end:])
				default:
					result.WriteByte(replacement[i])
					continue
				}
				i++
				continue
			}
			result.WriteByte(replacement[i])
		}
		offset = end
	}
}
