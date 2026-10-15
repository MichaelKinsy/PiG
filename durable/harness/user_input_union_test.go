package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/harness UserInput = string | (TextContent | ImageContent)[]: a submission takes either member of the union, settles done, and the user entry it
// places holds the content as it was sent, as text or as blocks with their images.
// mutation-checked: making a submission place only the first block of UserContentBlocks, or turn UserText into blocks, fails it.
func TestUserInputUnionEveryKindIsSubmittedAndRecorded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input durable.UserInput
	}{
		{"plain text", ai.UserText("hello")},
		{"text blocks", ai.UserContentBlocks{ai.TextContent{Text: "one"}, ai.TextContent{Text: "two"}}},
		{"text and image blocks", ai.UserContentBlocks{ai.TextContent{Text: "look"}, ai.ImageContent{Data: "aGk=", MimeType: "image/png"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := chatSetup(t)
			setup.Faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}})})
			harness, root := openChat(t, newControlledStorage(), setup)
			submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: tc.input}))
			settled := must(submission.Wait(testContext))
			if settled.Status != durable.SubmissionDone || settled.Entry == nil || settled.Answer == nil {
				t.Fatalf("settled = %+v, want done with an entry and an answer", settled)
			}
			entry := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
				return durable.TxEntry(tx, durable.UserEntry, *settled.Entry)
			})
			var user ai.UserMessage
			for _, message := range entry.Model {
				if typed, ok := message.(ai.UserMessage); ok {
					user = typed
				}
			}
			if user.Content == nil || reflect.TypeOf(user.Content) != reflect.TypeOf(tc.input) || !reflect.DeepEqual(user.Content, tc.input) {
				t.Fatalf("the user entry holds %#v, want the submitted %#v", user.Content, tc.input)
			}
			closeHarness(t, harness)
		})
	}
}
