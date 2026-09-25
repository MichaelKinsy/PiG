package codingagent

import "testing"

// TestClassifyCSIuEventTypes pins the fix for Ctrl+C / Ctrl+D going dead under
// the Kitty keyboard protocol. Pig negotiates \x1b[>7u (flags 1+2+4), so keys
// arrive with an event-type sub-parameter (\x1b[99;5:1u) and, with alternate
// keys, a codepoint sub-parameter (\x1b[99:67;5:1u). The decoder must match on
// the base codepoint + base modifier and ignore those sub-parameters.
func TestClassifyCSIuEventTypes(t *testing.T) {
	cases := []struct {
		name string
		data string
		want keyAction
	}{
		{"ctrl+c no event type", "\x1b[99;5u", actionClearEditor},
		{"ctrl+c press event", "\x1b[99;5:1u", actionClearEditor},
		{"ctrl+c repeat event", "\x1b[99;5:2u", actionClearEditor},
		{"ctrl+c alternate-key + press", "\x1b[99:67;5:1u", actionClearEditor},
		{"ctrl+d no event type", "\x1b[100;5u", actionExit},
		{"ctrl+d press event", "\x1b[100;5:1u", actionExit},
		{"escape no modifier field", "\x1b[27u", actionInterrupt},
		{"escape with modifier+event", "\x1b[27;1:1u", actionInterrupt},
		{"enter no modifier field", "\x1b[13u", actionSubmit},
		{"ctrl+o press event", "\x1b[111;5:1u", actionToggleTools},
		{"alt+up press event (modified cursor)", "\x1b[1;3:1A", actionDequeue},
		{"plain up press (unmodified) stays insert", "\x1b[1;1:1A", actionInsert},
		{"garbage stays insert", "\x1b[abc;defu", actionInsert},
	}
	km := otherColumnKeys()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyKeyWithBindings(tc.data, km); got != tc.want {
				t.Fatalf("classifyKey(%q) = %d, want %d", tc.data, got, tc.want)
			}
		})
	}
}
