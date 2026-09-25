package tui

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestRenderNormalStyledDifferentialMatchesPinnedPi(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 10)
	lines := []string{"fits", "ok"}
	ui.Add(renderFuncComponent(func(int) []string { return lines }))
	ui.Render()
	out.Reset()
	lines = []string{"fits", "\x1b[31m界é👩‍💻\x1b[0m"}
	ui.Render()
	// Exact bytes from pinned TuiMainScreen.doRender. First/full frames have
	// pre-existing extra CSI 2K in PiG; this asserts the differential path only.
	want := "\x1b[?2026h\r\x1b[2K\x1b[31m界é👩‍💻\x1b[0m" + widthx.SegmentReset + "\x1b[?2026l\x1b[?25l"
	if got := out.String(); got != want {
		t.Fatalf("terminal bytes = %q, want %q", got, want)
	}
}
