package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// Pi packages/durable/src/harness/inbox.ts:98 places a queued input as `{ role: "user", content: item.content as UserInput }` and submissions.ts:185
// keeps the submitted content in the inbox item, so a steer or follow-up queued behind a running turn becomes the user message with the same content
// form: a block array stays a block array, also when it holds only text blocks (a decoder that joined the text would still pass a test whose blocks
// include an image), and a string stays a string.
// mutation-checked: decoding a queued item's text-only block array into one string (durable.DecodeUserContent) fails the steer and follow-up cases.
func TestQueuedInputKeepsItsContentForm(t *testing.T) {
	for _, tc := range []struct {
		name     string
		whenBusy durable.WhenBusy
		content  durable.UserInput
	}{
		{"steer with text blocks", durable.WhenBusySteer, ai.UserContentBlocks{ai.TextContent{Text: "one"}, ai.TextContent{Text: "two"}}},
		{"follow-up with a single text block", durable.WhenBusyFollowUp, ai.UserContentBlocks{ai.TextContent{Text: "only"}}},
		{"follow-up with a string", durable.WhenBusyFollowUp, ai.UserText("plain")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := chatSetup(t)
			first := gatedReply("first")
			setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second"), fauxAnswer("third")})
			harness, root := openChat(t, storage.NewMemoryStorage(), setup)
			defer closeHarness(t, harness)
			idle := submit(t, root, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("start")})
			first.awaitReached(t)
			queued := submit(t, root, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: tc.content, WhenBusy: tc.whenBusy})
			if record := must(queued.Status(testContext)); record.Status != durable.SubmissionQueued {
				t.Fatalf("input behind a running turn is %s, want queued", record.Status)
			}
			first.release()
			must(queued.Wait(testContext))
			must(idle.Wait(testContext))
			want := []durable.UserInput{ai.UserText("start"), tc.content}
			if got := userEntryContents(t, root); !reflect.DeepEqual(got, want) {
				t.Fatalf("user entry contents = %#v, want %#v", got, want)
			}
		})
	}
}
