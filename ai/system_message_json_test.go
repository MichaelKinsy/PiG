package ai

import (
	"encoding/json"
	"reflect"
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

// MarshalJSON lists the members of SystemMessage by hand: a new field must be added there and to the cases above.
func TestSystemMessageMarshalCoversEveryField(t *testing.T) {
	if got := reflect.TypeFor[SystemMessage]().NumField(); got != 5 {
		t.Fatalf("SystemMessage has %d fields; update SystemMessage.MarshalJSON and TestSystemMessageMarshalsMembersInPiOrder", got)
	}
}
