package extension

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// extensions/runner.ts emitBoundary hands a handler `{...baseEvent, entries, continue, context}`: turn_end's base event is {type, turnIndex, message, toolResults, messageEntryId, toolResultEntryIds, outcome} (agent-session.ts:867-875) and agent_before_settle's is {type, outcome} (agent-session.ts:1984). A TS extension that stringifies or iterates the event saw those members in that order under real Pi; PiG wrote the boundary members first.
func TestBoundaryEventsKeepPiMemberOrder(t *testing.T) {
	keys := func(t *testing.T, event any) []string {
		t.Helper()
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var ordered []string
		dec := json.NewDecoder(bytes.NewReader(data))
		if _, err := dec.Token(); err != nil {
			t.Fatal(err)
		}
		for dec.More() {
			key, _ := dec.Token()
			ordered = append(ordered, key.(string))
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				t.Fatal(err)
			}
		}
		return ordered
	}
	message := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hi")}}
	state := &BoundaryState{Outcome: "completed", Entries: []SessionBoundaryDraft{}}
	for _, test := range []struct {
		name  string
		event any
		want  string
	}{
		{"turn_end", TurnEndEvent{Type: "turn_end", Message: message, BoundaryState: state}, "type turnIndex message toolResults messageEntryId toolResultEntryIds outcome entries continue context"},
		{"turn_end before the boundary state", TurnEndEvent{Type: "turn_end", Message: message}, "type turnIndex message toolResults messageEntryId toolResultEntryIds"},
		{"agent_before_settle", AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: *state}, "type outcome entries continue context"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ""
			for i, key := range keys(t, test.event) {
				if i > 0 {
					got += " "
				}
				got += key
			}
			if got != test.want {
				t.Fatalf("members = %q, want %q", got, test.want)
			}
		})
	}
}

// sdk.ts:402-408 emits provider_stream_event as { data, type, provider, api, model }.
func TestProviderStreamEventKeepsPiMemberOrder(t *testing.T) {
	data, err := json.Marshal(ProviderStreamEvent{Type: "provider_stream_event", Provider: "p", API: "a", Model: "m", Data: map[string]any{"x": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"data":{"x":1},"type":"provider_stream_event","provider":"p","api":"a","model":"m"}`; string(data) != want {
		t.Fatalf("event = %s, want %s", data, want)
	}
}
