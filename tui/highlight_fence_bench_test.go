//go:build !pig_strip_syntax_highlight

package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// fenceOf is a fenced code block of about size bytes of the oracle corpus samples of lang, cut at a line.
func fenceOf(tb testing.TB, lang string, size int) string {
	tb.Helper()
	data, err := os.ReadFile("internal/hljs/testdata/corpus.json")
	if err != nil {
		tb.Fatal(err)
	}
	var corpus []struct{ Lang, Code string }
	if err := json.Unmarshal(data, &corpus); err != nil {
		tb.Fatal(err)
	}
	var code strings.Builder
	for code.Len() < size {
		before := code.Len()
		for _, c := range corpus {
			if c.Lang == lang && code.Len() < size {
				code.WriteString(c.Code)
				code.WriteString("\n")
			}
		}
		if code.Len() == before {
			tb.Fatalf("no corpus sample of %s", lang)
		}
	}
	return "```" + lang + "\n" + code.String()
}

// BenchmarkStreamedFence streams an unclosed fence 24 bytes at a time and renders every frame, as a fast model does; the metric is the time of a frame over the last tenth of the stream.
func BenchmarkStreamedFence(b *testing.B) {
	LoadAllHighlightLanguages()
	for _, lang := range []string{"python", "typescript", "go", "bash", "json"} {
		b.Run(lang, func(b *testing.B) {
			body := fenceOf(b, lang, 20000)
			for range b.N {
				hlMu.Lock()
				resetHighlightCacheLocked()
				hlMu.Unlock()
				md := NewMarkdown("")
				frames := 0
				for end := 24; ; end += 24 {
					end = min(end, len(body))
					md.SetText(body[:end])
					md.Render(160)
					frames++
					if end == len(body) {
						break
					}
				}
				_ = frames
			}
		})
	}
}
