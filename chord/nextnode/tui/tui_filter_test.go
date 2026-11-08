package tui

import (
	"errors"
	"strings"
	"testing"
)

// Tests of the terminal's output filter (slice 11 §4): text and drawing sequences pass; the clipboard (OSC 52), titles, other
// control strings, unknown modes and charset changes are dropped; an unterminated sequence ends with its frame; a frame over
// 64 KiB is refused. Ports packages/chord/test/node/tui/filter.test.ts.

func filtered(t *testing.T, frame string) string {
	t.Helper()
	got, err := FilterOutput(frame)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWhatPassesKeepsTextCursorMovesErasingColoursTheAlternateScreenAndBracketedPaste(t *testing.T) {
	frame := "\x1b[?1049h\x1b[2J\x1b[H\x1b[1;31mred\x1b[0m\r\n\x1b[38;2;1;2;3mtrue\x1b[39m\x1b[5;10H\x1b[K\x1b[?2004h\x1b[?25l\ttab"
	if got := filtered(t, frame); got != frame {
		t.Fatalf("filtered = %q, want %q", got, frame)
	}
}

func TestWhatIsDroppedDropsTheClipboardTheTitleAndEveryOtherControlStringKeepingTheTextAroundThem(t *testing.T) {
	for frame, want := range map[string]string{
		"a\x1b]52;c;aGVsbG8=\x07b":  "ab",
		"a\x1b]0;evil title\x1b\\b": "ab",
		"a\x1bPq#0;2;0;0;0\x1b\\b":  "ab",
		"a\u009d52;c;x\u009cb":      "ab",
	} {
		if got := filtered(t, frame); got != want {
			t.Fatalf("filtered(%q) = %q, want %q", frame, got, want)
		}
	}
}

func TestWhatIsDroppedDropsUnknownModesCharsetChangesOtherESCSequencesAndControlCharacters(t *testing.T) {
	for frame, want := range map[string]string{
		"\x1b[?1003h\x1b[?9h\x1b(0\x1bc\x07\x00x\x7f": "x",
		"\x1b[99999m": "",
		"\x1b[1;2;3H": "",
	} {
		if got := filtered(t, frame); got != want {
			t.Fatalf("filtered(%q) = %q, want %q", frame, got, want)
		}
	}
}

func TestWhatIsDroppedEndsAnUnterminatedSequenceWithItsFrameNothingSmuggledIntoTheNext(t *testing.T) {
	if got := filtered(t, "ok\x1b]52;c;aGVsbG8="); got != "ok" {
		t.Fatalf("filtered = %q, want ok", got)
	}
	if got := filtered(t, "then text"); got != "then text" {
		t.Fatalf("filtered = %q", got)
	}
}

func TestWhatIsDroppedRefusesAFrameOver64KiB(t *testing.T) {
	if _, err := FilterOutput(strings.Repeat("x", MaxFrame+1)); !errors.Is(err, ErrFrameTooLarge) || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("error = %v, want a 64 KiB refusal", err)
	}
}

// FuzzFilterOutput: whatever the sandbox writes, nothing but the allowed sequences comes out: no escape other than CSI of the
// allowed kinds, no C1 control, no control string.
func FuzzFilterOutput(f *testing.F) {
	for _, seed := range []string{"\x1b]52;c;x\x07", "\x1b[?1049h", "\x9d0;t\x9c", "\x1b", "\x1b[", "a\x1bPq\x1b\\b", "\xed\xa0\x80"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, frame string) {
		got, err := FilterOutput(frame)
		if err != nil {
			return
		}
		for i := 0; i < len(got); i++ {
			if got[i] == 0x1b && !(i+1 < len(got) && got[i+1] == '[') {
				t.Fatalf("a bare escape in %q (from %q)", got, frame)
			}
			if got[i] == 0x07 || got[i] == 0x00 || got[i] == 0x7f {
				t.Fatalf("a control character in %q (from %q)", got, frame)
			}
		}
		if strings.Contains(got, "\x1b]") || strings.ContainsAny(got, "\u0080\u0090\u009b\u009d\u009c") {
			t.Fatalf("a control string or C1 control in %q (from %q)", got, frame)
		}
	})
}
