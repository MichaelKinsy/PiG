package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// sendMessage's message is Pick<CustomMessage, "customType" | "content" | "display" | "details"> with display a required boolean
// (types.ts:2075-2078; CustomMessage.display: boolean). The Session stores the message's display as given, so a message that does not set it is not
// displayed, as an entry with display false is not rendered.
func TestSendMessageStoresTheMessagesDisplayFlag(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message extension.CustomMessageRef
		want    bool
	}{
		{"display true", extension.CustomMessageRef{CustomType: "note", Content: "c", Display: true}, true},
		{"display false", extension.CustomMessageRef{CustomType: "note", Content: "c", Display: false}, false},
		{"display not set", extension.CustomMessageRef{CustomType: "note", Content: "c"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
			var got []agent.AgentMessage
			h.session.Subscribe(func(event agent.AgentEvent) {
				if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Custom != nil {
					got = append(got, end.Message)
				}
			})
			if err := h.session.SendCustomMessage(t.Context(), tc.message, nil); err != nil {
				t.Fatal(err)
			}
			if err := h.session.FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("custom messages delivered = %d, want 1", len(got))
			}
			if display, _ := got[0].Custom["display"].(bool); display != tc.want {
				t.Fatalf("display = %v, want %v (message %v)", got[0].Custom["display"], tc.want, got[0].Custom)
			}
		})
	}
}
