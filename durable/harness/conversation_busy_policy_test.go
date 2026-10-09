package harness

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// packages/durable/src/harness/submissions.ts:140-166 admitSubmission: "A busy conversation queues it in `pi.inbox`, or rejects `whenBusy: "reject"`
// input with `ConversationBusy`". Only input with whenBusy "reject" is rejected: input with another policy (or none) is admitted while a run is
// active, and a write is admitted whatever its draft says. The ConversationBusy that is returned names the conversation.
// mutation-checked: rejecting on busy input whatever its whenBusy, rejecting a write, or rejecting while idle fails it.
func TestConversationBusyRejectsOnlyInputWithWhenBusyReject(t *testing.T) {
	storage := newControlledStorage()
	setup := chatSetup(t)
	busy := unanswered()
	setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
	harness, root := openChat(t, storage, setup)
	defer closeHarness(t, harness)
	// An idle conversation admits input whatever its policy: reject only applies while a run is active.
	first, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi"), WhenBusy: durable.WhenBusyReject})
	if err != nil {
		t.Fatalf("idle input with reject = %v, want it placed", err)
	}
	<-busy.reached
	if record := must(first.Status(testContext)); record.Status != durable.SubmissionPlaced {
		t.Fatalf("idle input with reject is %s, want placed", record.Status)
	}

	for name, policy := range map[string]durable.WhenBusy{"no policy": "", "steer": durable.WhenBusySteer, "followUp": durable.WhenBusyFollowUp} {
		submission, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("later " + name), WhenBusy: policy})
		if err != nil {
			t.Fatalf("busy input with %s = %v, want it queued", name, err)
		}
		record := must(submission.Status(testContext))
		if record.Status != durable.SubmissionQueued {
			t.Fatalf("busy input with %s is %s, want queued", name, record.Status)
		}
	}

	_, err = root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("no"), WhenBusy: durable.WhenBusyReject})
	var rejected *durable.ConversationBusy
	if !errors.As(err, &rejected) || rejected.ConversationId != root.Id() {
		t.Fatalf("busy input with reject = %v, want ConversationBusy for %d", err, root.Id())
	}

	write := durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, WhenBusy: durable.WhenBusyReject, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "w"}}}
	if _, err := root.Submit(testContext, write); errors.As(err, &rejected) {
		t.Fatalf("a write was rejected as busy: %v", err)
	}
}
