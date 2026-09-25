package tui

import "testing"

// TestHighlightCode_CacheMatchesUncached is the oracle for the highlight
// memo: the cached HighlightCode must return byte-identical output to the
// pure highlighter, for repeated calls and across a theme change.
func TestHighlightCode_CacheMatchesUncached(t *testing.T) {
	samples := []struct{ lang, code string }{
		{"go", "func f(x int) int {\n\treturn x*2 + 1 // doubled\n}"},
		{"python", "def g(a):\n    return a + 1"},
		{"", "plain text no language here"},
		{"json", "{\n  \"a\": 1,\n  \"b\": [true, null]\n}"},
	}

	check := func(when string) {
		for _, s := range samples {
			want := highlightCodeUncached(s.code, s.lang, ActiveTheme())
			// two cached calls to exercise store-then-hit
			for range 2 {
				got := HighlightCode(s.code, s.lang)
				if len(got) != len(want) {
					t.Fatalf("%s lang=%q: len got=%d want=%d", when, s.lang, len(got), len(want))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("%s lang=%q line %d:\n got=%q\nwant=%q", when, s.lang, i, got[i], want[i])
					}
				}
			}
		}
	}

	orig := ActiveTheme().Name
	defer SetTheme(orig)

	SetTheme("dark")
	check("dark")
	SetTheme("light") // theme change must invalidate and still match
	check("light")
	SetTheme("dark")
	check("dark-again")
}

// BenchmarkMarkdownStreamingCached re-runs the streaming benchmark with the
// highlight memo active. Closed code blocks re-highlight once, not every frame.
func BenchmarkMarkdownStreamingCached(b *testing.B) {
	const finalLen = 8000
	const frames = 120
	full := streamingBody(finalLen)
	const width = 100
	b.ResetTimer()
	for range b.N {
		hlMu.Lock()
		clear(hlCache) // cold per iteration, fair vs the uncached bench
		hlMu.Unlock()
		for f := 1; f <= frames; f++ {
			end := finalLen * f / frames
			md := NewMarkdown(full[:end])
			_ = md.Render(width)
		}
	}
}
