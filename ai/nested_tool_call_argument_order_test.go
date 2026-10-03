package ai

import (
	"encoding/json"
	"testing"
)

// A nested call's recorded arguments are persisted on the tool result. Pi writes JSON.parse of the arguments the script sent,
// in the script's order (nested-tool-calls.ts:56-77); a reloaded session writes them again unchanged.
func TestNestedToolCallRecordKeepsArgumentMemberOrderThroughJSONAndClone(t *testing.T) {
	const wire = `{"id":"c/1","name":"probe","status":"ok","arguments":{"zeta":1,"alpha":{"yy":2,"bb":3}}}`
	var record NestedToolCallRecord
	if err := json.Unmarshal([]byte(wire), &record); err != nil {
		t.Fatal(err)
	}
	for name, subject := range map[string]NestedToolCallRecord{
		"decoded": record,
		"cloned":  cloneNestedToolCalls(&NestedToolCalls{Calls: []NestedToolCallRecord{record}}).Calls[0],
	} {
		encoded, err := json.Marshal(subject)
		if err != nil || string(encoded) != wire {
			t.Errorf("%s record = %s, %v; want %s", name, encoded, err, wire)
		}
	}
	var viaSetter NestedToolCallRecord
	if err := viaSetter.SetArgumentsJSON([]byte(`{"zeta":1,"alpha":2}`)); err != nil {
		t.Fatal(err)
	}
	if encoded, err := json.Marshal(viaSetter); err != nil || string(encoded) != `{"id":"","name":"","status":"","arguments":{"zeta":1,"alpha":2}}` {
		t.Errorf("record from SetArgumentsJSON = %s, %v", encoded, err)
	}
	var omitted NestedToolCallRecord
	if err := json.Unmarshal([]byte(`{"id":"c/2","name":"probe","status":"ok","argumentsBytes":99}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.Arguments != nil {
		t.Errorf("a record without arguments decoded %v, want nil", omitted.Arguments)
	}
}
