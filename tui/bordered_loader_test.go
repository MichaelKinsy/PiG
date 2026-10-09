package tui

// pi: packages/coding-agent/src/modes/interactive/components/bordered-loader.ts

import (
	"maps"
	"strings"
	"testing"
)

// TestBorderedLoader_Render_Structure verifies the bordered loader renders
// the expected structural elements: top border, loader line, optional
// cancel hint, spacer, and bottom border.
// Mirrors upstream bordered-loader.ts (68 LOC).
func TestBorderedLoader_Render_Structure(t *testing.T) {
	t.Run("cancellable", func(t *testing.T) {
		bl := NewBorderedLoader(nil, ActiveTheme(), "Loading data...")
		lines := bl.Render(80)

		// Must have: border + loader + blank + hint + blank + border = 6 lines minimum.
		if len(lines) < 6 {
			t.Fatalf("expected >= 6 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
		}

		// First line: dynamic border (all ─ chars).
		first := stripANSI(lines[0])
		if !strings.ContainsRune(first, '─') {
			t.Errorf("first line should be a border, got: %q", first)
		}

		// Last line: dynamic border.
		last := stripANSI(lines[len(lines)-1])
		if !strings.ContainsRune(last, '─') {
			t.Errorf("last line should be a border, got: %q", last)
		}

		// Should contain the message text.
		all := stripANSI(strings.Join(lines, "\n"))
		if !strings.Contains(all, "Loading data") {
			t.Errorf("output should contain loader message, got:\n%s", all)
		}

		// Should contain the upstream tui.select.cancel hint.
		if !strings.Contains(all, " escape/ctrl+c cancel") {
			t.Errorf("cancellable loader should show Esc cancel hint, got:\n%s", all)
		}
	})

	t.Run("non-cancellable", func(t *testing.T) {
		bl := NewBorderedLoader(nil, ActiveTheme(), "Processing...", BorderedLoaderOptions{Cancellable: new(false)})
		lines := bl.Render(80)

		// Must have: border + loader + blank + border = 4 lines minimum.
		if len(lines) < 4 {
			t.Fatalf("expected >= 4 lines, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
		}

		// First line: dynamic border.
		first := stripANSI(lines[0])
		if !strings.ContainsRune(first, '─') {
			t.Errorf("first line should be a border, got: %q", first)
		}

		// Last line: dynamic border.
		last := stripANSI(lines[len(lines)-1])
		if !strings.ContainsRune(last, '─') {
			t.Errorf("last line should be a border, got: %q", last)
		}

		// Should contain the message text.
		all := stripANSI(strings.Join(lines, "\n"))
		if !strings.Contains(all, "Processing") {
			t.Errorf("output should contain loader message, got:\n%s", all)
		}

		// Should NOT contain cancel hint.
		if strings.Contains(all, "cancel") {
			t.Errorf("non-cancellable loader should not show cancel hint, got:\n%s", all)
		}
	})
}

// TestBorderedLoader_NextFrame verifies the spinner advances.
func TestBorderedLoader_NextFrame(t *testing.T) {
	bl := NewBorderedLoader(nil, ActiveTheme(), "Thinking...")
	before := strings.Join(bl.Render(80), "\n")
	bl.NextFrame()
	after := strings.Join(bl.Render(80), "\n")
	// At least one line should change (the spinner frame).
	if before == after {
		t.Error("NextFrame should change the rendered output (spinner animation)")
	}
}

// TestBorderedLoaderIsAContainerOfItsParts: upstream's BorderedLoader extends
// Container and adds border, loader, [spacer, hint], spacer, border as children
// (bordered-loader.ts constructor); addChild/removeChild/clear/children are
// inherited.
func TestBorderedLoaderIsAContainerOfItsParts(t *testing.T) {
	for _, test := range []struct {
		cancellable bool
		children    int
	}{{true, 6}, {false, 4}} {
		bl := NewBorderedLoader(nil, ActiveTheme(), "msg", BorderedLoaderOptions{Cancellable: new(test.cancellable)})
		if got := len(bl.Children()); got != test.children {
			t.Fatalf("cancellable=%t children = %d, want %d", test.cancellable, got, test.children)
		}
		before := bl.Render(40)
		bl.Add(NewSpacer(2))
		if got := len(bl.Render(40)); got != len(before)+2 {
			t.Fatalf("a child added to the container renders: %d lines, want %d", got, len(before)+2)
		}
		bl.Clear()
		if len(bl.Children()) != 0 || len(bl.Render(40)) != 0 {
			t.Fatalf("Clear leaves children=%d", len(bl.Children()))
		}
	}
}

// packages/coding-agent/src/modes/interactive/components/bordered-loader.ts handleInput (:55-59): a cancellable loader forwards the input to its CancellableLoader, which aborts on
// tui.select.cancel and calls onAbort; a loader built with cancellable false ignores input.
func TestBorderedLoader_HandleInput(t *testing.T) {
	t.Run("cancellable aborts on the cancel key only", func(t *testing.T) {
		bl := NewBorderedLoader(nil, ActiveTheme(), "Working")
		aborts := 0
		bl.CancellableContext().OnAbort = func() { aborts++ }

		bl.HandleInput("x")
		if bl.CancellableContext().Aborted() || aborts != 0 {
			t.Fatalf("a key that is not tui.select.cancel aborted: aborted=%v aborts=%d", bl.CancellableContext().Aborted(), aborts)
		}
		bl.HandleInput("\x1b")
		if !bl.CancellableContext().Aborted() || aborts != 1 {
			t.Fatalf("escape did not abort once: aborted=%v aborts=%d", bl.CancellableContext().Aborted(), aborts)
		}
	})
	t.Run("not cancellable ignores input", func(t *testing.T) {
		bl := NewBorderedLoader(nil, ActiveTheme(), "Working", BorderedLoaderOptions{Cancellable: new(false)})
		aborts := 0
		bl.OnAbort = func() { aborts++ }
		for _, key := range []string{"\x1b", "\x03", "x"} {
			bl.HandleInput(key)
		}
		if bl.CancellableContext() != nil || aborts != 0 {
			t.Fatalf("a loader built with cancellable false reacted to input: cancellable=%v aborts=%d", bl.CancellableContext(), aborts)
		}
	})
}

// bordered-loader.ts:12-33 constructor(tui, theme, message, options?): the spinner and message colours come from the theme passed in (theme.fg("accent")), not a global one, and options.cancellable defaults to true.
func TestBorderedLoaderTakesItsColoursFromTheThemeItIsGiven(t *testing.T) {
	custom := *ActiveTheme()
	custom.fgAnsi = maps.Clone(custom.fgAnsi)
	custom.fgAnsi["accent"] = "\x1b[38;5;201m"
	loader := NewBorderedLoader(nil, &custom, "working")
	if got := strings.Join(loader.Render(40), "\n"); !strings.Contains(got, "\x1b[38;5;201m") {
		t.Fatalf("the accent of the theme passed in is not used: %q", got)
	}
	if !loader.isCancellable {
		t.Fatal("cancellable defaults to true")
	}
	if NewBorderedLoader(nil, nil, "x", BorderedLoaderOptions{Cancellable: new(false)}).isCancellable {
		t.Fatal("options.cancellable false")
	}
}
