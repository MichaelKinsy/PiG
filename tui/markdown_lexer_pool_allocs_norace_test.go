//go:build !race

package tui

import (
	"runtime"
	"testing"
)

// A render returns its lexer state to the pool, so the next render of a component reuses the lexer's buffers (the token slabs, the queue, the rune and token arenas)
// instead of allocating them again: the bytes a render allocates stay far below the size of those buffers.
func TestMarkdownRenderReusesPooledLexerState(t *testing.T) {
	text := uniqueParagraphs(3, "Paragraph N with **bold**, `code` and a [link](http://a.b).")
	m := NewMarkdown(text)
	least := ^uint64(0)
	for range 5 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		const runs = 10
		for range runs {
			m.Invalidate()
			m.Render(60)
		}
		runtime.ReadMemStats(&after)
		least = min(least, (after.TotalAlloc-before.TotalAlloc)/runs)
	}
	// 7,000 bytes with the pooled state and 16,000 when every render allocates its own.
	const limit = 11000
	if least > limit {
		t.Fatalf("a render of three paragraphs allocates %d bytes, more than %d: the lexer state is not reused", least, limit)
	}
}
