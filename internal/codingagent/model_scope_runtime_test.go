package codingagent

import (
	"context"
	"errors"
	"strings"
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

// upstream: the awaited getAvailable rejection propagates; resolveModelScope console.warns chalk.yellow(`Warning: ${message}`) per diagnostic and returns only the scope (model-resolver.ts:372-382).
func TestResolveModelScopePropagatesRuntimeErrorsAndWarnsPerDiagnostic(t *testing.T) {
	boom := errors.New("auth refresh failed")
	if _, err := ResolveModelScopeWithDiagnostics(t.Context(), []string{"x"}, &scopeRuntime{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	var plain, colored strings.Builder
	runtime := &scopeRuntime{models: []RuntimeModel{{Provider: "p", ID: "alpha"}}}
	scoped, err := resolveModelScopeWarningTo(t.Context(), &plain, false, []string{"p/alpha", "nope", "nada"}, runtime)
	if err != nil || len(scoped) != 1 {
		t.Fatalf("scoped = %+v, err = %v", scoped, err)
	}
	result, _ := ResolveModelScopeWithDiagnostics(t.Context(), []string{"p/alpha", "nope", "nada"}, runtime)
	if len(result.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	want := "Warning: " + result.Diagnostics[0].Message + "\nWarning: " + result.Diagnostics[1].Message + "\n"
	if plain.String() != want {
		t.Fatalf("warnings = %q, want %q (console.warn per diagnostic, in pattern order)", plain.String(), want)
	}
	if _, err := resolveModelScopeWarningTo(t.Context(), &colored, true, []string{"nope"}, runtime); err != nil {
		t.Fatal(err)
	}
	if want := "\x1b[33mWarning: " + result.Diagnostics[0].Message + "\x1b[39m\n"; colored.String() != want {
		t.Fatalf("colored warning = %q, want chalk.yellow %q", colored.String(), want)
	}
	if _, err := ResolveModelScope(t.Context(), nil, &scopeRuntime{err: boom}); !errors.Is(err, boom) {
		t.Fatalf("ResolveModelScope err = %v", err)
	}
}
