package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// A system message is held as JSON.parse returned it, like every other role, and one without content projects { ...message, content: "" } (session-manager.ts sessionEntryToContextMessages). Expectations: node JSON.stringify of the parsed object and of the spread.
func TestSystemMessageKeepsTheObjectItWasDecodedFrom(t *testing.T) {
	for _, tc := range []struct {
		text, written, empty string
		null                 bool
	}{
		{text: `{"timestamp":4,"content":"sys","role":"system","zz":1}`, written: `{"timestamp":4,"content":"sys","role":"system","zz":1}`},
		{text: `{"role":"system","timestamp":5}`, written: `{"role":"system","timestamp":5}`, empty: `{"role":"system","timestamp":5,"content":""}`, null: true},
		{text: `{"role":"system","content":null,"timestamp":6}`, written: `{"role":"system","content":null,"timestamp":6}`, empty: `{"role":"system","content":"","timestamp":6}`, null: true},
	} {
		var m AgentMessage
		if err := json.Unmarshal([]byte(tc.text), &m); err != nil {
			t.Fatalf("%s: %v", tc.text, err)
		}
		if got := canonicalMessage(t, m); got != tc.written {
			t.Errorf("written\n got %s\nwant %s", got, tc.written)
		}
		if got := canonicalMessage(t, m.Clone()); got != tc.written {
			t.Errorf("clone written\n got %s\nwant %s", got, tc.written)
		}
		if m.NullContent() != tc.null {
			t.Errorf("%s: NullContent()=%v", tc.text, !tc.null)
		}
		if tc.null {
			if got := canonicalMessage(t, m.WithEmptyContent()); got != tc.empty {
				t.Errorf("with empty content\n got %s\nwant %s", got, tc.empty)
			}
		}
	}
}

func canonicalMessage(t *testing.T, m AgentMessage) string {
	t.Helper()
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jsonstringify.Canonicalize(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}
