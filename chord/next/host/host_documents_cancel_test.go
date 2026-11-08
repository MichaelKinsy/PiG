package host

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// pi: packages/chord/src/next/documents/documents.ts
// Mirrors documents.test.ts "transactions their context's end cancelled (decision "Mistakes that wait are reported at
// once" (e))" on a host: the `transaction_cancelled` report is the host's.

// Mirrors "... › are reported besides rejecting: a change started in a root and not awaited, its fn settled first".
func TestDocumentsTransactionsTheirContextsEndCancelledAreReportedBesidesRejectingAChangeStartedInARootAndNotAwaitedItsFnSettledFirst(t *testing.T) {
	var list *singletonDoc
	run := restartable(t, func(_ int, self Self) { list = stateOf(self, attemptListDoc) }, loadedOptions{})
	settled := make(chan struct{})
	rejected := make(chan error, 1)
	_, err := run.Host.Root(func(ctx Ctx) (any, error) {
		// Footgun 10: the empty catch would hide a change that never happened. The change's fn runs after the root's.
		go func() {
			rejected <- list.Change(func(draft *delta.Object) {
				<-settled
				_, _ = draft.Array("items").Push("lost")
			}, ctx)
		}()
		return nil, nil
	}, background, RootOptions{})
	must(t, err)
	close(settled)
	err = <-rejected
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeReleased); !ok || chordErr.Reason != data.ReasonSettled {
		t.Fatalf("change = %v, want released/settled", err)
	}
	deliveredOn(run.Host)
	var value *delta.JsonObject
	run.Host.(*chordHost).enter(func() { value = list.Value() })
	if !reflect.DeepEqual(value, jsonObject("items", []any{})) {
		t.Fatalf("list = %v, want {items: []}", value)
	}
	reports := run.Errors.All()
	if len(reports) != 1 || reports[0].Kind != ErrTransactionCancelled || reports[0].Object != "app.restartable(1)" ||
		reports[0].Error == nil || reports[0].Error.Error() != "host.root: its fn settled" {
		t.Fatalf("errors = %+v, want one transaction_cancelled of app.restartable(1): host.root: its fn settled", reports)
	}
	// Awaited inside the root (the rule: await what you start in a root or a handler), it commits.
	_, err = run.Host.Root(func(ctx Ctx) (any, error) {
		return nil, list.Change(func(draft *delta.Object) { _, _ = draft.Array("items").Push("kept") }, ctx)
	}, background, RootOptions{})
	must(t, err)
	run.Host.(*chordHost).enter(func() { value = list.Value() })
	if !reflect.DeepEqual(value, jsonObject("items", []any{"kept"})) || len(run.Errors.All()) != 1 {
		t.Fatalf("list = %v, errors = %d, want {items: [kept]} and 1", value, len(run.Errors.All()))
	}
	must(t, run.Host.Close(background))
}

// Mirrors "... › are reported for a caller's cancellation while the change waits on the line" on a host: the object's
// owner reports it as `transaction_cancelled` with the cancellation's cause.
func TestDocumentsTransactionsTheirContextsEndCancelledAreReportedForACallersCancellationWhileTheChangeWaitsOnTheLineOnAHost(t *testing.T) {
	var list *singletonDoc
	run := restartable(t, func(_ int, self Self) { list = stateOf(self, attemptListDoc) }, loadedOptions{})
	gate := make(chan struct{})
	entered := make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- list.Change(func(*delta.Object) { close(entered); <-gate }, background)
	}()
	<-entered
	cancelled, cancel := context.WithCancelCause(background)
	waiting := make(chan error, 1)
	go func() {
		waiting <- list.Change(func(draft *delta.Object) { _, _ = draft.Array("items").Push("lost") }, cancelled)
	}()
	boom := errors.New("caller left")
	cancel(boom)
	close(gate)
	must(t, <-held)
	if err := <-waiting; !errors.Is(err, boom) {
		t.Fatalf("waiting change = %v, want the caller's cause", err)
	}
	reports := run.Errors.All()
	if len(reports) != 1 || reports[0].Kind != ErrTransactionCancelled || !errors.Is(reports[0].Error, boom) {
		t.Fatalf("errors = %+v, want one transaction_cancelled with the caller's cause", reports)
	}
	must(t, run.Host.Close(background))
}
