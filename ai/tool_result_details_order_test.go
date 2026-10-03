package ai

import (
	"encoding/json"
	"testing"
)

// A tool result's `details` is the value the tool wrote, and its members keep the order they were written in wherever the transcript is read back (packages/ai/src/types.ts ToolResultMessage.details is `unknown`; JSON.stringify keeps insertion order). The transcript's validating clone must not rewrite a raw JSON object into a map, which sorts its members.
func TestTranscriptKeepsRawToolResultDetailsInWrittenOrder(t *testing.T) {
	const details = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`
	source := json.RawMessage(details)
	messages := NormalizeContext(Context{Messages: []Message{
		ToolResultMessage{ToolCallID: "call", ToolName: "tool", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}, Details: source},
	}}).Messages()
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	got, ok := messages[0].(ToolResultMessage)
	if !ok {
		t.Fatalf("message = %#v", messages[0])
	}
	if encoded, err := json.Marshal(got.Details); err != nil || string(encoded) != details {
		t.Fatalf("details = %s, %v, want %s", encoded, err, details)
	}
	// The transcript owns its copy: a caller that reuses its buffer does not change it.
	copy(source, `{"x":0,"y":0,"z":0,"w":0,"v":0,"u":0,"t":0,"s":0,"r":0,"q":0,"p":0,"o":0,"n":0}`[:len(details)])
	if encoded, err := json.Marshal(got.Details); err != nil || string(encoded) != details {
		t.Fatalf("details after the source changed = %s, %v, want %s", encoded, err, details)
	}
}

// An invalid raw value is rejected where any other unserializable value is, instead of failing later in a provider request.
func TestTranscriptRejectsInvalidRawToolResultDetails(t *testing.T) {
	context := NormalizeContext(Context{Messages: []Message{
		ToolResultMessage{ToolCallID: "call", ToolName: "tool", Details: json.RawMessage(`{"a":`)},
	}})
	if got := context.Messages(); got != nil {
		t.Fatalf("messages = %#v, want the transcript rejected", got)
	}
}
