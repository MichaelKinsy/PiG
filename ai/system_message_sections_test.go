package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi's durable store writes a system message with an empty sections object as "sections":{}; an absent member is a
// different record.
func TestSystemMessageWritesAnEmptySectionsObjectButNotAnAbsentOne(t *testing.T) {
	empty, err := json.Marshal(SystemMessage{Content: SystemText(""), Sections: OrderedSections{}})
	if err != nil || !strings.Contains(string(empty), `"sections":{}`) {
		t.Fatalf("empty sections: %s, %v", empty, err)
	}
	absent, err := json.Marshal(SystemMessage{Content: SystemText("x")})
	if err != nil || strings.Contains(string(absent), "sections") {
		t.Fatalf("absent sections: %s, %v", absent, err)
	}
}

// getCurrentSystemMessage spreads sections only when one remains (transcript.ts); a replay with no sections, or whose
// sections were all removed, encodes without the member.
func TestCurrentSystemMessageOmitsSectionsWhenNoneRemain(t *testing.T) {
	for name, messages := range map[string][]Message{
		"no sections": {SystemMessage{Content: SystemText("x")}},
		"all removed": {
			SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "a", Value: new("1")}}},
			SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "a"}}},
		},
	} {
		encoded, err := json.Marshal(GetCurrentSystemMessage(messages))
		if err != nil || strings.Contains(string(encoded), "sections") {
			t.Errorf("%s: %s, %v", name, encoded, err)
		}
	}
	kept, err := json.Marshal(GetCurrentSystemMessage([]Message{SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "a", Value: new("1")}}}}))
	if err != nil || !strings.Contains(string(kept), `"sections":{"a":"1"}`) {
		t.Errorf("a remaining section: %s, %v", kept, err)
	}
}
