package tui

// pi: packages/coding-agent/src/modes/interactive/components/bordered-loader.ts

import (
	"strings"
	"testing"
)

// Pi bordered-loader.ts:8-42: BorderedLoader extends Container. Its children
// are DynamicBorder, loader, [Spacer, Text hint,] Spacer, DynamicBorder.
func TestBorderedLoaderIsAContainerWithUpstreamChildren(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cancellable bool
		children    int
	}{{"cancellable", true, 6}, {"non-cancellable", false, 4}} {
		t.Run(tc.name, func(t *testing.T) {
			bl := NewBorderedLoader(nil, ActiveTheme(), "working", BorderedLoaderOptions{Cancellable: new(tc.cancellable)})
			children := bl.Children()
			if len(children) != tc.children {
				t.Fatalf("children = %d, want %d", len(children), tc.children)
			}
			if _, ok := children[0].(*DynamicBorder); !ok {
				t.Fatalf("first child = %T, want *DynamicBorder", children[0])
			}
			if _, ok := children[len(children)-1].(*DynamicBorder); !ok {
				t.Fatalf("last child = %T, want *DynamicBorder", children[len(children)-1])
			}
			before := len(bl.Render(40))
			extra := NewText("extra")
			bl.AddChild(extra)
			if got := len(bl.Render(40)); got != before+1 {
				t.Fatalf("addChild did not render the new child: %d -> %d", before, got)
			}
			bl.RemoveChild(extra)
			if got := len(bl.Render(40)); got != before {
				t.Fatalf("removeChild did not drop the child: %d -> %d", before, got)
			}
			bl.Clear()
			if got := bl.Render(40); len(got) != 0 || len(bl.Children()) != 0 {
				t.Fatalf("clear left %d children and %d lines", len(bl.Children()), len(got))
			}
		})
	}
}

// Pi Container.handleMouse (tui.ts:372-392) routes an event to the child under
// the pointer and offsets y by the heights of the children above it.
func TestBorderedLoaderHandleMouseRoutesToChildUnderPointer(t *testing.T) {
	bl := NewBorderedLoader(nil, ActiveTheme(), "working", BorderedLoaderOptions{Cancellable: new(false)})
	probe := &mouseProbe{lines: []string{"probe"}}
	bl.Clear()
	bl.AddChild(NewText("above"))
	bl.AddChild(probe)
	lines := bl.Render(20)
	bl.HandleMouse(TuiMouseEvent{Type: MousePress, Width: 20, Height: len(lines), Y: 1})
	if len(probe.events) != 1 || probe.events[0].Y != 0 {
		t.Fatalf("probe events = %+v, want one event at y=0", probe.events)
	}
}

// Pi bordered-loader.ts:45-49: onAbort is forwarded to the cancellable loader
// and ignored when the loader is not cancellable.
func TestBorderedLoaderOnAbort(t *testing.T) {
	bl := NewBorderedLoader(nil, ActiveTheme(), "working")
	aborted := 0
	bl.OnAbort = func() { aborted++ }
	bl.HandleInput("\x1b")
	if aborted != 1 || bl.CancellableContext().Context().Err() == nil {
		t.Fatalf("aborted=%d ctxErr=%v", aborted, bl.CancellableContext().Context().Err())
	}
	non := NewBorderedLoader(nil, ActiveTheme(), "working", BorderedLoaderOptions{Cancellable: new(false)})
	non.OnAbort = func() { t.Fatal("onAbort must not fire for a non-cancellable loader") }
	non.HandleInput("\x1b")
}

// bordered-loader.ts constructor(tui, theme, message, options): the border takes theme.fg("border") from the given theme, not the
// active one; options.cancellable defaults to true; and the loader reports to tui.
func TestBorderedLoaderConstructorArguments(t *testing.T) {
	registry := ActiveThemeRegistry()
	var themes []*Theme
	for _, name := range registry.Names() {
		th := registry.Get(name)
		if len(themes) == 0 || th.Fg("border", "x") != themes[0].Fg("border", "x") {
			themes = append(themes, th)
		}
		if len(themes) == 2 {
			break
		}
	}
	if len(themes) < 2 {
		t.Skip("needs two themes with different border colours")
	}
	// Give the loader the theme that is not active, so a loader reading the active theme renders another colour.
	given, active := themes[0], ActiveTheme()
	if given.Fg("border", "x") == active.Fg("border", "x") {
		given = themes[1]
	}
	rendered := NewBorderedLoader(nil, given, "work", BorderedLoaderOptions{}).Render(20)
	if want := given.Fg("border", strings.Repeat("─", 20)); rendered[0] != want {
		t.Fatalf("border row %q, want the given theme's %q", rendered[0], want)
	}
	if got := len(NewBorderedLoader(nil, themes[0], "work", BorderedLoaderOptions{}).Children()); got != 6 {
		t.Fatalf("default options gave %d children, want the 6 of a cancellable loader", got)
	}
	ui := &renderCountingTUI{}
	NewBorderedLoader(ui, themes[0], "work", BorderedLoaderOptions{})
	if ui.requests.Load() < 1 {
		t.Fatal("the loader did not report to the constructor's ui")
	}
}
