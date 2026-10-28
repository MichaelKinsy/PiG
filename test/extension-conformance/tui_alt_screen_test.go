package extensionconformance

import (
	"context"
	"strings"
	"testing"
	"time"
)

// wantTuiAltProbe is what Pi's own TuiAltScreen reports for tui-alt-screen-probe (the Node fixture runs the real class): a recording 20x6
// terminal, a 12-row document, scroll, flash, overlay, focus, input listener, debug key, color scheme and layout root steps. Every SDK
// reaches the host's port of the class and must report the same values.
const wantTuiAltProbe = "tui-alt: mode=fullscreen; children=1; terminal=true; viewport=6/true; first=row 7 w=20; last=row 12 w=20; redraws=true; entered=true; up=4/false; top=0/false; topfirst=row 1 w=20; bottom=6/true; flash=true; overlay=true/true/true/8; hidden=true; afterHide=false/false; focus=true/true; inputs=k,c; debug=1; flags=true/true; copy=false/false/false; schemes=dark,light; colors={}; mouse=null; render=row 1 w=10|row 2 w=10; root=true; removed=0; cleared=0; stopped=true:info"

// wantTuiAltFrameProbe is Pi's own result for tui-alt-frame-probe: a component's render reads the screen as it was when the frame began, an
// overlay's visible callback decides whether it shows, invalidate reaches the overlays' components, and a render that changes the screen (flags,
// children, focus, an overlay with a visible callback that reads the screen, scroll, invalidate) reads its own changes back at once.
const wantTuiAltFrameProbe = "tui-alt-frame: frame1=0/true/0/false/false/false; frame2=6/true/6/false/false/false; visibleOn=true/true/true; visibleOff=false/true; invalidate=true/true; mutate=flags=false/false/true,set=true/true/false,add=3,remove=2,focus=true,overlay=true/true/true/true,hidden=false,scroll=5/false,invalidate=true,lines=6; after=true/true/false/2:info"

// wantTuiAltChildrenProbe is Pi's own result for tui-alt-children-probe: children read after each addChild, removeChild and clear, with three
// components of equal content that stay distinct, a duplicate child and a component that is not a child (D207: the SDKs return a copy).
const wantTuiAltChildrenProbe = "tui-alt-children: empty=-; added=abc; removeB=ac; removeAbsent=ac; duplicate=aca; removeFirst=ca; readd=cab; render=same|same|same; focus=b; cleared=-:info"

// wantTuiAltReentryProbe is what the Go, Rust and Python SDKs report for tui-alt-reentry-probe: from inside a component's render the calls that would
// re-enter the frame fail with the host's error and the screen goes on (D208). Pi's own class recurses or re-enters there, so Node has no such row.
const wantTuiAltReentryProbe = "tui-alt-reentry: render=err; renderNow=err; start=err; stop=err; copy=err; colors=err; after=true:info"

// wantTuiAltGoIdentityProbe is what the Go SDK reports for tui-alt-go-identity-probe: Pi compares components by reference, so the Go SDK takes only
// pointers: a value (even one equal to another) is rejected by the call that receives it, and two pointers to equal content stay distinct.
const wantTuiAltGoIdentityProbe = "tui-alt-go-identity: add=rejected; remove=rejected; focus=rejected; root=rejected; overlay=rejected; unfocus=rejected; children=0; distinct=a/1:info"

func TestTuiAltScreenAcrossSDKs(t *testing.T) {
	runTuiAltProbe(t, "tui-alt-screen-probe", wantTuiAltProbe, allTuiAltCells)
}

// TestTuiAltScreenFrameReadsAcrossSDKs fails on the deadlock a component's render that reads or changes its screen would cause, within a bounded time,
// and on any read that does not see the change a call made earlier in the same render.
func TestTuiAltScreenFrameReadsAcrossSDKs(t *testing.T) {
	runTuiAltProbe(t, "tui-alt-frame-probe", wantTuiAltFrameProbe, allTuiAltCells)
}

func TestTuiAltScreenChildrenAcrossSDKs(t *testing.T) {
	runTuiAltProbe(t, "tui-alt-children-probe", wantTuiAltChildrenProbe, allTuiAltCells)
}

// TestTuiAltScreenFrameReentryAcrossSDKs pins D208 in the SDKs that proxy the host's screen.
func TestTuiAltScreenFrameReentryAcrossSDKs(t *testing.T) {
	runTuiAltProbe(t, "tui-alt-reentry-probe", wantTuiAltReentryProbe, func(name string) bool {
		return allTuiAltCells(name) && !strings.Contains(name, "node")
	})
}

// TestTuiAltScreenGoComponentIdentity pins the Go SDK's pointer rule.
func TestTuiAltScreenGoComponentIdentity(t *testing.T) {
	runTuiAltProbe(t, "tui-alt-go-identity-probe", wantTuiAltGoIdentityProbe, func(name string) bool {
		return allTuiAltCells(name) && strings.Contains(name, "go")
	})
}

// allTuiAltCells selects the cells that run the TuiAltScreen rows every SDK shares: all of them.
func allTuiAltCells(string) bool { return true }

func runTuiAltProbe(t *testing.T, command, want string, cell func(name string) bool) {
	if testing.Short() {
		t.Skip("skipping TuiAltScreen conformance in short mode (builds subprocess fixtures)")
	}
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !cell(tc.name) {
				t.Skip("this row is not one of this SDK's")
			}
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
