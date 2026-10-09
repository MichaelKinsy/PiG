// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func persistedTypes(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	var types []string
	for line := range bytes.SplitSeq(bytes.TrimSpace(data), []byte("\n")) {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		if entry.Type == "message" {
			types = append(types, entry.Message.Role)
		} else {
			types = append(types, entry.Type)
		}
	}
	return types
}

func persistUser(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
}

// Persist is SessionManager._persist (session-manager.ts:1172). The three cases are the file-creation suite in
// test/session-manager/file-operations.test.ts:436-475: setup entries leave no file, the first user message creates it with the header and
// every buffered entry, and later entries append without rewriting.
func TestSessionPersistCreatesTheFileOnTheFirstConversationEntry(t *testing.T) {
	t.Run("does not create a file for a session with only setup entries", func(t *testing.T) {
		sess, err := NewSessionManagerWithDir(t.TempDir(), t.TempDir()).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendModelChange("anthropic", "claude-sonnet-4-5"); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendThinkingLevelChange("off"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(sess.Path()); !os.IsNotExist(err) {
			t.Fatalf("session file exists after setup entries only (stat err = %v)", err)
		}
	})
	t.Run("creates the file when the first user message is appended", func(t *testing.T) {
		sess, err := NewSessionManagerWithDir(t.TempDir(), t.TempDir()).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendModelChange("anthropic", "claude-sonnet-4-5"); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendMessage(persistUser("first question")); err != nil {
			t.Fatal(err)
		}
		got := persistedTypes(t, sess.Path())
		if want := []string{"session", "model_change", "user"}; !slices.Equal(got, want) {
			t.Fatalf("file entries = %v, want %v", got, want)
		}
	})
	t.Run("appends later entries to the file without rewriting earlier ones", func(t *testing.T) {
		sess, err := NewSessionManagerWithDir(t.TempDir(), t.TempDir()).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendMessage(persistUser("first question")); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(sess.Path())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sess.AppendCustomEntry("preset-state", map[string]any{"name": "plan"}); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(sess.Path())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(after, before) {
			t.Fatalf("earlier lines were rewritten:\nbefore %q\nafter  %q", before, after)
		}
		if got, want := persistedTypes(t, sess.Path()), []string{"session", "user", "custom"}; !slices.Equal(got, want) {
			t.Fatalf("file entries = %v, want %v", got, want)
		}
	})
}

// A session without a file path persists nothing (_persist returns when !persist || !sessionFile, session-manager.ts:1173).
func TestSessionPersistWithoutAFilePersistsNothing(t *testing.T) {
	sess := NewSession("memory", t.TempDir())
	if sess.Path() != "" {
		t.Fatalf("an in-memory session has path %q", sess.Path())
	}
	id, err := sess.AppendMessage(persistUser("hello"))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := sess.GetEntry(id)
	if !ok {
		t.Fatal("entry missing")
	}
	if err := sess.Persist(entry); err != nil {
		t.Fatalf("Persist on an in-memory session: %v", err)
	}
}
