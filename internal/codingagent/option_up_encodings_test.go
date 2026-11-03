package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Option+Up on each terminal and setting reaches the key decoder as these bytes (the wire column), which Pi's stdin buffer frames and the
// decoder classifies. Terminal.app's default profile loses the Option modifier, which is why D228 gives plain Up the dequeue role there.
//
// Provenance: "captured" rows were read from a raw-mode hex logger on the owner's Mac (issue #402 handoff); "documented" rows follow
// xterm's control sequences (modifyCursorKeys: CSI 1;3 A for Alt+Up) and Pi's stdin-buffer.ts:241-250 for ESC-prefixed chords; "expected" rows
// were not captured by us.
func TestOptionUpEncodingsPerTerminalAgainstTheKeyDecoder(t *testing.T) {
	for _, tc := range []struct {
		terminal, setting, provenance string
		wire                          string
		wantChunks                    []string
		wantActions                   []keyAction
	}{
		{"Terminal.app", "default profile, Option-as-Meta off", "captured", "\x1b[A", []string{"\x1b[A"}, []keyAction{actionInsert}},
		{"Terminal.app", "Option+Up mapped to \\033[1;3A", "documented", "\x1b[1;3A", []string{"\x1b[1;3A"}, []keyAction{actionDequeue}},
		{"Terminal.app", "Option-as-Meta on", "expected", "\x1b\x1b[A", []string{"\x1b", "\x1b[A"}, []keyAction{actionInterrupt, actionInsert}},
		{"iTerm2", "Left Option Normal", "expected", "\x1b[1;3A", []string{"\x1b[1;3A"}, []keyAction{actionDequeue}},
		{"iTerm2", "Left Option Esc+", "expected", "\x1b\x1b[A", []string{"\x1b", "\x1b[A"}, []keyAction{actionInterrupt, actionInsert}},
		{"Ghostty", "default", "reported in issue #402", "\x1b[1;3A", []string{"\x1b[1;3A"}, []keyAction{actionDequeue}},
		{"xterm", "default modifyCursorKeys", "documented", "\x1b[1;3A", []string{"\x1b[1;3A"}, []keyAction{actionDequeue}},
		{"xterm", "Alt sends Escape (metaSendsEscape), legacy", "documented", "\x1bp", []string{"\x1bp"}, []keyAction{actionDequeue}},
		{"tmux", "outer terminal sends Alt+Up, passed through", "documented", "\x1b[1;3A", []string{"\x1b[1;3A"}, []keyAction{actionDequeue}},
		{"tmux", "outer Terminal.app default, application cursor mode", "expected", "\x1bOA", []string{"\x1bOA"}, []keyAction{actionInsert}},
		{"tmux", "outer Terminal.app default", "expected", "\x1b[A", []string{"\x1b[A"}, []keyAction{actionInsert}},
	} {
		t.Run(tc.terminal+": "+tc.setting, func(t *testing.T) {
			buffer := tui.NewStdinBuffer(tui.StdinBufferOptions{})
			chunks := append(buffer.ProcessString(tc.wire), buffer.FlushExpired()...)
			if !slices.Equal(chunks, tc.wantChunks) {
				t.Fatalf("%s framed as %q, want %q", tc.provenance, chunks, tc.wantChunks)
			}
			var actions []keyAction
			for _, chunk := range chunks {
				actions = append(actions, classifyKeyWithBindings(chunk, otherColumnKeys()))
			}
			if !slices.Equal(actions, tc.wantActions) {
				t.Fatalf("%q classified as %v, want %v", tc.wire, actions, tc.wantActions)
			}
		})
	}
}
