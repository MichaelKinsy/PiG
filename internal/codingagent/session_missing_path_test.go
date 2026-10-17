package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi session-manager.ts:1029-1058 preserves an explicit missing path and defers persistence until the first user or assistant message.
func TestSessionManagerLoadMissingDefersExplicitPath(t *testing.T) {
	root := t.TempDir()
	sm := NewSessionManagerWithDir(root, filepath.Join(root, "sessions"))
	path := filepath.Join(root, "missing.jsonl")
	s, err := sm.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Path() != path || s.ID() == "" || sm.Current() != s || len(s.GetEntries()) != 0 {
		t.Fatalf("session=%+v", s)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("premature write: %v", err)
	}
	// Pi agent-session-runtime.ts:274-287 rejects both absent entries and non-user entries before creating a fork.
	for _, entryID := range []string{"absent", ""} {
		if _, _, err := sm.ForkToNewSession(s, entryID); err == nil || err.Error() != "Invalid entry ID for forking" {
			t.Fatalf("fork %q: %v", entryID, err)
		}
	}
	if _, err := s.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonStop}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sm.ForkToNewSession(s, *s.GetLeafID()); err == nil || err.Error() != "Invalid entry ID for forking" {
		t.Fatalf("fork assistant: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
