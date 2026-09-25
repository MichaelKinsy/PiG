package coding

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func agentToolNames(sess *Session) []string {
	var names []string
	for _, tool := range sess.Agent().Tools() {
		names = append(names, tool.Name())
	}
	return names
}

// Upstream navigateTree restores the active tools declared by the target
// branch's transcript (agent-session.ts _restoreToolsFromTranscript).
func TestNavigateTreeRestoresActiveToolsFromTranscript(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{
		Model: fakeModel(), SkipBuiltinTools: true,
		Tools: []agent.AgentTool{&fakeTool{name: "alpha"}, &fakeTool{name: "beta"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	go func() {
		for range sess.Events() { //nolint:revive // drain
		}
	}()
	if _, err := sess.Send(context.Background(), "with both tools"); err != nil {
		t.Fatal(err)
	}
	var firstAssistant string
	for _, entry := range sess.Entries() {
		if message, ok := entry.AsMessage(); ok && message.Message.Assistant != nil {
			firstAssistant = entry.Base.ID
			break
		}
	}
	sess.Agent().SetTools([]agent.AgentTool{&fakeTool{name: "alpha"}})
	if _, err := sess.Send(context.Background(), "with alpha only"); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.NavigateTree(context.Background(), firstAssistant, NavigateTreeOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := agentToolNames(sess); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("active tools after navigation = %v, want [alpha beta]", got)
	}
}

// Upstream setActiveToolsByName keeps the requested order and ignores names
// with no registered tool.
func TestSetActiveToolsByName(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{
		Model: fakeModel(), SkipBuiltinTools: true,
		Tools: []agent.AgentTool{&fakeTool{name: "alpha"}, &fakeTool{name: "beta"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	sess.SetActiveToolsByName([]string{"beta", "missing", "alpha"})
	if got := sess.ActiveToolNames(); !slices.Equal(got, []string{"beta", "alpha"}) {
		t.Fatalf("active tools = %v, want [beta alpha]", got)
	}
	sess.SetActiveToolsByName(nil)
	if got := sess.ActiveToolNames(); len(got) != 0 {
		t.Fatalf("active tools = %v, want none", got)
	}
}
