package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
)

// Pi orders what a publication resolves, not a wait that was rejected: a wait of a WithContinuation caller whose context was
// cancelled has no continuation a later publication waits for.
func TestACancelledWaitOfAContinuationDoesNotHoldLaterPublications(t *testing.T) {
	gate := deferred()
	gated := tkGated("test.cancelled-continuation", gate)
	opened := tkOpenRoot(t, []durable.AnyTask{gated.Erased()})
	id := tkStart(t, opened.root, gated.Erased())
	opened.harness.Resume()
	tasks := opened.harness.(*harnessImpl).tasks
	cancelling, cancel := context.WithCancelCause(testContext)
	continued, _ := session.WithContinuation(cancelling)
	waiting := asyncErr(func() error {
		_, err := opened.harness.WaitForTask(continued, id)
		return err
	})
	eventually(t, func() bool { return len(tasks.taskWaiters.Keys()) > 0 })
	// The caller is parked in its wait.
	awaitQuiescent(t)
	cause := errors.New("stop waiting")
	cancel(cause)
	if _, err := waiting.wait(); !errors.Is(err, cause) {
		t.Fatalf("the wait returned %v, want the cancellation cause", err)
	}
	writing := asyncErr(func() error {
		_, err := opened.root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}})
		return err
	})
	select {
	case <-writing.done:
	case <-time.After(10 * time.Second):
		t.Fatal("a publication waited for the continuation of a cancelled wait")
	}
	if _, err := writing.wait(); err != nil {
		t.Fatal(err)
	}
	gate.resolve()
	tkWaitOutcome(t, opened.harness, id)
	mustClose(t, opened.harness)
}
