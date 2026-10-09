package coding

import (
	"context"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// providerStreamFn streams through the model's own Provider, as the removed agent fallback did.
func providerStreamFn(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	if model == nil || model.Provider == nil {
		return nil, agent.ErrNoModelSelected
	}
	return model.Provider.Stream(ctx, transcript, options)
}

// mustNewAgent is agent.NewAgent for tests that do not test the constructor's stream-function requirement.
func mustNewAgent(opts agent.AgentOptions) *agent.Agent {
	if opts.StreamFn == nil && opts.DefaultStreamFn == nil {
		if _, err := agent.GetDefaultStreamFn(); err != nil {
			opts.DefaultStreamFn = providerStreamFn
		}
	}
	a, err := agent.NewAgent(opts)
	if err != nil {
		panic(err)
	}
	return a
}
