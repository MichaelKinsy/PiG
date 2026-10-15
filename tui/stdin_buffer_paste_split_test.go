package tui

import (
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// feedChunks pushes text through a StdinBuffer in pieces of about size bytes, split only between characters, and returns the paste payloads and data sequences it published.
func feedChunks(text string, size int) (pastes, data []string) {
	var buffer StdinBuffer
	buffer.On(StdinBufferEventPaste, NewStdinBufferListener(func(s string) { pastes = append(pastes, s) }))
	buffer.On(StdinBufferEventData, NewStdinBufferListener(func(s string) { data = append(data, s) }))
	for len(text) > 0 {
		n := min(size, len(text))
		for n < len(text) && !utf8.RuneStart(text[n]) {
			n++
		}
		buffer.Process(text[:n])
		text = text[n:]
	}
	return pastes, data
}

// upstream: packages/tui/src/stdin-buffer.ts:324-343 (paste mode appends each chunk to pasteBuffer and looks for the end marker): however a paste is
// split across reads, including inside the 6-byte end marker, the paste event carries the payload before the first end marker and the text after it
// is processed as ordinary input. Pig keeps this a linear-time append that only searches the new bytes plus the marker-length seam.
// mutation-checked: searching only the new chunk (not the seam), or dropping the text after the marker, fails it.
func TestStdinBufferPasteSplitAtEveryByteBoundary(t *testing.T) {
	const payload = "héllo\nwörld\x1b[A 😀 end"
	input := "x\x1b[200~" + payload + "\x1b[201~yz"
	for size := 1; size <= len(input); size++ { // a size splits the end marker at every offset
		pastes, data := feedChunks(input, size)
		if len(pastes) != 1 || pastes[0] != payload {
			t.Fatalf("chunk size %d: pastes = %q, want one %q", size, pastes, payload)
		}
		if got := strings.Join(data, ""); got != "xyz" {
			t.Fatalf("chunk size %d: data = %q, want x then yz around the paste", size, data)
		}
	}
}

// upstream: packages/tui/src/stdin-buffer.ts:325 `this.pasteBuffer += this.buffer`: a JavaScript string concatenation joins UTF-16 units, so a lone high
// surrogate at the end of one chunk and a lone low surrogate at the start of the next form the one pair (WTF-8 "\xed\xa0\xbd" + "\xed\xb8\x80" is "😀").
// mutation-checked: skipping the seam canonicalisation leaves the six WTF-8 bytes in the payload and fails it.
func TestStdinBufferPasteJoinsASurrogatePairSplitAcrossChunks(t *testing.T) {
	var buffer StdinBuffer
	var pastes []string
	buffer.On(StdinBufferEventPaste, NewStdinBufferListener(func(s string) { pastes = append(pastes, s) }))
	buffer.Process("\x1b[200~a\xed\xa0\xbd")
	buffer.Process("\xed\xb8\x80b\x1b[201~")
	if len(pastes) != 1 || pastes[0] != "a😀b" {
		t.Fatalf("pastes = %q, want %q", pastes, "a😀b")
	}
}

// A multi-megabyte paste arrives in terminal-sized reads. Pi's stdin-buffer.ts:325-328 re-searches the whole pasteBuffer per chunk, which is
// quadratic in the paste; Pig must stay linear. Allocation is deterministic: the quadratic copy of a 4 MiB paste in 4 KiB chunks allocates
// about 4 GiB, the linear append a small multiple of the paste.
// mutation-checked: rebuilding the accumulated paste per chunk (the previous implementation) fails it.
func TestStdinBufferPasteAllocatesLinearlyInChunks(t *testing.T) {
	const size, chunk = 4 << 20, 4 << 10
	body := strings.Repeat("0123456789abcdef", size/16)
	input := "\x1b[200~" + body + "\x1b[201~"
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	pastes, _ := feedChunks(input, chunk)
	runtime.ReadMemStats(&after)
	if len(pastes) != 1 || pastes[0] != body {
		t.Fatalf("got %d pastes, first %d bytes; want one of %d", len(pastes), len(pastes[0]), len(body))
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 64<<20 {
		t.Fatalf("a %d-byte paste in %d-byte chunks allocated %d bytes; want linear (< 64 MiB)", size, chunk, allocated)
	}
}
