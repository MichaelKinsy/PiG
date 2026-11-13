package codingagent

// Pi: packages/coding-agent/src/core/model-resolver.ts

import (
	"context"
	"errors"
	"testing"
)

type causeScopeRuntime struct{ called int }

func (r *causeScopeRuntime) GetAvailable(ctx context.Context) ([]RuntimeModel, error) {
	r.called++
	return nil, context.Cause(ctx)
}

// model-resolver.ts:409-422: resolveModelScope awaits getAvailable(options.signal); an aborted request rejects with the signal's reason, so no scope is built and no "no match" warning is written for patterns that were never resolved.
func TestResolveModelScopeAbortedAvailabilityBuildsNoScopeAndWarnsNothing(t *testing.T) {
	cause := errors.New("startup cancelled")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	runtime := &causeScopeRuntime{}
	result, err := ResolveModelScopeWithDiagnostics(ctx, []string{"p/alpha", "nope"}, runtime)
	if !errors.Is(err, cause) || len(result.ScopedModels) != 0 || len(result.Diagnostics) != 0 {
		t.Fatalf("result = %+v, err = %v, want an empty result and %v", result, err, cause)
	}
	if runtime.called != 1 {
		t.Fatalf("GetAvailable calls = %d, want 1", runtime.called)
	}
}
