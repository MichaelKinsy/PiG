package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 honors PI_CLEAR_ON_SHRINK only in SettingsManager.getClearOnShrink
// (terminal.clearOnShrink wins, then PI_CLEAR_ON_SHRINK=1, else false) and
// hands the result to the TUI through setClearOnShrink. The TUI itself no
// longer reads the variable, so the interactive renderer must get it from
// settings.
func TestInteractiveRendererClearOnShrinkComesFromSettings(t *testing.T) {
	enabled, disabled := true, false
	cases := []struct {
		name    string
		env     string
		setting *bool
		want    bool
	}{
		{"env enables when unset", "1", nil, true},
		{"setting false overrides env", "1", &disabled, false},
		{"setting true without env", "", &enabled, true},
		{"default off", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_CLEAR_ON_SHRINK", tc.env)
			m := newSwitchTuiProbe(t)
			m.opts.Settings.ClearOnShrink = tc.setting
			if !m.switchTuiMode("fullscreen", false) || !m.switchTuiMode("regular", false) {
				t.Fatal("renderer swap refused")
			}
			regular, ok := m.tuiInst.(*tui.TUI)
			if !ok {
				t.Fatalf("regular renderer = %T", m.tuiInst)
			}
			if got := regular.GetClearOnShrink(); got != tc.want {
				t.Fatalf("GetClearOnShrink() = %v, want %v", got, tc.want)
			}
		})
	}
}
