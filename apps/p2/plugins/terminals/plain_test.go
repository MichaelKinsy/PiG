package terminals

import "testing"

// Ports test/p2/terminal-plain.test.ts: the output ring's absolute offsets and overflow, and plain text as a terminal shows
// it line by line (escape sequences removed, carriage-return overwrites, backspaces, erase in line).

func since(ring *OutputRing, from int64) (string, int64) {
	bytes, skipped := ring.Since(from)
	return string(bytes), skipped
}

func TestTheOutputRingKeepsTheLastBytesAtAbsoluteOffsetsAndCountsWhatItLost(t *testing.T) {
	ring := NewOutputRing(8)
	ring.Append([]byte("abc"))
	if ring.Start() != 0 || ring.End() != 3 {
		t.Fatalf("[start end] = [%d %d], want [0 3]", ring.Start(), ring.End())
	}
	if text, skipped := since(ring, 1); text != "bc" || skipped != 0 {
		t.Fatalf("since(1) = %q, %d", text, skipped)
	}
	ring.Append([]byte("defgh"))
	ring.Append([]byte("ij"))
	if ring.Start() != 2 || ring.End() != 10 {
		t.Fatalf("[start end] = [%d %d], want [2 10]", ring.Start(), ring.End())
	}
	// Reading from 0: the first two bytes are gone.
	if text, skipped := since(ring, 0); text != "cdefghij" || skipped != 2 {
		t.Fatalf("since(0) = %q, %d", text, skipped)
	}
	if text, _ := since(ring, 7); text != "hij" {
		t.Fatalf("since(7) = %q", text)
	}
	if text, _ := since(ring, 10); text != "" {
		t.Fatalf("since(10) = %q", text)
	}
	// A chunk larger than the ring keeps its tail.
	ring.Append([]byte("0123456789AB"))
	if ring.Start() != 14 || ring.End() != 22 {
		t.Fatalf("[start end] = [%d %d], want [14 22]", ring.Start(), ring.End())
	}
	if text, _ := since(ring, 14); text != "456789AB" {
		t.Fatalf("since(14) = %q", text)
	}
	if _, skipped := since(ring, 12); skipped != 2 {
		t.Fatalf("since(12).skipped = %d", skipped)
	}
}

func TestPlainTextEscapeSequencesGoOverwritesAndBackspacesApply(t *testing.T) {
	for _, each := range []struct{ name, input, want string }{
		// Colors (CSI), a title (OSC with BEL and with ST), a DCS string, charset selection, a keypad mode.
		{"sequences", "\x1b[1;31mred\x1b[0m \x1b]0;title\x07\x1b]2;t\x1b\\x\x1bPq#0\x1b\\y\x1b(Bz\x1b=!", "red xyz!"},
		// A progress bar keeps its final state; CRLF is a newline.
		{"progress", "10%\r50%\r100%\r\ndone\r\n", "100%\ndone\n"},
		// Overwriting a longer line keeps its tail unless the line is erased.
		{"overwrite", "downloading\rdone", "doneloading"},
		{"erase to end", "downloading\r\x1b[Kdone", "done"},
		{"erase line", "abc\x1b[2Kx", "   x"},
		{"backspace", "abc\b\bX", "aXc"},
		{"leading backspaces", "\b\bab", "ab"},
		// Other control characters go; tabs stay; trailing spaces are trimmed, except on the last line (a prompt).
		{"controls", "a\x07b\x00c\td   \n$ ", "abc\td\n$ "},
		// C1 forms of CSI and OSC.
		{"c1", "a\u009b31mb\u009d0;t\u009cc", "abc"},
		// A sequence cut off at the end leaves nothing behind.
		{"cut csi", "ok\x1b[3", "ok"},
		{"cut osc", "ok\x1b]0;unterminated", "ok"},
	} {
		if got := PlainText(each.input); got != each.want {
			t.Errorf("%s: PlainText(%q) = %q, want %q", each.name, each.input, got, each.want)
		}
	}
}
