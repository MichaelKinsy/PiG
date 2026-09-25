package chord

import (
	"context"
	"errors"
	"testing"
)

// Upstream services/state.ts resets #changing in a finally block when a
// mutation throws. A failed transaction must not prevent later publications.
func TestChangePanicDiscardsDraftAndAllowsNextChange(t *testing.T) {
	counter := newCounter(t)
	failure := errors.New("mutation failed")
	var escaped any
	err := func() (err error) {
		defer func() { escaped = recover() }()
		return counter.state.Change(context.Background(), func(draft *counterState) error {
			draft.Count = 99
			panic(failure)
		})
	}()
	if escaped != nil {
		t.Fatalf("mutation panic escaped instead of returning an error: %v", escaped)
	}
	if !errors.Is(err, failure) {
		t.Fatalf("mutation failure = %v, want %v", err, failure)
	}
	if !counter.state.core.changeMu.TryLock() {
		t.Fatal("mutation panic left state locked; later Change and Replace would deadlock")
	}
	counter.state.core.changeMu.Unlock()
	if counter.state.Value().Count != 0 || counter.state.Sequence() != 0 {
		t.Fatal("failed mutation committed its draft")
	}
	if got, err := counter.Add(context.Background(), 1, "after failure"); err != nil || got != 1 {
		t.Fatalf("next change = %d, %v", got, err)
	}
}

type panicMethodCounter struct {
	*counterImpl
	failure error
}

func (counter *panicMethodCounter) Fail(context.Context) error { panic(counter.failure) }

// Upstream provider.ts invoke() turns a thrown method failure into a rejected Promise.
func TestRemoteMethodPanicIsReturnedToCaller(t *testing.T) {
	ctx := context.Background()
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	t.Cleanup(func() {
		if err := fixture.binding.Dispose(ctx); err != nil {
			t.Error(err)
		}
		fixture.endpoint.Dispose()
		if err := fixture.provider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	failure := errors.New("method failed")
	counter := &panicMethodCounter{counterImpl: newCounter(t), failure: failure}
	if err := Provide[Counter](fixture.provider, counterDefinition, counter); err != nil {
		t.Fatal(err)
	}
	service, err := UseRemote(fixture.binding, counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	var escaped any
	func() {
		defer func() { escaped = recover() }()
		_, err = service.Call(ctx, "fail")
	}()
	if escaped != nil {
		t.Fatalf("method panic escaped instead of returning a call error: %v", escaped)
	}
	if !errors.Is(err, failure) {
		t.Fatalf("call error = %v, want %v", err, failure)
	}
	if got, err := CallResult[int](ctx, service, "add", 1, "after failure"); err != nil || got != 1 {
		t.Fatalf("next call = %d, %v", got, err)
	}
	noBindingErrors(t, fixture)
}
