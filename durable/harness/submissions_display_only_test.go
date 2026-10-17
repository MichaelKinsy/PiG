// SPDX-License-Identifier: MIT

package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// TestDisplayOnlyWritesAreAppendedAtOnce mirrors submissions.ts admitSubmission and inbox.ts isDisplayOnly/hasQueuedReset: a
// write of only kind and data changes nothing the model sees, so it is appended and settled done at once, even while a run
// is busy; a write with a model, head, or edits (present counts, even if empty) queues; and behind a queued reset a
// display-only write queues too, so it lands after the reset.
// mutation-checked: dropping the display-only branch of AdmitSubmission fails the at-once case; treating a present Model as
// absent fails the present-counts case; ignoring hasQueuedReset fails the reset case.
func TestDisplayOnlyWritesAreAppendedAtOnce(t *testing.T) {
	busyRoot := func(t *testing.T, replies ...ai.FauxResponseStep) (Harness, Conversation, gatedAnswer) {
		t.Helper()
		setup := chatSetup(t)
		first := gatedReply("first")
		setup.Faux.SetResponses(append([]ai.FauxResponseStep{first.step}, replies...))
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		submit(t, root, inputDraft("a"))
		first.awaitReached(t)
		return harness, root, first
	}

	t.Run("a data-only write settles done at once while busy and leaves the inbox empty", func(t *testing.T) {
		harness, root, first := busyRoot(t)
		write := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Data: "w"}))
		record := status(t, write)
		if record.Status != durable.SubmissionDone || record.Entry == nil {
			t.Fatalf("record = %+v, want done with its entry", record)
		}
		expectEqualJSON(t, inboxOf(t, harness, root), jsonText(t, [][]any{}))
		expectTranscript(t, root, "pi.user:a", "note")
		first.release()
		closeHarness(t, harness)
	})

	t.Run("a write with a model, edits, or an empty model queues while busy", func(t *testing.T) {
		harness, root, first := busyRoot(t)
		withModel := submit(t, root, writeDraft(queuedNote("m")))
		emptyModel := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Model: []ai.Message{}}))
		emptyEdits := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Edits: []durable.ContextEdit{}}))
		for _, submission := range []durable.Submission{withModel, emptyModel, emptyEdits} {
			if record := status(t, submission); record.Status != durable.SubmissionQueued {
				t.Fatalf("status = %s, want queued", record.Status)
			}
		}
		first.release()
		closeHarness(t, harness)
	})

	t.Run("a data-only write queues behind a queued reset", func(t *testing.T) {
		harness, root, first := busyRoot(t)
		if err := root.Reset(testContext, nil); err != nil {
			t.Fatal(err)
		}
		write := submit(t, root, writeDraft(durable.EntryDraft{Kind: "note", Data: "after"}))
		if record := status(t, write); record.Status != durable.SubmissionQueued {
			t.Fatalf("status = %s, want queued behind the reset", record.Status)
		}
		first.release()
		must(write.Wait(testContext))
		closeHarness(t, harness)
	})
}
