package documents

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports the describes `transactions their context's end cancelled (decision "Mistakes that wait are reported at once"
// (e))` and `commits in telemetry (slice 9)` of packages/chord/test/documents/documents.test.ts, on the harness object
// of support_test.go. The cases that start a change in `host.root` and leave it (its fn settled) belong to the host's
// tests: `host.root` is the host's.
// pi: packages/chord/src/documents/documents.ts

// TestTransactionsTheirContextsEndCancelledAreReportedForACallersCancellationWhileTheChangeWaitsOnTheLine runs `it("are
// reported for a caller's cancellation while the change waits on the line")`.
func TestTransactionsTheirContextsEndCancelledAreReportedForACallersCancellationWhileTheChangeWaitsOnTheLine(t *testing.T) {
	listDefinition, draftsDefinition := documentsDefinitions(t)
	var list *Singleton
	var drafts *Keyed
	o, _ := activated(t, objectOptions{}, func(a *attempt) {
		list = a.Singleton(listDefinition)
		drafts = a.Keyed(draftsDefinition)
	})
	gate := make(chan struct{})
	entered := make(chan struct{})
	holding := make(chan error, 1)
	go func() {
		_, err := drafts.Change(background, "a", func(*delta.Object) (any, error) {
			close(entered)
			<-gate
			return nil, nil
		})
		holding <- err
	}()
	<-entered
	ctx, cancel := chordctx.WithCancel(background)
	waiting := make(chan error, 1)
	go func() {
		_, err := list.Change(ctx, slotPush("lost"))
		waiting <- err
	}()
	cancel(nil)
	close(gate)
	if err := <-holding; err != nil {
		t.Fatalf("the change holding the line: %v", err)
	}
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled change = %v, want a cancellation", err)
	}
	if got := o.Cancelled(); len(got) != 1 {
		t.Fatalf("transaction_cancelled reports = %v, want one", got)
	}
}

// TestCommitsInTelemetryRecordADocumentsOpsAndDurationAsAnEventOfItsContextsSpanAndItsDurationMetric runs `it("records a
// commit's documents, ops and duration as an event of its context's span, and its duration metric")`.
func TestCommitsInTelemetryRecordADocumentsOpsAndDurationAsAnEventOfItsContextsSpanAndItsDurationMetric(t *testing.T) {
	listDefinition, _ := documentsDefinitions(t)
	meter := nexttest.Metered(t)
	o := txObject(t, "app.owner(1)", objectOptions{}, meter, nil)
	a := o.newAttempt()
	list := a.Singleton(listDefinition)
	requireNoError(t, a.Activate())
	err := meter.Telemetry.StartSpan(telemetry.SpanOptions{Name: "work"}, func(span telemetry.TelemetrySpan) error {
		traced := chordctx.WithTelemetry(background, span)
		if _, err := list.Change(traced, func(draft *delta.Object) (any, error) {
			_, err := draft.Array("items").Push("a", "b")
			return nil, err
		}); err != nil {
			return err
		}
		// A transaction that changes nothing commits nothing: no event.
		_, err := list.Change(traced, func(*delta.Object) (any, error) { return nil, nil })
		return err
	})
	requireNoError(t, err)
	events := commitEvents(meter, "work")
	if len(events) != 1 {
		t.Fatalf("chord.commit events = %v, want one", events)
	}
	for name, want := range map[string]float64{"documents": 1, "ops": 1, "rows": 0} {
		if got, _ := events[0].Get(name); got != want {
			t.Fatalf("event %s = %v, want %v", name, got, want)
		}
	}
	if got, ok := events[0].Get("duration"); !ok {
		t.Fatalf("event duration = %v, want a number", got)
	}
	found := false
	for _, measurement := range meter.Telemetry.GetMetrics() {
		if measurement.Name == "chord.commit.duration" && measurement.Unit != nil && *measurement.Unit == "ms" {
			found = true
		}
	}
	if !found {
		t.Fatal("no chord.commit.duration metric in ms")
	}
}

// TestCommitsInTelemetryCountTheTableRowsACommitsWritesChanged runs `it("counts the table rows a commit's writes
// changed: a put one, an update or delete the rows it changed")`.
func TestCommitsInTelemetryCountTheTableRowsACommitsWritesChanged(t *testing.T) {
	meter := nexttest.Metered(t)
	s := storedOn(t, nil, false, meter)
	edits := changeTables(t, s, 1)[0]
	err := meter.Telemetry.StartSpan(telemetry.SpanOptions{Name: "work"}, func(span telemetry.TelemetrySpan) error {
		traced := chordctx.WithTelemetry(background, span)
		if _, err := s.self().Tx(func(tx Tx) (any, error) {
			for _, id := range []string{"1", "2", "3"} {
				if err := tx.Put(edits, store.Row{"id": id, "path": "a/" + id, "note": "a"}); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}, traced); err != nil {
			return err
		}
		_, err := s.self().Tx(func(tx Tx) (any, error) {
			if err := tx.UpdateWhere(edits, func(e store.ColumnRefs) store.Condition { return store.Eq(e["note"], "a") }, store.Row{"note": "b"}); err != nil {
				return nil, err
			}
			return nil, tx.Delete(edits, store.Row{"id": "9"})
		}, traced)
		return err
	})
	requireNoError(t, err)
	events := commitEvents(meter, "work")
	if len(events) != 2 {
		t.Fatalf("chord.commit events = %v, want two", events)
	}
	for i, event := range events {
		for name, want := range map[string]float64{"documents": 0, "ops": 0, "rows": 3} {
			if got, _ := event.Get(name); got != want {
				t.Fatalf("event %d %s = %v, want %v", i, name, got, want)
			}
		}
	}
}

// commitEvents is the `chord.commit` events of the span named name, in order.
func commitEvents(meter *nexttest.Meter, name string) []telemetry.SpanAttributes {
	var events []telemetry.SpanAttributes
	for _, span := range meter.Telemetry.GetSpans() {
		if span.Name != name {
			continue
		}
		for _, event := range span.Events {
			if event.Name == "chord.commit" {
				events = append(events, event.Attributes)
			}
		}
	}
	return events
}

// countNow counts the reads of the clock a timed commit uses (the seam `commitNow`, as Pi's tests fake Date).
func countNow(t *testing.T) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	previous := commitNow
	commitNow = func() time.Time { calls.Add(1); return previous() }
	t.Cleanup(func() { commitNow = previous })
	return &calls
}

// TestCommitsInTelemetryTimesNothingWhenNothingRecords runs `it("times nothing when nothing records")`; the control is
// a commit that something records, which reads the clock at its start and its end.
func TestCommitsInTelemetryTimesNothingWhenNothingRecords(t *testing.T) {
	listDefinition, _ := documentsDefinitions(t)
	var list *Singleton
	activated(t, objectOptions{}, func(a *attempt) { list = a.Singleton(listDefinition) })
	calls := countNow(t)
	if _, err := list.Change(background, slotPush("a")); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("the clock was read %d times, want none", got)
	}

	meter := nexttest.Metered(t)
	o := txObject(t, "app.owner(1)", objectOptions{}, meter, nil)
	a := o.newAttempt()
	metered := a.Singleton(listDefinition)
	requireNoError(t, a.Activate())
	calls.Store(0)
	if _, err := metered.Change(background, func(draft *delta.Object) (any, error) {
		_, err := draft.Array("items").Push("a")
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("the clock was read %d times for a recorded commit, want 2 (start, end)", got)
	}
}
