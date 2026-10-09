package extensionconformance

import (
	"strings"
	"testing"
	"time"
)

// TestConformance_OverlayRenderWidth pins that every SDK renders a focused overlay component at the width Pi's TUI.resolveOverlayLayout resolves (tui.ts:1212-1233): min(80, available) by default and a share of the terminal for a percentage, not the terminal width. The Go, Python and Rust SDKs rendered at the terminal width, so a component laid out for the full terminal was cropped by its 80-column overlay. The terminal is 150 columns, a width no SDK falls back to, so each expected width can come only from the overlay layout.
func TestConformance_OverlayRenderWidth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.host.NotifyWidth(150)
			pollUntilConformance(t, 5*time.Second, "width never reached the extension", func() bool {
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "report_geometry")
				for _, n := range h.ui.Recorded() {
					if strings.HasPrefix(n, "geometry:150x") {
						return true
					}
				}
				return false
			})
			h.ui.ClearRecorded()
			runConformanceCommand(t, h, "overlay-width-probe")
			var got string
			pollUntilConformance(t, 10*time.Second, "overlay-width-probe never reported", func() bool {
				for _, n := range h.ui.Recorded() {
					if strings.HasPrefix(n, "overlay-width ") {
						got = n
						return true
					}
				}
				return false
			})
			if want := "overlay-width default=80 percent=75:info"; got != want {
				t.Errorf("%s: %q, want %q", tc.name, got, want)
			}
		})
	}
}
