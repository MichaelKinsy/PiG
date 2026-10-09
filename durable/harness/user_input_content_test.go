package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func userEntryContents(t *testing.T, conversation Conversation) []durable.UserInput {
	t.Helper()
	var contents []durable.UserInput
	for _, entry := range allEntries(t, conversation) {
		if entry.Kind != "pi.user" {
			continue
		}
		message, ok := entry.Model[0].(ai.UserMessage)
		if !ok {
			t.Fatalf("pi.user entry carries %T, want ai.UserMessage", entry.Model[0])
		}
		contents = append(contents, message.Content)
	}
	return contents
}

// upstream: packages/durable/src/harness/types.ts:52 UserInput = UserMessage["content"]. A submission's content, whether a string or content blocks, becomes the user message content unchanged, both when an idle conversation places it at once and when a busy one queues it first (pi.inbox) and places it at the final boundary.
func TestUserInputVariantsBecomeTheUserMessageContent(t *testing.T) {
	setup := chatSetup(t)
	first := gatedReply("first")
	setup.Faux.SetResponses([]ai.FauxResponseStep{first.step, fauxAnswer("second"), fauxAnswer("third")})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup)
	var text durable.UserInput = ai.UserText("plain text")
	blocks := durable.UserInput(ai.UserContentBlocks{ai.TextContent{Text: "look"}, ai.ImageContent{Data: "AAA", MimeType: "image/png"}})
	idle := submit(t, root, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: text})
	first.awaitReached(t)
	queued := submit(t, root, durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: blocks})
	first.release()
	must(queued.Wait(testContext))
	must(idle.Wait(testContext))
	got := userEntryContents(t, root)
	want := []durable.UserInput{text, blocks}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("user entry contents = %#v, want %#v", got, want)
	}
	closeHarness(t, harness)
}
