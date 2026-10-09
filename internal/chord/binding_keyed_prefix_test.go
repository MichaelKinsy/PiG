package chord

import (
	"context"
	"sync"
	"testing"
)

// upstream: packages/chord/src/services/consumer.ts:312-327 resets keyed instances before awaiting subscription release.
func TestBeginRebindRevokesKeyedHandleBeforeSubscriptionRelease(t *testing.T) {
	t.Parallel()
	fixture := newRemoteFixture(t, KeyedService(counterDefinition))
	counter := newCounter(t)
	closeInstance, err := Spawn[Counter](fixture.provider, counterDefinition, "one", counter)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = closeInstance()
		fixture.endpoint.Dispose()
		if err := fixture.provider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() {
		unblock()
		if err := fixture.binding.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	fixture.binding.transport = closeGateTransport{RemoteServiceTransport: NewJSONCopyTransport(fixture.endpoint), release: release}
	observed := make(chan *RemoteService, 1)
	stop, err := fixture.binding.Observe(counterDefinition.Id(), func(_ context.Context, service *RemoteService) error { observed <- service; return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if err := fixture.binding.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := <-observed
	operation := fixture.binding.BeginRebind(t.Context(), false)
	_, err = service.Call(t.Context(), "add", 1, "late")
	if !hasRemoteServiceErrorCode(err, ErrServiceStaleInstance) {
		t.Fatalf("retained keyed handle error=%v, want stale instance", err)
	}
	unblock()
	if err := operation.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.binding.Dispose(context.Background()); err != nil {
		t.Fatal(err)
	}
}
