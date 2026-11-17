package subprocess

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// noDispatch is the context of a handler whose event belongs to no dispatch (a unit test that calls the reply path directly).
func noDispatch() context.Context { return context.Background() }

// aDispatch is the context of one dispatch: the Context the runner attaches for every handler of an emit.
func aDispatch() context.Context {
	return extension.WithContext(context.Background(), extension.NewContext("", nil, func() error { return nil }, extension.ContextActions{}))
}

// A reply's entries of the second form are recorded on the dispatch the handler ran in, in the order they arrive, and a later reply's entry of the same member replaces the earlier.
func TestMemberEntriesAreRecordedOnTheDispatch(t *testing.T) {
	ctx := aDispatch()
	event := extension.TurnStartEvent{Type: "turn_start"}
	if got := extension.EventMembers(ctx); got != nil {
		t.Fatalf("a dispatch with no edit holds members: %v", got)
	}
	if _, err := unwrapEventReply(ctx, "turn_start", event, json.RawMessage(`{"_pigEventEdits":[{"member":"note","value":{"a":1}},{"member":"turnIndex","deleted":true}],"_pigEventResult":null}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := unwrapEventReply(ctx, "turn_start", event, json.RawMessage(`{"_pigEventEdits":[{"member":"note","value":"again"}]}`)); err != nil {
		t.Fatal(err)
	}
	got := extension.EventMembers(ctx)
	if len(got) != 2 || got[0].Name != "note" || string(got[0].Value) != `"again"` || got[1].Name != "turnIndex" || !got[1].Deleted {
		t.Fatalf("members %+v", got)
	}
}

// An entry that does not fit refuses the whole reply: none of its members is recorded and none of its messages is applied.
func TestMemberEntriesRefuseAMisfitAndApplyNone(t *testing.T) {
	good := `{"member":"note","value":1}`
	for name, edits := range map[string]string{
		"no name":           `[` + good + `,{"member":"","value":1}]`,
		"neither":           `[` + good + `,{"member":"x"}]`,
		"both":              `[` + good + `,{"member":"x","value":1,"deleted":true}]`,
		"named twice":       `[` + good + `,{"member":"note","deleted":true}]`,
		"not json":          `[` + good + `,{"member":"x","value":}]`,
		"a position too":    `[` + good + `,{"member":"x","value":1,"at":0}]`,
		"an unknown member": `[` + good + `,{"member":"x","value":1,"other":2}]`,
		"a message too":     `[` + good + `,{"member":"x","value":1,"message":{"role":"user"}}]`,
		"a message not fit": `[` + good + `,{"at":5,"message":{"role":"user","content":"x","timestamp":1}}]`,
	} {
		ctx := aDispatch()
		event := extension.AgentEndEvent{Type: "agent_end", Messages: []agent.AgentMessage{agentUserMessage("first")}}
		if _, err := unwrapEventReply(ctx, "agent_end", event, json.RawMessage(`{"_pigEventEdits":`+edits+`}`)); err == nil {
			t.Errorf("%s: the reply was not refused", name)
		}
		if got := extension.EventMembers(ctx); got != nil {
			t.Errorf("%s: members %+v recorded from a refused reply", name, got)
		}
	}
}

// Only the events whose members Pi's handlers share carry a member entry, and only a dispatch can hold it.
func TestMemberEntriesNeedAnEventAndADispatchThatTakeThem(t *testing.T) {
	entry := json.RawMessage(`{"_pigEventEdits":[{"member":"note","value":1}]}`)
	if _, err := unwrapEventReply(aDispatch(), "message_end", extension.MessageEndEvent{Type: "message_end"}, entry); err == nil || !strings.Contains(err.Error(), "takes no edit of a member") {
		t.Errorf("message_end: %v", err)
	}
	if _, err := unwrapEventReply(noDispatch(), "agent_start", extension.AgentStartEvent{Type: "agent_start"}, entry); err == nil || !strings.Contains(err.Error(), "no dispatch") {
		t.Errorf("no dispatch: %v", err)
	}
}

// The members are written over the payload: a member the payload has keeps its place, a deleted one is gone, a new one goes last in the order it was added, and the payload is not modified.
func TestApplyEventMembersKeepsTheObjectOrder(t *testing.T) {
	payload := json.RawMessage(`{"type":"turn_start","turnIndex":2,"timestamp":5}`)
	got, err := applyEventMembers(payload, []extension.EventMember{
		{Name: "note", Value: json.RawMessage(`{"k":[1,2]}`)},
		{Name: "turnIndex", Deleted: true},
		{Name: "type", Value: json.RawMessage(`"other"`)},
		{Name: "gone", Deleted: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"other","timestamp":5,"note":{"k":[1,2]}}`; string(got) != want {
		t.Errorf("payload %s, want %s", got, want)
	}
	if string(payload) != `{"type":"turn_start","turnIndex":2,"timestamp":5}` {
		t.Errorf("the payload changed: %s", payload)
	}
}
