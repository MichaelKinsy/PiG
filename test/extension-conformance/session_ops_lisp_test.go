package extensionconformance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestLispSessionActionsReachTheHostActions: with the host's session actions bound, the Lisp SDK's newSession, fork, navigateTree and
// switchSession reach them with the options the extension gave and return the host's answer (a cancellation is true, which the unbound
// answer never is) or its error. The unbound row (TestConformance_ExtensionUnboundSessionActions) pins the other side.
func TestLispSessionActionsReachTheHostActions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	h := lispHarnessCase().make(t)
	t.Cleanup(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
		h.host.Shutdown("test done")
	})
	var mu sync.Mutex
	var calls []string
	record := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, fmt.Sprintf(format, args...))
	}
	h.bridge.SetHostAction("newSession", func(_ context.Context, opts *extension.NewSessionOptions) (extension.CancelledResult, error) {
		record("new:%s", opts.ParentSession)
		return extension.CancelledResult{Cancelled: true}, nil
	})
	h.bridge.SetHostAction("fork", func(_ context.Context, entryID string, opts *extension.ForkOptions) (extension.CancelledResult, error) {
		record("fork:%s:%s", entryID, opts.Position)
		return extension.CancelledResult{Cancelled: true}, nil
	})
	h.bridge.SetHostAction("navigateTree", func(_ context.Context, targetID string, opts *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
		record("navigate:%s:%t:%s:%t:%s", targetID, opts.Summarize, opts.CustomInstructions, opts.ReplaceInstructions, opts.Label)
		return extension.CancelledResult{Cancelled: true}, nil
	})
	h.bridge.SetHostAction("switchSession", func(_ context.Context, path string, _ *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
		record("switch:%s", path)
		return extension.CancelledResult{}, errors.New("switch refused")
	})
	*h.notify = nil
	runConformanceCommand(t, h, "session_actions_bound")
	const want = "bound:new=true,fork=true,navigate=true,switch=error:switch refused:info"
	pollUntilConformance(t, "the Lisp SDK never reported its bound session actions", func() bool { return slices.Contains(*h.notify, want) })
	mu.Lock()
	defer mu.Unlock()
	wantCalls := []string{"new:/parent.jsonl", "fork:entry-1:at", "navigate:entry-2:true:brief:true:mark", "switch:/target.jsonl"}
	if !slices.Equal(calls, wantCalls) {
		t.Fatalf("the host actions received %v, want %v", calls, wantCalls)
	}
}
