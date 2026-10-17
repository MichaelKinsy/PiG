package tui

import (
	"bytes"
	"strings"
	"testing"
)

// upstream: tui.ts:553 fullRedraws counts full repaints; tui-main-screen.ts mode = "regular", tui-alt-screen.ts mode = "fullscreen".
// mutation-checked: zeroing the results of TUI.Mode fails it
func TestTUIModeAndFullRedraws(t *testing.T) {
	ui := NewWithOutput(&bytes.Buffer{}, 20, 4)
	if ui.Mode() != TuiModeRegular || string(ui.Mode()) != "regular" {
		t.Fatalf("Mode = %q", ui.Mode())
	}
	ui.Add(renderFuncComponent(func(int) []string { return []string{"one"} }))
	if ui.FullRedraws() != 0 {
		t.Fatalf("FullRedraws before any render = %d", ui.FullRedraws())
	}
	ui.Render()
	if ui.FullRedraws() != 1 {
		t.Fatalf("the first render is a full render: FullRedraws = %d, want 1", ui.FullRedraws())
	}
	ui.Render()
	if ui.FullRedraws() != 1 {
		t.Fatalf("an unchanged render must not count: FullRedraws = %d", ui.FullRedraws())
	}
	ui.ForceFullRender()
	ui.Render()
	if ui.FullRedraws() != 2 {
		t.Fatalf("a forced render is a full redraw: FullRedraws = %d, want 2", ui.FullRedraws())
	}
}

// upstream: tui.ts:982 renderNow(force) renders immediately and force makes it a full repaint; tui.ts:990 requestRender(force=true) is a full repaint, as RepaintAll does.
func TestRenderNowAndRepaintAll(t *testing.T) {
	var out bytes.Buffer
	ui := NewWithOutput(&out, 20, 4)
	lines := []string{"one"}
	ui.Add(renderFuncComponent(func(int) []string { return lines }))
	ui.RenderNow()
	if ui.FullRedraws() != 1 {
		t.Fatalf("first RenderNow: FullRedraws = %d, want 1", ui.FullRedraws())
	}
	lines = []string{"one", "two"}
	ui.RenderNow()
	if ui.FullRedraws() != 1 || !strings.Contains(out.String(), "two") {
		t.Fatalf("an incremental RenderNow must paint without a full redraw (FullRedraws %d, output %q)", ui.FullRedraws(), out.String())
	}
	ui.RenderNow(true)
	if ui.FullRedraws() != 2 {
		t.Fatalf("RenderNow(true): FullRedraws = %d, want 2", ui.FullRedraws())
	}
	ui.RepaintAll()
	if ui.FullRedraws() != 3 {
		t.Fatalf("RepaintAll: FullRedraws = %d, want 3", ui.FullRedraws())
	}
	ui.Stop()
	before := ui.FullRedraws()
	ui.RenderNow(true)
	if ui.FullRedraws() != before {
		t.Fatal("a stopped TUI must not render")
	}
}
