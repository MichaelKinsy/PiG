package codingagent

import "testing"

func TestClassifyCSIu(t *testing.T) {
	tests := []struct {
		name string
		data string
		want keyAction
	}{
		// CSI-u format
		{"Ctrl+C", "\x1b[99;5u", actionClearEditor}, // modifier 5 = 1+Ctrl(4)
		{"Ctrl+D", "\x1b[100;5u", actionExit},
		{"Ctrl+O", "\x1b[111;5u", actionToggleTools},
		{"Ctrl+P", "\x1b[112;5u", actionCycleModelForward},
		{"Shift+Ctrl+P", "\x1b[112;6u", actionCycleModelBackward}, // modifier 6 = 1+Shift(1)+Ctrl(4)
		{"Shift+Enter", "\x1b[13;2u", actionNewline},              // modifier 2 = 1+Shift(1)
		{"Alt+Enter", "\x1b[13;3u", actionFollowUp},               // modifier 3 = 1+Alt(2)
		{"Enter bare", "\x1b[13;1u", actionSubmit},                // modifier 1 = no modifiers
		{"Ctrl+T", "\x1b[116;5u", actionToggleThinking},
		{"Ctrl+Z", "\x1b[122;5u", actionSuspend},
		{"Shift+Tab", "\x1b[9;2u", actionCycleThinking},
		{"Escape", "\x1b[27;1u", actionInterrupt},

		// xterm modifyOtherKeys format
		{"xterm Shift+Enter", "\x1b[27;2;13~", actionNewline},
		{"xterm Alt+Enter", "\x1b[27;3;13~", actionFollowUp},
		{"xterm Shift+Ctrl+P", "\x1b[27;6;112~", actionCycleModelBackward},

		// Modified cursor keys
		{"Alt+Up", "\x1b[1;3A", actionDequeue},

		// Unknown sequences → actionInsert
		{"unknown", "\x1b[42;5u", actionInsert},
		{"not CSI-u", "hello", actionInsert},
	}
	km := otherColumnKeys()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyKeyWithBindings(tc.data, km)
			if got != tc.want {
				t.Errorf("classifyKey(%q) = %d, want %d", tc.data, got, tc.want)
			}
		})
	}
}
