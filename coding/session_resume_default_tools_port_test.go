package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/default-tools-setting.test.ts (Pi bd3c9b8237, #11062/#11075) describe("resume").

func toolSchemas(names []string) []ai.ToolSchema {
	tools := make([]ai.ToolSchema, len(names))
	for i, name := range names {
		tools[i] = ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
	}
	return tools
}

// recordLoadoutChange appends a system message that adds and removes tools, as a run records loadout changes.
func recordLoadoutChange(t *testing.T, manager *SessionManager, added, removed []string) string {
	t.Helper()
	message := &ai.SystemMessage{Content: ai.SystemText(""), Timestamp: 1}
	if len(added) > 0 {
		message.ToolsAdded = toolSchemas(added)
	}
	for _, name := range removed {
		message.ToolsRemoved = append(message.ToolsRemoved, ai.ToolReference{Name: name})
	}
	id, err := manager.AppendMessage(agent.AgentMessage{System: message})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func appendRestoreUserMessage(t *testing.T, manager *SessionManager, text string) string {
	t.Helper()
	id, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// recordedToolsSessionManager is a session whose transcript records the loadout names, as a run records it.
func recordedToolsSessionManager(t *testing.T, names []string) *SessionManager {
	t.Helper()
	manager, err := NewInMemorySessionManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recordLoadoutChange(t, manager, names, nil)
	return manager
}

// changedToolsSessionManager is a session that recorded read, grep and inactive_tool, and deactivated active_tool.
func changedToolsSessionManager(t *testing.T) *SessionManager {
	t.Helper()
	manager := recordedToolsSessionManager(t, []string{"read", "grep", "active_tool"})
	recordLoadoutChange(t, manager, []string{"inactive_tool"}, []string{"active_tool"})
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
	check := func(t *testing.T, session *Session, want []string) {
		t.Helper()
		if got := sortedActiveNames(session); !slices.Equal(got, want) {
			t.Fatalf("active = %q, want %q", got, want)
		}
	}
	t.Run("restores the loadout the transcript records", func(t *testing.T) {
		check(t, resumeDefaultToolsPort(t, changedToolsSessionManager(t), SessionOptions{}), []string{"grep", "inactive_tool", "read"})
	})
	// #11062
	t.Run("activates extension tools the transcript never added or removed", func(t *testing.T) {
		// Built-in tools and tools registered inactive stay inactive.
		check(t, resumeDefaultToolsPort(t, recordedToolsSessionManager(t, []string{"read", "grep"}), SessionOptions{}), []string{"active_tool", "grep", "read"})
	})
	t.Run("keeps tools removed before a compaction inactive", func(t *testing.T) {
		manager := changedToolsSessionManager(t)
		kept := appendRestoreUserMessage(t, manager, "hi")
		if _, err := manager.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
			t.Fatal(err)
		}
		check(t, resumeDefaultToolsPort(t, manager, SessionOptions{}), []string{"grep", "inactive_tool", "read"})
	})
	// #11075
	t.Run("restores the target branch's loadout on tree navigation", func(t *testing.T) {
		// The target branch never mentions active_tool, so it is active there. It removed grep.
		manager := recordedToolsSessionManager(t, []string{"read", "grep"})
		recordLoadoutChange(t, manager, nil, []string{"grep"})
		target := appendRestoreUserMessage(t, manager, "first")
		recordLoadoutChange(t, manager, []string{"grep"}, []string{"active_tool"})
		session := resumeDefaultToolsPort(t, manager, SessionOptions{})
		check(t, session, []string{"grep", "read"})
		if _, err := session.NavigateTree(t.Context(), target, NavigateTreeOptions{}); err != nil {
			t.Fatal(err)
		}
		check(t, session, []string{"active_tool", "read"})
	})
	t.Run("keeps registered tools outside the tools allowlist inactive on tree navigation", func(t *testing.T) {
		// An allowlist without mcp__ entries keeps MCP tools registered but inactive.
		manager := recordedToolsSessionManager(t, []string{"read"})
		target := appendRestoreUserMessage(t, manager, "first")
		lookup := registryTool("mcp__server__lookup", "Lookup", "MCP tool", "")
		session := newRegistryPortSession(t, nil, SessionOptions{SessionManager: manager, AllowedTools: map[string]struct{}{"read": {}}}, []extension.ToolDefinition{lookup}, nil)
		bindRegistryPort(t, session)
		check(t, session, []string{"read"})
		if _, err := session.NavigateTree(t.Context(), target, NavigateTreeOptions{}); err != nil {
			t.Fatal(err)
		}
		check(t, session, []string{"read"})
	})
	t.Run("does not restore excluded tools", func(t *testing.T) {
		session := resumeDefaultToolsPort(t, changedToolsSessionManager(t), SessionOptions{ExcludedTools: map[string]struct{}{"inactive_tool": {}}})
		check(t, session, []string{"grep", "read"})
	})
	t.Run("uses explicit tool options instead of the recorded loadout", func(t *testing.T) {
		allowlisted := resumeDefaultToolsPort(t, changedToolsSessionManager(t), SessionOptions{AllowedTools: map[string]struct{}{"read": {}, "write": {}}})
		if got, want := allowlisted.ActiveToolNames(), []string{"read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("allowlisted active = %q, want %q", got, want)
		}
		builtinless := resumeDefaultToolsPort(t, changedToolsSessionManager(t), SessionOptions{NoTools: "builtin"})
		if got, want := builtinless.ActiveToolNames(), []string{"active_tool"}; !slices.Equal(got, want) {
			t.Fatalf("builtinless active = %q, want %q", got, want)
		}
	})
	t.Run("applies +name and -name tool options to the recorded loadout", func(t *testing.T) {
		session := resumeDefaultToolsPort(t, changedToolsSessionManager(t), SessionOptions{DefaultToolModifiers: []string{"-grep", "+write"}})
		check(t, session, []string{"inactive_tool", "read", "write"})
	})
	t.Run("uses the default tools when the transcript records no loadout", func(t *testing.T) {
		manager, err := NewInMemorySessionManager(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		appendRestoreUserMessage(t, manager, "hi")
		check(t, resumeDefaultToolsPort(t, manager, SessionOptions{}), []string{"active_tool", "bash", "edit", "read", "write"})
	})
}
