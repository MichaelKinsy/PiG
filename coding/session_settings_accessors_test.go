package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// agent-session.ts:1623-1630,3235-3243,3802-3804: steeringMode and followUpMode read the agent; autoCompactionEnabled and
// autoRetryEnabled read the settings manager, and setAutoCompactionEnabled writes settingsManager.setCompactionEnabled.
func TestSessionReportsQueueModesAndTheAutoSettings(t *testing.T) {
	services := newTestServices(t)
	sess, err := NewSession(services, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	if err := sess.SetSteeringMode(agent.QueueModeAll); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetFollowUpMode(agent.QueueModeAll); err != nil {
		t.Fatal(err)
	}
	if sess.SteeringMode() != agent.QueueModeAll || sess.FollowUpMode() != agent.QueueModeAll {
		t.Fatalf("modes = %q %q, want all all", sess.SteeringMode(), sess.FollowUpMode())
	}
	if !sess.AutoCompactionEnabled() || !sess.AutoRetryEnabled() {
		t.Fatal("auto-compaction and auto-retry are enabled by default")
	}
	if err := sess.SetAutoCompactionEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetAutoRetryEnabled(false); err != nil {
		t.Fatal(err)
	}
	if sess.AutoCompactionEnabled() || sess.AutoRetryEnabled() || services.SettingsManager().GetCompactionEnabled() {
		t.Fatal("the setters must reach the settings manager")
	}
}
