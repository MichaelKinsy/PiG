package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// recordIdentityMessages are messages of every role and content block, with text that needs escapes and text JSON.stringify writes as it is.
func recordIdentityMessages() map[string]agent.AgentMessage {
	text := "line one\n\tquoted \"<&>\" \u2028 \U0001F600 back\\slash \x01 end"
	usage := &ai.Usage{Input: 12, Output: 3, CacheRead: 4, TotalTokens: 19, Cost: ai.UsageCost{Input: 0.25, Output: 1e-7, Total: 0.2500001}}
	duration := int64(42)
	return map[string]agent.AgentMessage{
		"user text":   {User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText(text), Timestamp: 1760000000000}},
		"user blocks": {User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}, ai.ImageContent{Data: "aGVsbG8=", MimeType: "image/png"}}, Timestamp: 2}},
		"assistant": {Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: "openai-completions", Provider: "faux", ModelID: "faux-1", Usage: usage,
			StopReason: ai.StopReasonToolUse, Timestamp: 3, Content: []ai.AssistantContentBlock{
				ai.ThinkingContent{Thinking: text, ThinkingSignature: "sig"},
				ai.TextContent{Text: text},
				ai.ToolCall{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.go", "offset": 2.0, "nested": map[string]any{"z": []any{true, nil, "x"}, "a": 1.5}}},
			}}},
		"assistant error": {Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1", Usage: &ai.Usage{},
			StopReason: ai.StopReasonError, ErrorMessage: "boom: " + text, Timestamp: 4, Content: []ai.AssistantContentBlock{}}},
		"tool result typed details": {ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call_1", ToolName: "read", Timestamp: 5,
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: &tools.ReadDetails{Path: "a.go", Truncation: &tools.TruncationResult{
				Content: text, Truncated: true, TruncatedBy: "lines", TotalLines: 9, OutputLines: 2, MaxLines: 2}}}},
		"tool result map details": {ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call_2", ToolName: "bash", IsError: true,
			DurationMs: &duration, Usage: usage, Timestamp: 6, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
			Details: map[string]any{"exitCode": 1.0, "2": "integer-like key", "b": []any{}}}},
		"tool result no details": {ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call_3", ToolName: "ls", Timestamp: 7,
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat(text, 3000)}}}},
	}
}

// jsValuedID is the id of an entry whose record holds a number as its id, as a session loaded from such a file holds it.
func jsValuedID(t *testing.T) string {
	t.Helper()
	base := sessionentry.DecodeSessionEntry(json.RawMessage(`{"type":"model_change","id":7,"parentId":null,"timestamp":"2026-10-13T08:00:00.000Z","provider":"p","modelId":"m"}`)).Base()
	if !sessionentry.IsJSValue(base.ID) {
		t.Fatalf("id %q is not a JavaScript value key", base.ID)
	}
	return base.ID
}

// A message entry the session holds without its record encodes to the record it was appended with: the record is the encoding of the entry's
// own value, and every reader of the entry (Raw, MarshalJSON, the encoder's AppendStringify) gets those bytes.
func TestReleasedRecordEncodesToTheAppendedRecord(t *testing.T) {
	hex := "0a1b2c3d"
	parents := map[string]*string{"root": nil, "hex parent": &hex}
	js := jsValuedID(t)
	parents["number-valued parent"] = &js
	for name, msg := range recordIdentityMessages() {
		for parentName, parent := range parents {
			t.Run(name+"/"+parentName, func(t *testing.T) {
				entry := MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: "4e5f6a7b", ParentID: parent, Timestamp: "2026-10-13T08:00:00.123Z"},
					Message: msg}
				raw, err := marshalSessionLine(entry)
				if err != nil {
					t.Fatal(err)
				}
				if _, appended := heldMessageEntry(entry, raw); !appended {
					t.Skip("the session holds the record's decode, with its record")
				}
				if got := entry.Raw(); !bytes.Equal(got, raw) {
					t.Fatalf("Raw re-encodes\n%s\nthe record is\n%s", got, raw)
				}
				if got, err := jsstring.MarshalJSON(entry); err != nil || !bytes.Equal(got, raw) {
					t.Fatalf("jsstring.MarshalJSON re-encodes\n%s (%v)\nthe record is\n%s", got, err, raw)
				}
				if got, err := marshalSessionLine(entry); err != nil || !bytes.Equal(got, raw) {
					t.Fatalf("marshalSessionLine re-encodes\n%s (%v)\nthe record is\n%s", got, err, raw)
				}
			})
		}
	}
}

// A session serves the appended records through every read after it let go of them. The same messages are appended to a session with a file
// and one without, and a compaction evicts the history before its kept entry, so the oldest are compressed or read back: every message entry
// of each encodes to the file's line for it, with its own id, parentId and timestamp.
func TestSessionServesTheRecordsAFileHolds(t *testing.T) {
	dir := t.TempDir()
	withFile, err := NewSessionManagerWithDir(dir, t.TempDir()).Create("records", "")
	if err != nil {
		t.Fatal(err)
	}
	without := NewSession("no-file", dir)
	messages := recordIdentityMessages()
	names := make([]string, 0, len(messages))
	for name := range messages {
		names = append(names, name)
	}
	slices.Sort(names)
	var kept [2]string
	for round := range retainTailEntries/len(names) + 3 {
		for _, name := range names {
			for k, session := range []*Session{withFile, without} {
				id, err := session.AppendMessage(messages[name])
				if err != nil {
					t.Fatal(err)
				}
				if round == 2 && kept[k] == "" {
					kept[k] = id
				}
			}
		}
	}
	// A compaction takes the history before its kept entry out of the resident log: the file-backed session reads it back from the file, the
	// other from the compressed records eviction made.
	for k, session := range []*Session{withFile, without} {
		if _, err := session.AppendCompaction("summary", kept[k], 1000, nil, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if resident := without.residentCountForTest(); resident >= len(without.GetEntries())-1 {
		t.Fatalf("%d of %d entries resident: the compaction evicted none", resident, len(without.GetEntries()))
	}
	data, err := os.ReadFile(withFile.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))[1:] // the header first
	rebase := func(line []byte, from, to SessionEntryBase) []byte {
		quote := func(s string) string { encoded, _ := json.Marshal(s); return string(encoded) }
		out := strings.Replace(string(line), `"id":`+quote(from.ID), `"id":`+quote(to.ID), 1)
		if from.ParentID != nil && to.ParentID != nil {
			out = strings.Replace(out, `"parentId":`+quote(*from.ParentID), `"parentId":`+quote(*to.ParentID), 1)
		}
		return []byte(strings.Replace(out, `"timestamp":`+quote(from.Timestamp), `"timestamp":`+quote(to.Timestamp), 1))
	}
	fileEntries := withFile.GetEntries()
	for name, session := range map[string]*Session{"with a file": withFile, "without a file": without} {
		reads := map[string][]SessionEntry{"GetEntries": session.GetEntries(), "GetBranch": session.GetBranch()}
		for read, entries := range reads {
			if len(entries) != len(lines) {
				t.Fatalf("%s %s: %d entries, the file holds %d lines", name, read, len(entries), len(lines))
			}
			for i, entry := range entries {
				fileEntry := fileEntries[i].Base()
				if fileEntry.Type != "message" {
					continue // a compaction names its kept entry, whose id differs between the sessions
				}
				if want := rebase(lines[i], fileEntry, entry.Base()); !bytes.Equal(entry.Raw(), want) {
					t.Fatalf("%s %s entry %d serves\n%s\nthe file holds\n%s", name, read, i, entry.Raw(), want)
				}
			}
		}
	}
}

// residentCountForTest is the number of entries the session holds decoded.
func (s *Session) residentCountForTest() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for i := range s.slots {
		if s.slots[i].entry != nil {
			n++
		}
	}
	return n
}
