package ai

import "context"

// endpointProvider is a per-model Provider whose endpoint can be replaced for one request: the three HTTP API providers that hold the
// model's base URL.
type endpointProvider interface {
	Provider
	// endpointModel is the model the provider serves, as far as an API wrapper reads it: its endpoint.
	endpointModel() *Model
	// endpointEnv is the provider's configured Env: the auth resolution env Pi's Models.applyAuth puts under the request env.
	endpointEnv() ProviderEnv
	// withEndpoint is the provider with another base URL, sending through the same HTTP client; the original is unchanged. The same base
	// URL returns the provider itself.
	withEndpoint(baseURL string) Provider
}

func (p *openAIProvider) endpointModel() *Model {
	return &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: APIOpenAICompletions, BaseURL: p.cfg.BaseURL}}
}

func (p *openAIProvider) endpointEnv() ProviderEnv { return p.cfg.Env }

func (p *openAIProvider) withEndpoint(baseURL string) Provider {
	if baseURL == p.cfg.BaseURL {
		return p
	}
	cfg := p.cfg
	cfg.BaseURL = baseURL
	return &openAIProvider{cfg: cfg, client: p.client}
}

func (p *openAIResponsesProvider) endpointModel() *Model {
	return &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: APIOpenAIResponses, BaseURL: p.cfg.BaseURL}}
}

func (p *openAIResponsesProvider) endpointEnv() ProviderEnv { return p.cfg.Env }

func (p *openAIResponsesProvider) withEndpoint(baseURL string) Provider {
	if baseURL == p.cfg.BaseURL {
		return p
	}
	cfg := p.cfg
	cfg.BaseURL = baseURL
	return &openAIResponsesProvider{cfg: cfg, client: p.client}
}

func (p *anthropicProvider) endpointModel() *Model {
	return &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: APIAnthropicMessages, BaseURL: p.cfg.BaseURL}}
}

func (p *anthropicProvider) endpointEnv() ProviderEnv { return p.cfg.Env }

func (p *anthropicProvider) withEndpoint(baseURL string) Provider {
	if baseURL == p.cfg.BaseURL {
		return p
	}
	cfg := p.cfg
	cfg.BaseURL = baseURL
	return &anthropicProvider{cfg: cfg, client: p.client}
}

// wrappedStreamsProvider is a per-model Provider whose requests pass through the wrapper that the provider's upstream constructor puts on
// its API streams.
type wrappedStreamsProvider struct {
	Provider
	streams *ProviderStreams
	model   *Model
	env     ProviderEnv
}

// Stream hands the wrapper the env Pi's Models.applyAuth passes the provider's API streams: the resolution env under the request env,
// per field (models.ts:868). The API provider merges its configured Env the same way, so the wrapper and the request agree.
func (p *wrappedStreamsProvider) Stream(ctx context.Context, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
	options.Env = mergeProviderEnv(p.env, options.Env)
	return p.streams.Stream(ctx, p.model, transcript, options)
}

// WrapBuiltinProvider applies to a per-model provider the wrapper that the built-in provider's upstream constructor puts on its API
// streams (providers/cloudflare-workers-ai.ts:20, providers/cloudflare-ai-gateway.ts:22-24: cloudflareStreams), the same table that
// registers the built-in providers uses. A provider without a wrapper, or whose API is not one of the HTTP providers that carry an
// endpoint, is returned as it is.
func WrapBuiltinProvider(providerID string, provider Provider) Provider {
	wrap := builtinStreamWrappers[providerID]
	endpoint, ok := provider.(endpointProvider)
	if wrap == nil || !ok {
		return provider
	}
	inner := func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
		return endpoint.withEndpoint(model.ProviderMeta.BaseURL).Stream(ctx, transcript, options)
	}
	return &wrappedStreamsProvider{Provider: provider, streams: wrap(&ProviderStreams{Stream: inner, StreamSimple: inner}), model: endpoint.endpointModel(), env: endpoint.endpointEnv()}
}
