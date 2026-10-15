package planmode

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pi PR #10739 (examples/extensions/plan-mode/index.ts): the plan context is injected before a user prompt, not before a run that a custom message starts.
func TestPlanContextIsInjectedOnlyBeforeUserRuns(t *testing.T) {
	mode := &planMode{enabled: true}
	for _, messageType := range []string{"custom", ""} {
		if got, err := mode.onBeforeAgentStart(sdk.Context{}, map[string]any{"messageType": messageType}); err != nil || got != nil {
			t.Errorf("messageType %q: got %v, %v; want no injection", messageType, got, err)
		}
	}
	got, err := mode.onBeforeAgentStart(sdk.Context{}, map[string]any{"messageType": "user"})
	if err != nil || got == nil {
		t.Fatalf("user run: got %v, %v; want the plan context", got, err)
	}
}
