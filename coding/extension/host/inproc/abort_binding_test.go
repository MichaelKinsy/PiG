package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session.ts:3095-3107 binds abort alongside, not instead of, the mode's other context actions.
func TestBindAbortPreservesOtherContextActions(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	var oldCalls, newCalls int
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{
		Abort: func() { oldCalls++ }, IsProjectTrusted: func() bool { return false },
	}, nil)
	runner.BindAbort(func() { newCalls++ })
	ctx := extension.FromContext(runner.DispatchContext(t.Context()))
	if err := ctx.Abort(); err != nil {
		t.Fatal(err)
	}
	trusted, err := ctx.IsProjectTrusted()
	if err != nil || trusted || oldCalls != 0 || newCalls != 1 {
		t.Fatalf("trusted=%t error=%v old=%d new=%d", trusted, err, oldCalls, newCalls)
	}
	runner.Invalidate("closed")
	if err := ctx.Abort(); err == nil || newCalls != 1 {
		t.Fatalf("stale abort = %v, calls=%d", err, newCalls)
	}
}
