package widthx

import (
	"strings"
	"testing"
)

func TestUpstreamTruncateToWidth(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:6
	t.Run("keeps output within width for very large unicode input", func(t *testing.T) {
		got := TruncateToWidth(strings.Repeat("🙂界", 100_000), 40, "…", false)
		if VisibleWidth(got) > 40 || !strings.HasSuffix(got, "…\x1b[0m") {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:14
	t.Run("preserves ANSI styling for kept text and resets before and after ellipsis", func(t *testing.T) {
		got := TruncateToWidth("\x1b[31m"+strings.Repeat("hello ", 1000)+"\x1b[0m", 20, "…", false)
		if VisibleWidth(got) > 20 || !strings.Contains(got, "\x1b[31m") || !strings.HasSuffix(got, "\x1b[0m…\x1b[0m") {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:23
	t.Run("closes a BEL-terminated OSC 8 link when truncating its label", func(t *testing.T) {
		open, close := "\x1b]8;;https://example.com\x07", "\x1b]8;;\x07"
		got := TruncateToWidth(open+"some-longer-label-here"+close, 15, "...", false)
		if want := open + "some-longer-" + close + "\x1b[0m...\x1b[0m"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:31
	t.Run("handles malformed ANSI escape prefixes without hanging", func(t *testing.T) {
		if got := TruncateToWidth("abc\x1bnot-ansi "+strings.Repeat("🙂", 1000), 20, "…", false); VisibleWidth(got) > 20 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:38
	t.Run("clips wide ellipsis safely and brackets it with resets", func(t *testing.T) {
		if got := TruncateToWidth("abcdef", 1, "🙂", false); got != "" {
			t.Fatal(got)
		}
		if got := TruncateToWidth("abcdef", 2, "🙂", false); got != "\x1b[0m🙂\x1b[0m" || VisibleWidth(got) > 2 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:44
	t.Run("returns the original text when it already fits even if ellipsis is too wide", func(t *testing.T) {
		for _, text := range []string{"a", "界"} {
			if got := TruncateToWidth(text, 2, "🙂", false); got != text {
				t.Fatal(got)
			}
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:49
	t.Run("pads truncated output to requested width", func(t *testing.T) {
		if got := TruncateToWidth("🙂界🙂界🙂界", 8, "…", true); VisibleWidth(got) != 8 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:54
	t.Run("adds a trailing reset when truncating without an ellipsis", func(t *testing.T) {
		got := TruncateToWidth("\x1b[31m"+strings.Repeat("hello", 100), 10, "", false)
		if VisibleWidth(got) > 10 || !strings.HasSuffix(got, "\x1b[0m") {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:60
	t.Run("keeps a contiguous prefix instead of skipping a wide grapheme and resuming later", func(t *testing.T) {
		got := TruncateToWidth("🙂\t界 \x1b_abc\x07", 7, "…", true)
		if want := "🙂\t\x1b[0m…\x1b[0m "; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

func TestUpstreamVisibleWidth(t *testing.T) {
	cases := []struct {
		name    string
		samples []string
		widths  []int
	}{
		// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:67
		{"counts tabs inline and skips ANSI inline", []string{"\t\x1b[31m界\x1b[0m"}, []int{5}},
		// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:71
		{"counts Indic conjunct spacing code points within grapheme clusters", []string{"र्क", "नेटवर्क", "सर्वाधिकार सुरक्षित। ऑर्डर पर क्लिक करें", "র্ক", "ર્ક", "ର୍କ", "ర్క", "ര്‍ക"}, []int{2, 5, 33, 2, 2, 2, 2, 2}},
		// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:82
		{"keeps ordinary combining marks zero-width", []string{"e\u0301", "čřžůú", "שָׁ", "بّ", "རྐ", "ᜠ᜴", "가〮", "가〯"}, []int{1, 5, 1, 1, 1, 1, 2, 2}},
		// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:93
		{"keeps CJK and Japanese width accounting unchanged", []string{"网络", "ネットワーク", "が", "か\u3099"}, []int{4, 12, 2, 2}},
		// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:100
		{"counts Myanmar marks that terminals allocate cells for", []string{"ကာ", "ကေ", "က်", "ကျ", "ကြ", "ကဳ", "ကဴ", "ကဵ", "ကး", "ကို", "က္"}, []int{2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1}},
		// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:114
		{"keeps Thai and Lao AM clusters at their normal cell width", []string{"ำ", "ຳ", "กำ", "ກຳ"}, []int{1, 1, 2, 2}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			for i, sample := range tt.samples {
				if got := VisibleWidth(sample); got != tt.widths[i] {
					t.Errorf("VisibleWidth(%q) = %d, want %d", sample, got, tt.widths[i])
				}
			}
		})
	}
	// .upstream/v0.87.1/packages/tui/test/truncate-to-width.test.ts:121
	t.Run("normalizes Thai and Lao AM vowels only for terminal output", func(t *testing.T) {
		for _, tt := range []struct{ input, want string }{{"ำ", "ํา"}, {"ຳ", "ໍາ"}} {
			if got := NormalizeTerminalOutput(tt.input); got != tt.want {
				t.Errorf("normalize %q = %q, want %q", tt.input, got, tt.want)
			}
			if VisibleWidth(NormalizeTerminalOutput(tt.input+"abc")) != VisibleWidth(tt.input+"abc") {
				t.Errorf("normalization changed width for %q", tt.input)
			}
		}
	})
}
