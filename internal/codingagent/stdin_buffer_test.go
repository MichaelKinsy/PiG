package codingagent

import (
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestStdinBuffer_PartialCSISequenceAcrossReads(t *testing.T) {
	var b StdinBuffer
	if got := b.ProcessString("\x1b"); len(got) != 0 {
		t.Fatalf("first chunk = %#v want nil", got)
	}
	if !b.HasPendingFlush() {
		t.Fatal("expected pending flush after partial ESC")
	}
	got := b.ProcessString("[A")
	want := []string{"\x1b[A"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("completed CSI = %#v want %#v", got, want)
	}
	if b.HasPendingFlush() {
		t.Fatal("unexpected pending flush after complete CSI")
	}
}

func TestStdinBuffer_SplitKittyNegotiationIsConsumedBeforeDispatch(t *testing.T) {
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() { tui.SetKittyProtocolActive(false) })
	var b StdinBuffer
	if got := b.ProcessString("\x1b[?"); len(got) != 0 {
		t.Fatalf("partial negotiation = %#v", got)
	}
	chunks := b.ProcessString("7u")
	if len(chunks) != 1 || !tui.HandleKeyboardProtocolNegotiationSequence(chunks[0]) {
		t.Fatalf("completed negotiation was not consumed: %#v", chunks)
	}
	if !tui.IsKittyProtocolActive() {
		t.Fatal("split Kitty response did not activate the protocol")
	}
}

func TestStdinBuffer_FlushesIncompleteEscapeRemainder(t *testing.T) {
	var b StdinBuffer
	if got := b.ProcessString("\x1b["); len(got) != 0 {
		t.Fatalf("partial CSI = %#v want nil", got)
	}
	got := b.Flush()
	want := []string{"\x1b["}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flush() = %#v want %#v", got, want)
	}
	if b.HasPendingFlush() {
		t.Fatal("buffer should be empty after flush")
	}
}

func TestStdinBuffer_HandlesOSCAndAPCSequencesAcrossReads(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  []string
	}{
		{"osc hyperlink", []string{"\x1b]8;;https://e.com", "\x07"}, []string{"\x1b]8;;https://e.com\x07"}},
		{"dcs xtversion", []string{"\x1bP>|Ghostty", "\x1b\\"}, []string{"\x1bP>|Ghostty\x1b\\"}},
		{"apc kitty", []string{"\x1b_Gi=1;OK", "\x1b\\"}, []string{"\x1b_Gi=1;OK\x1b\\"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b StdinBuffer
			var got []string
			for _, part := range tc.parts {
				got = append(got, b.ProcessString(part)...)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("events = %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestStdinBuffer_BracketedPasteAcrossReads(t *testing.T) {
	var b StdinBuffer
	var got []string
	got = append(got, b.ProcessString("ab")...)
	got = append(got, b.ProcessString("\x1b[200~hello")...)
	got = append(got, b.ProcessString("\nworld\x1b[201~z")...)
	want := []string{"a", "b", bracketedPasteStart + "hello\nworld" + bracketedPasteEnd, "z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v want %#v", got, want)
	}
}

func TestStdinBuffer_KittyPrintableDedup(t *testing.T) {
	var b StdinBuffer
	var got []string
	got = append(got, b.ProcessString("\x1b[97u")...)
	got = append(got, b.ProcessString("a")...)
	want := []string{"\x1b[97u"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kitty printable dedup = %#v want %#v", got, want)
	}
}

func TestStdinBuffer_HighByteMetaConversion(t *testing.T) {
	var b StdinBuffer
	got := b.ProcessBytes([]byte{225}) // 225-128 = 97 = 'a' => ESC+a
	want := []string{"\x1ba"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("high-byte conversion = %#v want %#v", got, want)
	}
}

func TestStdinBuffer_WezTermDoubleEscapeKittyRelease(t *testing.T) {
	var b StdinBuffer
	got, rem := extractCompleteSequences("\x1b\x1b[27;1u")
	want := []string{"\x1b", "\x1b[27;1u"}
	if !reflect.DeepEqual(got, want) || rem != "" {
		t.Fatalf("extractCompleteSequences() = (%#v, %q) want (%#v, %q)", got, rem, want, "")
	}

	var emitted []string
	emitted = append(emitted, b.ProcessString("\x1b\x1b[27;1u")...)
	if !reflect.DeepEqual(emitted, want) {
		t.Fatalf("ProcessString() = %#v want %#v", emitted, want)
	}
}

// Ports upstream stdin-buffer.test.ts old-style mouse framing: ESC[M is only
// the prefix; its three payload bytes belong to the same input sequence.
func TestStdinBuffer_OldStyleMouseSequence(t *testing.T) {
	var b StdinBuffer
	if got := b.ProcessString("\x1b[M "); len(got) != 0 {
		t.Fatalf("partial X10 mouse sequence = %#v, want no input", got)
	}
	got := b.ProcessString("!\"")
	want := []string{"\x1b[M !\""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("completed X10 mouse sequence = %#v, want %#v", got, want)
	}
}

func TestExtractCompleteSequences(t *testing.T) {
	cases := []struct {
		name          string
		buffer        string
		wantSeq       []string
		wantRemainder string
	}{
		{"plain chars", "ab", []string{"a", "b"}, ""},
		{"complete esc + plain", "\x1b[Ax", []string{"\x1b[A", "x"}, ""},
		{"incomplete esc", "\x1b[", nil, "\x1b["},
		{"mixed prefix then incomplete", "a\x1b[", []string{"a"}, "\x1b["},
		// ESC+ESC at the buffer tail (no following byte): must not panic
		// on the remaining[seqEnd] peek. Matches upstream's undefined
		// nextChar fall-through (emit the double-ESC as one sequence).
		{"double esc at tail", "\x1b\x1b", []string{"\x1b\x1b"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSeq, gotRem := extractCompleteSequences(tc.buffer)
			if !reflect.DeepEqual(gotSeq, tc.wantSeq) || gotRem != tc.wantRemainder {
				t.Fatalf("extractCompleteSequences(%q) = (%#v, %q) want (%#v, %q)", tc.buffer, gotSeq, gotRem, tc.wantSeq, tc.wantRemainder)
			}
		})
	}
}

// Ports upstream stdin-buffer.test.ts "should handle unicode characters":
// regular input is emitted one character at a time, not one byte at a time.
func TestStdinBuffer_UnicodeCharacters(t *testing.T) {
	var b StdinBuffer
	got := b.ProcessString("hello 世界")
	want := []string{"h", "e", "l", "l", "o", " ", "世", "界"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProcessString() = %q want %q", got, want)
	}
}

// Kitty printable dedup compares one emitted character with the code point of
// the preceding CSI-u key, so it needs whole characters for non-ASCII text.
func TestStdinBuffer_KittyPrintableDedupNonASCII(t *testing.T) {
	var b StdinBuffer
	got := b.ProcessString("\x1b[233ué")
	want := []string{"\x1b[233u"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kitty printable dedup = %q want %q", got, want)
	}
}

// Upstream StdinBuffer waits DEFAULT_SEQUENCE_TIMEOUT_MS (50ms) for the rest of
// an incomplete sequence and DEFAULT_ESCAPE_TIMEOUT_MS (10ms) for a lone ESC;
// each option overrides only its own case ("does not apply the sequence
// timeout to a lone ESC", "should merge ESC + CR split across chunks within a
// larger timeout").
func TestStdinBuffer_FlushTimeout(t *testing.T) {
	cases := []struct {
		name    string
		options StdinBufferOptions
		input   string
		want    time.Duration
	}{
		{"default sequence", StdinBufferOptions{}, "\x1b[<35", 50 * time.Millisecond},
		{"default lone ESC", StdinBufferOptions{}, "\x1b", 10 * time.Millisecond},
		{"sequence option skips lone ESC", StdinBufferOptions{Timeout: 100 * time.Millisecond}, "\x1b", 10 * time.Millisecond},
		{"sequence option", StdinBufferOptions{Timeout: 100 * time.Millisecond}, "\x1b[<35", 100 * time.Millisecond},
		{"escape option", StdinBufferOptions{EscapeTimeout: 100 * time.Millisecond}, "\x1b", 100 * time.Millisecond},
		{"escape option skips sequences", StdinBufferOptions{EscapeTimeout: 100 * time.Millisecond}, "\x1b[<35", 50 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewStdinBuffer(tc.options)
			if got := b.ProcessString(tc.input); len(got) != 0 {
				t.Fatalf("incomplete input emitted %q", got)
			}
			if got := b.FlushTimeout(); got != tc.want {
				t.Fatalf("FlushTimeout() = %v want %v", got, tc.want)
			}
		})
	}
	var zero StdinBuffer
	zero.ProcessString("\x1b[<35")
	if got := zero.FlushTimeout(); got != 50*time.Millisecond {
		t.Fatalf("zero-value FlushTimeout() = %v want 50ms", got)
	}
}

// Terminal loops resolve the lone-ESC timeout like upstream ProcessTerminal.
func TestNewProcessStdinBufferUsesResolvedEscapeTimeout(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("PI_TUI_ESC_TIMEOUT", "80")
	b := newProcessStdinBuffer()
	b.ProcessString("\x1b")
	if got := b.FlushTimeout(); got != 80*time.Millisecond {
		t.Fatalf("lone ESC FlushTimeout() = %v want 80ms", got)
	}
	b.ProcessString("[<35")
	if got := b.FlushTimeout(); got != 50*time.Millisecond {
		t.Fatalf("sequence FlushTimeout() = %v want 50ms", got)
	}
}

// A split multi-byte character waits for its remaining bytes, as upstream's
// utf8-encoded stdin does; the continuation byte arriving alone in a read is
// not rewritten into an ESC-prefixed Meta key (0x94 would become ESC+Ctrl-T,
// and 0x8D would become ESC+CR, which is a newline).
func TestStdinBuffer_ProcessTerminalBytesHoldsSplitCharacter(t *testing.T) {
	b := newProcessStdinBuffer()
	var got []string
	got = append(got, b.ProcessTerminalBytes([]byte("a\xe2"))...)
	got = append(got, b.ProcessTerminalBytes([]byte("\x80"))...)
	if b.HasPendingFlush() {
		t.Fatal("an incomplete character must not arm the escape-sequence flush")
	}
	got = append(got, b.ProcessTerminalBytes([]byte("\x94"))...)
	got = append(got, b.ProcessTerminalBytes([]byte("\xe2\x80\x8d\r"))...)
	want := []string{"a", "—", "\u200d", "\r"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProcessTerminalBytes() = %q want %q", got, want)
	}
}

// A timed-out flush and waiting input can both be ready when the loop was busy.
// The input must win so a split sequence completes instead of its prefix being
// flushed as a separate key.
func TestPriorityInputPrefersWaitingInputOverFlush(t *testing.T) {
	readCh := make(chan []byte, 1)
	flushC := make(chan time.Time, 1)
	for range 200 {
		readCh <- []byte("[A")
		flushC <- time.Time{}
		if kind, buf := priorityInput(readCh, flushC); kind != priorityInputRead || string(buf) != "[A" {
			t.Fatalf("priorityInput() = (%v, %q), want the waiting read", kind, buf)
		}
		if kind, _ := priorityInput(readCh, flushC); kind != priorityInputFlush {
			t.Fatalf("priorityInput() = %v, want the flush once input is drained", kind)
		}
	}
	if kind, _ := priorityInput(readCh, flushC); kind != priorityInputNone {
		t.Fatalf("priorityInput() = %v with nothing ready", kind)
	}
	close(readCh)
	if kind, _ := priorityInput(readCh, flushC); kind != priorityInputClosed {
		t.Fatalf("priorityInput() = %v on a closed reader", kind)
	}
}

// The flush timer fires exactly FlushTimeout after the last input and is
// disarmed once the buffer is empty again.
func TestStdinFlushTimerArmsForPendingSequence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := NewStdinBuffer(StdinBufferOptions{})
		var flush stdinFlushTimer
		b.ProcessString("\x1b[<35")
		flush.sync(b)
		start := time.Now()
		<-flush.C
		if elapsed := time.Since(start); elapsed != 50*time.Millisecond {
			t.Fatalf("flush fired after %v, want 50ms", elapsed)
		}
		flush.stop()
		if got := b.Flush(); !reflect.DeepEqual(got, []string{"\x1b[<35"}) {
			t.Fatalf("Flush() = %q", got)
		}
		flush.sync(b)
		if flush.C != nil {
			t.Fatal("flush timer armed with nothing pending")
		}
	})
}
