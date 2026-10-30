package tui

import "testing"

// TestDetectCapabilitiesOrca ports "keeps images disabled for Orca without an image capability signal" and "enables images and
// hyperlinks for Orca advertising Kitty support" (Pi f09d01b8, #9329; packages/tui/test/terminal-image.test.ts): Orca draws Kitty
// images, but only an Orca that exports ORCA_IMAGE_PROTOCOL=kitty has the feature, so both signals are required.
func TestDetectCapabilitiesOrca(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   map[string]string
		probe bool
		want  TerminalCapabilities
	}{
		{"keeps images disabled for Orca without an image capability signal", map[string]string{"TERM_PROGRAM": "Orca"}, false, TerminalCapabilities{}},
		{"keeps images disabled for the signal without Orca", map[string]string{"ORCA_IMAGE_PROTOCOL": "kitty"}, false, TerminalCapabilities{}},
		{"keeps images disabled for another protocol value", map[string]string{"TERM_PROGRAM": "Orca", "ORCA_IMAGE_PROTOCOL": "iterm2"}, false, TerminalCapabilities{}},
		{"enables images and hyperlinks for Orca advertising Kitty support", map[string]string{"TERM_PROGRAM": "Orca", "ORCA_IMAGE_PROTOCOL": "kitty"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		{"keeps the tmux guard for Orca", map[string]string{"TERM_PROGRAM": "Orca", "ORCA_IMAGE_PROTOCOL": "kitty", "TMUX": "/tmp/tmux-1000/default,1234,0", "TERM": "tmux-256color"}, true, TerminalCapabilities{Hyperlinks: true}},
		{"keeps the screen guard for Orca", map[string]string{"TERM_PROGRAM": "Orca", "ORCA_IMAGE_PROTOCOL": "kitty", "TERM": "screen"}, false, TerminalCapabilities{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateCapabilityEnv(t)
			t.Setenv("CMUX_WORKSPACE_ID", "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := DetectCapabilities(func() bool { return tc.probe }); got != tc.want {
				t.Fatalf("caps=%#v want %#v", got, tc.want)
			}
		})
	}
}
