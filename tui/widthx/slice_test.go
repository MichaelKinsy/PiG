package widthx

import "testing"

func TestSliceByColumn_PlainAscii(t *testing.T) {
	cases := []struct {
		name          string
		line          string
		start, length int
		want          string
	}{
		{"prefix", "hello world", 0, 5, "hello"},
		{"middle", "hello world", 6, 5, "world"},
		{"past end", "hello", 10, 5, ""},
		{"length zero", "hello", 0, 0, ""},
		{"negative length", "hello", 0, -1, ""},
		{"overshoot", "ab", 0, 10, "ab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SliceByColumn(tc.line, tc.start, tc.length, false); got != tc.want {
				t.Errorf("SliceByColumn = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSliceByColumn_AnsiPreserved(t *testing.T) {
	// Style at offset 0 should follow into the sliced region.
	line := "\x1b[1;31mhello\x1b[0m world"
	got := SliceByColumn(line, 0, 5, false)
	if got == "" {
		t.Fatal("got empty result")
	}
	// Must contain the styling pending before content.
	if !contains(got, "\x1b[1;31m") {
		t.Errorf("expected ANSI prefix preserved, got %q", got)
	}
}

func TestSliceByColumn_WideCharNonStrict(t *testing.T) {
	// "a你b": columns: a=0,你=1-2,b=3
	// Slice [0, 2): non-strict: wide char at boundary INCLUDED.
	got := SliceByColumn("a你b", 0, 2, false)
	if got != "a你" {
		t.Errorf("non-strict slice = %q, want %q", got, "a你")
	}
}

func TestSliceByColumn_WideCharStrict(t *testing.T) {
	// Strict: wide char would overflow → excluded.
	got := SliceByColumn("a你b", 0, 2, true)
	if got != "a" {
		t.Errorf("strict slice = %q, want %q", got, "a")
	}
}

func TestSliceWithWidth_ReportsActualWidth(t *testing.T) {
	r := SliceWithWidth("a你b", 0, 3, false)
	if r.Text != "a你" {
		t.Errorf("text = %q, want %q", r.Text, "a你")
	}
	if r.Width != 3 {
		t.Errorf("width = %d, want 3", r.Width)
	}
}

func TestExtractSegments_PrefixOnly(t *testing.T) {
	r := ExtractSegments("hello world", 5, 0, 0, false)
	if r.Before != "hello" || r.BeforeWidth != 5 {
		t.Errorf("before = %q (w=%d), want %q (w=5)", r.Before, r.BeforeWidth, "hello")
	}
	if r.After != "" {
		t.Errorf("after = %q, want empty (afterLen=0)", r.After)
	}
}

func TestExtractSegments_BeforeAndAfter(t *testing.T) {
	// "0123456789", overlay at cols [3,6) → before [0,3)="012", after [6,10)="6789"
	r := ExtractSegments("0123456789", 3, 6, 4, false)
	if r.Before != "012" {
		t.Errorf("before = %q, want %q", r.Before, "012")
	}
	if r.After != "6789" {
		t.Errorf("after = %q, want %q", r.After, "6789")
	}
}

func TestExtractSegments_StyleCarriesIntoAfter(t *testing.T) {
	// Red styling opened before overlay continues into after segment.
	line := "\x1b[31m0123456789\x1b[0m"
	r := ExtractSegments(line, 3, 6, 4, false)
	if r.Before == "" || !contains(r.Before, "\x1b[31m") {
		t.Errorf("before should contain red opener, got %q", r.Before)
	}
	if !startsWithAnsi(r.After, "\x1b[31m") {
		t.Errorf("after should be prefixed with the active red style, got %q", r.After)
	}
}

func TestExtractSegments_HyperlinkCarriesIntoAfter(t *testing.T) {
	line := "\x1b]8;;https://e.com\x1b\\0123456789\x1b]8;;\x1b\\"
	r := ExtractSegments(line, 3, 6, 4, false)
	if !contains(r.After, "\x1b]8;;https://e.com") {
		t.Errorf("after should re-open active hyperlink, got %q", r.After)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func startsWithAnsi(s, prefix string) bool {
	// The "after" segment is prefixed with active codes before the first
	// content grapheme. Allow the prefix to be at offset 0.
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
