package coding

// pi: packages/coding-agent/src/core/model-resolver.ts

import (
	"context"
	"errors"
	"testing"
)

// model-resolver.ts:409-422 awaits modelRuntime.getAvailable(undefined, {signal}): the facade passes the caller's context through, and its rejection is the facade's rejection.
func TestResolveModelScopeCancelledContextRejectsAsyncModelsA(t *testing.T) {
	runtime := resolverRuntimeFixture(t, "anthropic")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := ResolveModelScopeWithDiagnostics(ctx, []string{"sonnet"}, runtime)
	if !errors.Is(err, context.Canceled) || len(result.ScopedModels) != 0 || len(result.Diagnostics) != 0 {
		t.Fatalf("result = %+v, err = %v, want an empty result and %v", result, err, context.Canceled)
	}
}
