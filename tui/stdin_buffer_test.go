package tui

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

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

func TestEscapeSequenceCompleteness(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare ESC at end", "\x1b", "incomplete"},
		{"ESC+char", "\x1ba", "complete"},
		{"CSI arrow", "\x1b[A", "complete"},
		{"CSI params", "\x1b[13;2u", "complete"},
		{"SS3", "\x1bOP", "complete"},
		{"SS3 at end", "\x1bO", "incomplete"},
		{"CSI no final", "\x1b[", "incomplete"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCompleteSequence(jsstring.ToUTF16(tc.in)); got != tc.want {
				t.Errorf("isCompleteSequence(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
