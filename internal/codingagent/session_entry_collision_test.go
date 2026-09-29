package codingagent

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi session-manager.ts:277-284 retries short IDs against byId for every entry producer, not only reconstructed labels.
func TestSessionGeneratedEntriesRejectExistingIDs(t *testing.T) {
	const occupied = "01020304"
	user := func(text string) agent.AgentMessage {
		return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
	}
	leaf := func(s *Session, err error) (string, error) {
		if err != nil {
			return "", err
		}
		return *s.LeafID(), nil
	}
	for _, tc := range []struct {
		name string
		add  func(*Session) (string, error)
	}{
		{"message", func(s *Session) (string, error) { return s.AppendMessage(user("next")) }},
		{"custom", func(s *Session) (string, error) { return s.AppendCustomMessage("probe", "next", true, nil) }},
		{"bash", func(s *Session) (string, error) {
			return s.AppendBashExecution(BashExecutionMessage{Command: "true", ExitCode: new(0), Timestamp: 1})
		}},
		{"model", func(s *Session) (string, error) { return leaf(s, s.AppendModelSwitch("provider", "model", "")) }},
		{"thinking", func(s *Session) (string, error) { return leaf(s, s.AppendThinkingLevelChange("off")) }},
		{"usage", func(s *Session) (string, error) {
			entry, err := s.AppendUsage("generation", "provider", "model", ai.Usage{}, "")
			return entry.ID, err
		}},
		{"compaction", func(s *Session) (string, error) { return s.AppendCompaction("summary", occupied, 1, nil, false, nil) }},
		{"branch-summary", func(s *Session) (string, error) {
			return s.AppendBranchSummary(new(occupied), "summary", nil, false, nil)
		}},
		{"label", func(s *Session) (string, error) { return leaf(s, s.AppendLabelChange(occupied, new("label"))) }},
		{"session-info", func(s *Session) (string, error) { return s.AppendSessionInfo("name") }},
		{"custom-entry", func(s *Session) (string, error) { return s.AppendCustomEntry("probe", "next") }},
		{"context-edit", func(s *Session) (string, error) { return s.AppendContextEdit(occupied, nil) }},
		{"extension-entry", func(s *Session) (string, error) {
			entry, err := AppendExtensionEntry(s, "probe", "next", nil)
			return entry.ID, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession("collision-fixture", t.TempDir())
			if err := s.AppendEntry(MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: occupied, Timestamp: "2026-01-01T00:00:00.000Z"}, Message: user("seed")}); err != nil {
				t.Fatal(err)
			}
			before, _ := s.EntryByID(occupied)
			// These cases are sequential. Only the entry generator consumes entropy after the Session is constructed.
			reader := rand.Reader
			rand.Reader = bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
			t.Cleanup(func() { rand.Reader = reader })
			id, err := tc.add(s)
			if err != nil {
				t.Fatal(err)
			}
			if id != "05060708" {
				t.Errorf("generated ID=%q; must reject occupied %q and use the next entropy value", id, occupied)
			}
			after, _ := s.EntryByID(occupied)
			if !bytes.Equal(before.Raw(), after.Raw()) {
				t.Error("collision replaced the existing index entry")
			}
		})
	}
}

// The real-Pi pair controls entropy at the same entry-allocation boundary and observes identities, parent links and retained indexed content.
func TestSessionEntryCollisionPairedProbe(t *testing.T) {
	s := NewSession("collision-fixture", t.TempDir())
	entropy := []byte{1, 2, 3, 4, 1, 2, 3, 4, 5, 6, 7, 8}
	reader := bytes.NewReader(entropy)
	original := rand.Reader
	rand.Reader = reader
	t.Cleanup(func() { rand.Reader = original })
	appendUser := func(text string) string {
		id, err := s.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second := appendUser("seed"), appendUser("next")
	entries := s.Entries()
	indexed, ok := s.EntryByID(first)
	if !ok {
		t.Fatal("seed entry disappeared")
	}
	message, ok := indexed.AsMessage()
	if !ok || message.Message.User == nil {
		t.Fatal("seed is not a user message")
	}
	seed := message.Message.User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text
	observation := struct {
		IDs     []string  `json:"ids"`
		Parents []*string `json:"parents"`
		Seed    string    `json:"seed"`
		Calls   int       `json:"calls"`
	}{[]string{first, second}, []*string{entries[0].Base.ParentID, entries[1].Base.ParentID}, seed, (len(entropy) - reader.Len()) / 4}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SESSION_COLLISION %s\n", encoded)
	if first != "01020304" || second != "05060708" || seed != "seed" {
		t.Fatalf("collision changed the log: %s", encoded)
	}
}
