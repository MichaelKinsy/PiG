package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:3591-3609 (reload): `addedDefaultTools` is a local of one reload() call. It reaches _buildRuntime only when
// everything before it succeeded, and it is read from the settings as that call reloaded them, so a later reload never inherits it.
// The Session keeps the list between ReloadSettings and the tool rebuild, so a reload that does not reach the rebuild must drop it.
func TestDefaultToolsAddedByAnAbandonedReloadDoNotActivateLater(t *testing.T) {
	newSession := func(t *testing.T) *Session {
		t.Helper()
		session := newRegistryPortSession(t, nil, SessionOptions{}, []extension.ToolDefinition{inactiveRegistryTool()}, nil)
		bindRegistryPort(t, session)
		return session
	}
	t.Run("a discarded reload activates nothing at the next unrelated refresh", func(t *testing.T) {
		session := newSession(t)
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"+grep"}})
		session.ReloadSettings()
		// The reload failed before the rebuild: an extension's runtime registerTool then refreshes the registry.
		session.DiscardAddedDefaultTools()
		if err := session.RefreshTools(); err != nil {
			t.Fatal(err)
		}
		if got := session.ActiveToolNames(); slices.Contains(got, "grep") {
			t.Fatalf("active = %q, want grep inactive: the abandoned reload never rebuilt the runtime", got)
		}
	})
	t.Run("a later reload replaces the list of an earlier one", func(t *testing.T) {
		session := newSession(t)
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"+grep"}})
		session.ReloadSettings()
		// The second reload reads the settings the first one already reloaded, so only find is new to it.
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"+grep", "+find"}})
		session.ReloadSettings()
		if err := session.RefreshTools(); err != nil {
			t.Fatal(err)
		}
		got := session.ActiveToolNames()
		if slices.Contains(got, "grep") || !slices.Contains(got, "find") {
			t.Fatalf("active = %q, want find newly active and grep not", got)
		}
	})
	t.Run("a completed reload still activates what it added", func(t *testing.T) {
		session := newSession(t)
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"+grep"}})
		reloadRegistryPort(t, session)
		session.DiscardAddedDefaultTools() // the deferred cleanup of a finished reload is a no-op
		if got := session.ActiveToolNames(); !slices.Contains(got, "grep") {
			t.Fatalf("active = %q, want grep", got)
		}
	})
}
