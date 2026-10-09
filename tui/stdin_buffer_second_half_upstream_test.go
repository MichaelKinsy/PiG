// SPDX-FileCopyrightText: Copyright (c) 2025 opentui
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The second half is cases 30..59 in the pinned file's declaration order (lines 278..515). The first owner retains cases 1..29.
func TestStdinBufferSecondHalfSequences(t *testing.T) {
	for _, tc := range []struct {
		line         int
		name         string
		chunks, want []string
	}{
		{278, "should handle plain characters mixed with Kitty sequences", []string{"a\x1b[97;1:3u"}, []string{"a", "\x1b[97;1:3u"}},
		{284, "should drop raw duplicate character after matching Kitty printable sequence", []string{"\x1b[224uà"}, []string{"\x1b[224u"}},
		{289, "should drop raw duplicate character after matching Kitty printable sequence across chunks", []string{"\x1b[64u", "@"}, []string{"\x1b[64u"}},
		{295, "should keep non-matching plain character after Kitty printable sequence", []string{"\x1b[97ub"}, []string{"\x1b[97u", "b"}},
		{300, "should keep raw character after modified Kitty printable sequence", []string{"\x1b[64;3u@"}, []string{"\x1b[64;3u", "@"}},
		{305, "should handle rapid typing simulation with Kitty protocol", []string{"\x1b[104u\x1b[104;1:3u\x1b[105u\x1b[105;1:3u"}, []string{"\x1b[104u", "\x1b[104;1:3u", "\x1b[105u", "\x1b[105;1:3u"}},
		{313, "should handle mouse press event", []string{"\x1b[<0;10;5M"}, []string{"\x1b[<0;10;5M"}},
		{318, "should handle mouse release event", []string{"\x1b[<0;10;5m"}, []string{"\x1b[<0;10;5m"}},
		{323, "should handle mouse move event", []string{"\x1b[<35;20;5m"}, []string{"\x1b[<35;20;5m"}},
		{328, "should handle split mouse events", []string{"\x1b[<3", "5;1", "5;", "10m"}, []string{"\x1b[<35;15;10m"}},
		{336, "should handle multiple mouse events", []string{"\x1b[<35;1;1m\x1b[<35;2;2m\x1b[<35;3;3m"}, []string{"\x1b[<35;1;1m", "\x1b[<35;2;2m", "\x1b[<35;3;3m"}},
		{341, "should handle old-style mouse sequence (ESC[M + 3 bytes)", []string{"\x1b[M abc"}, []string{"\x1b[M ab", "c"}},
		{359, "should handle empty input", []string{""}, []string{""}},
		{398, "should handle very long sequences", []string{"\x1b[" + strings.Repeat("1;", 50) + "H"}, []string{"\x1b[" + strings.Repeat("1;", 50) + "H"}},
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

func TestStdinBufferSecondHalfPartialAndFlush(t *testing.T) {
	t.Run("should buffer incomplete old-style mouse sequence", func(t *testing.T) {
		// packages/tui/test/stdin-buffer.test.ts:346.
		buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
		var got []string
		for _, step := range []struct{ input, pending string }{{"\x1b[M", "\x1b[M"}, {" a", "\x1b[M a"}} {
			got = append(got, buffer.ProcessString(step.input)...)
			if buffer.GetBuffer() != step.pending {
				t.Fatalf("buffer=%q, want %q", buffer.GetBuffer(), step.pending)
			}
		}
		got = append(got, buffer.ProcessString("b")...)
		if !slices.Equal(got, []string{"\x1b[M ab"}) {
			t.Fatalf("events=%q", got)
		}
	})
	t.Run("should handle buffer input", func(t *testing.T) {
		// packages/tui/test/stdin-buffer.test.ts:393.
		buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
		if got := buffer.ProcessBytes([]byte("\x1b[A")); !slices.Equal(got, []string{"\x1b[A"}) {
			t.Fatalf("events=%q", got)
		}
	})
	for _, tc := range []struct {
		line        int
		name, input string
		want        []string
	}{
		{385, "should handle lone escape character with explicit flush", "\x1b", []string{"\x1b"}},
		{406, "should flush incomplete sequences", "\x1b[<35", []string{"\x1b[<35"}},
		{413, "should return empty array if nothing to flush", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("packages/tui/test/stdin-buffer.test.ts:%d", tc.line)
			buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
			if tc.input != "" {
				if got := buffer.ProcessString(tc.input); len(got) != 0 {
					t.Fatalf("premature events=%q", got)
				}
			}
			if got := buffer.Flush(); !slices.Equal(got, tc.want) {
				t.Fatalf("flush=%q, want %q", got, tc.want)
			}
			if buffer.GetBuffer() != "" {
				t.Fatalf("retained buffer=%q", buffer.GetBuffer())
			}
		})
	}
	t.Run("should clear buffered content without emitting", func(t *testing.T) {
		// packages/tui/test/stdin-buffer.test.ts:430.
		buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
		got := buffer.ProcessString("\x1b[<35")
		if buffer.GetBuffer() != "\x1b[<35" {
			t.Fatalf("buffer=%q", buffer.GetBuffer())
		}
		buffer.Clear()
		if buffer.GetBuffer() != "" || len(got) != 0 {
			t.Fatalf("clear retained buffer=%q or emitted=%q", buffer.GetBuffer(), got)
		}
	})
}

// The production TerminalInput owns the parser's timer and teardown in Go. Only event observation and command delivery live in this harness; it does not implement another timer or destroy operation.
type stdinSecondHalfOwner struct {
	mu       sync.Mutex
	input    *TerminalInput
	commands chan func()
	done     chan struct{}
	data     []string
	closed   bool
}

func newStdinSecondHalfOwner(t *testing.T, options StdinBufferOptions) *stdinSecondHalfOwner {
	t.Helper()
	h := &stdinSecondHalfOwner{commands: make(chan func()), done: make(chan struct{})}
	h.input = NewProcessTerminalWithOutput(nil, nil, io.Discard).NewTerminalInput(func(sequence string) { h.data = append(h.data, sequence) })
	h.input.buffer = NewStdinBuffer(options)
	go func() {
		defer close(h.done)
		for {
			select {
			case command, ok := <-h.commands:
				if !ok {
					return
				}
				h.mu.Lock()
				command()
				h.mu.Unlock()
			case <-h.input.C:
				h.mu.Lock()
				h.input.Flush()
				h.mu.Unlock()
			}
		}
	}()
	t.Cleanup(h.destroy)
	return h
}

func (h *stdinSecondHalfOwner) command(f func()) {
	ack := make(chan struct{})
	h.commands <- func() { f(); close(ack) }
	<-ack
	synctest.Wait()
}
func (h *stdinSecondHalfOwner) process(input string) {
	h.command(func() { h.input.Process([]byte(input)) })
}
func (h *stdinSecondHalfOwner) destroy() {
	if h.closed {
		return
	}
	h.command(h.input.Close)
	close(h.commands)
	<-h.done
	h.closed = true
}
func (h *stdinSecondHalfOwner) assert(t *testing.T, pending string, want ...string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if got := h.input.buffer.GetBuffer(); got != pending {
		t.Fatalf("buffer=%q, want %q", got, pending)
	}
	if !slices.Equal(h.data, want) {
		t.Fatalf("events=%q, want %q", h.data, want)
	}
}

func TestStdinBufferSecondHalfTimeoutAndDestroy(t *testing.T) {
	for _, tc := range []struct {
		line        int
		name, input string
		options     StdinBufferOptions
		wait        time.Duration
	}{
		{365, "should handle lone escape character with timeout", "\x1b", StdinBufferOptions{Timeout: 10 * time.Millisecond}, 15 * time.Millisecond},
		{374, "flushes a lone escape promptly with the longer default sequence timeout", "\x1b", StdinBufferOptions{}, 20 * time.Millisecond},
		{418, "should emit flushed data via timeout", "\x1b[<35", StdinBufferOptions{Timeout: 10 * time.Millisecond}, 15 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				t.Logf("packages/tui/test/stdin-buffer.test.ts:%d", tc.line)
				h := newStdinSecondHalfOwner(t, tc.options)
				h.process(tc.input)
				h.assert(t, tc.input)
				time.Sleep(tc.wait)
				synctest.Wait()
				h.assert(t, "", tc.input)
			})
		})
	}
	for _, tc := range []struct {
		line int
		name string
		wait time.Duration
	}{
		{507, "should clear buffer on destroy", 0},
		{515, "should clear pending timeouts on destroy", 15 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				t.Logf("packages/tui/test/stdin-buffer.test.ts:%d", tc.line)
				h := newStdinSecondHalfOwner(t, StdinBufferOptions{Timeout: 10 * time.Millisecond})
				h.process("\x1b[<35")
				h.assert(t, "\x1b[<35")
				h.destroy()
				time.Sleep(tc.wait)
				synctest.Wait()
				h.assert(t, "")
				if h.input.C != nil {
					t.Fatal("destroy retained a timer")
				}
			})
		})
	}
}

func TestStdinBufferSecondHalfPaste(t *testing.T) {
	for _, tc := range []struct {
		line                int
		name                string
		chunks, data, paste []string
	}{
		{459, "should emit paste event for complete bracketed paste", []string{"\x1b[200~hello world\x1b[201~"}, nil, []string{"hello world"}},
		{470, "should handle paste arriving in chunks", []string{"\x1b[200~", "hello ", "world\x1b[201~"}, nil, []string{"hello world"}},
		{482, "should handle paste with input before and after", []string{"a", "\x1b[200~pasted\x1b[201~", "b"}, []string{"a", "b"}, []string{"pasted"}},
		{491, "should handle paste with newlines", []string{"\x1b[200~line1\nline2\nline3\x1b[201~"}, nil, []string{"line1\nline2\nline3"}},
		{498, "should handle paste with unicode", []string{"\x1b[200~Hello 世界 🎉\x1b[201~"}, nil, []string{"Hello 世界 🎉"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("packages/tui/test/stdin-buffer.test.ts:%d", tc.line)
			buffer := NewStdinBuffer(StdinBufferOptions{Timeout: 10 * time.Millisecond})
			var data, paste []string
			for i, chunk := range tc.chunks {
				for _, event := range buffer.ProcessString(chunk) {
					// Go delivers paste as one framed payload; expose the same separate paste observation as Pi.
					if payload, ok := strings.CutPrefix(event, bracketedPasteStart); ok {
						payload, _ = strings.CutSuffix(payload, bracketedPasteEnd)
						paste = append(paste, payload)
					} else {
						data = append(data, event)
					}
				}
				if tc.line == 470 && i < len(tc.chunks)-1 && len(paste) != 0 {
					t.Fatalf("paste emitted before terminator: %q", paste)
				}
			}
			if !slices.Equal(data, tc.data) || !slices.Equal(paste, tc.paste) {
				t.Fatalf("data=%q paste=%q, want data=%q paste=%q", data, paste, tc.data, tc.paste)
			}
		})
	}
}

func BenchmarkStdinSecondHalfFlow(b *testing.B) {
	buffer := NewStdinBuffer(StdinBufferOptions{})
	b.ReportAllocs()
	for b.Loop() {
		buffer.ProcessString("\x1b[64u")
		buffer.Flush()
		buffer.ProcessString("@\x1b[<35;20;5m")
	}
}

func TestStdinBufferEmptyFlushPreservesKittyDuplicateState(t *testing.T) {
	// stdin-buffer.ts:410-423 returns before resetting the pending printable codepoint when the buffer is empty.
	buffer := NewStdinBuffer(StdinBufferOptions{})
	if got := buffer.ProcessString("\x1b[64u"); !slices.Equal(got, []string{"\x1b[64u"}) {
		t.Fatal(got)
	}
	if got := buffer.Flush(); len(got) != 0 {
		t.Fatal(got)
	}
	if got := buffer.ProcessString("@"); len(got) != 0 {
		t.Fatalf("empty flush forgot matching Kitty printable: %q", got)
	}
}

// stdin-buffer.ts:441-443 destroy() is clear(): it drops the pending partial sequence, an open bracketed paste and a pending Kitty
// printable, so bytes fed afterwards start from an empty buffer. terminal.ts:451 calls it when the terminal stops.
func TestStdinBufferDestroyClearsEveryPendingState(t *testing.T) {
	for _, c := range []struct{ name, pending string }{
		{"partial CSI", "\x1b[1;"},
		{"open bracketed paste", "\x1b[200~half a paste"},
	} {
		t.Run(c.name, func(t *testing.T) {
			buffer := NewStdinBuffer(StdinBufferOptions{})
			buffer.ProcessString(c.pending)
			buffer.Destroy()
			if got := buffer.GetBuffer(); got != "" {
				t.Fatalf("buffer after Destroy = %q", got)
			}
			if got := buffer.ProcessString("a"); len(got) != 1 || got[0] != "a" {
				t.Fatalf("input after Destroy = %q, want [a] (no stale prefix or paste)", got)
			}
		})
	}
}
