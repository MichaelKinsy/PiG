package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/harness/types.ts:52 `UserInput = UserMessage["content"]` (a string, or text and image blocks) and its use as the `content` of an input
// SubmissionDraft (types.ts:60): an idle input places a user entry whose model message carries that content unchanged, whichever form it has.
// mutation-checked: placing a block array as its text only (or a string as a block array) fails the matching case.
func TestUserInputPlacesBothContentForms(t *testing.T) {
	cases := []struct {
		name    string
		content durable.UserInput
		want    string
	}{
		{"a string", ai.UserText("hello"), `[{"role":"user","content":"hello","timestamp":42}]`},
		{"text blocks", ai.UserContentBlocks{ai.TextContent{Text: "a"}, ai.TextContent{Text: "b"}}, `[{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}],"timestamp":42}]`},
		{"an image block", ai.UserContentBlocks{ai.ImageContent{Data: "AAAA", MimeType: "image/png"}}, `[{"role":"user","content":[{"type":"image","data":"AAAA","mimeType":"image/png"}],"timestamp":42}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup := chatSetup(t)
			setup.SetNow(func() float64 { return 42 })
			busy := unanswered()
			setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
			harness, root := openChat(t, newControlledStorage(), setup)
			defer closeHarness(t, harness)
			submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: tc.content}))
			<-busy.reached
			record := must(submission.Status(testContext))
			if record.Status != durable.SubmissionPlaced || record.Entry == nil {
				t.Fatalf("input status %q entry %v, want placed with an entry", record.Status, record.Entry)
			}
			entry := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
				return durable.TxEntry(tx, durable.UserEntry, *record.Entry)
			})
			expectEqualJSON(t, entry.Model, tc.want)
		})
	}
}
