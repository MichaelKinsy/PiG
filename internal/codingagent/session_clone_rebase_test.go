package codingagent

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// A cloned session's entries are what their records decode to: the base each record names (its parentId chained to the entry before it in
// the clone) and, for a message entry built from the source's decoded message, a message that encodes as the record's does. Its file holds
// those records.
func TestClonedEntriesAreTheirRecords(t *testing.T) {
	dir := t.TempDir()
	manager := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions"))
	s, err := manager.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	mustAppend := func(_ string, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(s.AppendMessage(mkUserMsg("read \"main.go\" <&>")))
	mustAppend(s.AppendModelChange("faux", "faux-2"))
	mustAppend(s.AppendMessage(mkAssistantMsg("reading")))
	mustAppend(s.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "read",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n"}}, Details: &tools.ReadDetails{Path: "main.go"}, Timestamp: 3}}))
	first, _ := s.AppendMessage(mkUserMsg("next"))
	mustAppend(s.AppendLabelChange(first, new("start")))
	// Clone moves labels to the end, so the message after one gets a new parent.
	mustAppend(s.AppendMessage(mkAssistantMsg("after the label")))
	mustAppend(s.AppendCustomMessageEntry("note", "custom text", true, map[string]any{"b": 1.0, "a": 2.0}))
	mustAppend(s.AppendCompaction("summary", first, 100, nil, false, nil))
	mustAppend(s.AppendMessage(mkAssistantMsg("after the compaction")))
	clone, err := manager.Clone(s, *s.GetLeafID())
	if err != nil {
		t.Fatal(err)
	}
	var parent *string
	for _, entry := range clone.GetBranch() {
		raw := entry.Raw()
		decoded := sessionentry.DecodeSessionEntry(bytes.Clone(raw))
		base := entry.Base()
		if !reflect.DeepEqual(base, decoded.Base()) {
			t.Fatalf("entry %s has base %+v, its record %+v", base.ID, base, decoded.Base())
		}
		if base.Type != "label" && !reflect.DeepEqual(base.ParentID, parent) {
			t.Fatalf("entry %s has parent %v, want the entry before it, %v", base.ID, base.ParentID, parent)
		}
		if message, ok := asMessage(entry); ok {
			want, _ := asMessage(decoded)
			got, gotErr := jsstring.MarshalJSON(message.Message)
			wantText, wantErr := jsstring.MarshalJSON(want.Message)
			if gotErr != nil || wantErr != nil || !bytes.Equal(got, wantText) {
				t.Fatalf("entry %s holds a message encoding as\n%s\nits record's as\n%s", base.ID, got, wantText)
			}
		}
		id := base.ID
		parent = &id
	}
}
