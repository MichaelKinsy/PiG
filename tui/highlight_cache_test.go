package tui

import (
	"slices"
	"testing"
)

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
			want := highlightCodeUncached(highlightRegistry(), s.code, s.lang, false, ActiveTheme())
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

// TestHighlightCodeLinesBelongToTheCaller: theme.ts highlightCode returns a new array on every call, and write.ts
// updateWriteHighlightCacheIncremental writes into the array it got. The memo and the resume state (D112) keep the lines
// they computed, so a caller that writes into its lines must not change what a later call returns: neither the same code
// again (a memo hit) nor a longer prefix of it (a resumed run). The empty block is the one a write card highlights before
// its content streams in.
func TestHighlightCodeLinesBelongToTheCaller(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")
	block := "package main\n\nfunc f(x int) int {\n\treturn x*2 + 1 // doubled\n}\n\nfunc g() string {\n\treturn \"g\"\n}\n\nvar h = f(2)\n"
	for _, c := range []struct {
		name             string
		highlight        func(code, lang string) []string
		codeBlockOnError bool
	}{{"HighlightCode", HighlightCode, false}, {"markdown highlightCode", highlightMarkdownCode, true}} {
		hlMu.Lock()
		resetHighlightCacheLocked()
		hlMu.Unlock()
		for end := 0; end <= len(block); end += 7 {
			code := block[:end]
			want := highlightCodeUncached(highlightRegistry(), code, "go", c.codeBlockOnError, ActiveTheme())
			for call := range 2 {
				got := c.highlight(code, "go")
				if !slices.Equal(got, want) {
					t.Fatalf("%s at %d bytes, call %d after the caller wrote into earlier lines:\n got %q\nwant %q", c.name, end, call, got, want)
				}
				for i := range got {
					got[i] = "written by the caller"
				}
			}
		}
	}
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
		resetHighlightCacheLocked() // cold per iteration, fair vs the uncached bench
		hlMu.Unlock()
		for f := 1; f <= frames; f++ {
			end := finalLen * f / frames
			md := NewMarkdown(full[:end])
			_ = md.Render(width)
		}
	}
}
