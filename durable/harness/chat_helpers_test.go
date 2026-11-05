package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

func emptyObjectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func submitWhenBusy(t *testing.T, conversation Conversation, content string, whenBusy ...durable.WhenBusy) durable.Submission {
	t.Helper()
	draft := durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content)}
	if len(whenBusy) > 0 {
		draft.WhenBusy = whenBusy[0]
	}
	return must(conversation.Submit(testContext, draft))
}

// changeLive edits pi.live through its draft, as upstream's tx.doc(LiveDoc) mutations.
func changeLive(t *testing.T, conversation Conversation, edit func(live *delta.Object) error) {
	t.Helper()
	commitValue(t, conversation, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc(tx, LiveDoc, conversation.Id())
		if err != nil {
			return nil, err
		}
		return nil, edit(draft)
	})
}
