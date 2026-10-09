package harness

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/harness/types.ts:74-76 SettledSubmissionRecord = SubmissionRecord & { status: "done" | "unanswered" }, returned by
// Submission.wait (types.ts:82; submissions.ts:75-86 and 137-139): an already settled submission resolves at once, a pending one resolves only when a
// settling publication arrives, and neither path ever resolves with a queued or placed record. Here a queued input behind a busy run is waited on
// while pending, aborted (submissions.ts abort settles it unanswered/"aborted"), and its wait resolves unanswered with the abort reason; a second wait on
// the settled submission resolves at once with the same record; an idle write settles done.
// mutation-checked: resolving the waiter with the record read before settlement (still queued), or Wait returning a placed record, fails this test.
func TestSettledSubmissionRecordIsOnlyDoneOrUnanswered(t *testing.T) {
	setup := chatSetup(t)
	busy := unanswered()
	setup.Faux.SetResponses([]ai.FauxResponseStep{busy.step})
	harness, root := openChat(t, newControlledStorage(), setup)
	defer closeHarness(t, harness)

	written := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "x"}}}))
	if settled := must(written.Wait(testContext)); settled.Status != durable.SubmissionDone {
		t.Fatalf("an idle write waited as %q, want done", settled.Status)
	}

	running := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("first")}))
	<-busy.reached
	if record := must(running.Status(testContext)); record.Status != durable.SubmissionPlaced {
		t.Fatalf("the running input is %q, want placed", record.Status)
	}
	queued := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("second")}))
	if record := must(queued.Status(testContext)); record.Status != durable.SubmissionQueued {
		t.Fatalf("the input behind a busy run is %q, want queued", record.Status)
	}

	type waited struct {
		record durable.SettledSubmissionRecord
		err    error
	}
	pending := make(chan waited, 1)
	go func() {
		record, err := queued.Wait(testContext)
		pending <- waited{record, err}
	}()
	select {
	case got := <-pending:
		t.Fatalf("Wait resolved with %+v before the submission settled", got.record)
	case <-time.After(50 * time.Millisecond):
	}

	if result := must(queued.Abort(testContext)); result != durable.SubmissionAborted {
		t.Fatalf("abort = %q, want aborted", result)
	}
	got := <-pending
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.record.Status != durable.SubmissionUnanswered || got.record.Reason == nil || *got.record.Reason != "aborted" || got.record.Id != queued.Id() {
		t.Fatalf("pending wait settled as %+v, want submission %d unanswered/aborted", got.record, queued.Id())
	}
	again := must(queued.Wait(testContext))
	expectEqualJSON(t, again, jsonText(t, got.record))

}
