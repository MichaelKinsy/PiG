package codingagent

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// truncationDetails is a tool's typed details, a Go value that is not canonical JSON.
type truncationDetails struct {
	Truncated bool `json:"truncated"`
	Lines     int  `json:"lines"`
}

// filelessEncodes is how many records a session without a file encodes for each message of the corpus: none for a message held as appended,
// one for a message whose decoded form differs from it (a user message with no content, an assistant message with no usage), which is held as
// its record decodes, and one for the custom message, a custom_message entry.
var filelessEncodes = map[int]uint64{2: 1, 4: 1, 8: 1}

func filelessEncodeCorpus() []agent.AgentMessage {
	usage := &ai.Usage{Input: 10, Output: 20, TotalTokens: 30, Cost: ai.UsageCost{Total: 0.01}}
	text := strings.Repeat("a <b> & \"c\"\n\t", 4096)
	return []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("go <now> & \u2028 ok"), Timestamp: 1}},
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "blocks"}}, Timestamp: 2}},
		{User: &agent.UserMessage{Role: agent.RoleUser, Timestamp: 3}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "p", ModelID: "m", Usage: usage, StopReason: ai.StopReasonToolUse, Timestamp: 4,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "a"}, ai.ToolCall{ID: "c1", Name: "read", Arguments: map[string]any{"path": "a"}}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "p", ModelID: "m", StopReason: ai.StopReasonStop, Timestamp: 5}},
		{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "read", Timestamp: 6,
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}},
		{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "read", Timestamp: 7, Details: truncationDetails{Truncated: true, Lines: 7},
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}},
		{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "bash", Timestamp: 8, IsError: true, Details: map[string]any{"b": 1, "a": "x"},
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "boom"}}}},
		{Custom: map[string]any{"role": agent.RoleCustom, "customType": "note", "content": "hello", "display": true, "timestamp": float64(9)}},
	}
}

// messageRecord is the member of a message record that is the same for every session: the type and the message, which holds no id or time.
func messageRecord(t *testing.T, entry SessionEntry) (string, string) {
	t.Helper()
	var fields struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(entry.Raw(), &fields); err != nil {
		t.Fatal(err)
	}
	return fields.Type, string(fields.Message)
}

// A session without a file writes no record, so it encodes none for a message it holds as appended (every one the corpus has but the three in filelessEncodes); what its entries, accounting and gate
// show must be what a session with a file shows for the same messages.
func TestFilelessSessionEncodesNoRecordAndShowsWhatAFileSessionShows(t *testing.T) {
	filed := NewSession("filed", "/project")
	filed.SetPath(filepath.Join(t.TempDir(), "s.jsonl"))
	fileless := NewSession("fileless", "/project")

	var encodedFiled uint64
	for i, message := range filelessEncodeCorpus() {
		before := sessionLinesEncoded.Load()
		if _, err := fileless.AppendMessage(message); err != nil {
			t.Fatalf("fileless message %d: %v", i, err)
		}
		mid := sessionLinesEncoded.Load()
		if _, err := filed.AppendMessage(message); err != nil {
			t.Fatalf("filed message %d: %v", i, err)
		}
		if got, want := mid-before, filelessEncodes[i]; got != want {
			t.Errorf("message %d: a session without a file encoded %d records, want %d", i, got, want)
		}
		encodedFiled += sessionLinesEncoded.Load() - mid
	}
	if encodedFiled < uint64(len(filelessEncodeCorpus())) {
		t.Fatalf("a session with a file encoded %d records, want one per message", encodedFiled)
	}

	got, want := fileless.GetEntries(), filed.GetEntries()
	if len(got) != len(want) {
		t.Fatalf("entries: %d without a file, %d with one", len(got), len(want))
	}
	for i := range want {
		gotType, gotMessage := messageRecord(t, got[i])
		wantType, wantMessage := messageRecord(t, want[i])
		if gotType != wantType || gotMessage != wantMessage {
			t.Errorf("entry %d record:\n without a file %s %s\n with a file    %s %s", i, gotType, gotMessage, wantType, wantMessage)
		}
		gotEntry, gotOK := asMessage(got[i])
		wantEntry, wantOK := asMessage(want[i])
		if gotOK != wantOK || !reflect.DeepEqual(gotEntry.Message, wantEntry.Message) {
			t.Errorf("entry %d message differs:\n without a file %#v\n with a file    %#v", i, gotEntry.Message, wantEntry.Message)
		}
	}
	if gotAcc, wantAcc := fileless.Accounting(), filed.Accounting(); !reflect.DeepEqual(gotAcc, wantAcc) {
		t.Errorf("accounting: without a file %+v, with a file %+v", gotAcc, wantAcc)
	}
	// The record of an entry held without one is the same on every read, and is what the file session would write.
	for i := range got {
		if !bytes.Equal(got[i].Raw(), got[i].Raw()) {
			t.Errorf("entry %d record changes between reads", i)
		}
	}
}

// A message that cannot be encoded is reported as the append's error with or without a file, as before.
func TestFilelessSessionStillReportsAMessageThatCannotBeEncoded(t *testing.T) {
	message := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c", ToolName: "t", Details: make(chan int),
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}}}
	filed := NewSession("filed", "/project")
	filed.SetPath(filepath.Join(t.TempDir(), "s.jsonl"))
	_, wantErr := filed.AppendMessage(message)
	if wantErr == nil {
		t.Fatal("a message with a channel in its details was appended with a file")
	}
	fileless := NewSession("fileless", "/project")
	_, err := fileless.AppendMessage(message)
	if err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("without a file: %v, with a file: %v", err, wantErr)
	}
	if n := fileless.GetEntryCount(); n != 0 {
		t.Fatalf("the failed append left %d entries", n)
	}
}
