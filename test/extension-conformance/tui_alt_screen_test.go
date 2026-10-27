package extensionconformance

import (
	"context"
	"testing"
	"time"
)

// wantTuiAltProbe is what Pi's own TuiAltScreen reports for tui-alt-screen-probe (the Node fixture runs the real class): a recording 20x6
// terminal, a 12-row document, scroll, flash, overlay, focus, input listener, debug key, color scheme and layout root steps. Every SDK
// reaches the host's port of the class and must report the same values (D204).
const wantTuiAltProbe = "tui-alt: mode=fullscreen; children=1; terminal=true; viewport=6/true; first=row 7 w=20; last=row 12 w=20; redraws=true; entered=true; up=4/false; top=0/false; topfirst=row 1 w=20; bottom=6/true; flash=true; overlay=true/true/true/8; hidden=true; afterHide=false/false; focus=true/true; inputs=k,c; debug=1; flags=true/true; copy=false/false/false; schemes=dark,light; colors={}; mouse=null; render=row 1 w=10|row 2 w=10; root=true; removed=0; cleared=0; stopped=true:info"

// wantTuiAltFrameProbe is Pi's own result for tui-alt-frame-probe: a component's render reads the screen as it was when the frame began, an
// overlay's visible callback decides whether it shows, and invalidate reaches the overlays' components.
const wantTuiAltFrameProbe = "tui-alt-frame: frame1=0/true/0/false/false/false; frame2=6/true/6/false/false/false; visibleOn=true/true/true; visibleOff=false/true; invalidate=true/true:info"

func TestTuiAltScreenAcrossSDKs(t *testing.T) {
	runTuiAltProbe(t, "tui-alt-screen-probe", wantTuiAltProbe)
}

// TestTuiAltScreenFrameReadsAcrossSDKs fails on the deadlock a component's render that reads its screen would cause, within a bounded time.
func TestTuiAltScreenFrameReadsAcrossSDKs(t *testing.T) {
	runTuiAltProbe(t, "tui-alt-frame-probe", wantTuiAltFrameProbe)
}

func runTuiAltProbe(t *testing.T, command, want string) {
	if testing.Short() {
		t.Skip("skipping TuiAltScreen conformance in short mode (builds subprocess fixtures)")
	}
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
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
			cmd, ok := findCommand(h.runner, command)
			if !ok {
				t.Fatal(command + " missing")
			}
			h.ui.ClearRecorded()
			// A deadlock in the screen would hold the command forever; the bound turns it into a failure.
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			if err := cmd.Handler(h.runner.DispatchContext(ctx), ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 })
			if got := h.ui.Recorded(); len(got) != 1 || got[0] != want {
				t.Fatalf("record=%q\nwant   %q", got, want)
			}
		})
	}
}
