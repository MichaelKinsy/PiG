package extensionconformance

import (
	"testing"
)

// wantTuiAltProbe is what Pi's own TuiAltScreen reports for tui-alt-screen-probe (the Node fixture runs the real class): a recording 20x6
// terminal, a 12-row document, scroll, flash, overlay, focus, input listener, debug key, color scheme and layout root steps. Every SDK
// reaches the host's port of the class and must report the same values (D204).
const wantTuiAltProbe = "tui-alt: mode=fullscreen; children=1; terminal=true; viewport=6/true; first=row 7 w=20; last=row 12 w=20; redraws=true; entered=true; up=4/false; top=0/false; topfirst=row 1 w=20; bottom=6/true; flash=true; overlay=true/true/true/8; hidden=true; afterHide=false/false; focus=true/true; inputs=k,c; debug=1; flags=true/true; copy=false/false/false; schemes=dark,light; colors={}; mouse=null; render=row 1 w=10|row 2 w=10; root=true; removed=0; cleared=0; stopped=true:info"

func TestTuiAltScreenAcrossSDKs(t *testing.T) {
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
			cmd, ok := findCommand(h.runner, "tui-alt-screen-probe")
			if !ok {
				t.Fatal("tui-alt-screen-probe missing")
			}
			h.ui.ClearRecorded()
			if err := cmd.Handler(h.runner.DispatchContext(t.Context()), ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 })
			if got := h.ui.Recorded(); len(got) != 1 || got[0] != wantTuiAltProbe {
				t.Fatalf("record=%q\nwant   %q", got, wantTuiAltProbe)
			}
		})
	}
}
