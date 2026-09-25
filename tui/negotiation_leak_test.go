package tui

import (
	"os"
	"strings"
	"testing"
)

// TestReadInputStripsEveryNegotiationResponse pins that no part of the
// terminal's reply to extendedKeyInit reaches a component as input.
//
// extendedKeyInit sends both \x1b[?u and \x1b[c, so a terminal answers with a
// Kitty flags report and a Device Attributes report, normally in a single read.
// Stripping only the first left the second to be returned as user input: it
// arrived at whatever component was focused, and the tree selector's
// type-to-search appended it, so opening /tree showed \x1b[?62;22c in the
// search box every time.
func TestReadInputStripsEveryNegotiationResponse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write string
		want  string
	}{
		{"both responses, nothing typed", "\x1b[?7u\x1b[?62;22c", ""},
		{"both responses then a keystroke", "\x1b[?7u\x1b[?62;22ca", "a"},
		{"DA alone then a keystroke", "\x1b[?62;22cb", "b"},
		{"flags alone then a keystroke", "\x1b[?7uc", "c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			defer func() { _ = r.Close(); _ = w.Close() }()

			term := NewProcessTerminalWithOutput(r, nil, ioDiscard{})
			payload := tc.write
			if tc.want == "" {
				// readInput blocks until it has something to return, so give it
				// a keystroke after the responses to prove they were consumed.
				payload += "z"
			}
			if _, err := w.Write([]byte(payload)); err != nil {
				t.Fatalf("write: %v", err)
			}

			got, err := term.readInput(r)
			if err != nil {
				t.Fatalf("readInput: %v", err)
			}
			want := tc.want
			if want == "" {
				want = "z"
			}
			if string(got) != want {
				t.Errorf("readInput returned %q, want %q: a negotiation response leaked into input", got, want)
			}
			if strings.Contains(string(got), "\x1b[?") {
				t.Errorf("negotiation response reached the caller: %q", got)
			}
		})
	}
}
