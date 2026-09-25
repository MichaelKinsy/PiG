package widthx

import (
	"strings"
	"testing"
)

func TestTruncateToWidth_BasicAscii(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		maxWidth int
		ellipsis string
		pad      bool
		want     string
	}{
		{"short fits, no pad", "hi", 10, "...", false, "hi"},
		{"short fits, pad", "hi", 5, "...", true, "hi   "},
		{"exactly fits", "hello", 5, "...", false, "hello"},
		{"empty no pad", "", 10, "...", false, ""},
		{"empty pad", "", 5, "...", true, "     "},
		{"zero width", "hello", 0, "...", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateToWidth(tc.text, tc.maxWidth, tc.ellipsis, tc.pad)
			if got != tc.want {
				t.Errorf("TruncateToWidth(%q, %d, %q, %v) = %q, want %q",
					tc.text, tc.maxWidth, tc.ellipsis, tc.pad, got, tc.want)
			}
		})
	}
}

func TestTruncateToWidth_AppendsEllipsis(t *testing.T) {
	got := TruncateToWidth("hello world", 8, "...", false)
	// "hello world" is 11 wide. Target = 8 - 3 = 5. Output: "hello\x1b[0m...\x1b[0m"
	if !strings.HasPrefix(got, "hello") {
		t.Errorf("got %q, expected to start with %q", got, "hello")
	}
	if !strings.Contains(got, "...") {
		t.Errorf("got %q, expected ellipsis", got)
	}
	if visible := VisibleWidth(got); visible != 8 {
		t.Errorf("visible width = %d, want 8 (got %q)", visible, got)
	}
}

func TestTruncateToWidth_EllipsisWiderThanMax(t *testing.T) {
	// maxWidth=2, ellipsis="...". Result: clipped ellipsis.
	got := TruncateToWidth("hello world", 2, "...", false)
	if VisibleWidth(got) > 2 {
		t.Errorf("got %q (visible %d), want <= 2", got, VisibleWidth(got))
	}
}

func TestTruncateToWidth_WideChars(t *testing.T) {
	// "你好世界" = 8 cols. Truncate to 6 with "..." (3 cols) → target=3 cols of CJK → "你"=2 cols, "好"=2 cols would push to 4 > 3 → stop at "你".
	got := TruncateToWidth("你好世界", 6, "...", false)
	if !strings.HasPrefix(got, "你") {
		t.Errorf("got %q, want prefix %q", got, "你")
	}
	if w := VisibleWidth(got); w > 6 {
		t.Errorf("visible width = %d, want <= 6", w)
	}
}

func TestTruncateToWidth_AnsiPreserved(t *testing.T) {
	got := TruncateToWidth("\x1b[31mhello world\x1b[0m", 8, "...", false)
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("got %q, expected ANSI color preserved", got)
	}
	if w := VisibleWidth(got); w != 8 {
		t.Errorf("visible width = %d, want 8", w)
	}
}

func TestTruncateToWidth_TabsExpandToThree(t *testing.T) {
	// "a\tb": VisibleWidth = 1+3+1 = 5. Truncate to 4: target = 1.
	got := TruncateToWidth("a\tb", 4, "...", false)
	if w := VisibleWidth(got); w > 4 {
		t.Errorf("visible width = %d, want <= 4 (got %q)", w, got)
	}
}

func TestTruncateToWidth_PadToExactWidth(t *testing.T) {
	got := TruncateToWidth("hello world", 8, "...", true)
	if w := VisibleWidth(got); w != 8 {
		t.Errorf("padded visible width = %d, want 8 (got %q)", w, got)
	}
}

func TestTruncateToWidth_PadShortText(t *testing.T) {
	got := TruncateToWidth("hi", 5, "...", true)
	if got != "hi   " {
		t.Errorf("got %q, want %q", got, "hi   ")
	}
}
