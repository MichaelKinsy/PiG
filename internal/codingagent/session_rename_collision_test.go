package codingagent

import (
	"bytes"
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestRenameSessionChecksExistingEntryIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	s := NewSession("fixture", dir)
	s.SetPath(path)
	seed := MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: "01020304", Timestamp: "2026-01-01T00:00:00.000Z"}, Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "seed"}}, StopReason: ai.StopReasonStop}}}
	if err := s.AppendEntry(seed); err != nil {
		t.Fatal(err)
	}
	original := rand.Reader
	rand.Reader = bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	defer func() { rand.Reader = original }()
	if err := NewSessionManagerWithDir(dir, dir).RenameSession(path, "renamed"); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := loaded.Entries()
	if len(entries) != 2 || entries[0].Base.ID != "01020304" || entries[1].Base.ID != "05060708" || entries[1].Base.ParentID == nil || *entries[1].Base.ParentID != entries[0].Base.ID {
		t.Fatalf("rename did not preserve the original entry and append its unique child: %#v", entries)
	}
}
