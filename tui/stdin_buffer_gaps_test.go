package tui

import (
	"slices"
	"testing"
	"time"
)

func collectStdin(b *StdinBuffer) (data, pastes *[]string) {
	data, pastes = &[]string{}, &[]string{}
	b.On(StdinBufferEventData, NewStdinBufferListener(func(s string) { *data = append(*data, s) }))
	b.On(StdinBufferEventPaste, NewStdinBufferListener(func(s string) { *pastes = append(*pastes, s) }))
	return data, pastes
}

// stdin-buffer.ts: the flush timeout is the escape timeout for a lone ESC and the sequence timeout for any other incomplete input, each defaulting when unset.
// Pi: packages/tui/src/stdin-buffer.ts (timeout, escapeTimeout options; process).
func TestStdinBufferFlushTimeoutFollowsTheBufferedRemainder(t *testing.T) {
	b := NewStdinBuffer(StdinBufferOptions{Timeout: 7 * time.Millisecond, EscapeTimeout: 3 * time.Millisecond})
	b.ProcessString("\x1b")
	if got := b.FlushTimeout(); got != 3*time.Millisecond {
		t.Errorf("lone ESC timeout = %v, want 3ms", got)
	}
	b.Clear()
	b.ProcessString("\x1b[1;")
	if got := b.FlushTimeout(); got != 7*time.Millisecond {
		t.Errorf("incomplete sequence timeout = %v, want 7ms", got)
	}
	d := NewStdinBuffer(StdinBufferOptions{})
	d.ProcessString("\x1b")
	if got := d.FlushTimeout(); got != defaultEscapeTimeout {
		t.Errorf("default escape timeout = %v", got)
	}
	d.Clear()
	d.ProcessString("\x1b[1;")
	if got := d.FlushTimeout(); got != defaultSequenceTimeout {
		t.Errorf("default sequence timeout = %v", got)
	}
}

// Terminal reads decode UTF-8 across reads: a character split between reads is held until complete, and only a trailing incomplete sequence is held.
// Pi: packages/tui/src/terminal.ts stdin.setEncoding("utf8"), stdin-buffer.ts process.
func TestStdinBufferHoldsSplitUTF8CharactersAcrossReads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reads  [][]byte
		events []string
	}{
		{"three-byte split 2+1", [][]byte{{0xE4, 0xB8}, {0xAD}}, []string{"中"}},
		{"three-byte split 1+2", [][]byte{{0xE4}, {0xB8, 0xAD}}, []string{"中"}},
		{"four-byte split 3+1", [][]byte{{0xF0, 0x9F, 0x98}, {0x80}}, []string{"\xed\xa0\xbd", "\xed\xb8\x80"}},
		{"two-byte split", [][]byte{{'a', 0xC3}, {0xA9}}, []string{"a", "é"}},
		{"complete character is not held", [][]byte{[]byte("é中")}, []string{"é", "中"}},
		{"ascii after a complete multibyte", [][]byte{[]byte("中a")}, []string{"中", "a"}},
	} {
		b := NewStdinBuffer(StdinBufferOptions{})
		var got []string
		for _, read := range tc.reads {
			got = append(got, b.ProcessTerminalBytes(read)...)
		}
		if !slices.Equal(got, tc.events) {
			t.Errorf("%s: events %q, want %q", tc.name, got, tc.events)
		}
	}
	// A read of only the head of a character emits nothing and holds it.
	b := NewStdinBuffer(StdinBufferOptions{})
	if got := b.ProcessTerminalBytes([]byte{0xE4, 0xB8}); got != nil {
		t.Errorf("a partial character emitted %q", got)
	}
	b.Clear()
	if got := b.ProcessTerminalBytes([]byte("a")); !slices.Equal(got, []string{"a"}) {
		t.Errorf("Clear left a held partial character: %q", got)
	}
	for _, tc := range []struct {
		data []byte
		want int
	}{
		{nil, 0}, {[]byte("a"), 0}, {[]byte("a\xe4"), 1}, {[]byte("a\xe4\xb8"), 2}, {[]byte("\xe4\xb8\xad"), 0},
		{[]byte("\xf0\x9f\x98"), 3}, {[]byte("\xf0\x9f\x98\x80"), 0}, {[]byte("\xc3"), 1}, {[]byte("\x80"), 0}, {[]byte("\xf0\x9f\x98\x80\xc3"), 1},
	} {
		if got := incompleteUTF8Suffix(tc.data); got != tc.want {
			t.Errorf("incompleteUTF8Suffix(%q) = %d, want %d", tc.data, got, tc.want)
		}
	}
}

// stdin-buffer.ts process(Buffer): empty input with nothing buffered is one empty event, empty input with a buffered remainder is nothing, and a single high byte is a Meta key (ESC plus the byte minus 128). A supplementary character is emitted as its two UTF-16 units.
// Pi: packages/tui/src/stdin-buffer.ts process.
func TestStdinBufferProcessBytesAndEmptyInput(t *testing.T) {
	b := NewStdinBuffer(StdinBufferOptions{})
	if got := b.ProcessBytes(nil); !slices.Equal(got, []string{""}) {
		t.Errorf("empty bytes with an empty buffer = %q, want one empty event", got)
	}
	if got := b.ProcessString(""); !slices.Equal(got, []string{""}) {
		t.Errorf("empty string with an empty buffer = %q, want one empty event", got)
	}
	b.ProcessString("\x1b")
	if got := b.ProcessBytes(nil); got != nil {
		t.Errorf("empty bytes with a buffered remainder = %q, want none", got)
	}
	if got := b.ProcessString(""); got != nil {
		t.Errorf("empty string with a buffered remainder = %q, want none", got)
	}
	if b.GetBuffer() != "\x1b" || !b.HasPendingFlush() {
		t.Errorf("buffer %q pending %v", b.GetBuffer(), b.HasPendingFlush())
	}
	b.Clear()
	for _, tc := range []struct {
		data byte
		want string
	}{{0xE1, "\x1ba"}, {0x80, "\x1b\x00"}, {0xFF, "\x1b\x7f"}} {
		fresh := NewStdinBuffer(StdinBufferOptions{})
		if got := fresh.ProcessBytes([]byte{tc.data}); !slices.Equal(got, []string{tc.want}) {
			t.Errorf("single byte %#x = %q, want %q", tc.data, got, tc.want)
		}
	}
	if got := NewStdinBuffer(StdinBufferOptions{}).ProcessBytes([]byte{0x7f}); !slices.Equal(got, []string{"\x7f"}) {
		t.Errorf("DEL byte = %q", got)
	}
}

// stdin-buffer.ts bracketed paste: a paste is one framed payload (and one paste event) whether it arrives in one read or several, text before it is emitted first and an incomplete sequence before it is dropped, text after it is processed, paste content never shows in the buffer, and the paste and Kitty-duplicate state are reset when it ends.
// Pi: packages/tui/src/stdin-buffer.ts process (bracketed paste).
func TestStdinBufferBracketedPasteFramingStateAndEvents(t *testing.T) {
	const start, end = "\x1b[200~", "\x1b[201~"
	b := NewStdinBuffer(StdinBufferOptions{})
	data, pastes := collectStdin(b)

	if got := b.ProcessString(start + "hello" + end); !slices.Equal(got, []string{start + "hello" + end}) || !slices.Equal(*pastes, []string{"hello"}) {
		t.Fatalf("single-read paste = %q, events %q", got, *pastes)
	}
	if len(*data) != 0 {
		t.Errorf("a paste emitted data events %q", *data)
	}
	*pastes = nil

	// Split paste: nothing is emitted or buffered until the end marker, then the paste and the text after it.
	if got := b.ProcessString(start + "a"); got != nil || b.GetBuffer() != "" || b.HasPendingFlush() {
		t.Fatalf("paste in progress: out %q buffer %q pending %v", got, b.GetBuffer(), b.HasPendingFlush())
	}
	if got := b.ProcessString("b"); got != nil {
		t.Fatalf("paste continuation = %q", got)
	}
	got := b.ProcessString("c" + end + "xy")
	if want := []string{start + "abc" + end, "x", "y"}; !slices.Equal(got, want) || !slices.Equal(*pastes, []string{"abc"}) {
		t.Fatalf("split paste end = %q (events %q), want %q", got, *pastes, want)
	}
	*pastes = nil
	// The paste state is cleared: ordinary input is data again, and a second paste starts from nothing.
	if got := b.ProcessString("z"); !slices.Equal(got, []string{"z"}) {
		t.Errorf("input after a paste = %q", got)
	}
	if got := b.ProcessString(start + "second" + end); !slices.Equal(got, []string{start + "second" + end}) || !slices.Equal(*pastes, []string{"second"}) {
		t.Errorf("second paste = %q events %q", got, *pastes)
	}
	*pastes = nil

	// Text before the start marker is emitted first; an incomplete sequence before it is dropped.
	if got := b.ProcessString("ab" + start + "p" + end); !slices.Equal(got, []string{"a", "b", start + "p" + end}) {
		t.Errorf("text before a paste = %q", got)
	}
	if got := b.ProcessString("\x1b[1;" + start + "q" + end); !slices.Equal(got, []string{start + "q" + end}) {
		t.Errorf("incomplete sequence before a paste = %q, want it dropped", got)
	}
	// A split paste that carries text before its start marker and remaining text.
	*pastes = nil
	if got := b.ProcessString("m" + start + "r"); !slices.Equal(got, []string{"m"}) {
		t.Errorf("text before a split paste = %q", got)
	}
	if got := b.ProcessString(end); !slices.Equal(got, []string{start + "r" + end}) || !slices.Equal(*pastes, []string{"r"}) {
		t.Errorf("split paste completion = %q events %q", got, *pastes)
	}

	// The Kitty duplicate suppression does not survive a paste.
	k := NewStdinBuffer(StdinBufferOptions{})
	if got := k.ProcessString("\x1b[97u"); !slices.Equal(got, []string{"\x1b[97u"}) {
		t.Fatalf("kitty press = %q", got)
	}
	k.ProcessString(start + "p" + end)
	if got := k.ProcessString("a"); !slices.Equal(got, []string{"a"}) {
		t.Errorf("raw text after a paste following a Kitty press = %q, want it emitted", got)
	}
	k2 := NewStdinBuffer(StdinBufferOptions{})
	k2.ProcessString("\x1b[97u")
	k2.ProcessString(start + "p")
	k2.ProcessString(end)
	if got := k2.ProcessString("a"); !slices.Equal(got, []string{"a"}) {
		t.Errorf("raw text after a split paste following a Kitty press = %q, want it emitted", got)
	}
}

// stdin-buffer.ts clear/destroy: Clear drops the buffered remainder, paste mode, paste content, the Kitty duplicate state and a held partial character; Flush emits the remainder as one event.
// Pi: packages/tui/src/stdin-buffer.ts clear, flush, destroy.
func TestStdinBufferClearAndFlushResetEveryPendingState(t *testing.T) {
	k := NewStdinBuffer(StdinBufferOptions{})
	k.ProcessString("\x1b[97u")
	k.Clear()
	if got := k.ProcessString("a"); !slices.Equal(got, []string{"a"}) {
		t.Errorf("Clear kept the Kitty duplicate state: %q", got)
	}

	p := NewStdinBuffer(StdinBufferOptions{})
	p.ProcessString("\x1b[200~partial")
	p.Clear()
	if got := p.ProcessString("b"); !slices.Equal(got, []string{"b"}) {
		t.Errorf("Clear kept paste mode: %q", got)
	}
	p.ProcessString("\x1b[200~old")
	p.Clear()
	if got := p.ProcessString("\x1b[200~new\x1b[201~"); !slices.Equal(got, []string{"\x1b[200~new\x1b[201~"}) {
		t.Errorf("Clear kept paste content: %q", got)
	}

	f := NewStdinBuffer(StdinBufferOptions{})
	data, _ := collectStdin(f)
	f.ProcessString("\x1b[1;")
	if got := f.Flush(); !slices.Equal(got, []string{"\x1b[1;"}) || !slices.Equal(*data, []string{"\x1b[1;"}) || f.GetBuffer() != "" || f.HasPendingFlush() {
		t.Errorf("Flush = %q (events %q), buffer %q", got, *data, f.GetBuffer())
	}
	if got := f.Flush(); got != nil {
		t.Errorf("Flush with nothing buffered = %q", got)
	}
	d := NewStdinBuffer(StdinBufferOptions{})
	d.ProcessString("\x1b[200~x")
	d.ProcessString("\x1b")
	d.Destroy()
	if d.HasPendingFlush() || d.GetBuffer() != "" {
		t.Error("Destroy left pending input")
	}
	if got := d.ProcessString("q"); !slices.Equal(got, []string{"q"}) {
		t.Errorf("input after Destroy = %q", got)
	}
}
