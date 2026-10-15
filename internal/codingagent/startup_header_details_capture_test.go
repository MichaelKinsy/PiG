package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:996-1059 (Pi 1.0.0): init builds the header once and captures showDetails for the compact onboarding line, so changing quietStartup later in /settings (which refreshes the settings snapshot) does not rewrite the line; restoring the built-in header keeps it too.
func TestBuiltInHeaderOnboardingKeepsStartupDetailsCapture(t *testing.T) {
	km := &KeybindingsManager{definitions: appKeybindingDefinitions, ordered: appKeybindingOrder, platform: tui.HostKeybindingPlatform()}
	km.rebuild()
	for _, tc := range []struct {
		startup, later any
		want           string
	}{
		{startup: false, later: "header", want: "Press ctrl+o to show full startup help and loaded resources."},
		{startup: "header", later: false, want: "Press ctrl+o to show full startup help."},
	} {
		m := &InteractiveMode{
			opts:        InteractiveModeOptions{LoginVisible: true},
			keybindings: km,
			extHeader:   newSpecialLinesComponent(nil),
			tuiInst:     tui.NewWithOutput(io.Discard, 100, 40),
		}
		setUpstreamQuietStartup(t, m, tc.startup)
		m.restoreBuiltInHeader()
		onboarding := func() string {
			lines := strings.Split(stripANSITest(strings.Join(m.extHeader.Render(100), "\n")), "\n")
			if len(lines) < 3 {
				t.Fatalf("quietStartup %v then %v: header = %q", tc.startup, tc.later, lines)
			}
			// The onboarding line is the header's third, beside the pig head (D2).
			line := lines[2]
			if i := strings.Index(line, "Press"); i >= 0 {
				line = line[i:]
			}
			return strings.TrimSpace(line)
		}
		if got := onboarding(); got != tc.want {
			t.Fatalf("quietStartup %v: onboarding line = %q, want %q", tc.startup, got, tc.want)
		}
		setUpstreamQuietStartup(t, m, tc.later)
		m.extHeader.Invalidate()
		if got := onboarding(); got != tc.want {
			t.Errorf("quietStartup %v then %v: onboarding line = %q, want the startup %q", tc.startup, tc.later, got, tc.want)
		}
		m.restoreBuiltInHeader()
		if got := onboarding(); got != tc.want {
			t.Errorf("quietStartup %v then %v, restored header: onboarding line = %q, want the startup %q", tc.startup, tc.later, got, tc.want)
		}
	}
}
