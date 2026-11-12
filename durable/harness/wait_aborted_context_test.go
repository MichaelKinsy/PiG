package harness

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// delayedPublications holds each publication for 20ms once slow is set (SlowPublications in harness-submissions.test.ts).
type delayedPublications struct {
	*controlledStorage
	slow atomic.Bool
}

func (storage *delayedPublications) Publishing(_ context.Context, publish func()) {
	if storage.slow.Load() {
		time.Sleep(20 * time.Millisecond)
	}
	publish()
}

// Pi d9d535b6c9e4 (#11963, #11820): a wait cancelled before the line delivers its result still rejects. Pi marks the waiter
// promise handled so Node reports no unhandledRejection while it waits to be handed out; Go has no unhandled rejection, the
// rejection is the error of the same Wait call. So the contract is that the cancelled wait returns the cancellation cause as
// its own error while an earlier commit is still publishing, that the earlier commit completes, and that a close afterwards
// succeeds.
func TestWaitCancelledBeforeItsResultIsDeliveredReturnsTheCauseOnThatWait(t *testing.T) {
	storage := &delayedPublications{controlledStorage: newControlledStorage()}
	setup := chatSetup(t)
	setup.Faux.SetResponses([]ai.FauxResponseStep{unanswered().step})
	harness, root := openChat(t, storage, setup)
	submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
	storage.slow.Store(true)
	writing := make(chan error, 1)
	go func() {
		_, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "x"}}})
		writing <- err
	}()
	cause := errors.New("stop waiting")
	waitCtx, abort := cancelledContext(cause)
	abort()
	_, err := submission.Wait(waitCtx)
	if !errors.Is(err, cause) {
		t.Fatalf("wait error = %v, want the cancellation cause %v", err, cause)
	}
	if err := <-writing; err != nil {
		t.Fatal(err)
	}
	storage.slow.Store(false)
	closeHarness(t, harness)
}
