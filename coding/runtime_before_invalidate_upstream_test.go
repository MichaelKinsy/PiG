package coding

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports "runs beforeSessionInvalidate after session_shutdown and before rebindSession" of packages/coding-agent/test/agent-session-runtime-events.test.ts: the hook runs while the outgoing session's extension context is still usable, between session_shutdown and the rebind, and that context is stale afterwards.
// Pi: packages/coding-agent/src/core/agent-session-runtime.ts:129 (Runtime.setBeforeSessionInvalidate).
func TestBeforeSessionInvalidateRunsBetweenShutdownAndRebind(t *testing.T) {
	var phases []string
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_shutdown": {func(...any) (any, error) {
			phases = append(phases, "session_shutdown")
			return nil, nil
		}}}}
	}})
	old := h.runtime.Session()
	if err := old.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	captured := old.runner.CreateCommandContext()
	h.runtime.SetBeforeSessionInvalidate(func() {
		phases = append(phases, "beforeSessionInvalidate")
		if _, err := captured.CWD(); err != nil {
			t.Errorf("context unusable inside beforeSessionInvalidate: %v", err)
		}
	})
	h.runtime.SetRebindSession(func(_ context.Context, _ *Session) error {
		phases = append(phases, "rebindSession")
		return nil
	})
	if _, err := h.runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"session_shutdown", "beforeSessionInvalidate", "rebindSession"}; !slices.Equal(phases, want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	if _, err := captured.CWD(); err == nil || !strings.Contains(err.Error(), "stale after session replacement or reload") {
		t.Fatalf("context after replacement: %v", err)
	}
	h.runtime.SetBeforeSessionInvalidate(nil)
	h.runtime.SetRebindSession(nil)
}
