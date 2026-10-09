package cli

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// Pi: packages/coding-agent/src/core/extensions/types.ts:2152 (ExtensionActions.getSessionName); packages/coding-agent/src/core/extensions/types.ts:2156 (ExtensionActions.getSettings); packages/coding-agent/src/core/extensions/types.ts:2151 (ExtensionActions.setSessionName).
// agent-session.ts:3339: the actions every mode binds act on the live Session: setSessionName sanitizes and persists the
// name, getSessionName reads it back, getSettings returns the settings manager's effective settings as a copy; while no
// Session exists yet setSessionName fails, getSessionName is empty and getSettings is nil.
func TestSessionExtensionActionsActOnTheLiveSession(t *testing.T) {
	var live *coding.Session
	actions, _ := sessionExtensionActions(func() *coding.Session { return live })
	if err := actions.SetSessionName("early"); !errors.Is(err, errSessionNotReady) {
		t.Fatalf("SetSessionName before the Session exists = %v, want errSessionNotReady", err)
	}
	if name := actions.GetSessionName(); name != "" {
		t.Fatalf("GetSessionName before the Session exists = %q", name)
	}
	if settings := actions.GetSettings(); settings != nil {
		t.Fatalf("GetSettings before the Session exists = %v", settings)
	}

	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	live, err = coding.NewSession(services, coding.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := actions.SetSessionName(" from\nextension "); err != nil {
		t.Fatal(err)
	}
	if got := actions.GetSessionName(); got != "from extension" || live.SessionName() != got {
		t.Fatalf("GetSessionName = %q, Session name = %q, want the sanitized from extension", got, live.SessionName())
	}
	settings := actions.GetSettings()
	if settings == nil {
		t.Fatal("GetSettings = nil with a live Session")
	}
	settings["mutated-by-test"] = true
	if _, ok := actions.GetSettings()["mutated-by-test"]; ok {
		t.Fatal("GetSettings returned shared state; a change leaked into the next call")
	}
}
