package ai

// Ports packages/ai/src/providers/cloudflare-stream.ts (resolveCloudflareModel, cloudflareStreams).

import (
	"context"
)

// ResolveCloudflareModel materializes the Cloudflare account and gateway placeholders of model's base URL from env (cloudflare-stream.ts:6-17). A missing
// value keeps its placeholder and an empty one replaces it with the empty string. A nil env or an endpoint without placeholders returns model itself;
// otherwise the result is a copy and model is untouched.
func ResolveCloudflareModel(model *Model, env ProviderEnv) *Model {
	if env == nil {
		return model
	}
	baseURL := model.ProviderMeta.BaseURL
	for _, name := range []string{cloudflareAccountID, cloudflareGatewayID} {
		if value, exists := env[name]; exists {
			baseURL = replaceCloudflarePlaceholder(baseURL, "{"+name+"}", value)
		}
	}
	if baseURL == model.ProviderMeta.BaseURL {
		return model
	}
	resolved := *model
	resolved.ProviderMeta.BaseURL = baseURL
	return &resolved
}

// CloudflareStreams wraps an API implementation so Cloudflare account and gateway endpoint placeholders materialize from the resolved provider env
// before dispatch (cloudflare-stream.ts:19-29). Deferred operations are not part of Pi's wrapper and pass through.
func CloudflareStreams(streams *ProviderStreams) *ProviderStreams {
	wrapped := *streams
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
