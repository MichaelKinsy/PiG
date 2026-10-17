// SPDX-FileCopyrightText: Copyright (c) 2025 opentui
// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// stdin-buffer.test.ts describe("Terminal Reply Strings") (Pi 1.2.0). The production TerminalInput owns the parser's timer; the
// sequences are observed on the StdinBuffer "data" event as upstream buffer.on("data") does, before terminal-level routing.

var terminalReplyCases = []struct{ header, reply string }{
	{"\x1b]4;1;", "\x1b]4;1;rgb:eaea/6c6c/6d6d\x07"}, // Ghostty's Ayu Light palette color 1.
	{"\x1b]10;", "\x1b]10;rgb:ffff/ffff/ffff\x1b\\"},
	{"\x1b]11;", "\x1b]11;rgb:0000/0000/0000\x07"},
	{"\x1b]4;1;", "\x1b]4;1;rgb:6464/6464/4d4d\x07"}, // Another reported color-reply tail.
	{"\x1bP>|", "\x1bP>|Ghostty 1.3.1\x1b\\"},
	{"\x1b_Gi=1;", "\x1b_Gi=1;OK\x1b\\"},
	{"\x1b]7501;?", "\x1b]7501;?\x1b\\"},
}

type terminalReplyOwner struct {
	*stdinSecondHalfOwner
	sequences []string
}

// newTerminalReplyOwner builds a fresh buffer; timeout 0 is `new StdinBuffer()`, 10ms is the suite's beforeEach buffer.
func newTerminalReplyOwner(t *testing.T, timeout time.Duration) *terminalReplyOwner {
	h := &terminalReplyOwner{stdinSecondHalfOwner: newStdinSecondHalfOwner(t, StdinBufferOptions{Timeout: timeout})}
	h.input.buffer.On(StdinBufferEventData, NewStdinBufferListener(func(data string) { h.sequences = append(h.sequences, data) }))
	return h
}

func (h *terminalReplyOwner) tick(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

func (h *terminalReplyOwner) want(t *testing.T, want ...string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Equal(h.sequences, want) {
		t.Fatalf("sequences=%q, want %q", h.sequences, want)
	}
}

func TestStdinBufferTerminalReplyStrings(t *testing.T) {
	for _, tc := range terminalReplyCases {
		t.Run("keeps delayed "+tc.reply+" intact at every split after its reply header", func(t *testing.T) {
			for split := len(tc.header); split < len(tc.reply); split++ {
				synctest.Test(t, func(t *testing.T) {
					h := newTerminalReplyOwner(t, 0)
					h.process(tc.reply[:split])
					h.tick(5000 * time.Millisecond)
					h.process(tc.reply[split:] + "x")
					for _, sequence := range h.sequences {
						if MatchesKeyID(sequence, "ctrl+g") {
							t.Fatalf("split %d emitted ctrl+g: %q", split, h.sequences)
						}
					}
					h.want(t, tc.reply, "x")
				})
			}
		})
	}

	t.Run("keeps an OSC reply buffered across multiple expired sequence timeouts", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newTerminalReplyOwner(t, 10*time.Millisecond)
			h.process("\x1b]4;1;rgb:eaea/6c")
			h.tick(100 * time.Millisecond)
			h.want(t)
			h.process("6c/6d6d")
			h.tick(100 * time.Millisecond)
			h.want(t)
			h.process("\x07")
			h.want(t, terminalReplyCases[0].reply)
		})
	})

	t.Run("still times out bare string introducers as Alt keys", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newTerminalReplyOwner(t, 10*time.Millisecond)
			for _, prefix := range []string{"\x1b]", "\x1bP", "\x1b_"} {
				h.process(prefix)
				h.tick(10 * time.Millisecond)
			}
			h.want(t, "\x1b]", "\x1bP", "\x1b_")
		})
	})

	for _, ambiguous := range []string{"\x1b]a", "\x1b]1", "\x1b]10", "\x1b]0;title", "\x1b]4;", "\x1b]4;a;", "\x1bPa", "\x1bP>", "\x1b_a", "\x1b_G", "\x1b_Gi=1"} {
		t.Run("recovers batched legacy Alt input "+ambiguous+" within the sequence timeout", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTerminalReplyOwner(t, 0)
				h.process(ambiguous)
				h.tick(20 * time.Millisecond)
				h.process("x\r\x03\x04")
				h.tick(49 * time.Millisecond)
				h.want(t)
				h.tick(1 * time.Millisecond)
				want := []string{ambiguous[:2]}
				for _, c := range ambiguous[2:] {
					want = append(want, string(c))
				}
				want = append(want, "x", "\r", "\x03", "\x04")
				h.want(t, want...)
				if got := h.input.buffer.GetBuffer(); got != "" {
					t.Fatalf("buffer=%q, want empty", got)
				}
				h.process("y\r\x03\x04")
				h.mu.Lock()
				tail := slices.Clone(h.sequences[len(h.sequences)-4:])
				h.mu.Unlock()
				if !slices.Equal(tail, []string{"y", "\r", "\x03", "\x04"}) {
					t.Fatalf("tail=%q", tail)
				}
			})
		})
	}

	t.Run("recognizes a reply header assembled within the ambiguous-input timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newTerminalReplyOwner(t, 10*time.Millisecond)
			h.process("\x1b]1")
			h.tick(5 * time.Millisecond)
			h.process("0;")
			h.tick(5000 * time.Millisecond)
			h.want(t)
			h.process("rgb:ffff/ffff/ffff\x1b\\")
			h.want(t, terminalReplyCases[1].reply)
		})
	})

	t.Run("recovers from an unterminated reply when a new escape sequence arrives", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newTerminalReplyOwner(t, 10*time.Millisecond)
			h.process("\x1b]4;1;rgb:eaea/6c")
			h.tick(100 * time.Millisecond)
			h.process("\x1b")
			h.tick(100 * time.Millisecond)
			h.process("[Ahello")
			h.want(t, strings.Split("\x1b[A|h|e|l|l|o", "|")...)
		})
	})

	t.Run("discards a canceled reply without swallowing subsequent input", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newTerminalReplyOwner(t, 10*time.Millisecond)
			for _, cancel := range []string{"\x18", "\x1a"} {
				h.process("\x1b]11;rgb:0000" + cancel + "x")
			}
			h.want(t, "x", "x")
		})
	})
}
