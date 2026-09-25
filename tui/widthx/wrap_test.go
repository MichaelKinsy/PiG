package widthx

import (
	"reflect"
	"strings"
	"testing"
)

func TestWrapTextWithAnsi_BasicCases(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"empty", "", 10, []string{""}},
		{"single short word", "hello", 10, []string{"hello"}},
		{"two words fit", "hello world", 11, []string{"hello world"}},
		{"two words wrap", "hello world", 8, []string{"hello", "world"}},
		{"three words wrap", "a b c d e", 3, []string{"a b", "c d", "e"}},
		{"explicit newlines preserved", "line1\nline2", 20, []string{"line1", "line2"}},
		{"CRLF line endings split", "line1\r\nline2", 20, []string{"line1", "line2"}},
		{"lone CR splits", "line1\rline2", 20, []string{"line1", "line2"}},
		{"newline + wrap", "hello world\nfoo bar", 8, []string{"hello", "world", "foo bar"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WrapTextWithAnsi(tc.in, tc.width)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("WrapTextWithAnsi(%q, %d):\n  got:  %#v\n  want: %#v", tc.in, tc.width, got, tc.want)
			}
		})
	}
}

func TestWrapTextWithAnsi_LongWordBroken(t *testing.T) {
	// "abcdefghij" (10 wide) into width 4 → "abcd","efgh","ij"
	got := WrapTextWithAnsi("abcdefghij", 4)
	want := []string{"abcd", "efgh", "ij"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestWrapTextWithAnsi_StylePreservedAcrossWrap(t *testing.T) {
	// red text "hello world" at width 8 → red "hello" then re-opened red "world"
	got := WrapTextWithAnsi("\x1b[31mhello world\x1b[0m", 8)
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[1], "\x1b[31m") {
		t.Errorf("second line should reopen red, got %q", got[1])
	}
}

func TestWrapTextWithAnsi_UnderlineResetAtLineEnd(t *testing.T) {
	// underlined text wrapped: each line break must close underline so it
	// does not bleed into padding rendered by callers.
	got := WrapTextWithAnsi("\x1b[4munderlined wraps\x1b[0m", 11)
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d: %#v", len(got), got)
	}
	if !strings.HasSuffix(got[0], "\x1b[24m") {
		t.Errorf("first wrapped line should end with underline-off, got %q", got[0])
	}
	if !strings.HasPrefix(got[1], "\x1b[4m") {
		t.Errorf("second line should re-open underline, got %q", got[1])
	}
}

func TestWrapTextWithAnsi_HyperlinkClosedAtLineEnd(t *testing.T) {
	in := "\x1b]8;;https://example.com\x1b\\look at this long link target\x1b]8;;\x1b\\"
	got := WrapTextWithAnsi(in, 10)
	if len(got) < 2 {
		t.Fatalf("expected wrap, got %d lines: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "\x1b]8;;\x1b\\") {
		t.Errorf("first line should close hyperlink, got %q", got[0])
	}
	if !strings.Contains(got[1], "\x1b]8;;https://example.com") {
		t.Errorf("second line should reopen hyperlink, got %q", got[1])
	}
}

func TestWrapTextWithAnsi_WideCJK(t *testing.T) {
	// Each CJK char is 2 columns wide.
	got := WrapTextWithAnsi("你好世界", 4)
	if len(got) != 2 {
		t.Fatalf("expected 2 lines, got %d: %#v", len(got), got)
	}
	for _, line := range got {
		if w := VisibleWidth(line); w > 4 {
			t.Errorf("line %q width = %d, want <= 4", line, w)
		}
	}
}

func TestWrapTextWithAnsi_CJKBreaksWithinRemainingLineWidth(t *testing.T) {
	got := WrapTextWithAnsi("日本語テスト hello world 你好世界 test", 30)
	want := []string{"日本語テスト hello world 你好", "世界 test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed CJK wrapping = %#v, want %#v", got, want)
	}
}

func TestWrapTextWithAnsi_NoSplitOfMultiCharStyledRun(t *testing.T) {
	// Ensure visible width respects ANSI codes so we don't double-wrap.
	got := WrapTextWithAnsi("\x1b[1mhi\x1b[0m world", 10)
	if len(got) != 1 {
		t.Errorf("'<bold>hi</> world' (visible 8) at width 10 should fit on one line, got %d: %#v",
			len(got), got)
	}
}
