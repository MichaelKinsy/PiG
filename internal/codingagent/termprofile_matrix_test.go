package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Terminal capability-profile matrix for decoder B (classifyKey).
//
// Why this exists: pig decodes raw terminal bytes in multiple places, and each
// decoder was historically tested against the byte forms its author *assumed*
// terminals send (legacy \x1b[A, bare \x1b), never the forms a Kitty-protocol
// terminal actually emits after pig negotiates \x1b[>7u. That gap shipped four
// input bugs behind a green suite (Ctrl+C/D dead, double-Esc no tree, arrows,
// key-release double-input). This matrix crosses one semantic expectation table
// against N capability profiles so a regression in any profile goes red.
//
// Scope of THIS file: the keys decoder B owns (Escape, Ctrl+C, Ctrl+D, Enter,
// Shift+Enter) across the `legacy` and `kitty7` profiles. Selector-specific
// decoders have their own tests.
//
// Encoding provenance: `kitty7` forms are derived from the Kitty keyboard
// protocol spec for flags 1+2+4 (disambiguate + report-events +
// report-alternate), which is exactly what pig negotiates
// (tui/terminal.go extendedKeyInit). The contract asserted is that the
// decoder accepts EVERY spec-conformant form for a key, since a conformant
// terminal may pick any of them.
//
// Verified against a real terminal: a Windows Terminal capture (Windows 11
// 24H2) answers pig's query with "\x1b[?7u" and emits the spec forms asserted
// here, including a release for every press. See
// TestWindowsTerminalMeasuredEncodings for the measured bytes.

// keyPress lists the acceptable wire encodings a profile may emit for one key
// press, all of which the decoder must map to `want`.
type keyPress struct {
	key      string
	want     keyAction
	encoding []string
}

func legacyPresses() []keyPress {
	return []keyPress{
		{"escape", actionInterrupt, []string{"\x1b"}},
		{"ctrl+c", actionClearEditor, []string{"\x03"}},
		{"ctrl+d", actionExit, []string{"\x04"}},
		{"enter", actionSubmit, []string{"\r"}},
		// Shift+Enter is indistinguishable from Enter in a legacy terminal, so
		// there is no separate legacy encoding to assert.
	}
}

// kitty7Presses covers flags 1+2+4. A conformant terminal may send the event
// type sub-parameter (":1" = press) or omit it, and may send the modifier
// field (";1" = none) or omit it when there is no modifier. All map to `want`.
func kitty7Presses() []keyPress {
	return []keyPress{
		{"escape", actionInterrupt, []string{"\x1b[27u", "\x1b[27;1u", "\x1b[27;1:1u"}},
		{"ctrl+c", actionClearEditor, []string{"\x1b[99;5u", "\x1b[99;5:1u"}},
		{"ctrl+d", actionExit, []string{"\x1b[100;5u", "\x1b[100;5:1u"}},
		{"enter", actionSubmit, []string{"\x1b[13u", "\x1b[13;1u", "\x1b[13;1:1u"}},
		{"shift+enter", actionNewline, []string{"\x1b[13;2u", "\x1b[13;2:1u"}},
	}
}

func TestTermProfileMatrix_DecoderB(t *testing.T) {
	profiles := []struct {
		name    string
		presses []keyPress
	}{
		{"legacy", legacyPresses()},
		{"kitty7", kitty7Presses()},
	}
	for _, p := range profiles {
		for _, kp := range p.presses {
			for _, enc := range kp.encoding {
				name := p.name + "/" + kp.key + "/" + enc
				t.Run(name, func(t *testing.T) {
					got := classifyKey(enc)
					if got != kp.want {
						t.Fatalf("profile %s key %s encoding %q: classifyKey = %d, want %d",
							p.name, kp.key, enc, got, kp.want)
					}
				})
			}
		}
	}
}

// TestTermProfileMatrix_ReleasesDropped proves the kitty7 release events (event
// type 3) are recognized as releases and dropped before classifyKey, so a held
// or released key never double-fires its action. This is the guard for the
// key-release double-input class.
func TestTermProfileMatrix_ReleasesDropped(t *testing.T) {
	releases := map[string]string{
		"escape": "\x1b[27;1:3u",
		"ctrl+c": "\x1b[99;5:3u",
		"up":     "\x1b[1;1:3A",
		"down":   "\x1b[1;1:3B",
	}
	for key, enc := range releases {
		t.Run(key, func(t *testing.T) {
			if !tui.IsKeyRelease(enc) {
				t.Fatalf("kitty7 release for %s (%q) not recognized as a release; it would double-fire", key, enc)
			}
		})
	}
}
