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
	if text != "Say hi" || forked.Path() != "" || len(forked.Entries()) != 0 {
		t.Fatalf("fork text=%q path=%q entries=%v", text, forked.Path(), forked.Entries())
	}
}

// Pi session-manager.ts:createBranchedSession gates writes on the retained branch, not on whether the source once had an assistant.
func TestClonePreservesPersistenceModeAndDefersAssistantFreeBranches(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		persisted, keepAssistant bool
	}{
		{"memory user", false, false}, {"memory assistant", false, true},
		{"disk user", true, false}, {"disk assistant", true, true},
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
			user, err := source.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}})
			if err != nil {
				t.Fatal(err)
			}
			assistant, err := source.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "reply"}}, StopReason: ai.StopReasonStop}})
			if err != nil {
				t.Fatal(err)
			}
			leaf := user
			if tc.keepAssistant {
				leaf = assistant
			}
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
			if cloned.LeafID() == nil || *cloned.LeafID() != leaf {
				t.Fatalf("leaf=%v want=%s", cloned.LeafID(), leaf)
			}
			if tc.persisted {
				_, statErr := os.Stat(cloned.Path())
				if tc.keepAssistant && statErr != nil {
					t.Fatal(statErr)
				}
				if !tc.keepAssistant && !os.IsNotExist(statErr) {
					t.Fatalf("assistant-free clone was written: %v", statErr)
				}
			}
		})
	}
}
