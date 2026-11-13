// Package streamattachhook lets a test hold a straight-line stream consumer in the window between the provider stream's creation and the consumer's first read, where Pi's `for await` has none: Pi creates the stream and attaches in one synchronous prefix. Production code never installs a hook; with none installed Attach costs one atomic load.
package streamattachhook

import (
	"context"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

// Hook runs where a consumer is about to read its stream. ctx is the context the consumer iterates under; a consumer inside a stream continuation passes one that carries its observation (ai.StreamObservationFromContext).
type Hook func(ctx context.Context, stream *ai.AssistantMessageEventStream)

var installed atomic.Pointer[Hook]

// Install sets the hook until the returned restore function runs. Tests that install one must not run in parallel with each other.
func Install(hook Hook) (restore func()) {
	previous := installed.Swap(&hook)
	return func() { installed.Store(previous) }
}

// Attach runs the installed hook, if any. A consumer calls it after creating the stream and before reading its first event.
func Attach(ctx context.Context, stream *ai.AssistantMessageEventStream) {
	if hook := installed.Load(); hook != nil {
		(*hook)(ctx, stream)
	}
}
