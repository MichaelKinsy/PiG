// SPDX-FileCopyrightText: Copyright (c) 2025 opentui
// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// Cases 1..29 of the pinned stdin-buffer.test.ts in declaration order (lines 38..276): Regular Characters, Complete Escape Sequences, Partial Escape Sequences, Mixed Content, and the first nine Kitty Keyboard Protocol cases. Cases 30..59 live in stdin_buffer_second_half_upstream_test.go. The timer-driven cases run through the production TerminalInput timer owner under synctest.
func TestStdinBufferFirstHalfSequences(t *testing.T) {
	for _, tc := range []struct {
		line         int
		name         string
		chunks, want []string
	}{
		{38, "should pass through regular characters immediately", []string{"a"}, []string{"a"}},
		{43, "should pass through multiple regular characters", []string{"abc"}, []string{"a", "b", "c"}},
		{48, "should handle unicode characters", []string{"hello 世界"}, []string{"h", "e", "l", "l", "o", " ", "世", "界"}},
		{55, "should pass through complete mouse SGR sequences", []string{"\x1b[<35;20;5m"}, []string{"\x1b[<35;20;5m"}},
		{61, "should pass through complete arrow key sequences", []string{"\x1b[A"}, []string{"\x1b[A"}},
		{67, "should pass through complete function key sequences", []string{"\x1b[11~"}, []string{"\x1b[11~"}},
		{73, "should pass through meta key sequences", []string{"\x1ba"}, []string{"\x1ba"}},
		{79, "should pass through SS3 sequences", []string{"\x1bOA"}, []string{"\x1bOA"}},
		{101, "should buffer incomplete CSI sequence", []string{"\x1b[", "1;", "5H"}, []string{"\x1b[1;5H"}},
		{112, "should buffer split across many chunks", []string{"\x1b", "[", "<", "3", "5", ";", "2", "0", ";", "5", "m"}, []string{"\x1b[<35;20;5m"}},
		{196, "should handle characters followed by escape sequence", []string{"abc\x1b[A"}, []string{"a", "b", "c", "\x1b[A"}},
		{201, "should handle escape sequence followed by characters", []string{"\x1b[Aabc"}, []string{"\x1b[A", "a", "b", "c"}},
		{206, "should handle multiple complete sequences", []string{"\x1b[A\x1b[B\x1b[C"}, []string{"\x1b[A", "\x1b[B", "\x1b[C"}},
		{222, "should handle Kitty CSI u press events", []string{"\x1b[97u"}, []string{"\x1b[97u"}},
		{228, "should handle Kitty CSI u release events", []string{"\x1b[97;1:3u"}, []string{"\x1b[97;1:3u"}},
		{234, "should handle batched Kitty press and release", []string{"\x1b[97u\x1b[97;1:3u"}, []string{"\x1b[97u", "\x1b[97;1:3u"}},
		{240, "should handle multiple batched Kitty events", []string{"\x1b[97u\x1b[97;1:3u\x1b[98u\x1b[98;1:3u"}, []string{"\x1b[97u", "\x1b[97;1:3u", "\x1b[98u", "\x1b[98;1:3u"}},
		{246, "should handle Kitty arrow keys with event type", []string{"\x1b[1;1:1A"}, []string{"\x1b[1;1:1A"}},
		{252, "should handle Kitty functional keys with event type", []string{"\x1b[3;1:3~"}, []string{"\x1b[3;1:3~"}},
		{258, "should split ESC+ESC+CSI into standalone ESC and the CSI sequence (WezTerm Escape key regression)", []string{"\x1b\x1b[27;129:3u"}, []string{"\x1b", "\x1b[27;129:3u"}},
		{267, "should split ESC+ESC+CSI with no modifier (no num_lock)", []string{"\x1b\x1b[27;1:3u"}, []string{"\x1b", "\x1b[27;1:3u"}},
		{272, "should still emit ESC+ESC as a single sequence when not followed by a new escape", []string{"\x1b\x1b"}, []string{"\x1b\x1b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("packages/tui/test/stdin-buffer.test.ts:%d", tc.line)
			buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
			var got []string
			for _, chunk := range tc.chunks {
				got = append(got, buffer.ProcessString(chunk)...)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("events=%q, want %q", got, tc.want)
			}
		})
	}
}

// Pi source: packages/tui/src/stdin-buffer.ts
// mutation-checked: zeroing the results of StdinBuffer.GetBuffer fails it
func TestStdinBufferFirstHalfPartialWithPendingBuffer(t *testing.T) {
	// packages/tui/test/stdin-buffer.test.ts:87 (should buffer incomplete mouse SGR sequence).
	t.Run("should buffer incomplete mouse SGR sequence", func(t *testing.T) {
		buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
		var got []string
		for _, step := range []struct {
			input, pending string
			emitted        []string
		}{
			{"\x1b", "\x1b", nil},
			{"[<35", "\x1b[<35", nil},
			{";20;5m", "", []string{"\x1b[<35;20;5m"}},
		} {
			got = append(got, buffer.ProcessString(step.input)...)
			if !slices.Equal(got, step.emitted) || buffer.GetBuffer() != step.pending {
				t.Fatalf("after %q: events=%q buffer=%q, want events=%q buffer=%q", step.input, got, buffer.GetBuffer(), step.emitted, step.pending)
			}
		}
	})
	// packages/tui/test/stdin-buffer.test.ts:211 (should handle partial sequence with preceding characters).
	t.Run("should handle partial sequence with preceding characters", func(t *testing.T) {
		buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
		got := buffer.ProcessString("abc\x1b[<35")
		if !slices.Equal(got, []string{"a", "b", "c"}) || buffer.GetBuffer() != "\x1b[<35" {
			t.Fatalf("events=%q buffer=%q", got, buffer.GetBuffer())
		}
		got = append(got, buffer.ProcessString(";20;5m")...)
		if !slices.Equal(got, []string{"a", "b", "c", "\x1b[<35;20;5m"}) {
			t.Fatalf("events=%q", got)
		}
	})
}

func TestStdinBufferFirstHalfTimers(t *testing.T) {
	// Pi runs this file with legacy key matching, independent of terminal negotiation.
	kitty := IsKittyProtocolActive()
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKittyProtocolActive(kitty) })
	run := func(t *testing.T, line int, options StdinBufferOptions, body func(t *testing.T, h *stdinSecondHalfOwner)) {
		t.Helper()
		synctest.Test(t, func(t *testing.T) {
			t.Logf("packages/tui/test/stdin-buffer.test.ts:%d", line)
			body(t, newStdinSecondHalfOwner(t, options))
		})
	}
	sleep := func(d time.Duration) {
		time.Sleep(d)
		synctest.Wait()
	}
	// upstream:128 should flush incomplete sequence after timeout.
	t.Run("should flush incomplete sequence after timeout", func(t *testing.T) {
		run(t, 128, StdinBufferOptions{Timeout: 10 * time.Millisecond}, func(t *testing.T, h *stdinSecondHalfOwner) {
			h.process("\x1b[<35")
			h.assert(t, "\x1b[<35")
			sleep(15 * time.Millisecond)
			h.assert(t, "", "\x1b[<35")
		})
	})
	// upstream:138 should flush a lone ESC as Escape when CR arrives after the timeout. Legacy Alt+Enter is ESC + CR; a transport split wider than the timeout flushes ESC alone.
	t.Run("should flush a lone ESC as Escape when CR arrives after the timeout", func(t *testing.T) {
		run(t, 138, StdinBufferOptions{Timeout: 10 * time.Millisecond}, func(t *testing.T, h *stdinSecondHalfOwner) {
			h.process("\x1b")
			sleep(20 * time.Millisecond)
			h.process("\r")
			h.assert(t, "", "\x1b", "\r")
			if !MatchesKeyID(h.data[0], "escape") {
				t.Fatalf("first event %q is not escape", h.data[0])
			}
		})
	})
	// upstream:151 should merge ESC + CR split across chunks within a larger timeout.
	t.Run("should merge ESC + CR split across chunks within a larger timeout", func(t *testing.T) {
		run(t, 151, StdinBufferOptions{EscapeTimeout: 100 * time.Millisecond}, func(t *testing.T, h *stdinSecondHalfOwner) {
			h.process("\x1b")
			sleep(20 * time.Millisecond)
			h.process("\r")
			h.assert(t, "", "\x1b\r")
			if !MatchesKeyID(h.data[0], "alt+enter") {
				t.Fatalf("event %q is not alt+enter", h.data[0])
			}
		})
	})
	// upstream:166 does not apply the sequence timeout to a lone ESC.
	t.Run("does not apply the sequence timeout to a lone ESC", func(t *testing.T) {
		run(t, 166, StdinBufferOptions{Timeout: 100 * time.Millisecond}, func(t *testing.T, h *stdinSecondHalfOwner) {
			h.process("\x1b")
			sleep(20 * time.Millisecond)
			h.process("\r")
			h.assert(t, "", "\x1b", "\r")
			if !MatchesKeyID(h.data[0], "escape") {
				t.Fatalf("first event %q is not escape", h.data[0])
			}
		})
	})
	// upstream:181 keeps fragmented mouse sequences buffered across delayed chunks by default.
	t.Run("keeps fragmented mouse sequences buffered across delayed chunks by default", func(t *testing.T) {
		run(t, 181, StdinBufferOptions{}, func(t *testing.T, h *stdinSecondHalfOwner) {
			h.process("\x1b[")
			sleep(20 * time.Millisecond)
			h.assert(t, "\x1b[")
			h.process("<65;48;39M")
			h.assert(t, "", "\x1b[<65;48;39M")
		})
	})
}
