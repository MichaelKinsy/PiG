package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type widthFrameComponent struct{ renderedAt []int }

func (c *widthFrameComponent) Render(width int) []string {
	c.renderedAt = append(c.renderedAt, width)
	lines := make([]string, 0, 29)
	for i := 0; i < max(10, width*10/32); i++ {
		lines = append(lines, strings.Repeat("#", width))
	}
	return append(lines, "HUD"+strings.Repeat("=", width-3))
}
func (c *widthFrameComponent) Invalidate() {}

// TestOverlaySpecDoomFrameComposesUnframedAndUncropped composes the
// doom-overlay option set: component rendered at 75% width, centred below a
// one-row top margin, every row and the HUD visible, and no modal border.
func TestOverlaySpecDoomFrameComposesUnframedAndUncropped(t *testing.T) {
	pct := func(v float64) *OverlayValue { return &OverlayValue{Value: v, Percent: true} }
	opts := OverlaySpec{Width: pct(75), MaxHeight: pct(95), Anchor: "center", Margin: &OverlayMarginSpec{Top: 1}}.Options()
	ui := NewWithOutput(io.Discard, 120, 40)
	comp := &widthFrameComponent{}
	if ui.OpenOverlay(comp, opts) == nil {
		t.Fatal("overlay not mounted")
	}
	rows := ui.composeOverlayLines(nil, 120, 40)
	if len(comp.renderedAt) == 0 || comp.renderedAt[len(comp.renderedAt)-1] != 90 {
		t.Fatalf("component rendered at %v, want 90", comp.renderedAt)
	}
	for i := 6; i < 6+28; i++ {
		if got := widthx.StripAnsi(rows[i]); got != strings.Repeat(" ", 15)+strings.Repeat("#", 90) && strings.TrimRight(got, " ") != strings.Repeat(" ", 15)+strings.Repeat("#", 90) {
			t.Fatalf("row %d = %q, want full 90-col frame at col 15", i, got)
		}
	}
	if got := strings.TrimRight(widthx.StripAnsi(rows[34]), " "); got != strings.Repeat(" ", 15)+"HUD"+strings.Repeat("=", 87) {
		t.Fatalf("HUD row = %q", got)
	}
	for _, r := range rows {
		if strings.ContainsAny(r, "│┌└") {
			t.Fatalf("unexpected modal border: %q", r)
		}
	}
}
