package ai

import (
	"encoding/json"
	"testing"
)

// The parsed schema stays in toolArgumentSchemas after a validation. The objects Convert wrapped belong to that validation's arguments, so the cached schema must not keep them reachable afterwards, on success or on failure.
func TestValidateToolArgumentsDoesNotKeepWrappedObjectsInTheCachedSchema(t *testing.T) {
	schema := json.RawMessage(`{"~kind":"Object","type":"object","properties":{"edits":{"~kind":"Array","type":"array","items":{"~kind":"Object","type":"object","properties":{"oldText":{"~kind":"String","type":"string"},"newText":{"~kind":"String","type":"string"}},"required":["oldText","newText"]}}}}`)
	for _, args := range []string{`{"edits":{"newText":"N","oldText":"O"}}`, `{"edits":{"newText":"N"}}`} {
		if _, err := ValidateToolArgumentsJSON("probe", schema, json.RawMessage(args)); (err == nil) != (args == `{"edits":{"newText":"N","oldText":"O"}}`) {
			t.Fatalf("%s: unexpected result %v", args, err)
		}
		entry := toolArgumentSchemas.entry(schema)
		entry.mu.Lock()
		parsed, err := entry.parsed()
		entry.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if len(parsed.wrapped) != 0 {
			t.Fatalf("%s: the cached schema keeps %d wrapped argument objects after validation", args, len(parsed.wrapped))
		}
	}
}
