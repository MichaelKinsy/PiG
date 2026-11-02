package rpcclient

import (
	"encoding/json"
	"testing"
)

// rpc-client.ts:525-538 hands the listener `JSON.parse(line) as JsonAgentSessionEvent`: the record itself. Every event's Raw is the complete
// record whose "type" is Type. A message_update is in json-event.ts's shape: the message's usage, and an assistantMessageEvent without the
// cumulative `partial` snapshot (toJsonEvent, json-event.ts:55-69); message_end then carries the authoritative assistant message
// (json-event.ts doc comment), which decodes as an AgentMessage.
func TestJsonAgentSessionEventIsTheCompleteJSONRecordInJSONEventShape(t *testing.T) {
	f := newFauxFixture(t)
	events := f.promptAndWait(t, "reply with exactly: hello")
	var updates, assistantEnds int
	check := func(event JsonAgentSessionEvent) {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(event.Raw, &record); err != nil {
			t.Fatalf("%s Raw = %q is not a JSON record: %v", event.Type, event.Raw, err)
		}
		if got := string(record["type"]); got != `"`+event.Type+`"` {
			t.Fatalf("Raw type %s != Type %q", got, event.Type)
		}
		switch event.Type {
		case "message_update":
			updates++
			var update struct {
				Usage                 *json.RawMessage `json:"usage"`
				AssistantMessageEvent map[string]any   `json:"assistantMessageEvent"`
			}
			if err := json.Unmarshal(event.Raw, &update); err != nil {
				t.Fatal(err)
			}
			if _, hasPartial := update.AssistantMessageEvent["partial"]; update.Usage == nil || update.AssistantMessageEvent["type"] == nil || hasPartial {
				t.Fatalf("message_update Raw = %s, want usage and an assistantMessageEvent without partial", event.Raw)
			}
		case "message_end":
			var end struct {
				Message AgentMessage `json:"message"`
			}
			if err := json.Unmarshal(event.Raw, &end); err != nil {
				t.Fatal(err)
			}
			if end.Message.Role == "assistant" {
				assistantEnds++
				if len(end.Message.Raw) == 0 || end.Message.Raw[0] != '{' {
					t.Fatalf("message_end message Raw = %q, want the complete message object", end.Message.Raw)
				}
			}
		}
	}
	for _, event := range events {
		check(event)
	}
	if updates == 0 || assistantEnds != 1 {
		t.Fatalf("%d message_update and %d assistant message_end among %d events, want updates and one assistant message_end", updates, assistantEnds, len(events))
	}
}
