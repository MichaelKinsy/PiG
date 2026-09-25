package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

var keyedCounterDefinition = pico3.DefineService[Counter]("test.keyed-counter")

// countingEndpoint wraps the real endpoint and counts control calls.
type countingEndpoint struct {
	RemoteServiceEndpoint
	subscribes   atomic.Int64
	unsubscribes atomic.Int64
}

func (endpoint *countingEndpoint) Invoke(ctx context.Context, call ServiceCall, publish ServiceUpdatePublisher) (json.RawMessage, error) {
	if control, ok := DecodeServiceControlCall(call); ok {
		switch control.Type {
		case controlSubscribe:
			endpoint.subscribes.Add(1)
		case controlUnsubscribe:
			endpoint.unsubscribes.Add(1)
		}
	}
	return endpoint.RemoteServiceEndpoint.Invoke(ctx, call, publish)
}

func newCountingFixture(t *testing.T, definitions ...ServiceProviderDefinition) (*remoteFixture, *countingEndpoint) {
	t.Helper()
	fixture := newRemoteFixture(t, definitions...)
	counting := &countingEndpoint{RemoteServiceEndpoint: fixture.endpoint}
	ids := make([]string, len(definitions))
	for index, definition := range definitions {
		ids[index] = definition.ServiceId
	}
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services:  ids,
		Transport: NewJSONCopyTransport(counting),
		OnError:   func(err error) { fixture.errs <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.binding = binding
	return fixture, counting
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func noBindingErrors(t *testing.T, fixture *remoteFixture) {
	t.Helper()
	select {
	case err := <-fixture.errs:
		t.Fatalf("unexpected binding error: %v", err)
	default:
	}
}

func TestProviderSnapshotCoherenceAndActivationBuffering(t *testing.T) {
	ctx := context.Background()
	provider, err := NewRemoteServiceProvider(SingletonService(counterDefinition))
	if err != nil {
		t.Fatal(err)
	}
	counter := newCounter(t)
	if err := Provide[Counter](provider, counterDefinition, counter); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(ctx, 1, "covered"); err != nil {
		t.Fatal(err)
	}
	var received []ServiceProviderUpdate
	subscription, err := provider.Subscribe(counterDefinition.Id(), ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) {
		received = append(received, update)
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := subscription.Snapshot()
	state := snapshot.Instances[0].Members[2]
	if state.Name != "state" || state.Sequence != 1 || !pico3.IsBase(state.Ops) {
		t.Fatalf("snapshot state member = %+v", state)
	}
	for _, label := range []string{"b1", "b2"} {
		if _, err := counter.Add(ctx, 1, label); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 0 {
		t.Fatalf("updates delivered before activation: %+v", received)
	}
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(ctx, 1, "live"); err != nil {
		t.Fatal(err)
	}
	var sequences []int
	for _, update := range received {
		sequences = append(sequences, update.Sequence)
	}
	if !reflect.DeepEqual(sequences, []int{2, 3, 4}) {
		t.Fatalf("delivered sequences = %v, want [2 3 4]", sequences)
	}
	// Close is idempotent and stops delivery.
	if err := subscription.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := subscription.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(ctx, 1, "closed"); err != nil {
		t.Fatal(err)
	}
	if len(received) != 3 {
		t.Fatalf("delivery after close: %d updates", len(received))
	}
}

func TestReplicaRejectsGapsAndUpdatesBeforeHydration(t *testing.T) {
	ctx := context.Background()
	replica := newReplica(nil)
	if err := replica.update(ctx, 1, []pico3.Op{{"s", []any{"a"}, 1.0}}, nil); err == nil {
		t.Fatal("update before hydration accepted")
	}
	if err := replica.hydrate(ctx, 1, []pico3.Op{{"s", []any{"a"}, 1.0}}, nil); err == nil {
		t.Fatal("non-base hydration accepted")
	}
	if err := replica.hydrate(ctx, 1, []pico3.Op{{"r", map[string]any{"a": 0.0}}}, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := replica.snapshotValue()
	if err := replica.update(ctx, 2, []pico3.Op{{"s", []any{"a"}, 1.0}}, nil); err != nil {
		t.Fatal(err)
	}
	if first.(map[string]any)["a"] != 0.0 {
		t.Fatalf("retained value mutated by update: %v", first)
	}
	if err := replica.update(ctx, 4, []pico3.Op{{"s", []any{"a"}, 2.0}}, nil); err == nil || !strings.Contains(err.Error(), "gap") {
		t.Fatalf("gap error = %v", err)
	}
	if _, hydrated := replica.snapshotValue(); hydrated {
		t.Fatal("replica kept a value after a sequence gap")
	}
}

func TestStateListenerFailureIsReturnedAfterCommit(t *testing.T) {
	ctx := context.Background()
	counter := newCounter(t)
	failure := errors.New("listener failed")
	unsubscribe, err := counter.state.Subscribe(func(_ *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
		if info.Kind == DeliveryUpdate {
			panic(failure)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if _, err := counter.Add(ctx, 5, "x"); !errors.Is(err, failure) {
		t.Fatalf("Change error = %v, want listener failure", err)
	}
	if counter.state.Value().Count != 5 || counter.state.Sequence() != 1 {
		t.Fatalf("value was not committed: %+v", counter.state.Value())
	}
	// A failing hydration unregisters and returns the error.
	if _, err := counter.state.Subscribe(func(*counterState, context.Context, pico3.ReplicatedStateDelivery) { panic(failure) }); !errors.Is(err, failure) {
		t.Fatalf("hydrate failure = %v", err)
	}
}

func TestReentrantPublicationIsQueuedInOrder(t *testing.T) {
	ctx := context.Background()
	counter := newCounter(t)
	rec := newRecorder()
	var once sync.Once
	if _, err := counter.state.Subscribe(func(value *counterState, listenerCtx context.Context, info pico3.ReplicatedStateDelivery) {
		rec.listen(value, listenerCtx, info)
		if info.Kind == DeliveryUpdate {
			once.Do(func() {
				if _, err := counter.Add(ctx, 10, "reentrant"); err != nil {
					t.Error(err)
				}
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(ctx, 1, "outer"); err != nil {
		t.Fatal(err)
	}
	got := rec.snapshot()
	var sequences []int
	for _, entry := range got {
		sequences = append(sequences, entry.Sequence)
	}
	if !reflect.DeepEqual(sequences, []int{0, 1, 2}) || got[2].Value.Count != 11 {
		t.Fatalf("deliveries = %+v", got)
	}
}

func TestConcurrentChangesReachReplicaGapFree(t *testing.T) {
	ctx := context.Background()
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	counter := newCounter(t)
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
	replica, err := service.State("state")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var sequences []int
	if _, err := TypedReplica[*counterState](replica).Subscribe(func(_ *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
		mu.Lock()
		sequences = append(sequences, info.Sequence)
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	const writers, each = 8, 25
	var group sync.WaitGroup
	for writer := range writers {
		group.Go(func() {
			for index := range each {
				if _, err := service.Call(ctx, "add", 1, fmt.Sprintf("%d-%d", writer, index)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	group.Wait()
	value, ok, err := TypedReplica[*counterState](replica).Load()
	if err != nil || !ok {
		t.Fatalf("load: %v %v", ok, err)
	}
	if value.Count != writers*each || len(value.Log) != writers*each {
		t.Fatalf("replica = %d entries, count %d", len(value.Log), value.Count)
	}
	mu.Lock()
	defer mu.Unlock()
	for index, sequence := range sequences {
		if sequence != index {
			t.Fatalf("replica deliveries not gap-free: %v", sequences)
		}
	}
	noBindingErrors(t, fixture)
}

func TestSingletonReplaceRehydratesAndWithdrawClears(t *testing.T) {
	ctx := context.Background()
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	first := newCounter(t)
	if err := Provide[Counter](fixture.provider, counterDefinition, first); err != nil {
		t.Fatal(err)
	}
	service, err := UseRemote(fixture.binding, counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	replica, _ := service.State("state")
	rec := newRecorder()
	if _, err := TypedReplica[*counterState](replica).Subscribe(rec.listen); err != nil {
		t.Fatal(err)
	}
	second := newCounter(t)
	if _, err := second.Add(ctx, 40, "second"); err != nil {
		t.Fatal(err)
	}
	if err := Replace[Counter](fixture.provider, counterDefinition, second); err != nil {
		t.Fatal(err)
	}
	// The retired implementation no longer publishes.
	if _, err := first.Add(ctx, 1, "retired"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Call(ctx, "add", 2, "via-facade"); err != nil {
		t.Fatal(err)
	}
	got := rec.waitFor(t, 3)
	want := []delivery{
		{DeliveryHydrate, 0, counterState{Log: []string{}}},
		{DeliveryHydrate, 1, counterState{Count: 40, Log: []string{"second"}}},
		{DeliveryUpdate, 2, counterState{Count: 42, Log: []string{"second", "via-facade"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %+v, want %+v", got, want)
	}
	if err := Withdraw[Counter](fixture.provider, counterDefinition); err != nil {
		t.Fatal(err)
	}
	if _, hydrated := replica.Value(); hydrated {
		t.Fatal("replica retained a value after unavailable")
	}
	if _, err := service.Call(ctx, "add", 1, "gone"); !IsRemoteServiceErrorCode(err, ErrServiceNotFound) {
		t.Fatalf("call after withdraw = %v", err)
	}
	// A replacement with a different member shape is rejected.
	if err := ValidateReplacement[Counter](fixture.provider, counterDefinition, first); err != nil {
		t.Fatal(err)
	}
	noBindingErrors(t, fixture)
}

func TestKeyedGenerationsObservationAndStaleCalls(t *testing.T) {
	ctx := context.Background()
	fixture := newRemoteFixture(t, KeyedService(keyedCounterDefinition))
	type observation struct {
		generation int
		service    *RemoteService
		cancelled  chan struct{}
	}
	observed := make(chan observation, 8)
	stop, err := ObserveRemote(fixture.binding, keyedCounterDefinition, func(observeCtx context.Context, service *RemoteService) error {
		entry := observation{generation: service.Address().Generation, service: service, cancelled: make(chan struct{})}
		observed <- entry
		<-observeCtx.Done()
		close(entry.cancelled)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := newCounter(t)
	closeFirst, err := Spawn[Counter](fixture.provider, keyedCounterDefinition, "lane", first)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	var one observation
	select {
	case one = <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("instance was not observed")
	}
	if one.generation != 1 {
		t.Fatalf("generation = %d", one.generation)
	}
	if raw, err := one.service.Call(ctx, "add", 3, "k"); err != nil || string(raw) != "3" {
		t.Fatalf("keyed call = %s, %v", raw, err)
	}
	replica, err := one.service.State("state")
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "keyed replica update", func() bool {
		value, ok, _ := TypedReplica[*counterState](replica).Load()
		return ok && value.Count == 3
	})
	if err := closeFirst(); err != nil {
		t.Fatal(err)
	}
	if err := closeFirst(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-one.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("observer context was not cancelled on close")
	}
	if _, err := one.service.Call(ctx, "add", 1, "stale"); !IsRemoteServiceErrorCode(err, ErrServiceStaleInstance) {
		t.Fatalf("call on closed observation = %v", err)
	}
	second := newCounter(t)
	if _, err := Spawn[Counter](fixture.provider, keyedCounterDefinition, "lane", second); err != nil {
		t.Fatal(err)
	}
	var two observation
	select {
	case two = <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("respawned instance was not observed")
	}
	if two.generation != 2 {
		t.Fatalf("respawn generation = %d", two.generation)
	}
	// The provider rejects the previous generation directly.
	if _, err := fixture.provider.Invoke(ctx, ServiceCall{ServiceId: keyedCounterDefinition.Id(), Instance: &ServiceInstanceAddress{Key: "lane", Generation: 1}, Member: "add", Args: []json.RawMessage{mustRaw(1), mustRaw("x")}}); !IsRemoteServiceErrorCode(err, ErrServiceStaleInstance) {
		t.Fatalf("stale generation invoke = %v", err)
	}
	stop()
	stop()
	select {
	case <-two.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the observation")
	}
	noBindingErrors(t, fixture)
}

func TestRebindAndDisposeCloseSubscriptionsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	fixture, counting := newCountingFixture(t, SingletonService(counterDefinition))
	counter := newCounter(t)
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
	replica, _ := service.State("state")
	if err := fixture.binding.Rebind(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, hydrated := replica.Value(); hydrated {
		t.Fatal("unbound replica retained a value")
	}
	if _, err := service.Call(ctx, "add", 1, "unbound"); !IsRemoteServiceErrorCode(err, ErrServiceStaleInstance) {
		t.Fatalf("call while unbound = %v", err)
	}
	if got := counting.unsubscribes.Load(); got != 1 {
		t.Fatalf("unsubscribes after unbind = %d", got)
	}
	if _, err := counter.Add(ctx, 7, "while-unbound"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.binding.Rebind(ctx, true); err != nil {
		t.Fatal(err)
	}
	value, ok, err := TypedReplica[*counterState](replica).Load()
	if err != nil || !ok || value.Count != 7 {
		t.Fatalf("rehydrated replica = %+v %v %v", value, ok, err)
	}
	if err := fixture.binding.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.binding.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if subscribes, unsubscribes := counting.subscribes.Load(), counting.unsubscribes.Load(); subscribes != 2 || unsubscribes != 2 {
		t.Fatalf("subscribes=%d unsubscribes=%d, want 2/2", subscribes, unsubscribes)
	}
	if err := fixture.binding.Ready(ctx); err == nil {
		t.Fatal("Ready after dispose succeeded")
	}
	noBindingErrors(t, fixture)
}

func TestEndpointDisposeReleasesProviderSubscriptions(t *testing.T) {
	ctx := context.Background()
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	counter := newCounter(t)
	if err := Provide[Counter](fixture.provider, counterDefinition, counter); err != nil {
		t.Fatal(err)
	}
	var published atomic.Int64
	publish := func(context.Context, string, ServiceProviderUpdate) error { published.Add(1); return nil }
	raw, err := fixture.endpoint.Invoke(ctx, CreateServiceSubscribeCall("sub-1", counterDefinition.Id(), ServiceSingleton), publish)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot ServiceSubscriptionSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.ServiceId != counterDefinition.Id() {
		t.Fatalf("snapshot = %s, %v", raw, err)
	}
	if _, err := fixture.endpoint.Invoke(ctx, CreateServiceSubscribeCall("sub-1", counterDefinition.Id(), ServiceSingleton), publish); err == nil {
		t.Fatal("duplicate subscription ID accepted")
	}
	if _, err := counter.Add(ctx, 1, "a"); err != nil {
		t.Fatal(err)
	}
	fixture.endpoint.Dispose()
	fixture.endpoint.Dispose()
	if _, err := counter.Add(ctx, 1, "b"); err != nil {
		t.Fatal(err)
	}
	if got := published.Load(); got != 1 {
		t.Fatalf("published after endpoint dispose: %d", got)
	}
	if _, err := fixture.endpoint.Invoke(ctx, CreateServiceCatalogueCall(), publish); err == nil {
		t.Fatal("disposed endpoint accepted a call")
	}
}

func TestBindingAllowlistAndModeValidation(t *testing.T) {
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	other := pico3.DefineService[Counter]("test.other")
	if _, err := UseRemote(fixture.binding, other); !IsRemoteServiceErrorCode(err, ErrServiceNotAllowed) {
		t.Fatalf("non-allowlisted use = %v", err)
	}
	if _, err := UseRemote(fixture.binding, counterDefinition); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveRemote(fixture.binding, counterDefinition, func(context.Context, *RemoteService) error { return nil }); !IsRemoteServiceErrorCode(err, ErrServiceModeMismatch) {
		t.Fatalf("mode mismatch = %v", err)
	}
	if _, err := NewRemoteServiceProvider(SingletonService(counterDefinition), KeyedService(counterDefinition)); err == nil {
		t.Fatal("duplicate provider IDs accepted")
	}
	if _, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: []string{"a", "a"}}); err == nil {
		t.Fatal("duplicate binding IDs accepted")
	}
	// The provider reports a missing singleton subscription as service_not_found.
	if err := fixture.binding.Ready(context.Background()); !IsRemoteServiceErrorCode(err, ErrServiceNotFound) {
		t.Fatalf("ready without provider = %v", err)
	}
	if err := <-fixture.errs; !IsRemoteServiceErrorCode(err, ErrServiceNotFound) {
		t.Fatalf("reported error = %v", err)
	}
	_ = slices.Clone([]int{})
}

func TestSubscribeDuringDeliveryHydratesBeforeLaterUpdates(t *testing.T) {
	ctx := context.Background()
	counter := newCounter(t)
	late := newRecorder()
	var once sync.Once
	if _, err := counter.state.Subscribe(func(_ *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
		if info.Kind != DeliveryUpdate {
			return
		}
		once.Do(func() {
			// Queue publication 2, then subscribe while delivery of 1 drains.
			if _, err := counter.Add(ctx, 1, "queued"); err != nil {
				t.Error(err)
			}
			if _, err := counter.state.Subscribe(late.listen); err != nil {
				t.Error(err)
			}
		})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(ctx, 1, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := counter.Add(ctx, 1, "third"); err != nil {
		t.Fatal(err)
	}
	got := late.snapshot()
	var kinds []string
	for _, entry := range got {
		kinds = append(kinds, fmt.Sprintf("%s:%d", entry.Kind, entry.Sequence))
	}
	if want := []string{"hydrate:2", "update:3"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("late deliveries = %v, want %v", kinds, want)
	}
}
