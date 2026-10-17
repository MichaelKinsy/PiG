package sessionentry

import (
	"bytes"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A message entry appends what its MarshalJSON writes in the JSON.stringify mode: built in memory with a string, null or non-string
// parentId, and decoded from a record it writes back.
func TestMessageEntryAppendStringifyMatchesMarshalJSON(t *testing.T) {
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Timestamp: 1, Content: []ai.AssistantContentBlock{
		ai.TextContent{Text: "<b> & \u2028 \xed\xa0\x80 \xff"}, ai.ToolCall{ID: "c", Name: "n", Arguments: ai.JsonObject{"x": math.Copysign(0, -1)}}}}}
	parent := "p<1>"
	entries := map[string]MessageEntry{}
	for name, parentID := range map[string]*string{"string parent": &parent, "root": nil, "number parent": new(jsValueKey(json.RawMessage("-0"))),
		"undefined parent": new(jsValueKey(nil)), "object parent": new(jsValueKey(json.RawMessage(`{"a":"<"}`)))} {
		entries[name] = MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: "e&1", ParentID: parentID, Timestamp: "t"}, Message: message}
	}
	decoded, ok := DecodeSessionEntry(json.RawMessage(`{"type":"message","id":"e1","parentId":null,"timestamp":"t","extra":"\u003c","message":{"role":"user","content":"x","timestamp":1}}`)).(MessageEntry)
	if !ok {
		t.Fatal("record did not decode to a message entry")
	}
	entries["decoded"] = decoded
	for name, entry := range entries {
		marshaled, err := entry.MarshalJSON()
		if err != nil {
			t.Fatalf("%s: MarshalJSON: %v", name, err)
		}
		want, err := json.MarshalStringify(json.RawMessage(marshaled))
		if err != nil {
			t.Fatal(err)
		}
		appended, err := entry.AppendStringify([]byte("prefix:"))
		if err != nil || string(appended) != "prefix:"+string(want) {
			t.Fatalf("%s: AppendStringify = %s, %v\nwant prefix:%s", name, appended, err, want)
		}
		if line, err := json.MarshalStringify(entry); err != nil || !bytes.Equal(line, want) {
			t.Fatalf("%s: MarshalStringify = %s, %v\nwant %s", name, line, err, want)
		}
	}
}
