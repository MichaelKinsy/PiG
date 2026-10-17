package harness

import (
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// admitSubmission (packages/durable/src/harness/submissions.ts:149-166) looks a known request ID up before it checks whether the conversation is busy:
// a repeat of an admitted submission returns that submission even when it asks `whenBusy: "reject"` on a busy conversation, and only a new request is
// rejected with `ConversationBusy` (errors.ts:20-27: name "ConversationBusy", message "Conversation <id> is busy", conversationId). A request that was
// rejected has written nothing, so its request ID is still free: the same ID then queues as a new submission.
// mutation-checked: checking busy before the request-ID lookup, rejecting only requests without an ID, or changing the error's message or name fails it
func TestConversationBusyIsCheckedAfterTheRequestIDLookup(t *testing.T) {
	storage := newControlledStorage()
	setup := chatSetup(t)
	busy := unanswered()
	setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
	harness, root := openChat(t, storage, setup)
	defer closeHarness(t, harness)
	first := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), RequestId: new("known")}))
	<-busy.reached

	repeat, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), RequestId: new("known"), WhenBusy: durable.WhenBusyReject})
	if err != nil || repeat.Id() != first.Id() {
		t.Fatalf("a known request ID with reject while busy = %v, %v; want the existing submission %d", repeat, err, first.Id())
	}

	commits := storage.commitCount()
	_, err = root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("new"), RequestId: new("fresh"), WhenBusy: durable.WhenBusyReject})
	var rejected *durable.ConversationBusy
	if !errors.As(err, &rejected) || rejected.Name() != "ConversationBusy" || rejected.ConversationId != root.Id() {
		t.Fatalf("a new request with reject while busy = %v, want ConversationBusy for %d", err, root.Id())
	}
	if want := fmt.Sprintf("Conversation %d is busy", root.Id()); err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
	if storage.commitCount() != commits {
		t.Fatalf("the rejected request committed: %d commits, want %d", storage.commitCount(), commits)
	}

	queued, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("new"), RequestId: new("fresh")})
	if err != nil {
		t.Fatal(err)
	}
	record := must(queued.Status(testContext))
	if record.Status != durable.SubmissionQueued || record.RequestId == nil || *record.RequestId != "fresh" || queued.Id() == first.Id() {
		t.Fatalf("the rejected request's ID queued as %+v, want a new queued submission", record)
	}
}
