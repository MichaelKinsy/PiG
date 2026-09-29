package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestMarkdownChalkResetAndNewlineBoundaries(t *testing.T) {
	got := ansiSpan("\x1b[1m", "\x1b[22m", "a\x1b[22mb\nc")
	want := "\x1b[1ma\x1b[22m\x1b[1mb\x1b[22m\n\x1b[1mc\x1b[22m"
	if got != want {
		t.Fatalf("nested decoration=%q, want %q", got, want)
	}
}
func TestMarkdownMatchingCodeSpanRuns(t *testing.T) {
	for _, tt := range []struct{ source, want string }{
		{"`one` ``a`b`` ```c``d``` and `` `edge` ``", "one a`b c``d and `edge`"},
		{"before `incomplete", "before `incomplete"},
		{"before `one\ntwo` after", "before one two after"},
	} {
		got := mdPlain(NewMarkdownWithOptions(tt.source, 0, 0, mdUpstreamTheme(), nil, nil).Render(80))
		if !slices.Equal(got, []string{tt.want}) {
			t.Fatalf("%q => %q, want %q", tt.source, got, tt.want)
		}
	}
}
func TestUserMessageBoxKeepsMarkersAndDoesNotEraseOutsideWidth(t *testing.T) {
	block := NewUserMessageBlock("hello")
	for range 3 {
		lines := block.Render(20)
		if len(lines) != 3 {
			t.Fatal(lines)
		}
		want := "\x1b]133;A\x07" + UserMessageBgOpen() + strings.Repeat(" ", 20) + BgClose()
		if lines[0] != want {
			t.Fatalf("user padding=%q, want %q", lines[0], want)
		}
		if strings.Count(lines[0], "\x1b]133;A\x07") != 1 {
			t.Fatal("cached render duplicated marker")
		}
	}
	if got := NewUserMessageBlock("").Render(20); len(got) != 0 {
		t.Fatalf("empty user message should have no box rows: %q", got)
	}
}
func TestUserMessagePreservesSourceMarkdownOptions(t *testing.T) {
	output := widthx.StripAnsi(strings.Join(NewUserMessageBlock("1. first\n1. second\n\n\"\\\"").Render(24), "\n"))
	upstreamContains(t, output, "1. second")
	upstreamExcludes(t, output, "2. second")
	upstreamContains(t, output, "\"\\\"")
}
func TestMarkdownQuoteStyleWrapBoundary(t *testing.T) {
	md := NewMarkdownWithOptions("- > alpha beta gamma delta epsilon zeta", 0, 0, mdUpstreamTheme(), nil, nil)
	lines := md.Render(24)
	if len(lines) != 2 {
		t.Fatalf("quote rows=%q", lines)
	}
	upstreamExcludes(t, lines[0], "\x1b[23m")
	upstreamContains(t, lines[1], "\x1b[23m")
}

func TestMarkdownBackgroundCallbackOrder(t *testing.T) {
	var calls []string
	md := NewMarkdownWithOptions("alpha\nbeta", 1, 2, mdUpstreamTheme(), &DefaultTextStyle{BgColor: func(s string) string { calls = append(calls, s); return s }}, nil)
	lines := md.Render(12)
	want := []string{" alpha      ", " beta       ", strings.Repeat(" ", 12), strings.Repeat(" ", 12)}
	if !slices.Equal(calls, want) {
		t.Fatalf("background calls=%q, want %q", calls, want)
	}
	if len(lines) != 6 {
		t.Fatalf("rows=%d, want 6", len(lines))
	}
}
