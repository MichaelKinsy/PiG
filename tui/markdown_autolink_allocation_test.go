package tui

import (
	"strings"
	"testing"
)

// marked's url rule anchors at the token start, so a failed probe does not depend on the paragraph tail.
func BenchmarkAutoLinkProbeTail(b *testing.B) {
	for _, tail := range []struct{ name, text string }{
		{"short", "tail words"},
		{"long", strings.Repeat("tail words ", 1024)},
	} {
		b.Run(tail.name, func(b *testing.B) {
			input := []rune("ordinary " + tail.text)
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := markedURL(input); ok {
					b.Fatal("ordinary word became an autolink")
				}
			}
		})
	}
}
