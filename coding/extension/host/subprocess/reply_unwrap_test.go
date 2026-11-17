package subprocess

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The reply of a handler that edited nothing is the wrapper with the result alone, or an empty object when the result is undefined (Node). The host takes the result out of it on every
// tool_result and message_end of every extension, so it must not pay for the strict decode of the whole wrapper.
var noEditReplies = []struct {
	name, reply string
}{
	{"null result", `{"_pigX":null}`},
	{"empty object", `{}`},
	{"object result", `{"_pigX":{"content":[{"type":"text","text":"ok } { \"x\""}],"isError":false}}`},
	{"spaced like Python's json.dumps", `{"_pigX": {"a": [1, 2]}}`},
	{"string result", `{"_pigX":"a,\"b\":2}"}`},
}

func TestNoEditReplyIsUnwrappedWithoutAllocation(t *testing.T) {
	for _, event := range []string{"tool_result", "message_end"} {
		member := map[string]string{"tool_result": "_pigToolResultResult", "message_end": "_pigEventResult"}[event]
		for _, tc := range noEditReplies {
			raw := json.RawMessage(bytes.ReplaceAll([]byte(tc.reply), []byte("_pigX"), []byte(member)))
			// The host holds the event as an any already: boxing it is not the unwrap's cost.
			var toolResult extension.CustomToolResultEvent
			toolResult.Edits = &extension.ToolResultEdits{}
			var toolResultEvent, messageEndEvent any = toolResult, extension.MessageEndEvent{Type: "message_end"}
			unwrap := func() {
				var err error
				if event == "tool_result" {
					_, err = unwrapToolResultReply(toolResultEvent, raw)
				} else {
					_, err = unwrapEventReply(noDispatch(), "message_end", messageEndEvent, raw)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if got := testing.AllocsPerRun(100, unwrap); got > raceAllocSlack {
				t.Errorf("%s reply %s (%s): %v allocations to take the result out of a reply that edits nothing, want 0 (up to %d under the race detector)", event, tc.name, raw, got, raceAllocSlack)
			}
		}
	}
}

// Whatever the shortcut does, the result is the one the strict decode of the wrapper gives, and a reply the strict decode refuses is refused.
func TestReplyUnwrapAgreesWithTheStrictDecode(t *testing.T) {
	replies := []string{
		`null`, `{}`, ` { } `, `{"_pigEventResult":null}`, `{"_pigEventResult": {"message": {"role":"user"}} }`, "\n{\"_pigEventResult\"\t:\r\n1}\n",
		`{"_pigEventResult":1,"_pigEventResult":2}`, `{"_pigEventResult":1,"other":2}`, `{"other":2}`, `{"_pigEventResult":1,"_pigEventEdits":null}`,
		`{"_pigEventEdits":null,"_pigEventResult":1}`, `{"\u005fpigEventResult":1}`, `{"_pigEventResult":}`, `{"_pigEventResult"}`, `{"_pigEventResult":1`,
		`[]`, `"x"`, `{"_pigEventResultX":1}`, `{"_pigMessageEndResul":1}`,
		`{"_pigEventEdits":[],"_pigEventResult":1}`, `{"_pigEventEdits":{},"_pigEventResult":1}`, `{"_pigEventEdits":[{"at":0}]}`, `{"_pigEventEdits":[{"message":{"role":"user"}}]}`, `{"_pigEventEdits":[{"at":0,"message":null}]}`,
		`{"_pigEventEdits":[{"at":0,"message":"x"}]}`, `{"_pigEventEdits":[{"at":"0","message":{}}]}`, `{"_pigEventEdits":[{"at":0,"message":{},"x":1}]}`, `{"_pigEventEdits":[null]}`, `{"_pigEventEdits":[1]}`,
		`{"_pigEventEdits":"x"}`, `{"_pigMessageEndMessage":{},"_pigEventResult":1}`, `{"_pigMessageEndResult":1}`,
		`{"_pigToolResultResult":null}`, `{"_pigToolResultResult":"a"}`, `{"_pigToolResultResult":[1,{"a":"}"}]}`, `{"_pigToolResultEdits":null,"_pigToolResultResult":1}`,
		`{"_pigToolResultEdits":{"members":{"isError":true},"changed":["isError"],"shared":[]},"_pigToolResultResult":null}`, `{"_pigToolResultEdits":{"unknown":1},"_pigToolResultResult":1}`,
		`{"_pigToolResultResult":1,"_pigToolResultEdits":{"members":{},"changed":[],"shared":[]}}`, `{"_pigToolResultResult":1,"x":2}`, `{"_pigToolResultResult":1,}`,
	}
	for _, raw := range replies {
		var messageEnd EventResponsePayload
		wantME := json.Unmarshal([]byte(raw), &messageEnd)
		var toolResult ToolResultResponsePayload
		wantTR := json.Unmarshal([]byte(raw), &toolResult)
		if !json.Valid([]byte(raw)) {
			continue
		}
		if wantME == nil && len(messageEnd.Edits) == 0 {
			got, err := unwrapEventReply(noDispatch(), "message_end", extension.MessageEndEvent{Type: "message_end"}, json.RawMessage(raw))
			if err != nil || !bytes.Equal(got, messageEnd.Result) {
				t.Errorf("message_end reply %q: unwrapped %q, %v; the strict decode gives %q", raw, got, err, messageEnd.Result)
			}
		} else if wantME != nil {
			if _, err := unwrapEventReply(noDispatch(), "message_end", extension.MessageEndEvent{Type: "message_end"}, json.RawMessage(raw)); err == nil {
				t.Errorf("message_end reply %q: unwrapped without an error; the strict decode says %v", raw, wantME)
			}
		}
		if wantTR == nil && toolResult.Edits == nil {
			var event extension.CustomToolResultEvent
			event.Edits = &extension.ToolResultEdits{}
			got, err := unwrapToolResultReply(event, json.RawMessage(raw))
			if err != nil || !bytes.Equal(got, toolResult.Result) || len(event.Edits.Changed) != 0 {
				t.Errorf("tool_result reply %q: unwrapped %q, %v, edits %v; the strict decode gives %q", raw, got, err, event.Edits.Changed, toolResult.Result)
			}
		} else if wantTR != nil {
			if _, err := unwrapToolResultReply(extension.CustomToolResultEvent{}, json.RawMessage(raw)); err == nil {
				t.Errorf("tool_result reply %q: unwrapped without an error; the strict decode says %v", raw, wantTR)
			}
		}
	}
}

func TestToolResultReplyWithEditsReachesTheSink(t *testing.T) {
	var event extension.CustomToolResultEvent
	event.Edits = &extension.ToolResultEdits{}
	raw := `{"_pigToolResultEdits":{"members":{"isError":true},"changed":["isError"],"shared":["details"]},"_pigToolResultResult":{"details":1}}`
	got, err := unwrapToolResultReply(event, json.RawMessage(raw))
	if err != nil || string(got) != `{"details":1}` {
		t.Fatalf("result %q, %v; want {\"details\":1}", got, err)
	}
	if e := event.Edits; len(e.Changed) != 1 || e.Changed[0] != "isError" || len(e.Shared) != 1 || e.Shared[0] != "details" || e.Members.IsError == nil || !*e.Members.IsError {
		t.Fatalf("edits %+v, want isError changed to true and details shared", *e)
	}
}

func TestMessageEndReplyWithAMessageEditsTheEventsMessage(t *testing.T) {
	message := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1}}
	raw := `{"_pigEventEdits":[{"at":0,"message":{"role":"user","content":"changed","timestamp":1}}],"_pigEventResult":null}`
	got, err := unwrapEventReply(noDispatch(), "message_end", extension.MessageEndEvent{Type: "message_end", Message: message}, json.RawMessage(raw))
	if err != nil || string(got) != "null" {
		t.Fatalf("result %q, %v; want null", got, err)
	}
	if text, err := json.Marshal(message.User.Content); err != nil || string(text) != `"changed"` {
		t.Fatalf("the event's message holds %s, %v; want the edited \"changed\"", text, err)
	}
}

func TestResultMemberNamesAreTheWrappersJSONTags(t *testing.T) {
	for _, tc := range []struct {
		wrapper any
		member  string
	}{
		{EventResponsePayload{}, eventResultMember},
		{ToolResultResponsePayload{}, toolResultResultMember},
	} {
		field, ok := reflect.TypeOf(tc.wrapper).FieldByName("Result")
		if !ok || field.Tag.Get("json") != tc.member {
			t.Errorf("%T: the Result member's tag is %q, the shortcut reads %q", tc.wrapper, field.Tag.Get("json"), tc.member)
		}
	}
}

// soleMember is handed frames already decoded as JSON, but it does not rely on that: text that is not a whole object is not unwrapped.
func TestSoleMemberRefusesTextThatIsNotOneObject(t *testing.T) {
	for _, raw := range []string{`{"k":12`, `{"k":1}x`, `{"k":1}}`, `{"k":1,"k":2}`, `{"k"`, `{`, ``, `}`, `{"k":1 "x":2}`} {
		if got, ok := soleMember([]byte(raw), "k"); ok {
			t.Errorf("soleMember(%q) = %q, true; want false", raw, got)
		}
	}
	if got, ok := soleMember([]byte(` { "k" : [1, 2] } `), "k"); !ok || string(got) != "[1, 2]" {
		t.Errorf("soleMember of a spaced object = %q, %v; want [1, 2], true", got, ok)
	}
}

// Edits are validated before any is applied: a reply with one edit that cannot be applied changes nothing, and is refused.
func TestEventReplyRefusesEditsThatDoNotFitTheEventAndAppliesNone(t *testing.T) {
	user := func() agent.AgentMessage {
		return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello"), Timestamp: 1}}
	}
	for name, raw := range map[string]string{
		"position past the list":    `{"_pigEventEdits":[{"at":1,"message":{"role":"user","content":"changed","timestamp":1}}],"_pigEventResult":null}`,
		"negative position":         `{"_pigEventEdits":[{"at":-1,"message":{"role":"user","content":"changed","timestamp":1}}]}`,
		"another role":              `{"_pigEventEdits":[{"at":0,"message":{"role":"toolResult","toolCallId":"c","toolName":"t","content":[],"isError":false,"timestamp":1}}]}`,
		"named twice":               `{"_pigEventEdits":[{"at":0,"message":{"role":"user","content":"a","timestamp":1}},{"at":0,"message":{"role":"user","content":"b","timestamp":1}}]}`,
		"a good edit and a bad one": `{"_pigEventEdits":[{"at":0,"message":{"role":"user","content":"changed","timestamp":1}},{"at":3,"message":{"role":"user","content":"x","timestamp":1}}]}`,
	} {
		message := user()
		if _, err := unwrapEventReply(noDispatch(), "message_end", extension.MessageEndEvent{Type: "message_end", Message: message}, json.RawMessage(raw)); err == nil {
			t.Errorf("%s: the reply %s was not refused", name, raw)
		}
		if text, err := json.Marshal(message.User.Content); err != nil || string(text) != `"hello"` {
			t.Errorf("%s: the message holds %s, %v after a refused reply; want it unchanged", name, text, err)
		}
	}
}
