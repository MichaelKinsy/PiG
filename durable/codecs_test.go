package durable

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestTaskOutcomeKeepsAPresentNullResult(t *testing.T) {
	completed := TaskOutcome[JsonValue]{Status: OutcomeCompleted}
	if got := mustJSON(t, completed); got != `{"status":"completed","result":null}` {
		t.Fatalf("completed = %s", got)
	}
	var decoded TaskOutcome[JsonValue]
	if err := json.Unmarshal([]byte(`{"status":"completed","result":null}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Result == nil || *decoded.Result != nil {
		t.Fatalf("result = %#v, want a present null", decoded.Result)
	}
	var aborted TaskOutcome[JsonValue]
	if err := json.Unmarshal([]byte(`{"status":"aborted","reason":"user"}`), &aborted); err != nil {
		t.Fatal(err)
	}
	if aborted.Result != nil || aborted.Reason == nil || *aborted.Reason != "user" {
		t.Fatalf("aborted = %#v", aborted)
	}
	if got := mustJSON(t, aborted); got != `{"status":"aborted","reason":"user"}` {
		t.Fatalf("aborted = %s", got)
	}
}

func TestEntryRecordRoundTripsMessagesByRole(t *testing.T) {
	head := EntryId(3)
	entry := EntryRecord{
		Id: 4, ConversationId: 1, Kind: "pi.user",
		Model: []ai.Message{
			ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1},
			ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "a"}}, Timestamp: 2},
			ai.ToolResultMessage{ToolCallID: "c", ToolName: "t", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "r"}}, Timestamp: 3},
			ai.SystemMessage{Content: ai.SystemText(""), Timestamp: 4},
		},
		Head:  &head,
		Edits: []ContextEdit{{Target: 2, Action: EditOmit}, {Target: 3, Action: EditReplace, Messages: []ai.Message{}}},
	}
	encoded := mustJSON(t, entry)
	var decoded EntryRecord
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if mustJSON(t, decoded) != encoded {
		t.Fatalf("round trip:\n%s\n%s", encoded, mustJSON(t, decoded))
	}
	if _, ok := decoded.Model[2].(ai.ToolResultMessage); !ok {
		t.Fatalf("model[2] = %T", decoded.Model[2])
	}
	if decoded.Edits[0].Messages != nil || decoded.Edits[1].Messages == nil {
		t.Fatalf("edits = %#v", decoded.Edits)
	}
	var display EntryRecord
	if err := json.Unmarshal([]byte(`{"id":5,"conversationId":1,"kind":"note","data":{"a":1}}`), &display); err != nil {
		t.Fatal(err)
	}
	if display.Model != nil || !reflect.DeepEqual(display.Data, map[string]any{"a": float64(1)}) {
		t.Fatalf("display = %#v", display)
	}
}

func TestEntryDraftEncodesTheSelfHead(t *testing.T) {
	draft := EntryDraft{Kind: "pi.reset", HeadSelf: true}
	if got := mustJSON(t, draft); got != `{"kind":"pi.reset","head":"self"}` {
		t.Fatalf("draft = %s", got)
	}
	var decoded EntryDraft
	if err := json.Unmarshal([]byte(`{"kind":"pi.reset","head":"self"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.HeadSelf || decoded.Head != nil {
		t.Fatalf("decoded = %#v", decoded)
	}
	if err := json.Unmarshal([]byte(`{"kind":"pi.compaction","head":7}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.HeadSelf || decoded.Head == nil || *decoded.Head != 7 {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestDecodeUserContentAcceptsTextAndBlocks(t *testing.T) {
	text, err := DecodeUserContent("hi")
	if err != nil || text != ai.UserText("hi") {
		t.Fatalf("text = %#v, %v", text, err)
	}
	blocks, err := DecodeUserContent([]any{map[string]any{"type": "text", "text": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if typed, ok := blocks.(ai.UserContentBlocks); !ok || len(typed) != 1 || typed[0].(ai.TextContent).Text != "a" {
		t.Fatalf("blocks = %#v", blocks)
	}
}
