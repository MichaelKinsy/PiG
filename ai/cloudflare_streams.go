package ai

// Ports packages/ai/src/providers/cloudflare-stream.ts (resolveCloudflareModel, cloudflareStreams).

import (
	"context"
	"strings"
)

// ResolveCloudflareModel materializes the Cloudflare account and gateway placeholders of model's base URL from env (cloudflare-stream.ts:6-17). A missing
// value keeps its placeholder and an empty one replaces it with the empty string. A nil env or an endpoint without placeholders returns model itself;
// otherwise the result is a copy and model is untouched.
func ResolveCloudflareModel(model *Model, env ProviderEnv) *Model {
	baseURL := resolveCloudflarePlaceholders(model.ProviderMeta.BaseURL, env)
	if baseURL == model.ProviderMeta.BaseURL {
		return model
	}
	resolved := *model
	resolved.ProviderMeta.BaseURL = baseURL
	return &resolved
}

// CloudflareStreams wraps an API implementation so Cloudflare account and gateway endpoint placeholders materialize from the resolved provider env
// before dispatch (cloudflare-stream.ts:19-29). As Pi's wrapper returns an object with only stream and streamSimple, the result has no deferred
// operations, so a Cloudflare-wrapped API does not support deferred responses (models.ts:925-927).
func CloudflareStreams(streams *ProviderStreams) *ProviderStreams {
	var wrapped ProviderStreams
	if streams.Stream != nil {
		stream := streams.Stream
		wrapped.Stream = func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			return stream(ctx, ResolveCloudflareModel(model, options.Env), transcript, options)
		}
	}
	if streams.StreamSimple != nil {
		streamSimple := streams.StreamSimple
		wrapped.StreamSimple = func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			return streamSimple(ctx, ResolveCloudflareModel(model, options.Env), transcript, options)
		}
	}
	return &wrapped
}

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
		//portlint:allow utf16units offsets are byte indexes of the ASCII placeholder in this same string, so they sit on code-point boundaries and the copied text matches JS replaceAll (cloudflare-stream.ts:10-13) for non-BMP input too
		index := strings.Index(input[offset:], placeholder)
		if index < 0 {
			//portlint:allow utf16units offsets are byte indexes of the ASCII placeholder in this same string, so they sit on code-point boundaries and the copied text matches JS replaceAll (cloudflare-stream.ts:10-13) for non-BMP input too
			result.WriteString(input[offset:])
			return result.String()
		}
		start := offset + index
		end := start + len(placeholder)
		//portlint:allow utf16units offsets are byte indexes of the ASCII placeholder in this same string, so they sit on code-point boundaries and the copied text matches JS replaceAll (cloudflare-stream.ts:10-13) for non-BMP input too
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
