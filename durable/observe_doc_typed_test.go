package durable_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Pi types.ts Session.observeDoc, with the typed value the Go helper decodes: the synchronous observer of one document
// address, attached atomically on the Session line, that watches are built on.

type observedCounter struct {
	N int `json:"n"`
}

var observedCounterDoc = durable.DefineDoc(durable.DocDefinition[observedCounter]{
	CommonDocDefinition: durable.CommonDocDefinition[observedCounter]{
		Kind: "observe.typed-counter", Version: 1,
		Initial:        func() observedCounter { return observedCounter{N: 7} },
		CheckpointWhen: func(observedCounter, []durable.Op, durable.CheckpointInfo) bool { return false },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
})

type countObserver struct {
	mu    sync.Mutex
	calls []durable.WatchCall[observedCounter]
	ends  []durable.WatchEnd
	// advancePanic is what Advance panics with after recording its call.
	advancePanic error
}

func (observer *countObserver) Advance(_ context.Context, call durable.WatchCall[observedCounter]) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.calls = append(observer.calls, call)
	if observer.advancePanic != nil {
		panic(observer.advancePanic)
	}
}

func (observer *countObserver) End(end durable.WatchEnd) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.ends = append(observer.ends, end)
}

func (observer *countObserver) values() []int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	var values []int
	for _, call := range observer.calls {
		values = append(values, call.Value.N)
	}
	return values
}

func setObservedCounter(t *testing.T, harness sessiontest.Harness, n any) {
	t.Helper()
	if _, err := durable.Commit(t.Context(), harness.Session, func(tx durable.Tx) (struct{}, error) {
		draft, err := tx.Doc(observedCounterDoc)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, draft.Set("n", n)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestObserveDocDecodesTheTokensTypeAndKeepsCommitOrder(t *testing.T) {
	t.Run("an absent document attaches nothing", func(t *testing.T) {
		harness := sessiontest.OpenTestSession()
		observer := &countObserver{}
		observation, err := durable.ObserveDoc[observedCounter](t.Context(), harness.Session, observedCounterDoc, observer)
		if err != nil || observation != nil {
			t.Fatalf("observation of an absent document = %v, %v, want none", observation, err)
		}
		setObservedCounter(t, harness, 1)
		if got := observer.values(); len(got) != 0 {
			t.Fatalf("an observer that was never attached saw %v", got)
		}
	})

	t.Run("starts at the decoded committed value and advances with each commit in order", func(t *testing.T) {
		harness := sessiontest.OpenTestSession()
		setObservedCounter(t, harness, 3)
		observer := &countObserver{}
		observation, err := durable.ObserveDoc[observedCounter](t.Context(), harness.Session, observedCounterDoc, observer)
		if err != nil || observation == nil {
			t.Fatalf("ObserveDoc = %v, %v", observation, err)
		}
		if got := observation.Value(); got != (observedCounter{N: 3}) {
			t.Fatalf("observation value = %+v, want n 3", got)
		}
		for n := 4; n <= 8; n++ {
			setObservedCounter(t, harness, n)
		}
		if got := observer.values(); !slices.Equal(got, []int{4, 5, 6, 7, 8}) {
			t.Fatalf("the observer saw %v, want 4..8 in commit order", got)
		}
		observer.mu.Lock()
		first := observer.calls[0]
		observer.mu.Unlock()
		if first.Kind != durable.WatchCallValue || first.Retired || len(first.Ops) == 0 {
			t.Fatalf("first call = %+v, want a value with its ops", first)
		}
	})

	t.Run("Detach ends the observation: nothing follows it", func(t *testing.T) {
		harness := sessiontest.OpenTestSession()
		setObservedCounter(t, harness, 1)
		observer := &countObserver{}
		observation, err := durable.ObserveDoc[observedCounter](t.Context(), harness.Session, observedCounterDoc, observer)
		if err != nil || observation == nil {
			t.Fatalf("ObserveDoc = %v, %v", observation, err)
		}
		setObservedCounter(t, harness, 2)
		observation.Detach()
		observation.Detach()
		setObservedCounter(t, harness, 3)
		if got := observer.values(); !slices.Equal(got, []int{2}) {
			t.Fatalf("the observer saw %v, want only 2", got)
		}
		observer.mu.Lock()
		ended := len(observer.ends)
		observer.mu.Unlock()
		if ended != 0 {
			t.Fatalf("a detached observer got %d ends", ended)
		}
	})

	t.Run("a value that does not decode at attachment is an error and leaves the observer detached", func(t *testing.T) {
		harness := sessiontest.OpenTestSession()
		setObservedCounter(t, harness, "not a number")
		observer := &countObserver{}
		observation, err := durable.ObserveDoc[observedCounter](t.Context(), harness.Session, observedCounterDoc, observer)
		if err == nil || observation != nil {
			t.Fatalf("ObserveDoc = %v, %v, want the decode error", observation, err)
		}
		setObservedCounter(t, harness, 5)
		observer.mu.Lock()
		got := len(observer.calls) + len(observer.ends)
		observer.mu.Unlock()
		if got != 0 {
			t.Fatalf("the observer of a failed attachment got %d calls", got)
		}
	})

	t.Run("an observer that panics is reported and ends with its listener_error, through the typed wrapper", func(t *testing.T) {
		reports := make(chan error, 4)
		harness := sessiontest.OpenReportingTestSession(func(err error) { reports <- err })
		setObservedCounter(t, harness, 1)
		boom := errors.New("observer failed")
		observer := &countObserver{advancePanic: boom}
		observation, err := durable.ObserveDoc[observedCounter](t.Context(), harness.Session, observedCounterDoc, observer)
		if err != nil || observation == nil {
			t.Fatalf("ObserveDoc = %v, %v", observation, err)
		}
		setObservedCounter(t, harness, 2)
		setObservedCounter(t, harness, 3)
		observer.mu.Lock()
		defer observer.mu.Unlock()
		if len(observer.calls) != 1 {
			t.Fatalf("the observer was called %d times, want only the call that panicked", len(observer.calls))
		}
		if len(observer.ends) != 1 || observer.ends[0].Kind != durable.WatchListenerError || !errors.Is(observer.ends[0].Error, boom) {
			t.Fatalf("ends = %+v, want one listener_error carrying the panic", observer.ends)
		}
		select {
		case err := <-reports:
			if !errors.Is(err, boom) {
				t.Fatalf("reported %v, want %v", err, boom)
			}
		default:
			t.Fatal("the panic was not reported")
		}
	})
}
