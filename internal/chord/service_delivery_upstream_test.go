package chord

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/service-delivery.test.ts: per-subscription provider delivery queues with an explicit root "reset" after 100 pending updates, and the consumer's handling of it through the wire codecs.

type valueDocState struct {
	Value int `json:"value"`
}

type DeliveryCounter interface {
	State() ReplicatedStateOf[*valueDocState]
}

var deliveryCounterDefinition = DefineService[DeliveryCounter]("test.delivery-counter")

type deliveryCounterImpl struct {
	state *MutableReplicatedState[*valueDocState]
}

func (counter *deliveryCounterImpl) State() ReplicatedStateOf[*valueDocState] { return counter.state }

func newDeliveryCounter(t *testing.T, value int) *deliveryCounterImpl {
	t.Helper()
	state, err := NewReplicatedState(&valueDocState{Value: value})
	if err != nil {
		t.Fatal(err)
	}
	return &deliveryCounterImpl{state: state}
}

func (counter *deliveryCounterImpl) replace(t *testing.T, ctx context.Context, value int) {
	t.Helper()
	if err := counter.state.Replace(ctx, &valueDocState{Value: value}); err != nil {
		t.Fatal(err)
	}
}

func (counter *deliveryCounterImpl) set(t *testing.T, value int) {
	t.Helper()
	if err := counter.state.Change(context.Background(), func(draft *valueDocState) error { draft.Value = value; return nil }); err != nil {
		t.Fatal(err)
	}
}

type DeliveryPair interface {
	Left() ReplicatedStateOf[*valueDocState]
	Right() ReplicatedStateOf[*valueDocState]
}

var deliveryPairDefinition = DefineService[DeliveryPair]("test.delivery-pair")

type deliveryPairImpl struct {
	left, right *MutableReplicatedState[*valueDocState]
}

func (pair *deliveryPairImpl) Left() ReplicatedStateOf[*valueDocState]  { return pair.left }
func (pair *deliveryPairImpl) Right() ReplicatedStateOf[*valueDocState] { return pair.right }

func newDeliveryProvider(t *testing.T, definitions ...ServiceProviderDefinition) *RemoteServiceProvider {
	t.Helper()
	provider, err := NewRemoteServiceProvider(definitions...)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func provideCounter(t *testing.T, provider *RemoteServiceProvider, counter *deliveryCounterImpl) {
	t.Helper()
	if err := Provide[DeliveryCounter](provider, deliveryCounterDefinition, counter); err != nil {
		t.Fatal(err)
	}
}

type updateLog struct {
	mu      sync.Mutex
	updates []ServiceProviderUpdate
	ctxs    []context.Context
}

func (log *updateLog) listen(ctx context.Context, update ServiceProviderUpdate) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.updates = append(log.updates, update)
	log.ctxs = append(log.ctxs, ctx)
}

func (log *updateLog) types() []ServiceProviderUpdateType {
	log.mu.Lock()
	defer log.mu.Unlock()
	types := make([]ServiceProviderUpdateType, len(log.updates))
	for at, update := range log.updates {
		types[at] = update.Type
	}
	return types
}

func subscribeCounter(t *testing.T, provider *RemoteServiceProvider, listener UpdateListener) ServiceSubscription {
	t.Helper()
	subscription, err := provider.Subscribe(deliveryCounterDefinition.Id(), ServiceSingleton, listener)
	if err != nil {
		t.Fatal(err)
	}
	return subscription
}

func activate(t *testing.T, subscription ServiceSubscription) {
	t.Helper()
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
}

func rootReplacement(value int) []Op { return []Op{{"r", chordjson.ObjectOf("value", float64(value))}} }

func TestProviderDeliveryQueues(t *testing.T) {
	background := context.Background()

	t.Run("appends reentrant updates to the activation FIFO", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
		counter := newDeliveryCounter(t, 0)
		provideCounter(t, provider, counter)
		var received []int
		subscription := subscribeCounter(t, provider, func(ctx context.Context, update ServiceProviderUpdate) {
			if update.Type != UpdateState {
				return
			}
			received = append(received, update.Sequence)
			if update.Sequence == 1 {
				counter.replace(t, background, 3)
			}
		})
		counter.replace(t, background, 1)
		counter.replace(t, background, 2)
		activate(t, subscription)
		activate(t, subscription)
		if !reflect.DeepEqual(received, []int{1, 2, 3}) {
			t.Fatalf("received = %v", received)
		}
		_ = provider.Dispose()
	})

	for _, count := range []int{100, 101, 102, 201, 202} {
		t.Run(fmt.Sprintf("bounds %d pending updates with explicit root resets", count), func(t *testing.T) {
			provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
			counter := newDeliveryCounter(t, 0)
			provideCounter(t, provider, counter)
			log := &updateLog{}
			subscription := subscribeCounter(t, provider, log.listen)
			type markerKey struct{}
			marker := context.WithValue(background, markerKey{}, "pending")
			for value := 1; value <= count; value++ {
				counter.replace(t, marker, value)
			}
			if len(log.types()) != 0 {
				t.Fatalf("updates before activation = %v", log.types())
			}
			if member := subscription.Snapshot().Instances[0].Members[0]; member.Sequence != 0 {
				t.Fatalf("snapshot member = %+v", member)
			}
			activate(t, subscription)
			first := (count-1)/100*100 + 1
			if got := len(log.types()); got != count-first+1 {
				t.Fatalf("updates = %d, want %d", got, count-first+1)
			}
			if count > 100 {
				want := ServiceProviderUpdate{Type: UpdateReset, Reset: &ServiceSubscriptionSnapshot{
					ServiceId: deliveryCounterDefinition.Id(), Mode: ServiceSingleton,
					Instances: []ServiceInstanceSnapshot{{Members: []ServiceMemberSnapshot{{Name: "state", Kind: MemberState, Sequence: first, Ops: rootReplacement(first)}}}},
				}}
				if got := log.updates[0]; !reflect.DeepEqual(canon(t, got), canon(t, want)) {
					t.Fatalf("first update = %s\nwant %s", mustMarshal(t, got), mustMarshal(t, want))
				}
			}
			for _, ctx := range log.ctxs {
				if ctx != marker {
					t.Fatal("an update lost its publication context")
				}
			}
			counter.replace(t, marker, count+1)
			if last := log.updates[len(log.updates)-1]; last.Type != UpdateState || last.Sequence != count+1 {
				t.Fatalf("last update = %+v", last)
			}
			_ = provider.Dispose()
		})
	}

	t.Run("does not compact the running delivery when activation overflows reentrantly", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
		counter := newDeliveryCounter(t, 0)
		provideCounter(t, provider, counter)
		log := &updateLog{}
		subscription := subscribeCounter(t, provider, func(ctx context.Context, update ServiceProviderUpdate) {
			log.listen(ctx, update)
			if update.Type == UpdateState && update.Sequence == 1 {
				for value := 3; value <= 103; value++ {
					counter.replace(t, background, value)
				}
			}
		})
		counter.replace(t, background, 1)
		counter.replace(t, background, 2)
		activate(t, subscription)
		if got := log.types(); !reflect.DeepEqual(got, []ServiceProviderUpdateType{UpdateState, UpdateReset, UpdateState}) {
			t.Fatalf("updates = %v", got)
		}
		if log.updates[0].Sequence != 1 || log.updates[2].Sequence != 103 {
			t.Fatalf("updates = %+v", log.updates)
		}
		member := log.updates[1].Reset.Instances[0].Members[0]
		if member.Sequence != 102 || !reflect.DeepEqual(canon(t, member.Ops), canon(t, rootReplacement(102))) {
			t.Fatalf("reset member = %+v", member)
		}
		_ = provider.Dispose()
	})

	t.Run("suppresses notifications already covered by an overflow snapshot", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
		counter := newDeliveryCounter(t, 0)
		counter.state.core.subscribeOps(func(_ context.Context, _ []Op, sequence int) {
			if sequence == 101 {
				counter.replace(t, background, 102)
				counter.replace(t, background, 103)
			}
		})
		provideCounter(t, provider, counter)
		log := &updateLog{}
		subscription := subscribeCounter(t, provider, log.listen)
		for value := 1; value <= 101; value++ {
			counter.replace(t, background, value)
		}
		activate(t, subscription)
		if got := log.types(); !reflect.DeepEqual(got, []ServiceProviderUpdateType{UpdateReset}) {
			t.Fatalf("updates = %v", got)
		}
		if member := log.updates[0].Reset.Instances[0].Members[0]; member.Sequence != 103 {
			t.Fatalf("reset member = %+v", member)
		}
		counter.replace(t, background, 104)
		if got := log.types(); !reflect.DeepEqual(got, []ServiceProviderUpdateType{UpdateReset, UpdateState}) || log.updates[1].Sequence != 104 {
			t.Fatalf("updates = %v", got)
		}
		_ = provider.Dispose()
	})

	t.Run("preserves lifecycle ordering across subscribers during reentrant publication", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
		provideCounter(t, provider, newDeliveryCounter(t, 0))
		first, second := &updateLog{}, &updateLog{}
		activate(t, subscribeCounter(t, provider, func(ctx context.Context, update ServiceProviderUpdate) {
			first.listen(ctx, update)
			if len(first.types()) == 1 {
				if err := Replace[DeliveryCounter](provider, deliveryCounterDefinition, newDeliveryCounter(t, 2)); err != nil {
					t.Error(err)
				}
			}
		}))
		activate(t, subscribeCounter(t, provider, second.listen))
		if err := Replace[DeliveryCounter](provider, deliveryCounterDefinition, newDeliveryCounter(t, 1)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first.types(), second.types()) || len(second.types()) != 2 {
			t.Fatalf("first=%v second=%v", first.types(), second.types())
		}
		if canon(t, first.updates) == nil || !reflect.DeepEqual(canon(t, first.updates), canon(t, second.updates)) {
			t.Fatal("subscribers saw different updates")
		}
		replaced := second.updates[0]
		if replaced.Type != UpdateReplaced || !reflect.DeepEqual(canon(t, replaced.Snapshot.Members[0].Ops), canon(t, rootReplacement(1))) {
			t.Fatalf("first update = %s", mustMarshal(t, replaced))
		}
		_ = provider.Dispose()
	})

	t.Run("close during a callback prevents later buffered callbacks", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
		counter := newDeliveryCounter(t, 0)
		provideCounter(t, provider, counter)
		var received []ServiceProviderUpdate
		var subscription ServiceSubscription
		subscription = subscribeCounter(t, provider, func(_ context.Context, update ServiceProviderUpdate) {
			received = append(received, update)
			if err := subscription.Close(background); err != nil {
				t.Error(err)
			}
		})
		counter.replace(t, background, 1)
		counter.replace(t, background, 2)
		activate(t, subscription)
		if len(received) != 1 {
			t.Fatalf("received %d updates", len(received))
		}
		_ = provider.Dispose()
	})

	t.Run("drains terminal updates when disposed reentrantly", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
		counter := newDeliveryCounter(t, 0)
		provideCounter(t, provider, counter)
		log := &updateLog{}
		subscription := subscribeCounter(t, provider, func(ctx context.Context, update ServiceProviderUpdate) {
			log.listen(ctx, update)
			if update.Type == UpdateState {
				if err := provider.Dispose(); err != nil {
					t.Error(err)
				}
			}
		})
		counter.replace(t, background, 1)
		activate(t, subscription)
		if got := log.types(); !reflect.DeepEqual(got, []ServiceProviderUpdateType{UpdateState, UpdateUnavailable}) {
			t.Fatalf("updates = %v", got)
		}
	})
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// canon is the JSON round trip of a value.
func canon(t *testing.T, value any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal([]byte(mustMarshal(t, value)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// wireTransport sends every update and snapshot of a provider through the wire codecs and their parsers before the consumer sees them.
type wireTransport struct {
	provider       *RemoteServiceProvider
	beforeActivate func()
	log            *updateLog
}

func (transport wireTransport) Invoke(ctx context.Context, call ServiceCall) (json.RawMessage, error) {
	return transport.provider.Invoke(ctx, call)
}

func (transport wireTransport) Subscribe(_ context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	encoder, decoder := CreateServiceStateEncoder(), CreateServiceStateDecoder()
	subscription, err := transport.provider.Subscribe(serviceId, mode, func(ctx context.Context, update ServiceProviderUpdate) {
		wire, err := encoder.EncodeUpdate(update)
		if err != nil {
			panic(err)
		}
		parsed, err := ParseWireServiceProviderUpdate(mustRaw(wire))
		if err != nil {
			panic(err)
		}
		decoded, err := decoder.DecodeUpdate(parsed)
		if err != nil {
			panic(err)
		}
		transport.log.listen(ctx, decoded)
		listener(ctx, decoded)
	})
	if err != nil {
		return nil, err
	}
	wireSnapshot, err := encoder.EncodeSnapshot(subscription.Snapshot())
	if err != nil {
		return nil, err
	}
	parsed, err := ParseWireServiceSubscriptionSnapshot(mustRaw(wireSnapshot))
	if err != nil {
		return nil, err
	}
	snapshot, err := decoder.DecodeSnapshot(parsed)
	if err != nil {
		return nil, err
	}
	return &wireSubscription{inner: subscription, snapshot: snapshot, beforeActivate: transport.beforeActivate}, nil
}

type wireSubscription struct {
	inner          ServiceSubscription
	snapshot       ServiceSubscriptionSnapshot
	beforeActivate func()
}

func (subscription *wireSubscription) Snapshot() ServiceSubscriptionSnapshot {
	return subscription.snapshot
}
func (subscription *wireSubscription) Activate() error {
	subscription.beforeActivate()
	return subscription.inner.Activate()
}
func (subscription *wireSubscription) Close(ctx context.Context) error {
	return subscription.inner.Close(ctx)
}

func replicaValue(t *testing.T, service *RemoteService, member string) (valueDocState, bool) {
	t.Helper()
	replica, err := service.State(member)
	if err != nil {
		t.Fatal(err)
	}
	value, hydrated, err := TypedReplica[valueDocState](replica).Load()
	if err != nil {
		t.Fatal(err)
	}
	return value, hydrated
}

func newWireBinding(t *testing.T, provider *RemoteServiceProvider, serviceIds []string, beforeActivate func(), log *updateLog, errs *locked[error]) *RemoteServiceBinding {
	t.Helper()
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services:  ServiceIDs(serviceIds...),
		Transport: wireTransport{provider: provider, beforeActivate: beforeActivate, log: log},
		OnError:   func(err error) { errs.add(err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestRebaselinesEveryMemberThroughWireCodecsThenResumesContiguousDeltas(t *testing.T) {
	ctx := context.Background()
	provider := newDeliveryProvider(t, SingletonService(deliveryPairDefinition))
	newState := func() *MutableReplicatedState[*valueDocState] {
		state, err := NewReplicatedState(&valueDocState{})
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	pair := &deliveryPairImpl{left: newState(), right: newState()}
	if err := Provide[DeliveryPair](provider, deliveryPairDefinition, pair); err != nil {
		t.Fatal(err)
	}
	set := func(state *MutableReplicatedState[*valueDocState], value int) {
		if err := state.Change(ctx, func(draft *valueDocState) error { draft.Value = value; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	log, errs := &updateLog{}, &locked[error]{}
	binding := newWireBinding(t, provider, []string{deliveryPairDefinition.Id()}, func() {
		for value := 1; value <= 101; value++ {
			set(pair.left, value)
			set(pair.right, value)
		}
	}, log, errs)
	service, err := binding.Use(deliveryPairDefinition.Id())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if got := log.types(); !reflect.DeepEqual(got, []ServiceProviderUpdateType{UpdateReset, UpdateState}) {
		t.Fatalf("updates = %v", got)
	}
	for _, member := range []string{"left", "right"} {
		if value, hydrated := replicaValue(t, service, member); !hydrated || value.Value != 101 {
			t.Fatalf("%s = %+v hydrated=%v", member, value, hydrated)
		}
	}
	for value := 102; value <= 104; value++ {
		set(pair.left, value)
		set(pair.right, value)
	}
	for _, member := range []string{"left", "right"} {
		if value, _ := replicaValue(t, service, member); value.Value != 104 {
			t.Fatalf("%s = %+v", member, value)
		}
	}
	if got := errs.get(); len(got) != 0 {
		t.Fatalf("errors = %v", got)
	}
	_ = binding.Dispose(ctx)
	_ = provider.Dispose()
}

func TestAnOverflowResetCanMakeASingletonUnavailableBeforeALaterReplacement(t *testing.T) {
	ctx := context.Background()
	provider := newDeliveryProvider(t, SingletonService(deliveryCounterDefinition))
	provideCounter(t, provider, newDeliveryCounter(t, 0))
	log, errs := &updateLog{}, &locked[error]{}
	binding := newWireBinding(t, provider, []string{deliveryCounterDefinition.Id()}, func() {
		for value := 1; value <= 100; value++ {
			if err := Replace[DeliveryCounter](provider, deliveryCounterDefinition, newDeliveryCounter(t, value)); err != nil {
				t.Error(err)
			}
		}
		if err := Withdraw[DeliveryCounter](provider, deliveryCounterDefinition); err != nil {
			t.Error(err)
		}
	}, log, errs)
	service, err := binding.Use(deliveryCounterDefinition.Id())
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	want := []ServiceProviderUpdate{{Type: UpdateReset, Reset: &ServiceSubscriptionSnapshot{ServiceId: deliveryCounterDefinition.Id(), Mode: ServiceSingleton, Instances: []ServiceInstanceSnapshot{}}}}
	if !reflect.DeepEqual(canon(t, log.updates), canon(t, want)) {
		t.Fatalf("updates = %s", mustMarshal(t, log.updates))
	}
	if _, hydrated := replicaValue(t, service, "state"); hydrated {
		t.Fatal("state is hydrated after the singleton became unavailable")
	}
	replacement := newDeliveryCounter(t, 200)
	if err := Replace[DeliveryCounter](provider, deliveryCounterDefinition, replacement); err != nil {
		t.Fatal(err)
	}
	replacement.set(t, 201)
	if value, hydrated := replicaValue(t, service, "state"); !hydrated || value.Value != 201 {
		t.Fatalf("state = %+v hydrated=%v", value, hydrated)
	}
	if got := errs.get(); len(got) != 0 {
		t.Fatalf("errors = %v", got)
	}
	_ = binding.Dispose(ctx)
	_ = provider.Dispose()
}

func TestKeyedResetsRetainLiveGenerationsAndReconcileClosedAndReusedKeys(t *testing.T) {
	ctx := context.Background()
	provider := newDeliveryProvider(t, KeyedService(deliveryCounterDefinition))
	spawn := func(key string, counter *deliveryCounterImpl) func() error {
		closeInstance, err := Spawn[DeliveryCounter](provider, deliveryCounterDefinition, key, counter)
		if err != nil {
			t.Fatal(err)
		}
		return closeInstance
	}
	retained := newDeliveryCounter(t, 0)
	spawn("retained", retained)
	closeOld := spawn("reused", newDeliveryCounter(t, 0))
	removed := spawn("removed", newDeliveryCounter(t, 0))
	log, errs := &updateLog{}, &locked[error]{}
	binding := newWireBinding(t, provider, []string{deliveryCounterDefinition.Id()}, func() {}, log, errs)
	type observation struct {
		service *RemoteService
		ctx     context.Context
	}
	observed := &locked[observation]{}
	if _, err := binding.Observe(deliveryCounterDefinition.Id(), func(ctx context.Context, service *RemoteService) error {
		observed.add(observation{service, ctx})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(observed.get()); got != 3 {
		t.Fatalf("observed %d instances", got)
	}
	stable, old := observed.get()[1], observed.get()[2] // Provider snapshots are sorted by key.
	replacement := newDeliveryCounter(t, 500)
	replica, err := stable.service.State("state")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TypedReplica[valueDocState](replica).Subscribe(func(value valueDocState, _ context.Context, _ ReplicatedStateDelivery) {
		if value.Value != 1 {
			return
		}
		_ = closeOld()
		_ = removed()
		spawn("reused", replacement)
		retained.replace(t, ctx, 102)
		for next := 501; next <= 601; next++ {
			replacement.replace(t, ctx, next)
		}
	}); err != nil {
		t.Fatal(err)
	}
	retained.replace(t, ctx, 1)
	resets := 0
	for _, kind := range log.types() {
		if kind == UpdateReset {
			resets++
		}
	}
	if resets == 0 {
		t.Fatalf("no reset update: %v", log.types())
	}
	all := observed.get()
	if len(all) != 4 {
		t.Fatalf("observed %d instances", len(all))
	}
	if stable.ctx.Err() != nil {
		t.Fatal("a live generation was aborted by the reset")
	}
	if value, _ := replicaValue(t, stable.service, "state"); value.Value != 102 {
		t.Fatalf("stable = %+v", value)
	}
	if old.ctx.Err() == nil || all[0].ctx.Err() == nil {
		t.Fatal("closed instances were not aborted")
	}
	if value, _ := replicaValue(t, all[3].service, "state"); value.Value != 601 {
		t.Fatalf("new generation = %+v", value)
	}
	replacement.set(t, 602)
	if value, _ := replicaValue(t, all[3].service, "state"); value.Value != 602 {
		t.Fatalf("new generation = %+v", value)
	}
	if got := errs.get(); len(got) != 0 {
		t.Fatalf("errors = %v", got)
	}
	_ = binding.Dispose(ctx)
	_ = provider.Dispose()
}
