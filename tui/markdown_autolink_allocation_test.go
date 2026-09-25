package tui

import (
	"strings"
	"testing"
)

// The URL-prefix probe only needs the longest recognized scheme, not the
// paragraph tail. The exact prefix guard is deterministic under -race, unlike
// allocation totals that include the regex engine's randomized pool misses.
func TestAutoLinkPrefixIgnoresUnrelatedTail(t *testing.T) {
	for _, tc := range []struct {
		input string
		start int
		want  string
	}{
		{"", 0, ""},
		{"http://", 0, "http://"},
		{"ordinary short tail", 0, "ordinary"},
		{"ordinary " + strings.Repeat("tail words ", 1024), 0, "ordinary"},
		{"日本https://example.test", 2, "https://"},
	} {
		if got := autoLinkPrefix([]rune(tc.input), tc.start); got != tc.want {
			t.Fatalf("prefix at %d in %d bytes = %q, want %q", tc.start, len(tc.input), got, tc.want)
		}
	}
}

func BenchmarkAutoLinkProbeTail(b *testing.B) {
	for _, tail := range []struct{ name, text string }{
		{"short", "tail words"},
		{"long", strings.Repeat("tail words ", 1024)},
	} {
		b.Run(tail.name, func(b *testing.B) {
			input := []rune("ordinary " + tail.text)
			b.ReportAllocs()
			for b.Loop() {
				if _, _, _, ok := parseAutoLink(input, 0); ok {
					b.Fatal("ordinary word became an autolink")
				}
			}
		})
	}
}
