package codingagent

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// StreamAPILeaf runs one pi-ai builtin API request for a process that has no extension runtime and no Session: model is the pi-ai model the caller passed (provider, id, api, baseUrl,
// headers, compat), request its context and stream options (the caller supplies the API key or auth headers, as pi-ai's own API functions require). It is the provider bridge
// subprocess's stream: pi-ai compat stream/streamSimple over the global API registry, with the extension wire's decoding.
// pig divergence (D74): builtin pi-ai API implementations run in PiG's Go providers; this is the transport for callers outside an extension connection.
func StreamAPILeaf(ctx context.Context, simple bool, model, request map[string]any, transport ai.Transport, callbacks extension.ModelStreamRequest) (*ai.AssistantMessageEventStream, error) {
	llmModel, err := subprocessAPIModel(model)
	if err != nil {
		return nil, err
	}
	ctx, aiRequest, options, err := decodeSubprocessStream(ctx, llmModel, request, transport, withAPI(callbacks))
	if err != nil {
		return nil, err
	}
	if simple {
		return ai.StreamSimpleContext(ctx, llmModel, aiRequest, options)
	}
	return ai.Stream(ctx, llmModel, aiRequest, options)
}

func withAPI(callbacks extension.ModelStreamRequest) extension.ModelStreamRequest {
	callbacks.API = true
	return callbacks
}
