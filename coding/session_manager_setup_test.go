package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session-runtime.ts:254-255 `await options.setup(this.session.sessionManager)`: newSession's setup callback receives the new session's own
// SessionManager (session-manager.ts:987), so every operation it calls through the interface writes the log the runtime then projects.
func TestNewSessionSetupReceivesTheNewSessionsSessionManager(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	var got extension.SessionManager
	var before int
	_, err := h.runtime.NewSession(t.Context(), &extension.NewSessionOptions{
		Setup: func(manager extension.SessionManager) error {
			got = manager
			before = manager.GetEntryCount()
			if _, err := manager.AppendSessionInfo("from setup"); err != nil {
				return err
			}
			if _, err := manager.AppendCustomEntry("setup-data", map[string]any{"k": 1}); err != nil {
				return err
			}
			if manager.GetEntryCount() != before+2 {
				t.Errorf("setup appended 2 entries to %d, manager holds %d", before, manager.GetEntryCount())
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("setup did not run")
	}
	log := h.runtime.Session().SessionManager()
	if log.GetSessionName() != "from setup" || log.GetEntryCount() != before+2 {
		t.Fatalf("runtime session log: name %q, %d entries; want %d, the entries setup wrote", log.GetSessionName(), log.GetEntryCount(), before+2)
	}
	if leaf, ok := log.GetLeafEntry(); !ok || leaf.Base().Type != "custom" || got.GetSessionId() != log.GetSessionId() {
		t.Fatalf("leaf %+v, setup manager id %q, runtime id %q", leaf.Base(), got.GetSessionId(), log.GetSessionId())
	}
}
