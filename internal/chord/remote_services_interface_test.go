package chord

import (
	"context"
	"testing"
)

// upstream: packages/chord/src/types.ts:188 RemoteServices observe(service, handler), ready(context), dispose(context): observe
// reports each acquired service to the handler, the returned function stops the observation, ready waits for the initial snapshots,
// and a disposed set refuses use.
func TestRemoteServicesObserveReadyDispose(t *testing.T) {
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
	var services RemoteServices = fixture.binding
	observed := make(chan *RemoteService, 4)
	stop, err := services.Observe(counterDefinition.Id(), func(_ context.Context, service *RemoteService) error {
		observed <- service
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := services.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case service := <-observed:
		if service == nil {
			t.Fatal("the observer received a nil service")
		}
	default:
		t.Fatal("Ready returned before the observer saw the service")
	}
	stop()
	if err := services.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := services.Use(counterDefinition.Id()); err == nil {
		t.Fatal("a disposed RemoteServices still hands out services")
	}
}
