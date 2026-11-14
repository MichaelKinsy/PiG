package coding

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/src/core/provider-composer.ts

// apiRegistryProvider is the last step of composeModelProvider's stream for a model no extension callback and no built-in API serves: the
// model's api is looked up in the API registry (ai.GetAPIProvider, compat.ts getApiProvider: a Map, so any unregistered name misses) when the
// request starts, after the request's credential is resolved (model-runtime.ts prepareRequest runs before provider.stream), and an
// unregistered api is refused before anything is built for the model's endpoint.
// upstream: provider-composer.ts:603-607
type apiRegistryProvider struct {
	id     string
	api    ai.API
	model  *ai.Model
	apiKey func(context.Context) (string, error)
}

func (p *apiRegistryProvider) ID() string { return p.id }
func (*apiRegistryProvider) Close() error { return nil }
func (p *apiRegistryProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	key, err := p.apiKey(ctx)
	if err != nil {
		return nil, err
	}
	api := ai.GetAPIProvider(p.api)
	if api == nil {
		return nil, ai.NoAPIProviderError(p.api)
	}
	options.APIKey = key
	return api.StreamSimple(ctx, p.model, transcript, options)
}
