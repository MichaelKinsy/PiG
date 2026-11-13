package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// recordTerminalProgress records the progress calls m makes on its renderer's terminal, on the test's goroutine.
func recordTerminalProgress(t *testing.T, m *InteractiveMode) *[]bool {
	t.Helper()
	return &recordTerminalCalls(t, m).progress
}

// Pi gates OSC 9;4 progress on terminal.showTerminalProgress alone
// (interactive-mode.ts:3303, 3526, 3545, 3559), and the setting defaults to
// false (settings-manager.ts getShowTerminalProgress), which is what keeps
// unsupported terminals from receiving it. No terminal allowlist applies.
func TestTerminalProgressFollowsSettingOnly(t *testing.T) {
	tui.SetCapabilities(tui.TerminalCapabilities{})
	t.Cleanup(tui.ResetCapabilitiesCache)

	for _, tc := range []struct {
		name     string
		settings string
		want     []bool
	}{
		{"default off", `{}`, nil},
		{"explicit off", `{"terminal": {"showTerminalProgress": false}}`, nil},
		{"on in an unrecognized terminal", `{"terminal": {"showTerminalProgress": true}}`, []bool{true, false, true, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := statusBorderMode(t, false)
			writes := recordTerminalProgress(t, m)
			m.opts.Settings = settingsFromJSON(t, tc.settings)
			m.handleAgentEvent(agent.TurnStartEvent{})
			m.handleAgentEvent(agent.AgentEndEvent{})
			m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
			m.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual"})
			if len(*writes) != len(tc.want) {
				t.Fatalf("progress writes = %v, want %v", *writes, tc.want)
			}
			for i := range tc.want {
				if (*writes)[i] != tc.want[i] {
					t.Fatalf("progress writes = %v, want %v", *writes, tc.want)
				}
			}
		})
	}
}
