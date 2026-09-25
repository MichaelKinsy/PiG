package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestWideFrameRepushAtNarrowerWidthFallsBackToFullRender models the pi-chain
// resize sequence:
//  1. wide frame at wide terminal; 2. terminal narrows but widget still wide
//     (width_change not yet applied) -> fullClear paints wide, which wraps at
//     the new width into MORE physical rows; 3. widget re-pushes narrow at
//     the now-fixed width -> differential path must not leave the wrapped
//     wide remnants above.
//
// The differential path rewrites with \x1b[2K per LOGICAL row; a prev row
// whose visible width exceeds the terminal width physically wrapped to several
// screen rows, so per-logical clear under-clears. It must fall back to a full
// clear. This pins the decision rather than the terminal wrap itself.
func TestWideFrameRepushAtNarrowerWidthFallsBackToFullRender(t *testing.T) {
	var wide bool
	comp := renderFuncComponent(func(int) []string {
		if wide {
			return []string{
				"W0 abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz",
				"W1 1234567890",
			}
		}
		return []string{
			"N0 short",
			"N1 done",
		}
	})

	var out bytes.Buffer
	ui := NewWithOutput(&out, 120, 20)
	ui.Add(comp)

	wide = true
	ui.Render() // phase1 wide at 120
	out.Reset()

	// phase2: width drops to 20, widget still returns WIDE frame. The wide row
	// W0 (visible width >120) wraps at width 20 but prevLines holds it as ONE
	// logical row.
	ui.width = 20
	ui.Render()
	out.Reset()

	if widthx.VisibleWidth("W0 abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz abcdefghijklmnopqrstuvwxyz") <= 20 {
		t.Fatal("test setup: W0 must be wider than 20 to model terminal wrapping")
	}

	// phase3: widget re-pushes NARROW at the same width 20.
	wide = false
	ui.Render()
	got := out.String()

	// Phase 2 was a resize full render, which emits W0 unchanged as Pi does,
	// so the terminal wrapped it. Rewriting per logical row would under-clear.
	if !strings.Contains(got, "\x1b[2J\x1b[H\x1b[3J") {
		t.Errorf("phase3 did not fall back to a full clear after a wrapped row; output:\n%q", got)
	}
	for _, want := range []string{"N0 short", "N1 done"} {
		if !strings.Contains(got, want) {
			t.Errorf("phase3 lost %q; the narrow frame must still render:\n%q", want, got)
		}
	}
}

// TestOverWideRowStayingOverWideTerminates guards the other side of the D53
// fallback: a row over-wide in both frames does not take D53's full clear. It
// reaches the differential loop, where upstream tui-main-screen.ts:517-545
// writes the crash log, stops, and throws.
func TestOverWideRowStayingOverWideTerminates(t *testing.T) {
	wide := strings.Repeat("x", 140)
	comp := renderFuncComponent(func(int) []string {
		return []string{"header", wide, "footer"}
	})

	var out bytes.Buffer
	ui := NewWithOutput(&out, 60, 20)
	ui.SetLogDirectory(t.TempDir())
	ui.Add(comp)
	ui.Render()

	wide = strings.Repeat("y", 140)
	out.Reset()
	value := renderRecover(ui)
	if _, ok := value.(*RenderOverflowError); !ok {
		t.Fatalf("over-wide differential row recovered %v, want *RenderOverflowError", value)
	}
	if got := out.String(); strings.Contains(got, "\x1b[2J") {
		t.Errorf("a row that stays over-wide forced a D53 full clear:\n%q", got)
	}
}
