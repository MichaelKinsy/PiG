package harness

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/harness/define.ts defineTool: an identity function that types a tool; the registration comes back unchanged.
func TestDefineToolReturnsTheRegistrationUnchanged(t *testing.T) {
	registration := durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "echo", Description: "Echo input"}}
	defined := DefineTool(registration)
	if defined == nil || defined.Name != "echo" || defined.Description != "Echo input" {
		t.Fatalf("DefineTool = %+v", defined)
	}
	defined.Name = "changed"
	if registration.Name != "echo" {
		t.Fatal("DefineTool aliased its argument")
	}
}

// Pi packages/durable/src/harness/tool.ts: the built-in tool task's result is { entryId, control? }: the result entry and the controls the
// result requested. It is the wire form a recovered task outcome decodes into, so an absent control stays absent and a requested one survives.
func TestToolTaskResultIsTheEntryAndTheRequestedControls(t *testing.T) {
	handoff := "reviewer"
	for _, c := range []struct {
		name string
		json string
		want ToolTaskResult
	}{
		{"entry only", `{"entryId":7}`, ToolTaskResult{EntryId: 7}},
		{"terminate", `{"entryId":8,"control":{"terminate":true}}`, ToolTaskResult{EntryId: 8, Control: &durable.ToolControl{Terminate: true}}},
		{"add tools and handoff", `{"entryId":9,"control":{"addTools":["grep"],"handoff":"reviewer"}}`, ToolTaskResult{EntryId: 9, Control: &durable.ToolControl{AddTools: []string{"grep"}, Handoff: &handoff}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			encoded, err := json.Marshal(c.want)
			if err != nil || string(encoded) != c.json {
				t.Fatalf("encoded %s (err %v), want %s", encoded, err, c.json)
			}
			var decoded ToolTaskResult
			if err := json.Unmarshal([]byte(c.json), &decoded); err != nil || !reflect.DeepEqual(decoded, c.want) {
				t.Fatalf("decoded %+v (err %v), want %+v", decoded, err, c.want)
			}
		})
	}
}
