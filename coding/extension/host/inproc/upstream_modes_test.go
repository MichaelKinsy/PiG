package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestUpstreamRunnerUIModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode extension.ExtensionMode
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:578
		{"exposes rpc mode with hasUI true when an RPC UI context is provided", extension.ModeRPC},
		// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:589
		{"exposes tui mode with hasUI true when a TUI UI context is provided", extension.ModeTUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inproc.NewRunner(nil, t.TempDir())
			r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
			r.SetUIContext(&struct{ extension.UIContext }{extension.NoopUIContext}, tc.mode)
			ctx := extension.FromContext(r.DispatchContext(t.Context()))
			if mode, err := ctx.Mode(); err != nil || mode != tc.mode {
				t.Fatalf("mode=%q error=%v; want %q", mode, err, tc.mode)
			}
			if hasUI, err := ctx.HasUI(); err != nil || !hasUI {
				t.Fatalf("hasUI=%v error=%v", hasUI, err)
			}
		})
	}
}

// Pi runner.ts:811-826 and 889-897 keep UI and mode getters live in event and command contexts.
func TestRunnerCapturedContextsFollowUIBinding(t *testing.T) {
	r := inproc.NewRunner(nil, t.TempDir())
	event := extension.FromContext(r.DispatchContext(t.Context()))
	command := r.CreateCommandContext().Context
	r.SetUIContext(&struct{ extension.UIContext }{extension.NoopUIContext})
	for _, ctx := range []*extension.Context{event, command} {
		if hasUI, err := ctx.HasUI(); err != nil || !hasUI {
			t.Errorf("captured hasUI=%v error=%v; want true", hasUI, err)
		}
		if ui, err := ctx.UI(); err != nil || ui != r.GetUIContext() {
			t.Errorf("captured UI=%T error=%v; want current wrapper", ui, err)
		}
		if mode, err := ctx.Mode(); err != nil || mode != extension.ModePrint {
			t.Errorf("omitted mode=%q error=%v; want print", mode, err)
		}
	}
	for _, mode := range []extension.ExtensionMode{extension.ModeRPC, extension.ModeTUI, extension.ModeJSON, extension.ModePrint} {
		r.SetUIContext(nil, mode)
		for _, ctx := range []*extension.Context{event, command} {
			if got, err := ctx.Mode(); err != nil || got != mode {
				t.Errorf("captured mode=%q error=%v; want %q", got, err, mode)
			}
			if hasUI, err := ctx.HasUI(); err != nil || hasUI {
				t.Errorf("cleared hasUI=%v error=%v", hasUI, err)
			}
			if ui, err := ctx.UI(); err != nil || ui != extension.NoopUIContext {
				t.Errorf("cleared UI=%T error=%v", ui, err)
			}
		}
	}
	// Even a previously obtained no-op object is a supplied UI and gets wrapped (runner.ts:523).
	r.SetUIContext(r.GetUIContext())
	if !r.HasUI() {
		t.Fatal("supplied no-op UI was not wrapped")
	}
	r.Invalidate("replaced")
	for _, ctx := range []*extension.Context{event, command} {
		if _, err := ctx.Mode(); err == nil {
			t.Error("stale mode did not fail")
		}
		if _, err := ctx.UI(); err == nil {
			t.Error("stale UI did not fail")
		}
		if _, err := ctx.HasUI(); err == nil {
			t.Error("stale hasUI did not fail")
		}
	}
}
