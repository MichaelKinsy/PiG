package coding

// Ports packages/coding-agent/src/core/provider-composer.ts
// Ports packages/ai/src/api/lazy.ts

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// apiStreamProvider retains the API module's lazy forwarding boundary independently of provider composition. lazyApi's setup awaits load() before it calls the API (lazy.ts:76-79), so setup runs in the first reaction.
type apiStreamProvider struct {
	ai.Provider
	model *ai.Model
}

func (provider *apiStreamProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return ai.LazyStream(ctx, provider.model, func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
		return provider.Provider.Stream(ctx, transcript, options)
	}), nil
}

// composedStreamProvider retains provider-composer's streamWith boundary. It forwards extension callbacks as well as stock API streams. streamWith's setup has no await (provider-composer.ts:492-508), so the provider call is the caller's synchronous prefix.
type composedStreamProvider struct {
	ai.Provider
	model *ai.Model
}

func (provider *composedStreamProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return ai.LazyStreamSync(ctx, provider.model, func(ctx context.Context) (*ai.AssistantMessageEventStream, error) {
		return provider.Provider.Stream(ctx, transcript, options)
	}), nil
}
