package extensionconformance

import (
	"context"
	"slices"
	"testing"
	"time"
)

// Pi's `tui.invalidate()` (tui.ts:366) and `tui.requestRender(force)` (tui.ts:990), and D193's OSC 8 link claims, in every SDK and
// realization: isolated Go, Node, Rust and Python, packed Node, Go, Rust and Python, and fused Go. The host's recording UI keeps what reached
// it, so an SDK whose handle never makes a call, or whose claim never reaches the host, fails on the literal below and not only against
// another realization.
func TestConformance_TUIHandleAndLinkClaimsInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			tuiCmd, ok := findCommand(h.runner, "tui-probe")
			if !ok {
				t.Fatal("tui-probe command not registered")
			}
			if err := tuiCmd.Handler(ctx, ""); err != nil {
				t.Fatalf("tui-probe: %v", err)
			}
			waitFor(t, func() bool { return len(h.ui.TUICalls()) >= 3 })
			if want := []string{"invalidate", "requestRender:false", "requestRender:true"}; !slices.Equal(h.ui.TUICalls(), want) {
				t.Fatalf("the TUI handle's calls = %q, want %q", h.ui.TUICalls(), want)
			}

			*h.notify = nil
			linkCmd, ok := findCommand(h.runner, "link-probe")
			if !ok {
				t.Fatal("link-probe command not registered")
			}
			if err := linkCmd.Handler(ctx, ""); err != nil {
				t.Fatalf("link-probe: %v", err)
			}
			waitFor(t, func() bool { return len(*h.notify) >= 1 && h.ui.LinkSchemes() == "probe-link" })
			// A click on a scheme nobody claimed reaches no handler; a click on the claimed one reaches the extension's with its address.
			h.ui.ClickLink("unclaimed:1")
			h.ui.ClickLink("probe-link:42")
			waitFor(t, func() bool { return len(*h.notify) >= 2 })
			want := []string{`invalid_scheme: link scheme "https" is reserved: the host keeps opening it:error`, "click:probe-link:42:info"}
			if got := append([]string(nil), (*h.notify)...); !slices.Equal(got, want) {
				t.Fatalf("link probe notifications = %q, want %q", got, want)
			}
		})
	}
}
