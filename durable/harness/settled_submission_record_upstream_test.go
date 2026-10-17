package harness

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// upstream: packages/durable/src/harness/submissions.ts:75-86 wait and :137-139 isSettled (`status === "done" || status === "unanswered"`). A submission whose
// input already runs is placed, which is not settled, so its wait stays pending until the run answers and then resolves with the done record; a wait on
// the settled submission resolves at once. (TestSettledSubmissionRecordIsOnlyDoneOrUnanswered covers the queued and aborted path.)
// mutation-checked: counting a placed record as settled (isSettledSubmission being `status != queued`) fails it, as does a wait that is never resolved by the settling publication.
func TestSettledSubmissionRecordWaitsOutAPlacedInputUpstream(t *testing.T) {
	setup := chatSetup(t)
	gate := deferred()
	setup.Faux.SetResponses([]ai.FauxResponseStep{fauxAfter(gate, "answer")})
	harness, root := openChat(t, newControlledStorage(), setup)
	defer closeHarness(t, harness)
	defer gate.resolve()

	running := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("first")}))
	if record := must(running.Status(testContext)); record.Status != durable.SubmissionPlaced {
		t.Fatalf("the idle input is %q, want placed", record.Status)
	}

	type waited struct {
		record durable.SettledSubmissionRecord
		err    error
	}
	pending := make(chan waited, 1)
	go func() {
		record, err := running.Wait(testContext)
		pending <- waited{record, err}
	}()
	select {
	case got := <-pending:
		t.Fatalf("Wait resolved with %+v while the input was still placed", got.record)
	case <-time.After(50 * time.Millisecond):
	}

	gate.resolve()
	select {
	case got := <-pending:
		if got.err != nil || got.record.Status != durable.SubmissionDone || got.record.Id != running.Id() {
			t.Fatalf("wait after the run = %+v, %v, want submission %d done", got.record, got.err, running.Id())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait never resolved after the run answered")
	}
	if again := must(running.Wait(testContext)); again.Status != durable.SubmissionDone {
		t.Errorf("a wait on the settled submission = %q, want done at once", again.Status)
	}
}
