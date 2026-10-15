package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Pi writes a system message's members in the order of its constructor object: role, content, sections, timestamp,
// toolsAdded, toolsRemoved (system-prompt.ts buildSystemPromptState, agent-session.ts system message literals). A JSON
// stream or session file that reorders them differs byte for byte from Pi's.
func TestSystemMessageMarshalsMembersInPiOrder(t *testing.T) {
	text := "preamble text"
	for name, tc := range map[string]struct {
		message SystemMessage
		want    string
	}{
		"sections then timestamp then tools": {
			SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "preamble", Value: &text}}, Timestamp: 5, ToolsAdded: []ToolSchema{{Name: "read", Description: "d"}}},
			`{"role":"system","content":"","sections":{"preamble":"preamble text"},"timestamp":5,"toolsAdded":[{"name":"read","description":"d","parameters":null}]}`,
		},
		"empty sections stay present": {
			SystemMessage{Content: SystemText("x"), Sections: OrderedSections{}, Timestamp: 1},
			`{"role":"system","content":"x","sections":{},"timestamp":1}`,
		},
		"absent sections stay absent": {
			SystemMessage{Content: SystemText("x"), Timestamp: 1, ToolsRemoved: []ToolReference{{Name: "bash"}}},
			`{"role":"system","content":"x","timestamp":1,"toolsRemoved":[{"name":"bash"}]}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := json.Marshal(tc.message)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// Every member of SystemMessage reaches the wire: the fixture sets each field to a non-zero value (a new field left
// at its zero value fails here before it can be dropped by MarshalJSON), and the output names each field's json member.
func TestSystemMessageMarshalCoversEveryField(t *testing.T) {
	text := "t"
	message := SystemMessage{
		Content: SystemText("c"), Sections: OrderedSections{{Name: "s", Value: &text}}, Timestamp: 7,
		ToolsAdded: []ToolSchema{{Name: "a"}}, ToolsRemoved: []ToolReference{{Name: "r"}},
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	for field := range reflect.TypeFor[SystemMessage]().Fields() {
		if reflect.ValueOf(message).FieldByName(field.Name).IsZero() {
			t.Errorf("fixture leaves %s at its zero value; set it so the test can see it marshaled", field.Name)
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if _, ok := members[name]; !ok {
			t.Errorf("SystemMessage.%s is not marshaled as %q: %s", field.Name, name, raw)
		}
	}
}
