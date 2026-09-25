package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// countingComponent records how many times Render is called, for the memoization test.
type countingComponent struct {
	invalidatable
	lines   []string
	renders int
}

func (c *countingComponent) Render(int) []string {
	c.renders++
	return c.lines
}

func noRender() {}

func TestIntersect(t *testing.T) {
	tests := []struct {
		name string
		a, b LayoutRect
		want LayoutRect
	}{
		{"overlap", LayoutRect{0, 0, 10, 10}, LayoutRect{5, 5, 10, 10}, LayoutRect{5, 5, 5, 5}},
		{"disjoint", LayoutRect{0, 0, 3, 3}, LayoutRect{10, 10, 3, 3}, LayoutRect{10, 10, 0, 0}},
		{"contained", LayoutRect{0, 0, 10, 10}, LayoutRect{2, 2, 3, 3}, LayoutRect{2, 2, 3, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := intersect(tt.a, tt.b); got != tt.want {
				t.Errorf("intersect = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRenderCachedMemoizesPerWidth(t *testing.T) {
	c := &countingComponent{lines: []string{"x"}}
	ctx := &layoutContext{renderCache: map[Component]map[int][]string{}}
	renderCached(ctx, c, 10)
	renderCached(ctx, c, 10)
	renderCached(ctx, c, 10)
	if c.renders != 1 {
		t.Errorf("render called %d times at width 10, want 1 (memoized)", c.renders)
	}
	renderCached(ctx, c, 20)
	if c.renders != 2 {
		t.Errorf("render called %d times after new width, want 2", c.renders)
	}
}

func TestLayoutLeafClampsHeightAndScrollsToCursor(t *testing.T) {
	// 5 lines, cursor marker on line 4 (0-indexed), allocated height 3.
	lines := []string{"a", "b", "c", "d" + widthx.CursorMarker, "e"}
	c := &stubComponent{lines: lines}
	ctx := &layoutContext{renderCache: map[Component]map[int][]string{}}
	h := 3
	box := layoutComponent(ctx, c, 0, 0, 10, &h, LayoutRect{0, 0, 10, 3})
	if box.Rect.Height != 3 {
		t.Errorf("allocated height = %d, want 3", box.Rect.Height)
	}
	// cursorLine(3) >= allocatedHeight(3) -> lineOffset = 3 - 3 + 1 = 1.
	if box.LineOffset != 1 {
		t.Errorf("lineOffset = %d, want 1 (scroll to keep cursor visible)", box.LineOffset)
	}
}

func TestRenderLayoutFrameVStackGeometryAndPaint(t *testing.T) {
	a := &stubComponent{lines: []string{"AAA"}}
	b := &stubComponent{lines: []string{"BBB"}}
	root := NewVStack([]StackChild{{Component: a}, {Component: b}}, StackOptions{})
	frame := RenderLayoutFrame(root, 10, 5, noRender)

	if len(frame.Lines) != 5 || frame.Height != 5 || frame.Width != 10 {
		t.Fatalf("frame dims = %dx%d, %d lines", frame.Width, frame.Height, len(frame.Lines))
	}
	if len(frame.Root.Children) != 2 {
		t.Fatalf("root has %d children, want 2", len(frame.Root.Children))
	}
	// Vertical stacking: A at y=0, B at y=1, each height 1.
	if frame.Root.Children[0].Rect.Y != 0 || frame.Root.Children[0].Rect.Height != 1 {
		t.Errorf("child A rect = %+v, want y=0 h=1", frame.Root.Children[0].Rect)
	}
	if frame.Root.Children[1].Rect.Y != 1 || frame.Root.Children[1].Rect.Height != 1 {
		t.Errorf("child B rect = %+v, want y=1 h=1", frame.Root.Children[1].Rect)
	}
	if !strings.Contains(frame.Lines[0], "AAA") {
		t.Errorf("row 0 = %q, want to contain AAA", frame.Lines[0])
	}
	if !strings.Contains(frame.Lines[1], "BBB") {
		t.Errorf("row 1 = %q, want to contain BBB", frame.Lines[1])
	}
}

// tenLines builds a fixed 10-line content block.
func tenLines() []string {
	out := make([]string, 10)
	for i := range out {
		out[i] = strings.Repeat("x", 4)
	}
	return out
}

func TestRenderLayoutFrameScrollbarGeometry(t *testing.T) {
	content := &stubComponent{lines: tenLines()}
	sv := NewScrollView(content, ScrollViewOptions{Scrollbar: "always"})
	t.Cleanup(sv.Dispose)
	frame := RenderLayoutFrame(sv, 10, 3, noRender)

	if frame.PrimaryScrollView != sv {
		t.Error("primary scroll view should be the root ScrollView")
	}
	geom := GetScrollbarGeometry(frame.Root)
	if geom == nil {
		t.Fatal("expected scrollbar geometry for an always-visible scrollbar")
	}
	// content 10, track 3: thumbHeight = max(min(2,3), min(3, round(9/10)=1)) = 2.
	// column = x+width-1 = 9; maxScrollTop = 10-3 = 7; scrollTop 0 -> thumbTop = y = 0.
	want := ScrollbarGeometry{Column: 9, TrackTop: 0, TrackHeight: 3, ThumbTop: 0, ThumbHeight: 2, MaxScrollTop: 7}
	if *geom != want {
		t.Errorf("geometry = %+v, want %+v", *geom, want)
	}
}

func TestGetScrollViewBoxAndViewsAt(t *testing.T) {
	content := &stubComponent{lines: tenLines()}
	sv := NewScrollView(content, ScrollViewOptions{Scrollbar: "always"})
	t.Cleanup(sv.Dispose)
	frame := RenderLayoutFrame(sv, 10, 3, noRender)

	if GetScrollViewBox(frame, sv) != frame.Root {
		t.Error("GetScrollViewBox should locate the root scroll box")
	}
	views := GetScrollViewsAt(frame, 5, 1)
	if len(views) != 1 || views[0] != sv {
		t.Errorf("GetScrollViewsAt = %v, want [sv]", views)
	}
	// Outside the viewport (y past height) -> none.
	if got := GetScrollViewsAt(frame, 5, 9); len(got) != 0 {
		t.Errorf("GetScrollViewsAt outside clip = %v, want empty", got)
	}
}

// TestRenderLayoutFrameScrollContentShorterThanViewport pins the paint sub-path
// the (always-overflowing) fullscreen parity scenario does not exercise: when a
// scroll view's content is shorter than its viewport, follow-end keeps scrollTop
// at 0, the content paints at the top, and the remaining viewport rows are blank
// (padded below), not an out-of-range panic or a repeat of the last line.
func TestRenderLayoutFrameScrollContentShorterThanViewport(t *testing.T) {
	content := &stubComponent{lines: []string{"AAA", "BBB", "CCC"}}
	sv := NewScrollView(content, ScrollViewOptions{Follow: "end"})
	t.Cleanup(sv.Dispose)
	frame := RenderLayoutFrame(sv, 3, 6, noRender)

	if sv.ScrollTop() != 0 {
		t.Fatalf("short content must keep scrollTop at 0, got %d", sv.ScrollTop())
	}
	if len(frame.Lines) != 6 {
		t.Fatalf("frame height = %d, want 6", len(frame.Lines))
	}
	for i, want := range []string{"AAA", "BBB", "CCC"} {
		if !strings.Contains(frame.Lines[i], want) {
			t.Errorf("line %d = %q, want to contain %q (content should paint top-aligned)", i, frame.Lines[i], want)
		}
	}
	for i := 3; i < 6; i++ {
		if strings.TrimSpace(frame.Lines[i]) != "" {
			t.Errorf("line %d = %q, want blank (viewport padded below short content)", i, frame.Lines[i])
		}
	}
}
