package coding

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's newSession, fork and switchSession return a Promise, so a call an extension makes releases its call lane after the synchronous start. The host marks that start through extension.CallInitiated (subprocess/host_calls.go startsAsync). A Runtime replacement must mark it before it emits the first before hook, as the bare-Session path does (session_extension_replacement.go beforeExtensionReplacement), or the extension's call lane stays blocked until teardown, rebuild, rebind and session_start finish.
func TestRuntimeReplacementMarksCallInitiationBeforeTheBeforeHook(t *testing.T) {
	var initiated, sawInitiated atomic.Bool
	cancel := func() extension.HandlerFn {
		return func(...any) (any, error) {
			sawInitiated.Store(initiated.Load())
			return extension.SessionBeforeSwitchResult{Cancel: true}, nil
		}
	}
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_before_switch": {cancel()},
			"session_before_fork":   {cancel()},
		}}
	}})
	runtimePrompt(t, h.runtime, "hello")
	session := h.runtime.Session()
	actions := h.runtime.ExtensionCommandActions(session)
	entry := session.SessionManager().GetBranch()[0]
	for _, tc := range []struct {
		name string
		run  func(context.Context) (extension.CancelledResult, error)
	}{
		{"newSession", func(ctx context.Context) (extension.CancelledResult, error) {
			return actions.NewSessionContext(ctx, nil)
		}},
		{"switchSession", func(ctx context.Context) (extension.CancelledResult, error) {
			return actions.SwitchSessionContext(ctx, session.Path(), nil)
		}},
		{"fork", func(ctx context.Context) (extension.CancelledResult, error) {
			return actions.ForkContext(ctx, entry.Base.ID, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initiated.Store(false)
			sawInitiated.Store(false)
			ctx := extension.WithCallInitiation(t.Context(), func() { initiated.Store(true) })
			if result, err := tc.run(ctx); err != nil || !result.Cancelled {
				t.Fatalf("%s = %v, %v; want a cancelled replacement", tc.name, result, err)
			}
			if !sawInitiated.Load() {
				t.Fatalf("%s emitted its before hook before it marked call initiation", tc.name)
			}
		})
	}
}
