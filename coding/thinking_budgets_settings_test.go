package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A settings budget of zero or below is configured and wins over the default (simple-options.ts:82 spreads settings.thinkingBudgets over the defaults); an unset level keeps the default, and an empty settings
// object still reaches the agent as a non-nil budget set.
func TestSessionThinkingBudgetsKeepConfiguredZeroFromSettings(t *testing.T) {
	for _, tc := range []struct {
		settings string
		want     map[string]int
		empty    bool
	}{
		{`{"thinkingBudgets":{"medium":0,"high":-5,"low":2048}}`, map[string]int{"minimal": ai.DefaultThinkingBudgets().Minimal, "low": 2048, "medium": 0, "high": -5}, false},
		{`{"thinkingBudgets":{}}`, map[string]int{"medium": ai.DefaultThinkingBudgets().Medium}, true},
	} {
		cwd, agentDir := t.TempDir(), filepath.Join(t.TempDir(), "agent")
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(tc.settings), 0o600); err != nil {
			t.Fatal(err)
		}
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: fakeModel()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = result.Session.Close() })
		budgets := result.Session.Agent().ThinkingBudgets()
		if budgets == nil {
			t.Fatalf("%s: agent budgets are nil", tc.settings)
		}
		for level, want := range tc.want {
			if got := ai.ThinkingBudgetForLevel(level, budgets); got != want {
				t.Errorf("%s: %s budget = %d, want %d", tc.settings, level, got, want)
			}
		}
		if tc.empty && budgets.Set != 0 {
			t.Errorf("%s: empty settings set levels %b", tc.settings, budgets.Set)
		}
	}
}
