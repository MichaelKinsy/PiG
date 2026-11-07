package tui

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// liveHeap is the bytes of live heap after a collection.
func liveHeap() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// A finished message is lexed in passes whose strings are copied into chunks of up to 16 KiB; a memo entry must not keep the chunk its code was copied into, or one short snippet in a long answer pins the answer's chunk and the memo holds far more than hlEntrySize counts (hlCacheMaxBytes).
// Bound: after a session of finished messages of 9 KB with one Go block each, rendered whole and streamed, the memo retains about what it accounts for. A streamed block stores its frames' keys without a copy, so the key of its last frame must get one when the stream ends; the shapes that lex every frame from the start of the message (a link reference definition, a non-ASCII list) have the longest chunks.
func TestHighlightMemoRetainsWhatItAccountsFor(t *testing.T) {
	prose := strings.Repeat("Some prose that explains the change in a sentence of ordinary length.\n\n", 62)
	block := func(n string) string { return "```go\nfunc f" + n + "(x int) int { return x * " + n + " }\n```\n\n" }
	const definition = "[ref]: https://example.com/ref\n\n"
	for _, shape := range []struct {
		name     string
		messages int
		// step is the bytes a frame adds to a streamed message; 0 renders the message whole.
		step int
		text func(n string) string
	}{
		{"whole", 1000, 0, func(n string) string { return prose + block(n) + prose }},
		{"streamed", 300, 48, func(n string) string { return prose + block(n) + prose }},
		{"streamed after a definition, block in the middle", 300, 48, func(n string) string { return definition + prose + block(n) + prose }},
		{"streamed after a definition, block at the end", 300, 48, func(n string) string { return definition + prose + prose + block(n) }},
		{"streamed non-ASCII list", 250, 48, func(n string) string {
			return "- Item — with a body:\n\n  " + strings.ReplaceAll(prose+block(n), "\n", "\n  ")
		}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			hlMu.Lock()
			resetHighlightCacheLocked()
			hlMu.Unlock()
			t.Cleanup(func() {
				hlMu.Lock()
				resetHighlightCacheLocked()
				hlMu.Unlock()
			})
			theme := GetMarkdownTheme()
			kept := make([]*Markdown, 0, shape.messages)
			for i := range shape.messages {
				text := shape.text(strconv.Itoa(i))
				md := NewMarkdownWithOptions("", 0, 0, &theme, nil, nil)
				if shape.step == 0 {
					md.SetText(text)
					md.Render(100)
				}
				for end := shape.step; shape.step > 0 && end-shape.step < len(text); end += shape.step {
					for end < len(text) && !utf8.RuneStart(text[end]) {
						end++
					}
					md.SetText(text[:min(end, len(text))])
					md.Render(100)
				}
				md.FinishStreaming()
				md.Render(100)
				kept = append(kept, md)
			}
			hlMu.Lock()
			accounted, entries := hlCacheBytes, len(hlCache)
			sum := 0
			for key, lines := range hlCache {
				sum += hlEntrySize(key, lines)
			}
			hlMu.Unlock()
			if sum != accounted {
				t.Errorf("the memo accounts for %d bytes, its entries are %d bytes", accounted, sum)
			}
			if entries < shape.messages/2 {
				t.Fatalf("the memo holds %d entries after %d messages with a code block each", entries, shape.messages)
			}
			withMemo := liveHeap()
			hlMu.Lock()
			resetHighlightCacheLocked()
			hlMu.Unlock()
			withoutMemo := liveHeap()
			runtime.KeepAlive(kept)
			retained := int64(withMemo) - int64(withoutMemo)
			t.Logf("entries %d accounted %d retained %d", entries, accounted, retained)
			// The entries, their keys and lines are what is accounted; the map and slice headers are not.
			if limit := int64(2*accounted + 1<<20); retained > limit {
				t.Fatalf("the memo retains %d bytes in %d entries but accounts for %d, want at most %d: its keys pin the buffers the lexer copied the messages' strings to", retained, entries, accounted, limit)
			}
		})
	}
}
