package subprocess

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func renderRecovered(ui *tui.TUI) (value any) {
	defer func() { value = recover() }()
	ui.Render()
	return nil
}

// An extension frame computed at the old width that arrives after a narrowing
// resize is never painted; the resize requests a frame at the new width (the
// host's width_change broadcast), and the fresh frame is painted. Pi renders
// components synchronously at the current width, so neither binary paints a
// stale frame or terminates. A frame that is over-wide at the width it was
// rendered for still terminates, as a non-truncating component does in Pi.
func TestPushedWidgetFrameFromOldWidthIsNeverPaintedAfterResize(t *testing.T) {
	var out bytes.Buffer
	ui := tui.NewWithOutput(&out, 100, 20)
	ui.SetLogDirectory(t.TempDir())
	requested := make(chan int, 4)
	ui.SetOnWidthChange(func(width int) { requested <- width })
	proxy := NewPushProxy(func() {}, nil)
	ui.Add(proxy)
	oldRow := "old:" + strings.Repeat("o", 86) // 90 cells, rendered for 100

	proxy.UpdateLinesAt([]string{oldRow}, 100)
	if v := renderRecovered(ui); v != nil {
		t.Fatalf("initial render terminated: %v", v)
	}

	ui.SetFixedSize(60, 20)
	if v := renderRecovered(ui); v != nil {
		t.Fatalf("resize render terminated: %v", v)
	}
	// The width callback (the host's width_change broadcast, which makes the
	// extension re-render) runs on its own goroutine; receive its request.
	if got := <-requested; got != 60 {
		t.Fatalf("re-render requested at %d, want 60", got)
	}

	// The stale frame (rendered for 100 before the extension saw width_change)
	// arrives after the resize.
	out.Reset()
	proxy.UpdateLinesAt([]string{"old:" + strings.Repeat("p", 86)}, 100)
	if v := renderRecovered(ui); v != nil {
		t.Fatalf("stale frame terminated the renderer: %v", v)
	}
	if strings.Contains(out.String(), "old:") {
		t.Fatalf("stale frame painted: %q", out.String())
	}

	out.Reset()
	fresh := "new:" + strings.Repeat("n", 50)
	proxy.UpdateLinesAt([]string{fresh}, 60)
	if v := renderRecovered(ui); v != nil {
		t.Fatalf("fresh frame terminated: %v", v)
	}
	if !strings.Contains(out.String(), fresh) {
		t.Fatalf("fresh frame not painted: %q", out.String())
	}

	proxy.UpdateLinesAt([]string{"bad:" + strings.Repeat("b", 70)}, 60)
	if _, ok := renderRecovered(ui).(*tui.RenderOverflowError); !ok {
		t.Fatal("an over-wide frame at the current width did not terminate")
	}
}

// Render-proxy responses carry the width they were requested at; a cached
// response for another width is not painted with an over-wide row while the
// request at the current width is in flight.
func TestRenderProxyNeverServesStaleOverWideFrame(t *testing.T) {
	c := &renderProxyComponent{fallback: []string{"[x]"}}
	row := strings.Repeat("r", 35)
	c.lines, c.linesWidth, c.width = []string{row}, 40, 40
	if got := c.Render(40); !slices.Equal(got, []string{row}) {
		t.Fatalf("frame at its width = %q", got)
	}
	if got := c.Render(20); got != nil {
		t.Fatalf("stale frame served at 20: %q", got)
	}
	if got := c.Render(40); !slices.Equal(got, []string{row}) {
		t.Fatalf("frame after resizing back = %q", got)
	}
}

func TestPushProxyFrameWidth(t *testing.T) {
	p := NewPushProxy(nil, nil)
	row := strings.Repeat("w", 35)
	p.UpdateLinesAt([]string{row}, 40)
	if got := p.Render(20); got != nil {
		t.Fatalf("stale over-wide frame served: %q", got)
	}
	if got := p.Render(40); !slices.Equal(got, []string{row}) {
		t.Fatalf("frame at width 40 = %q", got)
	}
	p.UpdateLinesAt([]string{"fits"}, 40)
	if got := p.Render(60); got != nil {
		t.Fatalf("stale fitting frame served when wider: %q", got)
	}
	p.UpdateLinesAt([]string{row}, 0)
	if got := p.Render(20); !slices.Equal(got, []string{row}) {
		t.Fatalf("unknown-width frame = %q, want unchanged", got)
	}
}

// A stale frame is never painted even when its rows fit the new width.
func TestReviewStaleFittingWidgetFrameIsNotPainted(t *testing.T) {
	var out bytes.Buffer
	ui := tui.NewWithOutput(&out, 100, 20)
	ui.SetLogDirectory(t.TempDir())
	proxy := NewPushProxy(func() {}, nil)
	ui.Add(proxy)
	proxy.UpdateLinesAt([]string{"width=100"}, 100)
	ui.Render()
	ui.SetFixedSize(60, 20)
	out.Reset()
	ui.Render()
	if strings.Contains(out.String(), "width=100") {
		t.Fatalf("stale frame painted after resize: %q", out.String())
	}
	ui.SetFixedSize(120, 20)
	out.Reset()
	ui.Render()
	if strings.Contains(out.String(), "width=100") {
		t.Fatalf("stale frame painted after widening: %q", out.String())
	}
}
