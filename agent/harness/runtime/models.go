package runtime

import (
	"context"

	"github.com/MichaelKinsy/PiG/ai"
)

// Models is the provider collection used by the durable runtime. The runtime accepts the production collection and instrumented collections through the same request boundary.
// Ports packages/agent/src/harness/agent-harness.ts (AgentHarnessOptions.models).
type Models interface {
	GetModel(provider, id string) *ai.Model
	StreamSimple(context.Context, *ai.Model, ai.Context, ...ai.StreamOptions) *ai.AssistantMessageEventStream
	StreamDeferred(context.Context, *ai.Model, ai.DeferredHandle, ...ai.DeferredFetchOptions) *ai.AssistantMessageEventStream
	CancelDeferred(context.Context, *ai.Model, ai.DeferredHandle, ...ai.DeferredCancelOptions) error
	CompleteSimple(context.Context, *ai.Model, ai.Context, ...ai.StreamOptions) *ai.AssistantMessage
}

var _ Models = (*ai.Models)(nil)
