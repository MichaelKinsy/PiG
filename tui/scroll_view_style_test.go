package tui

import "testing"

// Pi scroll-view.ts:27-28,53-54: scrollbarTrackStyle and scrollbarThumbStyle are callable properties; the defaults wrap the text in SGR 90 and SGR 37, and an option replaces them.
func TestScrollViewScrollbarStylesAreCallable(t *testing.T) {
	defaults := NewScrollView(NewText("x"), ScrollViewOptions{})
	if got := defaults.ScrollbarTrackStyle("│"); got != "\x1b[90m│\x1b[39m" {
		t.Fatalf("default track style = %q", got)
	}
	if got := defaults.ScrollbarThumbStyle("█"); got != "\x1b[37m█\x1b[39m" {
		t.Fatalf("default thumb style = %q", got)
	}
	custom := NewScrollView(NewText("x"), ScrollViewOptions{
		ScrollbarTrackStyle: func(s string) string { return "<" + s + ">" },
		ScrollbarThumbStyle: func(s string) string { return "[" + s + "]" },
	})
	if custom.ScrollbarTrackStyle("a") != "<a>" || custom.ScrollbarThumbStyle("b") != "[b]" {
		t.Fatal("custom styles not used")
	}
}
