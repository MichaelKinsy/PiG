package coding

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports packages/coding-agent/src/core/model-runtime.ts
// Ports packages/coding-agent/src/core/provider-composer.ts
// registeredStreamProvider dispatches the registry-owned callback, not the process-global API inventory.
type registeredStreamProvider struct {
	id           string
	model        *ai.Model
	streamSimple extension.ProviderStreamSimple
	apiKey       func(context.Context) (string, error)
}

func (p *registeredStreamProvider) ID() string { return p.id }
func (*registeredStreamProvider) Close() error { return nil }
func (p *registeredStreamProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (stream *ai.AssistantMessageEventStream, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			stream = nil
			err = fmt.Errorf("provider %q streamSimple: %v", p.id, recovered)
		}
	}()
	key, err := p.apiKey(ctx)
	if err != nil {
		return nil, err
	}
	options.APIKey = key
	return icodingagent.InvokeProviderStreamSimple(ctx, p.id, p.streamSimple, p.model, transcript, options)
}
