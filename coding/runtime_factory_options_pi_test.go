package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestRuntimeReplacementFactoryReceivesPisOptions ports the object Pi passes to createRuntime on every replacement
// (agent-session-runtime.ts:35-41 CreateAgentSessionRuntimeFactory, :210-220 switchSession, :228+ newSession): the
// destination session manager and its cwd, the current services' agentDir, and the session_start event with the
// reason and the previous session file.
func TestRuntimeReplacementFactoryReceivesPisOptions(t *testing.T) {
	var seen []CreateAgentSessionRuntimeOptions
	h := newRuntimeTestHarness(t, runtimeTestOptions{recordFactoryOptions: func(o CreateAgentSessionRuntimeOptions) { seen = append(seen, o) }})
	runtimePrompt(t, h.runtime, "hello")
	agentDir := h.runtime.Services().AgentDir()
	first := h.runtime.Session().Path()
	seen = nil

	if result, err := h.runtime.NewSession(t.Context(), nil); err != nil || result.Cancelled {
		t.Fatalf("new = %+v, %v", result, err)
	}
	second := h.runtime.Session().Path()
	if result, err := h.runtime.SwitchSession(t.Context(), first); err != nil || result.Cancelled {
		t.Fatalf("resume = %+v, %v", result, err)
	}
	if len(seen) != 2 {
		t.Fatalf("factory ran %d times, want once per replacement", len(seen))
	}
	for i, want := range []struct {
		reason, previous, path string
	}{{"new", first, second}, {"resume", second, first}} {
		got := seen[i]
		if got.AgentDir != agentDir {
			t.Errorf("%s: agentDir = %q, want the current services' %q", want.reason, got.AgentDir, agentDir)
		}
		if got.SessionManager == nil || got.CWD != got.SessionManager.GetCwd() {
			t.Errorf("%s: cwd %q is not the destination manager's", want.reason, got.CWD)
		}
		if got.SessionManager != nil && got.SessionManager.Path() != want.path {
			t.Errorf("%s: session manager path = %q, want %q", want.reason, got.SessionManager.Path(), want.path)
		}
		event := got.SessionStartEvent
		if event == nil || *event != (extension.SessionStartEvent{Type: "session_start", Reason: want.reason, PreviousSessionFile: want.previous}) {
			t.Errorf("%s: session_start = %+v, want reason %s previous %s", want.reason, event, want.reason, want.previous)
		}
	}
}
