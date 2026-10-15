package tui

import (
	"strings"
	"testing"
	"unsafe"
)

// pooledMarkedBytes is what a markedInlineState's buffers hold.
func pooledMarkedBytes(state *markedInlineState) int {
	tokenSize := int(unsafe.Sizeof(markedToken{}))
	size := cap(state.runes)*int(unsafe.Sizeof(rune(0))) + cap(state.arena)*tokenSize
	for _, frame := range state.frames {
		size += cap(frame.tokens)*tokenSize + cap(frame.masked)*int(unsafe.Sizeof(rune(0)))
	}
	return size
}

// A long paragraph does not stay pinned in the lexer state pool (golang/go#23199): a state whose buffers grew past 64 KiB is dropped
// on Put, so one 1 MiB paragraph does not keep its code points and tokens alive per pooled state.
func TestMarkedInlineStatePoolDropsLargeBuffers(t *testing.T) {
	paragraph := strings.Repeat("word *em* `code` ", 1<<16)
	if rows := NewMarkdown(paragraph).Render(80); len(rows) == 0 {
		t.Fatal("no rows")
	}
	state := markedInlineStates.Get().(*markedInlineState)
	defer markedInlineStates.Put(state)
	if size := pooledMarkedBytes(state); size > 64<<10 {
		t.Fatalf("the pooled lexer state keeps %d bytes of buffers after a %d-byte paragraph, want at most 64 KiB", size, len(paragraph))
	}
}

// A pooled lexer state holds no string of the document rendered last: its tokens and link definitions are cleared on Put, so an idle
// pool does not keep the last rendered answer alive.
func TestMarkedInlineStatePoolKeepsNoDocumentStrings(t *testing.T) {
	document := "see [the docs][ref] and *this* `code` **bold** text\n\n[ref]: https://example.com/docs\n"
	if rows := NewMarkdown(document).Render(80); len(rows) == 0 {
		t.Fatal("no rows")
	}
	state := markedInlineStates.Get().(*markedInlineState)
	defer markedInlineStates.Put(state)
	if len(state.links) != 0 || len(state.defined) != 0 {
		t.Fatalf("the pooled lexer state keeps link definitions %v", state.links)
	}
	check := func(where string, tokens []markedToken) {
		for index, token := range tokens[:cap(tokens)] {
			if token.raw != "" || token.text != "" || token.href != "" || token.tokens != nil || token.latex != nil {
				t.Fatalf("the pooled lexer state's %s token %d still holds %+v", where, index, token)
			}
		}
	}
	check("arena", state.arena)
	for _, frame := range state.frames {
		check("frame", frame.tokens)
	}
}
