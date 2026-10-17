package tui

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// A paste that arrives over many reads gives the paste and the sequences after it that one pasteBuffer string gives
// (stdin-buffer.ts process: pasteBuffer += buffer, then indexOf(BRACKETED_PASTE_END)), when the end marker is split
// between reads and when a surrogate pair is split between reads (JavaScript joins the halves into one character).
func TestPasteOverManyReadsEqualsOnePasteBuffer(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	high, low := "\xed\xa0\xbd", "\xed\xb8\x80" // U+D83D and U+DE00, the halves of U+1F600, in WTF-8
	pieces := []string{"a", "bc", "\n", "é", "😀", high, low, "\x1b", "[", "201", "~", "\x1b[20", "1~", "\x1b[201~", "\x1b[200~", "\xff"}
	for trial := range 3000 {
		var stream strings.Builder
		stream.WriteString(bracketedPasteStart)
		for range rng.IntN(40) {
			stream.WriteString(pieces[rng.IntN(len(pieces))])
		}
		if rng.IntN(4) > 0 {
			stream.WriteString(bracketedPasteEnd + "x")
		}
		text := stream.String()
		var reads []string
		for rest := text; rest != ""; {
			n := min(len(rest), 1+rng.IntN(8))
			if reads == nil {
				n = min(len(rest), len(bracketedPasteStart)+rng.IntN(4))
			}
			reads = append(reads, rest[:n])
			rest = rest[n:]
		}

		buffer := NewStdinBuffer(StdinBufferOptions{})
		var pastes []string
		buffer.On(StdinBufferEventPaste, NewStdinBufferListener(func(s string) { pastes = append(pastes, s) }))
		var got []string
		for _, read := range reads {
			got = append(got, buffer.ProcessString(read)...)
		}

		wantOut, wantPastes := pasteByOneBuffer(reads)
		if !slices.Equal(got, wantOut) || !slices.Equal(pastes, wantPastes) {
			t.Fatalf("trial %d, reads %q:\n got %q pastes %q\nwant %q pastes %q", trial, reads, got, pastes, wantOut, wantPastes)
		}
	}
}

// pasteByOneBuffer is StdinBuffer.process for reads that start with a paste, keeping pasteBuffer as one string.
func pasteByOneBuffer(reads []string) (out, pastes []string) {
	pasteMode, pasteBuffer := false, ""
	rest := NewStdinBuffer(StdinBufferOptions{})
	rest.On(StdinBufferEventPaste, NewStdinBufferListener(func(s string) { pastes = append(pastes, s) }))
	for i, read := range reads {
		if !pasteMode && i > 0 {
			out = append(out, rest.ProcessString(read)...)
			continue
		}
		if !pasteMode {
			pasteMode, read = true, strings.TrimPrefix(jsstring.Canonical(read), bracketedPasteStart)
			if read == reads[0] {
				panic("the first read must start with the paste marker")
			}
		}
		pasteBuffer = jsstring.Canonical(pasteBuffer + jsstring.Canonical(read))
		end := strings.Index(pasteBuffer, bracketedPasteEnd)
		if end < 0 {
			continue
		}
		payload, remaining := pasteBuffer[:end], pasteBuffer[end+len(bracketedPasteEnd):]
		pasteMode, pasteBuffer = false, ""
		out = append(out, bracketedPasteStart+payload+bracketedPasteEnd)
		pastes = append(pastes, payload)
		if remaining != "" {
			out = append(out, rest.ProcessString(remaining)...)
		}
	}
	return out, pastes
}
