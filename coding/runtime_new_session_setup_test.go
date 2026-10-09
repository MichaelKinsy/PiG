package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session-runtime.ts:254-257: newSession runs setup with the replacement Session's SessionManager before withSession, and the
// entries it appends belong to the new Session. The manager answers every append the subprocess bridge forwards (sessionWrite).
func TestRuntimeNewSessionSetupSeedsTheReplacementSession(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	runtimePrompt(t, h.runtime, "hello")
	original := h.runtime.Session()
	var order []string
	result, err := h.runtime.NewSession(t.Context(), &extension.NewSessionOptions{
		Setup: func(manager extension.SessionManager) error {
			order = append(order, "setup")
			if _, err := manager.AppendCustomEntry("seed-note", map[string]any{"n": 1}); err != nil {
				return err
			}
			if _, err := manager.AppendSessionInfo("seeded"); err != nil {
				return err
			}
			_, err := manager.AppendCustomMessageEntry("seed-msg", "from setup", true, nil)
			return err
		},
		WithSession: func(*extension.ReplacedSessionContext) error {
			order = append(order, "withSession")
			return nil
		},
	})
	if err != nil || result.Cancelled {
		t.Fatalf("new=%+v err=%v", result, err)
	}
	if strings.Join(order, ",") != "setup,withSession" {
		t.Fatalf("callback order = %v, want setup then withSession", order)
	}
	session := h.runtime.Session()
	if session == original || session.SessionName() != "seeded" {
		t.Fatalf("replacement=%t name=%q; setup must seed the new Session, not the old one", session != original, session.SessionName())
	}
	var raw []string
	for _, entry := range session.Entries() {
		raw = append(raw, string(entry.Raw()))
	}
	joined := strings.Join(raw, "\n")
	if !strings.Contains(joined, `"seed-note"`) || !strings.Contains(joined, `from setup`) {
		t.Fatalf("new Session entries lack the seeded entries:\n%s", joined)
	}
	for _, entry := range original.Entries() {
		if strings.Contains(string(entry.Raw()), "seed-note") {
			t.Fatal("setup appended to the outgoing Session")
		}
	}
}
