package tui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type layoutUpstreamLines struct {
	lines   []string
	renders int
}

func (c *layoutUpstreamLines) Render(int) []string { c.renders++; return c.lines }
func (*layoutUpstreamLines) Invalidate()           {}
func layoutPlain(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = widthx.JSTrimEnd(widthx.StripTerminalSequences(line))
	}
	return out
}
func layoutWant(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("lines=%q, want %q", got, want)
	}
}
func layoutFrame(root Component, width, height int) LayoutFrame {
	return RenderLayoutFrame(root, width, height, noRender)
}
func layoutSV(t *testing.T, c Component, options ScrollViewOptions) *ScrollView {
	t.Helper()
	s := NewScrollView(c, options)
	t.Cleanup(s.Dispose)
	return s
}

func TestUpstreamViewportLayout(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:16
	t.Run("allocates vertical grow space deterministically", func(t *testing.T) {
		root := NewVStack([]StackEntry{{Component: NewText("top"), StackEntryOptions: StackEntryOptions{Basis: new(1), Shrink: new(0)}}, {Component: NewText("body"), StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1)}}}, StackOptions{})
		frame := layoutFrame(root, 10, 4)
		if frame.Root.Children[0].Rect.Height != 1 || frame.Root.Children[1].Rect.Height != 3 {
			t.Fatal("wrong grow heights")
		}
		layoutWant(t, layoutPlain(frame.Lines), []string{"top", "body", "", ""})
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:34
	t.Run("does not render fixed-basis scroll content during stack measurement", func(t *testing.T) {
		c := &layoutUpstreamLines{lines: []string{"one", "two", "three"}}
		sv := layoutSV(t, c, ScrollViewOptions{})
		root := NewVStack([]StackEntry{{Component: sv, StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1)}}, {Component: NewText("dock")}}, StackOptions{})
		layoutFrame(root, 10, 3)
		if c.renders != 1 {
			t.Fatalf("render count=%d, want 1", c.renders)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:51
	t.Run("paints only clipped rows from very large scroll content", func(t *testing.T) {
		const count = 1000000000
		lines := sparseLayoutLines(t, count)
		lines[count-4] = "before"
		lines[count-3] = "visible 1"
		lines[count-2] = "visible 2"
		lines[count-1] = "visible 3"
		sv := layoutSV(t, &layoutUpstreamLines{lines: lines}, ScrollViewOptions{Follow: "end"})
		frame := layoutFrame(sv, 10, 3)
		layoutWant(t, layoutPlain(frame.Lines), []string{"visible 1", "visible 2", "visible 3"})
		observation := struct {
			Lines         []string `json:"lines"`
			ScrollTop     int      `json:"scrollTop"`
			ContentHeight int      `json:"contentHeight"`
		}{frame.Lines, sv.ScrollTop(), frame.Root.Children[0].Rect.Height}
		encoded, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("LAYOUT_SPARSE_OBSERVATION:%s", encoded)
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:71
	t.Run("shrinks entries to their minimum sizes", func(t *testing.T) {
		root := NewVStack([]StackEntry{{Component: NewText("a1\na2\na3"), StackEntryOptions: StackEntryOptions{Shrink: new(1), MinSize: new(1)}}, {Component: NewText("b1\nb2\nb3"), StackEntryOptions: StackEntryOptions{Shrink: new(0)}}}, StackOptions{})
		frame := layoutFrame(root, 10, 4)
		if frame.Root.Children[0].Rect.Height != 1 || frame.Root.Children[1].Rect.Height != 3 {
			t.Fatal("wrong shrink heights")
		}
		layoutWant(t, layoutPlain(frame.Lines), []string{"a1", "b1", "b2", "b3"})
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:89
	t.Run("includes nested minimum sizes in intrinsic stack measurement", func(t *testing.T) {
		dock := NewVStack([]StackEntry{{Component: NewText("top1\ntop2\ntop3")}, {Component: NewText("selector"), StackEntryOptions: StackEntryOptions{MinSize: new(3)}}, {Component: NewText("below")}, {Component: NewText("footer"), StackEntryOptions: StackEntryOptions{MinSize: new(1)}}}, StackOptions{})
		root := NewVStack([]StackEntry{{Component: NewText("body"), StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1), MinSize: new(1)}}, {Component: dock, StackEntryOptions: StackEntryOptions{MinSize: new(1)}}}, StackOptions{})
		layoutWant(t, layoutPlain(layoutFrame(root, 10, 9).Lines), []string{"body", "top1", "top2", "top3", "selector", "", "", "below", "footer"})
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:119
	t.Run("omits gaps around invisible entries", func(t *testing.T) {
		stack := NewVStack([]StackEntry{{Component: NewText("one")}, {Component: NewText("hidden"), StackEntryOptions: StackEntryOptions{Visible: func(LayoutViewport) bool { return false }}}, {Component: NewText("two")}}, StackOptions{Gap: new(1)})
		layoutWant(t, layoutPlain(stack.Render(10)), []string{"one", "", "two"})
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:130
	t.Run("crops Kitty images at a scroll view's lower boundary", func(t *testing.T) {
		image := EncodeKitty("AAAA", 2, 3, 124, false)
		RegisterKittyImageMetadata(KittyImageMetadata{ImageID: 124, Columns: 2, Rows: 3, WidthPx: 100, HeightPx: 100})
		sv := layoutSV(t, &layoutUpstreamLines{lines: []string{"one", "two", image, "", ""}}, ScrollViewOptions{})
		root := NewVStack([]StackEntry{{Component: sv, StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1)}}, {Component: NewText("dock")}}, StackOptions{})
		frame := layoutFrame(root, 20, 4)
		if !strings.Contains(frame.Lines[2], "y=0,h=34,r=1") {
			t.Fatalf("image not cropped: %q", frame.Lines[2])
		}
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:148
	t.Run("composes horizontal children at allocated widths", func(t *testing.T) {
		root := NewHStack([]StackEntry{{Component: NewText("left"), StackEntryOptions: StackEntryOptions{Basis: new(6), Shrink: new(0)}}, {Component: NewText("right"), StackEntryOptions: StackEntryOptions{Basis: new(6), Shrink: new(0)}}}, StackOptions{})
		layoutWant(t, layoutPlain(layoutFrame(root, 12, 1).Lines), []string{"left  right"})
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:161
	t.Run("does not paint zero-width horizontal children", func(t *testing.T) {
		root := NewHStack([]StackEntry{{Component: NewText("hidden"), StackEntryOptions: StackEntryOptions{Basis: new(0), Shrink: new(0)}}, {Component: NewText("shown"), StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1)}}}, StackOptions{})
		layoutWant(t, layoutPlain(layoutFrame(root, 5, 1).Lines), []string{"shown"})
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:174
	t.Run("tracks follow-end state and returns unused scroll delta", func(t *testing.T) {
		sv := layoutSV(t, NewText("1\n2\n3\n4\n5\n6"), ScrollViewOptions{Follow: "end", Primary: true})
		layoutFrame(sv, 10, 3)
		if sv.ScrollTop() != 3 || !sv.IsFollowingEnd() {
			t.Fatal("not following end")
		}
		if sv.ScrollBy(-2) != 0 || sv.ScrollTop() != 1 || sv.IsFollowingEnd() {
			t.Fatal("wrong upward scroll")
		}
		if sv.ScrollBy(-3) != -2 || sv.ScrollTop() != 0 {
			t.Fatal("wrong negative remainder")
		}
		if sv.ScrollBy(10) != 7 || sv.ScrollTop() != 3 || !sv.IsFollowingEnd() {
			t.Fatal("wrong positive remainder")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:193
	t.Run("renders a proportional glyph scrollbar with an expanded active thumb", func(t *testing.T) { synctest.Test(t, testUpstreamProportionalScrollbar) })
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:306
	t.Run("preserves only the underlying background beneath overlay scrollbar glyphs", func(t *testing.T) {
		const bg = "\x1b[42m"
		const fg = "\x1b[31m"
		content := &widthRenderComponent{render: func(width int) []string {
			rows := make([]string, 8)
			for i := range rows {
				rows[i] = bg + strings.Repeat("x", width-1) + fg + "│\x1b[39m\x1b[49m"
			}
			return rows
		}}
		identity := func(s string) string { return s }
		sv := layoutSV(t, content, ScrollViewOptions{Scrollbar: "auto", ScrollbarTrackStyle: identity, ScrollbarThumbStyle: identity})
		layoutFrame(sv, 6, 4)
		sv.ScrollBy(1)
		frame := layoutFrame(sv, 6, 4)
		layoutWant(t, visibleFrameLines(frame.Lines), []string{"xxxxx│", "xxxxx┃", "xxxxx┃", "xxxxx│"})
		for _, line := range frame.Lines {
			if !strings.Contains(line, bg) || strings.Contains(line, fg) || !strings.Contains(line, "\x1b[0m\x1b]8;;\x07"+bg) {
				t.Fatalf("wrong scrollbar style: %q", line)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:334
	t.Run("updates reserved scrollbar layout at runtime", func(t *testing.T) {
		sv := layoutSV(t, NewText("123456"), ScrollViewOptions{Scrollbar: "always"})
		render := func() LayoutFrame {
			return layoutFrame(NewHStack([]StackEntry{{Component: sv}}, StackOptions{Align: "start"}), 6, 2)
		}
		frame := render()
		layoutWant(t, layoutPlain(frame.Lines), []string{"12345┃", "6    ┃"})
		if frame.Root.Children[0].Rect.Width != 6 || frame.Root.Children[0].Children[0].Rect.Width != 5 {
			t.Fatal("reserved width incorrect")
		}
		sv.SetScrollbar("hidden")
		if render().Root.Children[0].Children[0].Rect.Width != 6 || sv.IsScrollbarVisible() {
			t.Fatal("hidden scrollbar retained its column")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:347
	t.Run("measures nested scroll content from constrained child geometry", func(t *testing.T) {
		inner := layoutSV(t, NewText("1\n2\n3\n4\n5\n6"), ScrollViewOptions{})
		outer := layoutSV(t, NewVStack([]StackEntry{{Component: inner, StackEntryOptions: StackEntryOptions{Basis: new(2)}}, {Component: NewText("tail")}}, StackOptions{}), ScrollViewOptions{})
		layoutFrame(outer, 10, 2)
		if inner.ViewportHeight() != 2 || outer.ScrollBy(10) != 9 || outer.ScrollTop() != 1 {
			t.Fatal("nested scroll geometry mismatch")
		}
	})
	// .upstream/v0.87.1/packages/tui/test/layout.test.ts:357
	t.Run("rebuilds geometry after content changes", func(t *testing.T) {
		text := NewText("one")
		root := NewVStack([]StackEntry{{Component: text}}, StackOptions{})
		first := layoutFrame(root, 10, 4)
		text.SetText("one\ntwo\nthree")
		second := layoutFrame(root, 10, 4)
		if len(first.Root.Children[0].Lines) != 1 || len(second.Root.Children[0].Lines) != 3 {
			t.Fatal("geometry retained old content")
		}
	})
}

func testUpstreamProportionalScrollbar(t *testing.T) {
	t.Helper()
	source := []string{"abcd界", "abcde2", "abcde3", "abcde4", "abcde5", "abcde6", "abcde7", "abcde8"}
	const bg = "\x1b[42m"
	const track = "\x1b[38;5;2m"
	const thumb = "\x1b[38;5;1m"
	trackStyle := func(s string) string { return track + s + "\x1b[39m" }
	thumbStyle := func(s string) string { return thumb + s + "\x1b[39m" }
	content := NewPaddedText(strings.Join(source, "\n"), 0, 0, func(s string) string { return bg + s + "\x1b[49m" })
	sv := layoutSV(t, content, ScrollViewOptions{Scrollbar: "auto", ScrollbarTrackStyle: trackStyle, ScrollbarThumbStyle: thumbStyle, ScrollbarHideDelayMs: new(10)})
	render := func() []string { return layoutFrame(sv, 6, 4).Lines }
	layoutWant(t, visibleFrameLines(render()), source[:4])
	sv.ScrollBy(2)
	lines := render()
	layoutWant(t, visibleFrameLines(lines), []string{"abcde│", "abcde┃", "abcde┃", "abcde│"})
	if !slices.Equal(linesContaining(lines, track), []bool{true, false, false, true}) || !slices.Equal(linesContaining(lines, thumb), []bool{false, true, true, false}) {
		t.Fatal("wrong proportional thumb colors")
	}
	sv.SetScrollbarActive(true)
	lines = render()
	layoutWant(t, visibleFrameLines(lines), []string{"abcde│", "abcde█", "abcde█", "abcde│"})
	if !slices.Equal(linesContaining(lines, thumb), []bool{false, true, true, false}) {
		t.Fatal("wrong active thumb colors")
	}
	if strings.LastIndex(lines[1], bg) >= strings.LastIndex(lines[1], thumb) {
		t.Fatal("thumb painted before background")
	}
	sv.SetScrollbarActive(false)
	time.Sleep(30 * time.Millisecond)
	layoutWant(t, visibleFrameLines(render()), source[2:6])
	sv.ScrollToEnd()
	layoutWant(t, visibleFrameLines(render()), []string{"abcde│", "abcde│", "abcde┃", "abcde┃"})
	sv.ScrollToStart()
	if line := visibleFrameLines(render())[0]; line != "abcd ┃" {
		t.Fatalf("wide boundary=%q", line)
	}
	followedText := NewText(strings.Join(source, "\n"))
	followed := layoutSV(t, followedText, ScrollViewOptions{Follow: "end", Scrollbar: "auto", ScrollbarTrackStyle: trackStyle, ScrollbarThumbStyle: thumbStyle})
	layoutFrame(followed, 6, 4)
	if followed.ScrollTop() != 4 {
		t.Fatal("wrong followed top")
	}
	followedText.SetText(strings.Join(source, "\n") + "\nabcde9")
	growth := layoutFrame(followed, 6, 4)
	if followed.ScrollTop() != 5 || anyScrollbarGlyph(growth.Lines) {
		t.Fatal("growth revealed scrollbar or lost follow")
	}
	fitting := NewText("1\n2")
	automatic := layoutSV(t, fitting, ScrollViewOptions{Scrollbar: "auto", ScrollbarThumbStyle: thumbStyle})
	layoutFrame(automatic, 6, 4)
	automatic.ScrollBy(1)
	if anyScrollbarGlyph(layoutFrame(automatic, 6, 4).Lines) {
		t.Fatal("fitting automatic scrollbar visible")
	}
	always := layoutSV(t, fitting, ScrollViewOptions{Scrollbar: "always", ScrollbarThumbStyle: thumbStyle})
	frame := layoutFrame(always, 6, 4)
	if frame.Root.Children[0].Rect.Width != 5 || countSuffix(frame.Lines, "┃") != 4 {
		t.Fatal("fitting always scrollbar mismatch")
	}
	overflow := layoutSV(t, content, ScrollViewOptions{Scrollbar: "always", ScrollbarTrackStyle: trackStyle, ScrollbarThumbStyle: thumbStyle})
	frame = layoutFrame(overflow, 6, 4)
	if frame.Root.Children[0].Rect.Width != 5 || countSuffix(frame.Lines, "┃") != 2 || countSuffix(frame.Lines, "│") != 2 {
		t.Fatal("overflowing always scrollbar mismatch")
	}
	for _, line := range frame.Lines {
		style := max(strings.LastIndex(line, track), strings.LastIndex(line, thumb))
		reset := -1
		if style >= 0 {
			reset = strings.LastIndex(line[:style], "\x1b[0m\x1b]8;;\x07")
		}
		if reset <= strings.LastIndex(line, bg) {
			t.Fatalf("reserved column did not reset background: %q", line)
		}
	}
	for _, sample := range []struct{ height, thumb int }{{21, 19}, {40, 10}, {100, 4}, {400, 2}} {
		sized := layoutSV(t, NewText(strings.TrimSuffix(strings.Repeat("x\n", sample.height), "\n")), ScrollViewOptions{Scrollbar: "auto", ScrollbarThumbStyle: thumbStyle})
		layoutFrame(sized, 6, 20)
		sized.ScrollBy(1)
		if got := countSuffix(layoutFrame(sized, 6, 20).Lines, "┃"); got != sample.thumb {
			t.Fatalf("height %d thumb=%d, want %d", sample.height, got, sample.thumb)
		}
	}
}
