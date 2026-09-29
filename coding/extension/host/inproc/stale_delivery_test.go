package inproc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// upstream: runner.ts:928-1017 (emitBoundary, emit) never call assertActive;
// only the ctx getters and actions do (runner.ts:809-888). An invalidated
// runner still delivers events, and a handler that uses its ctx gets the stale
// error. AgentSession.dispose invalidates before an aborted run's turn_end and
// agent_settled reach extensions.
func TestEmitAfterInvalidateDeliversWithStaleContext(t *testing.T) {
	var delivered []string
	var ctxErrs []error
	record := func(name string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			delivered = append(delivered, name)
			ctx, _ := args[1].(context.Context)
			if extCtx := extension.FromContext(ctx); extCtx != nil {
				_, err := extCtx.CWD()
				ctxErrs = append(ctxErrs, err)
			} else {
				ctxErrs = append(ctxErrs, errors.New("no extension context"))
			}
			return nil, nil
		}
	}
	ext := newFakeExtension("/ext/a")
	ext.Handlers["agent_settled"] = []extension.HandlerFn{record("agent_settled")}
	ext.Handlers["turn_end"] = []extension.HandlerFn{record("turn_end")}
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	r.Invalidate("disposed")

	if _, err := r.EmitBoundary(context.Background(), extension.TurnEndEvent{Type: "turn_end", BoundaryState: &extension.BoundaryState{Outcome: extension.AgentActivityAborted}},
		func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{}, nil
		}); err != nil {
		t.Fatalf("EmitBoundary on an invalidated runner: %v", err)
	}
	if _, err := r.Emit(context.Background(), extension.AgentSettledEvent{Type: "agent_settled"}); err != nil {
		t.Fatalf("Emit on an invalidated runner: %v", err)
	}
	if len(delivered) != 2 || delivered[0] != "turn_end" || delivered[1] != "agent_settled" {
		t.Fatalf("delivered = %v, want [turn_end agent_settled]", delivered)
	}
	for i, err := range ctxErrs {
		var stale *inproc.StaleError
		if !errors.As(err, &stale) || stale.Message != "disposed" || !errors.Is(err, extension.ErrStaleContext) {
			t.Fatalf("%s ctx.CWD() err = %v, want the stale error", delivered[i], err)
		}
	}
}
