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
		member := map[string]string{"tool_result": "_pigToolResultResult", "message_end": "_pigMessageEndResult"}[event]
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
					_, err = unwrapMessageEndReply(messageEndEvent, raw)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if got := testing.AllocsPerRun(100, unwrap); got != 0 {
				t.Errorf("%s reply %s (%s): %v allocations to take the result out of a reply that edits nothing, want 0", event, tc.name, raw, got)
			}
		}
	}
}

// Whatever the shortcut does, the result is the one the strict decode of the wrapper gives, and a reply the strict decode refuses is refused.
func TestReplyUnwrapAgreesWithTheStrictDecode(t *testing.T) {
	replies := []string{
		`null`, `{}`, ` { } `, `{"_pigMessageEndResult":null}`, `{"_pigMessageEndResult": {"message": {"role":"user"}} }`, "\n{\"_pigMessageEndResult\"\t:\r\n1}\n",
		`{"_pigMessageEndResult":1,"_pigMessageEndResult":2}`, `{"_pigMessageEndResult":1,"other":2}`, `{"other":2}`, `{"_pigMessageEndResult":1,"_pigMessageEndMessage":null}`,
		`{"_pigMessageEndMessage":null,"_pigMessageEndResult":1}`, `{"\u005fpigMessageEndResult":1}`, `{"_pigMessageEndResult":}`, `{"_pigMessageEndResult"}`, `{"_pigMessageEndResult":1`,
		`[]`, `"x"`, `{"_pigMessageEndResultX":1}`, `{"_pigMessageEndResul":1}`,
		`{"_pigToolResultResult":null}`, `{"_pigToolResultResult":"a"}`, `{"_pigToolResultResult":[1,{"a":"}"}]}`, `{"_pigToolResultEdits":null,"_pigToolResultResult":1}`,
		`{"_pigToolResultEdits":{"members":{"isError":true},"changed":["isError"],"shared":[]},"_pigToolResultResult":null}`, `{"_pigToolResultEdits":{"unknown":1},"_pigToolResultResult":1}`,
		`{"_pigToolResultResult":1,"_pigToolResultEdits":{"members":{},"changed":[],"shared":[]}}`, `{"_pigToolResultResult":1,"x":2}`, `{"_pigToolResultResult":1,}`,
	}
	for _, raw := range replies {
		var messageEnd MessageEndResponsePayload
		wantME := json.Unmarshal([]byte(raw), &messageEnd)
		var toolResult ToolResultResponsePayload
		wantTR := json.Unmarshal([]byte(raw), &toolResult)
		if !json.Valid([]byte(raw)) {
			continue
		}
		if wantME == nil && len(messageEnd.Message) == 0 {
			got, err := unwrapMessageEndReply(extension.MessageEndEvent{Type: "message_end"}, json.RawMessage(raw))
			if err != nil || !bytes.Equal(got, messageEnd.Result) {
				t.Errorf("message_end reply %q: unwrapped %q, %v; the strict decode gives %q", raw, got, err, messageEnd.Result)
			}
		} else if wantME != nil {
			if _, err := unwrapMessageEndReply(extension.MessageEndEvent{Type: "message_end"}, json.RawMessage(raw)); err == nil {
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
	raw := `{"_pigMessageEndMessage":{"role":"user","content":"changed","timestamp":1},"_pigMessageEndResult":null}`
	got, err := unwrapMessageEndReply(extension.MessageEndEvent{Type: "message_end", Message: message}, json.RawMessage(raw))
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
		{MessageEndResponsePayload{}, messageEndResultMember},
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
