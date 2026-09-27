package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func visibleFrameLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = widthx.StripTerminalSequences(line)
	}
	return out
}

func linesContaining(lines []string, needle string) []bool {
	out := make([]bool, len(lines))
	for i, line := range lines {
		out[i] = strings.Contains(line, needle)
	}
	return out
}

func countSuffix(lines []string, suffix string) int {
	count := 0
	for _, line := range visibleFrameLines(lines) {
		if strings.HasSuffix(line, suffix) {
			count++
		}
	}
	return count
}

func anyScrollbarGlyph(lines []string) bool {
	for _, line := range visibleFrameLines(lines) {
		if strings.ContainsAny(line, "│┃") {
			return true
		}
	}
	return false
}

// TestLayoutRendersProportionalGlyphScrollbar ports upstream layout.test.ts
// "renders a proportional glyph scrollbar with an expanded active thumb".
func TestLayoutRendersProportionalGlyphScrollbar(t *testing.T) {
	sourceLines := []string{"abcd界", "abcde2", "abcde3", "abcde4", "abcde5", "abcde6", "abcde7", "abcde8"}
	const contentBackground = "\x1b[42m"
	const trackColor = "\x1b[38;5;2m"
	const thumbColor = "\x1b[38;5;1m"
	trackStyle := func(text string) string { return trackColor + text + "\x1b[39m" }
	thumbStyle := func(text string) string { return thumbColor + text + "\x1b[39m" }
	content := NewPaddedText(strings.Join(sourceLines, "\n"), 0, 0, func(text string) string { return contentBackground + text + "\x1b[49m" })
	// Long enough that the render right after a scroll sees the scrollbar
	// even on a loaded machine; the hide below is awaited, not slept on.
	delay := 200
	scrollView := NewScrollView(content, ScrollViewOptions{
		Scrollbar: "auto", ScrollbarTrackStyle: trackStyle, ScrollbarThumbStyle: thumbStyle, ScrollbarHideDelayMs: &delay,
	})
	t.Cleanup(scrollView.Dispose)
	render := func() []string { return RenderLayoutFrame(scrollView, 6, 4, noRender).Lines }
	assertVisible := func(lines []string, want []string) {
		t.Helper()
		if got := visibleFrameLines(lines); !slices.Equal(got, want) {
			t.Fatalf("visible lines = %q, want %q", got, want)
		}
	}

	assertVisible(render(), sourceLines[:4])

	scrollView.ScrollBy(2)
	lines := render()
	assertVisible(lines, []string{"abcde│", "abcde┃", "abcde┃", "abcde│"})
	if got := linesContaining(lines, trackColor); !slices.Equal(got, []bool{true, false, false, true}) {
		t.Fatalf("track rows = %v", got)
	}
	if got := linesContaining(lines, thumbColor); !slices.Equal(got, []bool{false, true, true, false}) {
		t.Fatalf("thumb rows = %v", got)
	}

	scrollView.SetScrollbarActive(true)
	lines = render()
	assertVisible(lines, []string{"abcde│", "abcde█", "abcde█", "abcde│"})
	if got := linesContaining(lines, thumbColor); !slices.Equal(got, []bool{false, true, true, false}) {
		t.Fatalf("active thumb rows = %v", got)
	}
	if strings.LastIndex(lines[1], contentBackground) >= strings.LastIndex(lines[1], thumbColor) {
		t.Fatalf("thumb must paint after the content background: %q", lines[1])
	}

	scrollView.SetScrollbarActive(false)
	deadline := time.Now().Add(10 * time.Second)
	for !slices.Equal(visibleFrameLines(render()), sourceLines[2:6]) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	assertVisible(render(), sourceLines[2:6])

	scrollView.ScrollToEnd()
	assertVisible(render(), []string{"abcde│", "abcde│", "abcde┃", "abcde┃"})

	scrollView.ScrollToStart()
	if got := visibleFrameLines(render())[0]; got != "abcd ┃" {
		t.Fatalf("wide grapheme under the scrollbar column = %q, want %q", got, "abcd ┃")
	}

	followedContent := NewText(strings.Join(sourceLines, "\n"))
	followed := NewScrollView(followedContent, ScrollViewOptions{Follow: "end", Scrollbar: "auto", ScrollbarTrackStyle: trackStyle, ScrollbarThumbStyle: thumbStyle})
	t.Cleanup(followed.Dispose)
	RenderLayoutFrame(followed, 6, 4, noRender)
	if followed.ScrollTop() != 4 {
		t.Fatalf("followed scrollTop = %d, want 4", followed.ScrollTop())
	}
	followedContent.SetText(strings.Join(sourceLines, "\n") + "\nabcde9")
	growthFrame := RenderLayoutFrame(followed, 6, 4, noRender)
	if followed.ScrollTop() != 5 {
		t.Fatalf("followed scrollTop after growth = %d, want 5", followed.ScrollTop())
	}
	if anyScrollbarGlyph(growthFrame.Lines) {
		t.Fatalf("content growth while following must not reveal the scrollbar: %q", visibleFrameLines(growthFrame.Lines))
	}

	fittingContent := NewText("1\n2")
	automatic := NewScrollView(fittingContent, ScrollViewOptions{Scrollbar: "auto", ScrollbarThumbStyle: thumbStyle})
	t.Cleanup(automatic.Dispose)
	RenderLayoutFrame(automatic, 6, 4, noRender)
	automatic.ScrollBy(1)
	if lines := RenderLayoutFrame(automatic, 6, 4, noRender).Lines; anyScrollbarGlyph(lines) {
		t.Fatalf("fitting auto content must not show a scrollbar: %q", visibleFrameLines(lines))
	}

	alwaysFitting := NewScrollView(fittingContent, ScrollViewOptions{Scrollbar: "always", ScrollbarThumbStyle: thumbStyle})
	alwaysFittingFrame := RenderLayoutFrame(alwaysFitting, 6, 4, noRender)
	if w := alwaysFittingFrame.Root.Children[0].Rect.Width; w != 5 {
		t.Fatalf("always scrollbar content width = %d, want 5", w)
	}
	if got := countSuffix(alwaysFittingFrame.Lines, "┃"); got != 4 {
		t.Fatalf("fitting always scrollbar thumb rows = %d, want 4: %q", got, visibleFrameLines(alwaysFittingFrame.Lines))
	}

	alwaysOverflowing := NewScrollView(content, ScrollViewOptions{Scrollbar: "always", ScrollbarTrackStyle: trackStyle, ScrollbarThumbStyle: thumbStyle})
	alwaysOverflowingFrame := RenderLayoutFrame(alwaysOverflowing, 6, 4, noRender)
	if w := alwaysOverflowingFrame.Root.Children[0].Rect.Width; w != 5 {
		t.Fatalf("always overflowing content width = %d, want 5", w)
	}
	if thumbs, tracks := countSuffix(alwaysOverflowingFrame.Lines, "┃"), countSuffix(alwaysOverflowingFrame.Lines, "│"); thumbs != 2 || tracks != 2 {
		t.Fatalf("always overflowing thumb/track rows = %d/%d, want 2/2", thumbs, tracks)
	}
	for _, line := range alwaysOverflowingFrame.Lines {
		styleIndex := max(strings.LastIndex(line, trackColor), strings.LastIndex(line, thumbColor))
		resetIndex := strings.LastIndex(line[:max(0, styleIndex)], "\x1b[0m\x1b]8;;\x07")
		if resetIndex <= strings.LastIndex(line, contentBackground) {
			t.Fatalf("reserved scrollbar column must reset styles after the content background: %q", line)
		}
	}

	thumbHeightFor := func(contentHeight int) int {
		sized := NewScrollView(NewText(strings.TrimSuffix(strings.Repeat("x\n", contentHeight), "\n")), ScrollViewOptions{Scrollbar: "auto", ScrollbarThumbStyle: thumbStyle})
		defer sized.Dispose()
		RenderLayoutFrame(sized, 6, 20, noRender)
		sized.ScrollBy(1)
		return countSuffix(RenderLayoutFrame(sized, 6, 20, noRender).Lines, "┃")
	}
	for contentHeight, want := range map[int]int{21: 19, 40: 10, 400: 2} {
		if got := thumbHeightFor(contentHeight); got != want {
			t.Fatalf("thumb height for %d content rows = %d, want %d", contentHeight, got, want)
		}
	}
}

// TestLayoutScrollbarPreservesOnlyUnderlyingBackground ports upstream
// "preserves only the underlying background beneath overlay scrollbar glyphs".
func TestLayoutScrollbarPreservesOnlyUnderlyingBackground(t *testing.T) {
	const background = "\x1b[42m"
	const borderForeground = "\x1b[31m"
	content := &widthRenderComponent{render: func(width int) []string {
		lines := make([]string, 8)
		for i := range lines {
			lines[i] = background + strings.Repeat("x", width-1) + borderForeground + "│\x1b[39m\x1b[49m"
		}
		return lines
	}}
	identity := func(text string) string { return text }
	scrollView := NewScrollView(content, ScrollViewOptions{Scrollbar: "auto", ScrollbarTrackStyle: identity, ScrollbarThumbStyle: identity})
	t.Cleanup(scrollView.Dispose)
	RenderLayoutFrame(scrollView, 6, 4, noRender)
	scrollView.ScrollBy(1)
	frame := RenderLayoutFrame(scrollView, 6, 4, noRender)

	if got, want := visibleFrameLines(frame.Lines), []string{"xxxxx│", "xxxxx┃", "xxxxx┃", "xxxxx│"}; !slices.Equal(got, want) {
		t.Fatalf("visible lines = %q, want %q", got, want)
	}
	for _, line := range frame.Lines {
		if !strings.Contains(line, background) || strings.Contains(line, borderForeground) ||
			!strings.Contains(line, "\x1b[0m\x1b]8;;\x07"+background) {
			t.Fatalf("scrollbar cell must keep only the underlying background: %q", line)
		}
	}
}

// TestLayoutAlwaysScrollbarPaintsThumbGlyphs ports the updated upstream
// "updates reserved scrollbar layout at runtime" expectation: an always
// scrollbar paints a thumb glyph in its reserved column.
func TestLayoutAlwaysScrollbarPaintsThumbGlyphs(t *testing.T) {
	scrollView := NewScrollView(NewText("123456"), ScrollViewOptions{Scrollbar: "always"})
	frame := RenderLayoutFrame(NewHStack([]StackChild{{Component: scrollView}}, StackOptions{Align: "start"}), 6, 2, noRender)
	got := visibleFrameLines(frame.Lines)
	for i := range got {
		got[i] = strings.TrimRight(got[i], " ")
	}
	if want := []string{"12345┃", "6    ┃"}; !slices.Equal(got, want) {
		t.Fatalf("visible lines = %q, want %q", got, want)
	}
}

// TestGetScrollbarGeometryIncludesHiddenAutoTrack pins the includeHiddenAuto
// branch: a hidden auto scrollbar over overflowing content reports geometry so
// pointer hover can reveal it, while fitting content never does.
func TestGetScrollbarGeometryIncludesHiddenAutoTrack(t *testing.T) {
	scrollView := NewScrollView(&stubComponent{lines: tenLines()}, ScrollViewOptions{Scrollbar: "auto"})
	t.Cleanup(scrollView.Dispose)
	frame := RenderLayoutFrame(scrollView, 10, 3, noRender)
	if GetScrollbarGeometry(frame.Root) != nil {
		t.Fatal("a hidden auto scrollbar must not paint")
	}
	if geometry := getScrollbarGeometry(frame.Root, true); geometry == nil || geometry.Column != 9 || geometry.TrackHeight != 3 {
		t.Fatalf("hidden auto geometry = %+v, want the column-9 track", geometry)
	}
	fitting := NewScrollView(&stubComponent{lines: []string{"a"}}, ScrollViewOptions{Scrollbar: "auto"})
	fittingFrame := RenderLayoutFrame(fitting, 10, 3, noRender)
	if getScrollbarGeometry(fittingFrame.Root, true) != nil {
		t.Fatal("fitting content has no scrollbar to reveal")
	}
}

// TestGetLayoutBoxesAtOrdersDeepestFirst pins upstream getLayoutBoxesAt: the hit
// path runs from the deepest box to the root.
func TestGetLayoutBoxesAtOrdersDeepestFirst(t *testing.T) {
	top := NewText("top")
	bottom := NewText("bottom")
	root := NewVStack([]StackChild{{Component: top}, {Component: bottom}}, StackOptions{})
	frame := RenderLayoutFrame(root, 10, 2, noRender)
	boxes := GetLayoutBoxesAt(frame, 1, 1)
	if len(boxes) != 2 || boxes[0].Component != bottom || boxes[1].Component != root {
		t.Fatalf("hit path = %v, want [bottom, root]", boxes)
	}
	if got := GetLayoutBoxesAt(frame, 20, 0); len(got) != 0 {
		t.Fatalf("hit path outside the frame = %v, want empty", got)
	}
}

// TestScrollViewScrollToDisableFollowAtEnd pins upstream scrollTo's
// disableFollow option: scrolling to the end without following keeps the view
// unpinned across layout passes until a regular scroll re-enables following.
func TestScrollViewScrollToDisableFollowAtEnd(t *testing.T) {
	scrollView := NewScrollView(&stubComponent{lines: tenLines()}, ScrollViewOptions{Follow: "end"})
	renders := 0
	scrollView.UpdateLayout(10, 3, func() { renders++ })
	if !scrollView.IsFollowingEnd() || !scrollView.FollowEnd() {
		t.Fatal("precondition: a follow-end view starts pinned")
	}
	scrollView.ScrollToWithOptions(7, ScrollViewScrollToOptions{DisableFollow: true})
	if scrollView.IsFollowingEnd() || scrollView.ScrollTop() != 7 || renders != 1 {
		t.Fatalf("disableFollow at end: following=%v top=%d renders=%d", scrollView.IsFollowingEnd(), scrollView.ScrollTop(), renders)
	}
	scrollView.UpdateLayout(10, 3, func() { renders++ })
	if scrollView.IsFollowingEnd() {
		t.Fatal("a layout pass must not re-pin a follow-suppressed view")
	}
	scrollView.ScrollBy(1)
	if !scrollView.IsFollowingEnd() || renders != 2 {
		t.Fatalf("a regular scroll at the end re-enables following: following=%v renders=%d", scrollView.IsFollowingEnd(), renders)
	}
}

// widthRenderComponent renders through a width-aware callback.
type widthRenderComponent struct {
	invalidatable
	render func(width int) []string
}

func (c *widthRenderComponent) Render(width int) []string { return c.render(width) }
