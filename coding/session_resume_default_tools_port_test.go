package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/default-tools-setting.test.ts (Pi 6f2f164a, #10785) describe("resume").

// recordedToolsSessionManager is a session whose transcript records the loadout names, as a run records it.
func recordedToolsSessionManager(t *testing.T, names []string) *SessionManager {
	t.Helper()
	manager, err := NewInMemorySessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := make([]ai.ToolSchema, len(names))
	for i, name := range names {
		tools[i] = ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), ToolsAdded: tools, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	return manager
}

func resumeDefaultToolsPort(t *testing.T, manager *SessionManager, opts SessionOptions) *Session {
	t.Helper()
	opts.SessionManager = manager
	session := newRegistryPortSession(t, nil, opts, []extension.ToolDefinition{inactiveRegistryTool(), activeRegistryTool()}, nil)
	bindRegistryPort(t, session)
	return session
}

func TestResumeRestoresTheRecordedLoadoutPort(t *testing.T) {
	recorded := []string{"read", "grep", "inactive_tool"}
	check := func(t *testing.T, session *Session, want []string) {
		t.Helper()
		if got := sortedActiveNames(session); !slices.Equal(got, want) {
			t.Fatalf("active = %q, want %q", got, want)
		}
	}
	// active_tool was deactivated during the session, inactive_tool activated.
	t.Run("restores the loadout the transcript records", func(t *testing.T) {
		check(t, resumeDefaultToolsPort(t, recordedToolsSessionManager(t, recorded), SessionOptions{}), []string{"grep", "inactive_tool", "read"})
	})
	t.Run("does not restore excluded tools", func(t *testing.T) {
		session := resumeDefaultToolsPort(t, recordedToolsSessionManager(t, recorded), SessionOptions{ExcludedTools: map[string]struct{}{"inactive_tool": {}}})
		check(t, session, []string{"grep", "read"})
	})
	t.Run("uses explicit tool options instead of the recorded loadout", func(t *testing.T) {
		allowlisted := resumeDefaultToolsPort(t, recordedToolsSessionManager(t, recorded), SessionOptions{AllowedTools: map[string]struct{}{"read": {}, "write": {}}})
		if got, want := allowlisted.ActiveToolNames(), []string{"read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("allowlisted active = %q, want %q", got, want)
		}
		builtinless := resumeDefaultToolsPort(t, recordedToolsSessionManager(t, recorded), SessionOptions{NoTools: "builtin"})
		if got, want := builtinless.ActiveToolNames(), []string{"active_tool"}; !slices.Equal(got, want) {
			t.Fatalf("builtinless active = %q, want %q", got, want)
		}
	})
	t.Run("applies +name and -name tool options to the recorded loadout", func(t *testing.T) {
		session := resumeDefaultToolsPort(t, recordedToolsSessionManager(t, recorded), SessionOptions{DefaultToolModifiers: []string{"-grep", "+write"}})
		check(t, session, []string{"inactive_tool", "read", "write"})
	})
	t.Run("uses the default tools when the transcript records no loadout", func(t *testing.T) {
		manager, err := NewInMemorySessionManager(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}, Timestamp: 1}}); err != nil {
			t.Fatal(err)
		}
		check(t, resumeDefaultToolsPort(t, manager, SessionOptions{}), []string{"active_tool", "bash", "edit", "read", "write"})
	})
}
