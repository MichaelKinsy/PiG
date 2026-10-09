package tui

import (
	"bytes"
	"strings"
	"testing"
)

// Pi's setLayoutRoot(component) swaps the layout tree and requests a render (packages/tui/src/tui-alt-screen.ts:321); undefined
// falls back to the base document (tui-alt-screen.ts:329: layoutRoot?.render(width) ?? super.render(width)). The test drives it through the
// ViewportTUI interface that isViewportTUI narrows to.
// Pi source: packages/tui/src/tui-alt-screen.ts, packages/tui/src/wheel-scroll.ts
// mutation-checked: zeroing the results of ViewportTUI.SetLayoutRoot fails it
// Pi: packages/tui/src/tui-alt-screen.ts:321 (setLayoutRoot)
// packages/tui/src/tui-alt-screen.ts:321 `setLayoutRoot(component)` replaces the base document; undefined restores it.
// packages/tui/src/tui-alt-screen.ts:321 (ViewportTUI.setLayoutRoot).
func TestViewportTUISetLayoutRootReplacesTheBaseDocumentAndNilRestoresIt(t *testing.T) {
	var out bytes.Buffer
	alt := newAltScreenForTest(&out, 30, 6, TuiAltScreenOptions{})
	alt.Add(NewText("base document"))
	var viewport ViewportTUI = alt
	alt.Start()
	frame := func() string { return strings.Join(alt.GetScreenLines(), "\n") }
	if !strings.Contains(frame(), "base document") {
		t.Fatalf("precondition: frame without a layout root = %q", frame())
	}

	viewport.SetLayoutRoot(&stubComponent{lines: []string{"layout root"}})
	alt.Render()
	if f := frame(); !strings.Contains(f, "layout root") || strings.Contains(f, "base document") {
		t.Fatalf("frame with a layout root = %q, want only the layout root", f)
	}

	viewport.SetLayoutRoot(nil)
	alt.Render()
	if f := frame(); !strings.Contains(f, "base document") || strings.Contains(f, "layout root") {
		t.Fatalf("frame after SetLayoutRoot(nil) = %q, want the base document", f)
	}
}

// Pi's ViewportTUI inherits fullRedraws and renderNow(force) from TUI (tui.ts:458,472,553,982): an unchanged frame is not
// a full redraw, and renderNow(true) repaints in full at once.
func TestViewportTUIFullRedrawsAndRenderNowThroughTheInterface(t *testing.T) {
	var out bytes.Buffer
	alt := newAltScreenForTest(&out, 30, 6, TuiAltScreenOptions{})
	alt.Add(NewText("document"))
	var viewport ViewportTUI = alt
	alt.Start()
	viewport.RenderNow()
	base := viewport.FullRedraws()
	if base != 1 {
		t.Fatalf("the first frame is one full redraw (tui-alt-screen.ts:1725): FullRedraws = %d, want 1", base)
	}
	viewport.RenderNow()
	if got := viewport.FullRedraws(); got != base {
		t.Fatalf("an unchanged RenderNow counted a full redraw: %d, want %d", got, base)
	}
	viewport.RenderNow(true)
	if got := viewport.FullRedraws(); got != base+1 {
		t.Fatalf("RenderNow(true) FullRedraws = %d, want %d", got, base+1)
	}
	viewport.RenderNow(false)
	if got := viewport.FullRedraws(); got != base+1 {
		t.Fatalf("RenderNow(false) counted a full redraw: %d, want %d", got, base+1)
	}
	alt.SetFixedSize(40, 8)
	viewport.RenderNow()
	if got := viewport.FullRedraws(); got != base+2 {
		t.Fatalf("a resize repaints in full: FullRedraws = %d, want %d", got, base+2)
	}
}
