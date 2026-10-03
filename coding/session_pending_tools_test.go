package coding

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func pendingTools(s *Session) []string {
	s.toolRegistryMu.Lock()
	defer s.toolRegistryMu.Unlock()
	return slices.Clone(s.pendingToolNames)
}

// agent-session.ts _pendingToolNames: a reload keeps tools that are not registered yet pending, reapplying the selection after a mode rebuilt the agent's tools is not a new loadout, and setActiveToolsByName drops the pending tools only when it deactivates a tool.
func TestSessionPendingToolsSurviveReloadAndReapplyButNotADeactivatingLoadout(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{
		Model: fakeModel(), SkipBuiltinTools: true,
		Tools: []agent.AgentTool{&fakeTool{name: "alpha"}, &fakeTool{name: "beta"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	sess.toolRegistryMu.Lock()
	sess.addPendingTools([]string{"late"})
	sess.toolRegistryMu.Unlock()

	if err := sess.RefreshToolsAfterReload(); err != nil {
		t.Fatal(err)
	}
	if got := pendingTools(sess); !slices.Equal(got, []string{"late"}) {
		t.Fatalf("pending after reload = %v, want [late]", got)
	}
	// Interactive mode replaces the agent's tools and applies the Session's selection again.
	active := sess.ActiveToolNames()
	sess.agent.SetTools([]agent.AgentTool{&fakeTool{name: "alpha"}, &fakeTool{name: "beta"}, &fakeTool{name: "gamma"}})
	sess.ReapplyActiveTools(active)
	if got := pendingTools(sess); !slices.Equal(got, []string{"late"}) {
		t.Fatalf("pending after reapply = %v, want [late]", got)
	}
	// Adding a tool keeps them.
	sess.SetActiveToolsByName([]string{"alpha", "beta", "late"})
	if got := pendingTools(sess); !slices.Equal(got, []string{"late"}) {
		t.Fatalf("pending after an additive loadout = %v, want [late]", got)
	}
	sess.SetActiveToolsByName([]string{"alpha"})
	if got := pendingTools(sess); len(got) != 0 {
		t.Fatalf("pending after a deactivating loadout = %v, want none", got)
	}
}

// withdrawCustomTool removes a custom tool from the registry and refreshes it, as a disconnected MCP server's tools
// leave the registry; restoreCustomTool registers it again, as the server's tools do when it connects.
func withdrawCustomTool(t *testing.T, sess *Session, name string) sessionToolEntry {
	t.Helper()
	sess.toolRegistryMu.Lock()
	index := slices.IndexFunc(sess.toolRegistry.custom, func(entry sessionToolEntry) bool { return entry.tool.Name() == name })
	if index < 0 {
		sess.toolRegistryMu.Unlock()
		t.Fatalf("no custom tool %s", name)
	}
	entry := sess.toolRegistry.custom[index]
	sess.toolRegistry.custom = slices.Delete(sess.toolRegistry.custom, index, index+1)
	sess.toolRegistryMu.Unlock()
	if err := sess.RefreshTools(); err != nil {
		t.Fatal(err)
	}
	return entry
}

func restoreCustomTool(t *testing.T, sess *Session, entry sessionToolEntry) {
	t.Helper()
	// Like a deferred MCP tool, the tool does not activate on registration by itself.
	entry.registration.Definition.DefaultActive = new(false)
	sess.toolRegistryMu.Lock()
	sess.toolRegistry.custom = append(sess.toolRegistry.custom, entry)
	sess.toolRegistryMu.Unlock()
	if err := sess.RefreshTools(); err != nil {
		t.Fatal(err)
	}
}

// Pi 1.0.0 agent-session.ts:426-431, 1488-1505, 1762-1782, 3541-3542: tools the restored transcript declares that are
// not registered yet stay pending and activate when they register; a loadout that deactivates a tool, or the next
// agent run, drops them.
func TestRestoredToolsActivateWhenTheyRegister(t *testing.T) {
	newSession := func(t *testing.T) *Session {
		sess, err := NewSession(newTestServices(t), SessionOptions{
			Model: fakeModel(), SkipBuiltinTools: true,
			Tools: []agent.AgentTool{&fakeTool{name: "alpha"}, &fakeTool{name: "late"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sess.Close() })
		go func() {
			for range sess.Events() { //nolint:revive // drain
			}
		}()
		if _, err := sess.Send(context.Background(), "declare both tools"); err != nil {
			t.Fatal(err)
		}
		return sess
	}
	// The resumed session restores its loadout before the late tool's server connects.
	restoreBeforeRegistration := func(t *testing.T, sess *Session) sessionToolEntry {
		entry := withdrawCustomTool(t, sess, "late")
		sess.restoreToolsFromTranscript()
		if got := sess.ActiveToolNames(); !slices.Equal(got, []string{"alpha"}) {
			t.Fatalf("active before registration = %v", got)
		}
		return entry
	}

	t.Run("registers", func(t *testing.T) {
		sess := newSession(t)
		entry := restoreBeforeRegistration(t, sess)
		restoreCustomTool(t, sess, entry)
		if got := sess.ActiveToolNames(); !slices.Contains(got, "late") {
			t.Fatalf("active after registration = %v, want late", got)
		}
	})
	t.Run("a loadout that only adds keeps pending tools", func(t *testing.T) {
		sess := newSession(t)
		entry := restoreBeforeRegistration(t, sess)
		sess.SetActiveToolsByName([]string{"alpha"})
		restoreCustomTool(t, sess, entry)
		if got := sess.ActiveToolNames(); !slices.Contains(got, "late") {
			t.Fatalf("active after registration = %v, want late", got)
		}
	})
	t.Run("a loadout that deactivates drops pending tools", func(t *testing.T) {
		sess := newSession(t)
		entry := restoreBeforeRegistration(t, sess)
		sess.SetActiveToolsByName(nil)
		restoreCustomTool(t, sess, entry)
		if got := sess.ActiveToolNames(); slices.Contains(got, "late") {
			t.Fatalf("active after registration = %v, want no late", got)
		}
	})
	// agent-session.ts:3631-3632: a reload keeps every active tool pending, so a tool whose extension registers it after
	// the reload, such as an MCP tool of a server that is still connecting, activates when it registers.
	t.Run("a reload keeps active tools pending until they register", func(t *testing.T) {
		sess := newSession(t)
		sess.toolRegistryMu.Lock()
		index := slices.IndexFunc(sess.toolRegistry.custom, func(entry sessionToolEntry) bool { return entry.tool.Name() == "late" })
		if index < 0 {
			sess.toolRegistryMu.Unlock()
			t.Fatal("no custom tool late")
		}
		entry := sess.toolRegistry.custom[index]
		sess.toolRegistry.custom = slices.Delete(sess.toolRegistry.custom, index, index+1)
		sess.toolRegistryMu.Unlock()
		if err := sess.RefreshToolsAfterReload(); err != nil {
			t.Fatal(err)
		}
		if got := sess.ActiveToolNames(); slices.Contains(got, "late") {
			t.Fatalf("active before registration = %v", got)
		}
		restoreCustomTool(t, sess, entry)
		if got := sess.ActiveToolNames(); !slices.Contains(got, "late") {
			t.Fatalf("active after registration = %v, want late", got)
		}
	})
	t.Run("the next run drops pending tools", func(t *testing.T) {
		sess := newSession(t)
		entry := restoreBeforeRegistration(t, sess)
		if _, err := sess.Send(context.Background(), "run before registration"); err != nil {
			t.Fatal(err)
		}
		restoreCustomTool(t, sess, entry)
		if got := sess.ActiveToolNames(); slices.Contains(got, "late") {
			t.Fatalf("active after registration = %v, want no late", got)
		}
	})
}
