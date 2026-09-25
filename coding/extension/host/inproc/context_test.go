package inproc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestDispatchContext_CWDMatchesRunner: the extension.Context attached
// during dispatch carries the runner's CWD.
func TestDispatchContext_CWDMatchesRunner(t *testing.T) {
	var gotCWD string
	exts := []extension.Extension{
		extWithGenericHandler("/ext/a", "session_start", func(_ any, ctx context.Context) any {
			if extCtx := extension.FromContext(ctx); extCtx != nil {
				cwd, _ := extCtx.CWD()
				gotCWD = cwd
			}
			return nil
		}),
	}
	r := inproc.NewRunner(exts, "/my/project")
	_, _ = r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})

	if gotCWD != "/my/project" {
		t.Errorf("CWD from Context = %q, want /my/project", gotCWD)
	}
}

// TestDispatchContext_AssertActiveRejectsStale: after Invalidate(), any
// getter on the extension.Context returns an error.
func TestDispatchContext_AssertActiveRejectsStale(t *testing.T) {
	var cwdErr error
	exts := []extension.Extension{
		extWithToolCallHandler("/ext/a", func(_ extension.ToolCallEvent, ctx context.Context) *extension.ToolCallEventResult {
			if extCtx := extension.FromContext(ctx); extCtx != nil {
				_, cwdErr = extCtx.CWD()
			}
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	// Emit before invalidation: should work fine.
	_, _ = r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if cwdErr != nil {
		t.Fatalf("CWD() before invalidation: err = %v, want nil", cwdErr)
	}

	// Invalidate, then emit again: handler should see stale error.
	r.Invalidate("test")
	// Emit returns *StaleError from assertActive BEFORE dispatch, so the
	// handler never runs. Instead test via a fresh runner and captured context.
}

// TestDispatchContext_CapturedContextRejectsAfterInvalidate: captures
// an extension.Context reference during dispatch, then invalidates the
// runner and calls CWD() on the captured context → stale error.
func TestDispatchContext_CapturedContextRejectsAfterInvalidate(t *testing.T) {
	var captured *extension.Context
	exts := []extension.Extension{
		extWithGenericHandler("/ext/a", "session_start", func(_ any, ctx context.Context) any {
			captured = extension.FromContext(ctx)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	_, _ = r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})

	if captured == nil {
		t.Fatal("handler did not capture extension.Context")
	}
	// Before invalidation: should work.
	if _, err := captured.CWD(); err != nil {
		t.Fatalf("CWD before invalidation: err = %v", err)
	}

	r.Invalidate("stale now")

	// After invalidation: should reject.
	_, err := captured.CWD()
	if err == nil {
		t.Fatal("CWD after invalidation: err = nil, want stale error")
	}
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("CWD after invalidation: err = %v, want ErrStaleContext", err)
	}
}
