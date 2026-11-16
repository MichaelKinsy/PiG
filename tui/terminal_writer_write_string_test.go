// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"
	"unsafe"
)

// stringTerminal is a Terminal that keeps the string of its last Write, and the number of writes.
type stringTerminal struct {
	Terminal
	last   string
	writes int
}

// sameStringMemory reports whether two strings are the same bytes in memory, which a copy is not.
func sameStringMemory(a, b string) bool {
	return unsafe.StringData(a) == unsafe.StringData(b) //nolint:gosec // G103: compares two addresses of live strings; nothing is read through them or kept
}

func (t *stringTerminal) Write(data string) {
	t.last = data
	t.writes++
}

// A frame chunk reaches the terminal's Write as the string the renderer built: terminalWriter has a WriteString, so writeString hands the string over
// where it used to copy it into a pooled byte slice for Write and Write copied the bytes back into a new string for the terminal. The chunk is the
// same memory at the terminal (strings are immutable, a Terminal.Write takes no ownership of bytes), and the sizes cover both sides of the
// write pool's bound (maxPooledTerminalWrite), which the byte copy took one allocation less or more for.
func TestTerminalWriterHandsAFrameChunkToTheTerminalWithoutCopying(t *testing.T) {
	for _, size := range []int{1, 512, maxPooledTerminalWrite, maxPooledTerminalWrite + 1, 2 << 20} {
		chunk := strings.Repeat("x", size)
		term := &stringTerminal{}
		out := terminalWriter{term}
		if n, err := writeString(out, chunk); err != nil || n != len(chunk) {
			t.Fatalf("size %d: wrote %d bytes, error %v, want %d", size, n, err, len(chunk))
		}
		if term.writes != 1 || term.last != chunk {
			t.Fatalf("size %d: the terminal got %d writes, last of %d bytes, want one write of the chunk", size, term.writes, len(term.last))
		}
		if !sameStringMemory(term.last, chunk) {
			t.Errorf("size %d: the terminal got a copy of the chunk, want the chunk itself", size)
		}
	}
}

// WriteToTerminal, the production path of a frame chunk over a renderer's writer, reaches the terminal once and without a copy for a Terminal that is not
// a ProcessTerminal too (it encodes lone surrogates first, which returns a valid chunk as it is).
func TestWriteToTerminalOverATerminalWriterDoesNotCopyAValidChunk(t *testing.T) {
	chunk := "\x1b[38;2;1;2;3m" + strings.Repeat("token ", 100) + "\x1b[0m\r\n"
	term := &stringTerminal{}
	WriteToTerminal(terminalWriter{term}, chunk)
	if term.writes != 1 || term.last != chunk {
		t.Fatalf("the terminal got %d writes, last %q, want one write of the chunk", term.writes, term.last)
	}
	if !sameStringMemory(term.last, chunk) {
		t.Error("the terminal got a copy of the chunk, want the chunk itself")
	}
}
