package chat

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// FuzzRestoredInput feeds arbitrary entry JSON of a queued input to restoredInput: it never panics, and what it accepts
// is a non-blank text with an author.
func FuzzRestoredInput(f *testing.F) {
	f.Add([]byte(`{"kind":"p2.input","data":{"author":{"kind":"owner","id":"owner","name":"Owner"},"text":"hi","attachments":[]}}`))
	f.Add([]byte(`{"kind":"x","data":{"text":" "}}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		value, err := chordjson.Decode(raw)
		if err != nil {
			return
		}
		object, _ := value.(*chordjson.Object)
		for _, mode := range []harness.InboxMode{harness.InboxSteer, harness.InboxFollowUp, harness.InboxMode("write")} {
			if input, ok := restoredInput(harness.InboxItem{Mode: mode, Entry: object}); ok {
				if mode == harness.InboxMode("write") || input.text == "" {
					t.Fatalf("accepted %q for mode %s", input.text, mode)
				}
			}
		}
	})
}
