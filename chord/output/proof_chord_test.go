package output

import "testing"

// Proof of output.ts's OutputBuffer and utf8ByteLength as a consumer sees them: the retained window, what it dropped, and
// a character split across byte chunks.

func TestUtf8ByteLengthCountsBytesNotCharacters(t *testing.T) {
	for text, want := range map[string]int{"": 0, "abc": 3, "é": 2, "€": 3, "😀": 4, "a😀é": 7} {
		if got := Utf8ByteLength(text); got != want {
			t.Fatalf("Utf8ByteLength(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestOutputBufferKeepsTheHeadOrTheTailWholeLinesAndCountsWhatItDropped(t *testing.T) {
	for _, tc := range []struct {
		retain      string
		text        string
		lines, size int
	}{
		{"head", "a\nb\n", 1, 2},
		{"tail", "b\nc\n", 1, 2},
	} {
		buffer := NewOutputBuffer(OutputLimits{MaxBytes: 100, MaxLines: 2, Retain: tc.retain})
		if !buffer.PushString("a\n") || !buffer.PushString("b\nc\n") {
			t.Fatalf("%s: a pushed chunk was not accepted", tc.retain)
		}
		buffer.End()
		got := buffer.Snapshot()
		if got.Text != tc.text || got.DroppedLines != tc.lines || got.DroppedBytes != tc.size {
			t.Fatalf("%s: Snapshot = %+v, want text %q, %d line and %d bytes dropped", tc.retain, got, tc.text, tc.lines, tc.size)
		}
	}
}

func TestOutputBufferJoinsACharacterSplitAcrossByteChunks(t *testing.T) {
	var buffer *OutputBuffer = NewOutputBuffer(OutputLimits{MaxBytes: 100, MaxLines: 10, Retain: "head"}) //nolint:staticcheck // ST1023: the declared type is the pin: the test asserts the symbol has it
	buffer.PushBytes([]byte{0xc3})
	buffer.PushBytes([]byte{0xa9, 'x'})
	buffer.End()
	if got := buffer.Snapshot().Text; got != "éx" {
		t.Fatalf("Snapshot = %q, want éx", got)
	}
	if _, err := buffer.Push(42, nil); err == nil {
		t.Fatal("Push accepted a chunk that is neither a string nor bytes")
	}
}
