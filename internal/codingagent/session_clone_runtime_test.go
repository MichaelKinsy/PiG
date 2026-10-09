package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's root-user fallback is still memory-only when the source manager is memory-only.
func TestForkBeforeRootUserKeepsMemoryOnlyStorage(t *testing.T) {
	cwd := t.TempDir()
	source := NewSession("source", cwd)
	user, err := source.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "Say hi"}}}})
	if err != nil {
		t.Fatal(err)
	}
	storage := NewSessionManagerWithDir(cwd, filepath.Join(t.TempDir(), "sessions"))
	forked, text, err := storage.ForkToNewSession(source, user)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Say hi" || forked.Path() != "" || len(forked.GetEntries()) != 0 {
		t.Fatalf("fork text=%q path=%q entries=%v", text, forked.Path(), forked.GetEntries())
	}
}

// Upstream 0.99.1 createBranchedSession gates writes on the retained branch holding a user or assistant message (session-manager.ts:1717-1725), not on whether the source once had one.
func TestClonePreservesPersistenceModeAndDefersConversationFreeBranches(t *testing.T) {
	for _, tc := range []struct {
		name       string
		persisted  bool
		leafKind   string
		wantOnDisk bool
	}{
		{"memory setup", false, "setup", false}, {"memory user", false, "user", false}, {"memory assistant", false, "assistant", false},
		{"disk setup", true, "setup", false}, {"disk user", true, "user", true}, {"disk assistant", true, "assistant", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			storage := NewSessionManagerWithDir(cwd, filepath.Join(t.TempDir(), "sessions"))
			source := NewSession("source", cwd)
			if tc.persisted {
				var err error
				source, err = storage.Create("source", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := source.AppendThinkingLevelChange("off"); err != nil {
				t.Fatal(err)
			}
			setup := *source.GetLeafID()
			user, err := source.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}})
			if err != nil {
				t.Fatal(err)
			}
			assistant, err := source.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "reply"}}, StopReason: ai.StopReasonStop}})
			if err != nil {
				t.Fatal(err)
			}
			leaf := map[string]string{"setup": setup, "user": user, "assistant": assistant}[tc.leafKind]
			cloned, err := storage.Clone(source, leaf)
			if err != nil {
				t.Fatal(err)
			}
			if cloned.ID() == source.ID() {
				t.Fatal("clone reused source identity")
			}
			if cloned.IsPersisted() != tc.persisted {
				t.Fatalf("persisted=%v want=%v", cloned.IsPersisted(), tc.persisted)
			}
			if cloned.GetLeafID() == nil || *cloned.GetLeafID() != leaf {
				t.Fatalf("leaf=%v want=%s", cloned.GetLeafID(), leaf)
			}
			if tc.persisted {
				_, statErr := os.Stat(cloned.Path())
				if tc.wantOnDisk && statErr != nil {
					t.Fatal(statErr)
				}
				if !tc.wantOnDisk && !os.IsNotExist(statErr) {
					t.Fatalf("clone without a user or assistant message was written: %v", statErr)
				}
			}
		})
	}
}
