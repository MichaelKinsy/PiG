package tui

import (
	"strings"
	"testing"
	"unsafe"
)

// The block lexer of a markedInlineState keeps its buffers for the next source (D112): token slabs, the inline queue, the token list and the code point buffer. A lexer that reused them wrongly lexes the same text to the same
// tokens, so TestMarkedBlockLexerReuseEqualsFresh and the oracle cannot see it; these tests state what the reuse is.

// Tokens fill slabs of markedSlabSize in order, a reset starts at the first slot of the first slab and leaves the slabs, and no slab is added while one has a free slot.
func TestMarkedBlockLexerFillsItsSlabsInOrderAndReusesThem(t *testing.T) {
	state := &markedInlineState{}
	lexer := state.blockLexer()
	var first []*markedBlock
	for range 2*markedSlabSize + 8 {
		first = append(first, lexer.newBlock(markedBlock{kind: "text"}))
	}
	if len(lexer.slabs) != 3 || lexer.slab != 2 || lexer.used != 8 {
		t.Fatalf("%d tokens: %d slabs, at slab %d slot %d, want 3 slabs at slab 2 slot 8", len(first), len(lexer.slabs), lexer.slab, lexer.used)
	}
	for i, token := range first {
		if want := &lexer.slabs[i/markedSlabSize][i%markedSlabSize]; token != want {
			t.Fatalf("token %d is not slot %d of slab %d", i, i%markedSlabSize, i/markedSlabSize)
		}
		if token.kind != "text" {
			t.Fatalf("token %d lost its value: %q", i, token.kind)
		}
	}
	lexer = state.blockLexer()
	if lexer.slab != 0 || lexer.used != 0 || len(lexer.slabs) != 3 {
		t.Fatalf("after the reset: slab %d slot %d, %d slabs, want 0, 0, 3", lexer.slab, lexer.used, len(lexer.slabs))
	}
	for i := range first {
		if got := lexer.newBlock(markedBlock{kind: "space"}); got != first[i] {
			t.Fatalf("token %d of the next source is not the slot of the first source's token", i)
		}
	}
	if len(lexer.slabs) != 3 {
		t.Fatalf("the same number of tokens added slabs: %d, want 3", len(lexer.slabs))
	}
}

// The inline queue and the token list of a source are empty for the next one, and the entries of the last queue are not kept alive in its spare capacity.
func TestMarkedBlockLexerResetsItsQueueAndKeepsItsTokenList(t *testing.T) {
	state := &markedInlineState{}
	lexer := state.blockLexer()
	src := "a\n\nb\n\n# h"
	first := lexer.lexRunes(state.blockRunes(src), src)
	if len(first) != 5 || len(lexer.queue) != 3 || len(lexer.topTokens) != len(first) {
		t.Fatalf("lexing %q: %d tokens, queue of %d, token list of %d, want 5, 3, 5", src, len(first), len(lexer.queue), len(lexer.topTokens))
	}
	lexer = state.blockLexer()
	if len(lexer.queue) != 0 {
		t.Fatalf("the queue of the next source has %d entries, want 0", len(lexer.queue))
	}
	for i, entry := range lexer.queue[:cap(lexer.queue)] {
		if entry != nil {
			t.Fatalf("queue slot %d keeps the entry of the last source", i)
		}
	}
	second := lexer.lexRunes(state.blockRunes("x\n\ny"), "x\n\ny")
	if len(second) != 3 || &second[0] != &first[0] {
		t.Fatalf("the token list of the next source is not the backing array of the last one's (%d tokens)", len(second))
	}
}

// The code point buffer is sized to the source, is kept while it is large enough (also when it is exactly), and is replaced by one of the source's size when it is not.
func TestMarkedInlineStateBlockRunesReusesItsBuffer(t *testing.T) {
	state := &markedInlineState{}
	first := state.blockRunes("abcde")
	if string(first) != "abcde" || cap(first) != 5 {
		t.Fatalf("a first source of 5 characters: %q in a buffer of %d, want one of 5", string(first), cap(first))
	}
	same := state.blockRunes("vwxyz")
	if string(same) != "vwxyz" || &same[0] != &first[0] {
		t.Fatalf("a source as long as the buffer did not reuse it: %q", string(same))
	}
	shorter := state.blockRunes("ab")
	if string(shorter) != "ab" || &shorter[0] != &first[0] {
		t.Fatalf("a shorter source did not reuse the buffer: %q", string(shorter))
	}
	longer := state.blockRunes("abcdefghij")
	if string(longer) != "abcdefghij" || cap(longer) != 10 {
		t.Fatalf("a longer source: %q in a buffer of %d, want one of 10", string(longer), cap(longer))
	}
}

// A source of one-byte characters has its tokens' text taken from the source string without a copy.
func TestMarkedBlockLexerTakesTextOfASCIISourcesWithoutCopying(t *testing.T) {
	state := &markedInlineState{}
	src := strings.Join([]string{"first paragraph", "", "second one", "continued", "", "", "third"}, "\n")
	tokens := state.blockLexer().lexRunes(state.blockRunes(src), src)
	base := uintptr(unsafe.Pointer(unsafe.StringData(src))) //nolint:gosec // G103: the address of the source's bytes, compared with the addresses of the tokens' text; nothing is read or written through it.
	for _, token := range tokens {
		for name, text := range map[string]string{"raw": token.raw, "text": token.text} {
			if text == "" {
				continue
			}
			at := uintptr(unsafe.Pointer(unsafe.StringData(text))) //nolint:gosec // G103: the address of a token text's bytes, which must lie inside the source's.
			if at < base || at+uintptr(len(text)) > base+uintptr(len(src)) {
				t.Errorf("%s token's %s %q is a copy of the source", token.kind, name, text)
			}
		}
	}
	if len(tokens) == 0 {
		t.Fatal("no tokens")
	}
}
