package extension

import (
	"encoding/json"
	"testing"
)

// Upstream MessageRenderOptions requires outputPad even when it is zero;
// EntryRenderOptions deliberately has no corresponding field.
func TestMessageRenderOptionsOutputPadWire(t *testing.T) {
	for _, wire := range []string{`{"expanded":false,"outputPad":1}`, `{"expanded":true,"outputPad":0}`} {
		var options MessageRenderOptions
		if err := json.Unmarshal([]byte(wire), &options); err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(options)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != wire {
			t.Errorf("options = %s, want %s", got, wire)
		}
	}
	got, err := json.Marshal(EntryRenderOptions{Expanded: true})
	if err != nil || string(got) != `{"expanded":true}` {
		t.Fatalf("entry options = %s, %v", got, err)
	}
}
