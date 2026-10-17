package coding

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// pi-mono #10785: resuming a session resets the active tools to the registration defaults. agent-session.ts restores them from the transcript only when no initial
// loadout is given (_initialActiveToolNames === undefined), but sdk.ts always passes one, so _restoreToolsFromTranscript never runs on resume and the next turn records the
// removal of every tool the user had enabled dynamically. The transcript's active tools are restored on resume, subject to the allowlist and denylist.
func TestResumeRestoresTheTranscriptsActiveTools(t *testing.T) {
	svcs := newTestServices(t)
	sessionDir := t.TempDir()
	tools := func() []agent.AgentTool { return []agent.AgentTool{&fakeTool{name: "alpha"}, &fakeTool{name: "beta"}} }
	first, err := NewSession(svcs, SessionOptions{Model: fakeModel(), Tools: tools(), SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range first.Events() { //nolint:revive // drain
		}
	}()
	first.SetActiveToolsByName([]string{"alpha"})
	if _, err := first.Send(context.Background(), "with alpha only"); err != nil {
		t.Fatal(err)
	}
	path := first.Path()
	_ = first.Close()

	resumed, err := NewSession(svcs, SessionOptions{Model: fakeModel(), Tools: tools(), SessionDir: sessionDir, ResumePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if got := resumed.ActiveToolNames(); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("active tools after resume = %v, want [alpha] as the transcript declares, not the defaults", got)
	}
}
