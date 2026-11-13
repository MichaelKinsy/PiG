package codingagent

import (
	"context"
	"errors"
	"testing"
)

type scopeRuntime struct {
	models []RuntimeModel
	err    error
	ctx    context.Context
}

func (r *scopeRuntime) GetAvailable(ctx context.Context) ([]RuntimeModel, error) {
	r.ctx = ctx
	return r.models, r.err
}

type scopeKey struct{}

// upstream: model-resolver.ts resolveModelScopeWithDiagnostics resolves against modelRuntime.getAvailable(); an unmatched pattern is a no-match warning, not an error.
// Pi: packages/coding-agent/src/core/model-resolver.ts:272 (ModelScopeDiagnostic.code); packages/coding-agent/src/core/model-resolver.ts:274 (ModelScopeDiagnostic.pattern); packages/coding-agent/src/core/model-resolver.ts:278 (ResolveModelScopeResult.scopedModels).
func TestResolveModelScopeWithDiagnosticsUsesTheRuntimesAvailableModels(t *testing.T) {
	runtime := &scopeRuntime{models: []RuntimeModel{{Provider: "p", ID: "alpha"}, {Provider: "p", ID: "beta"}}}
	ctx := context.WithValue(t.Context(), scopeKey{}, "k")
	result, err := ResolveModelScopeWithDiagnostics(ctx, []string{"p/alpha", "missing"}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ctx.Value(scopeKey{}) != "k" {
		t.Fatal("the caller's context did not reach GetAvailable")
	}
	if len(result.ScopedModels) != 1 || result.ScopedModels[0].Model.ID != "alpha" {
		t.Fatalf("scoped = %+v", result.ScopedModels)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "no-match" || result.Diagnostics[0].Pattern != "missing" {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

// upstream: the awaited getAvailable rejection propagates (model-resolver.ts resolveModelScopeWithDiagnostics). Pi bacd45b9c (#11973) removed resolveModelScope, the variant that printed each diagnostic with console.warn, and its test: the diagnostics are returned.
func TestResolveModelScopeWithDiagnosticsPropagatesRuntimeErrors(t *testing.T) {
	boom := errors.New("auth refresh failed")
	if _, err := ResolveModelScopeWithDiagnostics(t.Context(), []string{"x"}, &scopeRuntime{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
