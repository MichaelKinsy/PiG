package chord

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

type counterState struct {
	Count int      `json:"count"`
	Log   []string `json:"log"`
}

type Counter interface {
	State() pico3.ReplicatedStateOf[*counterState]
	Add(ctx context.Context, amount int, label string) (int, error)
	Fail(ctx context.Context) error
}

var counterDefinition = pico3.DefineService[Counter]("test.counter")

type counterImpl struct {
	state *MutableReplicatedState[*counterState]
}

func (counter *counterImpl) State() pico3.ReplicatedStateOf[*counterState] { return counter.state }

func (counter *counterImpl) Add(ctx context.Context, amount int, label string) (int, error) {
	var result int
	err := counter.state.Change(ctx, func(draft *counterState) error {
		draft.Count += amount
		draft.Log = append(draft.Log, label)
		result = draft.Count
		return nil
	})
	return result, err
}

func (counter *counterImpl) Fail(context.Context) error { return errors.New("counter failure") }

func newCounter(t *testing.T) *counterImpl {
	t.Helper()
	state, err := NewReplicatedState(&counterState{Log: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return &counterImpl{state: state}
}

type delivery struct {
	Kind     string
	Sequence int
	Value    counterState
}

type recorder struct {
	mu         sync.Mutex
	deliveries []delivery
	changed    chan struct{}
}

func newRecorder() *recorder { return &recorder{changed: make(chan struct{}, 64)} }

func (rec *recorder) listen(value *counterState, _ context.Context, info pico3.ReplicatedStateDelivery) {
	rec.mu.Lock()
	rec.deliveries = append(rec.deliveries, delivery{Kind: info.Kind, Sequence: info.Sequence, Value: *value})
	rec.mu.Unlock()
	rec.changed <- struct{}{}
}

func (rec *recorder) snapshot() []delivery {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]delivery(nil), rec.deliveries...)
}

func (rec *recorder) waitFor(t *testing.T, count int) []delivery {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if got := rec.snapshot(); len(got) >= count {
			return got
		}
		select {
		case <-rec.changed:
		case <-deadline:
			t.Fatalf("timed out waiting for %d deliveries; got %+v", count, rec.snapshot())
		}
	}
}

// remoteFixture wires the REAL provider -> endpoint -> JSON-copy transport ->
// binding path.
type remoteFixture struct {
	provider *RemoteServiceProvider
	endpoint RemoteServiceEndpoint
	binding  *RemoteServiceBinding
	errs     chan error
}

func newRemoteFixture(t *testing.T, definitions ...ServiceProviderDefinition) *remoteFixture {
	t.Helper()
	provider, err := NewRemoteServiceProvider(definitions...)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := CreateRemoteServiceEndpoint(provider)
	errs := make(chan error, 64)
	ids := make([]string, len(definitions))
	for index, definition := range definitions {
		ids[index] = definition.ServiceId
	}
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services:  ids,
		Transport: NewJSONCopyTransport(endpoint),
		OnError:   func(err error) { errs <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &remoteFixture{provider: provider, endpoint: endpoint, binding: binding, errs: errs}
}

func TestReplicatedStateHydrateThenOrderedUpdates(t *testing.T) {
	counter := newCounter(t)
	rec := newRecorder()
	unsubscribe, err := counter.state.Subscribe(rec.listen)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := counter.Add(ctx, 2, "a"); err != nil {
		t.Fatal(err)
	}
	// An unchanged draft publishes nothing.
	if err := counter.state.Change(ctx, func(*counterState) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// A failing mutate discards the draft.
	if err := counter.state.Change(ctx, func(draft *counterState) error {
		draft.Count = 99
		return errors.New("discard")
	}); err == nil {
		t.Fatal("mutate error was not returned")
	}
	if err := counter.state.Replace(ctx, &counterState{Count: 7, Log: []string{"r"}}); err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	if _, err := counter.Add(ctx, 1, "after"); err != nil {
		t.Fatal(err)
	}
	want := []delivery{
		{DeliveryHydrate, 0, counterState{Log: []string{}}},
		{DeliveryUpdate, 1, counterState{Count: 2, Log: []string{"a"}}},
		{DeliveryUpdate, 2, counterState{Count: 7, Log: []string{"r"}}},
	}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %+v, want %+v", got, want)
	}
	// Retained values are detached from later publications.
	value := counter.state.Value()
	value.Count = -1
	if counter.state.Value().Count != 8 {
		t.Fatalf("stored state was mutated through Value: %+v", counter.state.Value())
	}
}

func TestReplicatedStateRejectsNonObjectJSON(t *testing.T) {
	if _, err := NewReplicatedState(3); err == nil {
		t.Fatal("scalar state was accepted")
	}
	if _, err := NewReplicatedState(map[string]any{"bad": func() {}}); err == nil {
		t.Fatal("non-JSON state was accepted")
	}
}

func TestSingletonReplicaHydratesThenReceivesOperationStream(t *testing.T) {
	ctx := context.Background()
	fixture := newRemoteFixture(t, SingletonService(counterDefinition))
	counter := newCounter(t)
	if _, err := counter.Add(ctx, 1, "before-subscribe"); err != nil {
		t.Fatal(err)
	}
	if err := Provide[Counter](fixture.provider, counterDefinition, counter); err != nil {
		t.Fatal(err)
	}
	service, err := UseRemote(fixture.binding, counterDefinition)
	if err != nil {
		t.Fatal(err)
	}
	replica, err := service.State("state")
	if err != nil {
		t.Fatal(err)
	}
	if _, hydrated := replica.Value(); hydrated {
		t.Fatal("replica hydrated before the subscription snapshot")
	}
	if err := fixture.binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	if _, err := TypedReplica[*counterState](replica).Subscribe(rec.listen); err != nil {
		t.Fatal(err)
	}
	raw, err := service.Call(ctx, "add", 3, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "4" {
		t.Fatalf("add result = %s", raw)
	}
	got := rec.waitFor(t, 2)
	want := []delivery{
		{DeliveryHydrate, 1, counterState{Count: 1, Log: []string{"before-subscribe"}}},
		{DeliveryUpdate, 2, counterState{Count: 4, Log: []string{"before-subscribe", "remote"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deliveries = %+v, want %+v", got, want)
	}
	// A late subscriber receives the retained value as hydrate, not update.
	late := newRecorder()
	if _, err := TypedReplica[*counterState](replica).Subscribe(late.listen); err != nil {
		t.Fatal(err)
	}
	if got := late.snapshot(); len(got) != 1 || got[0].Kind != DeliveryHydrate || got[0].Sequence != 2 {
		t.Fatalf("late subscriber = %+v", got)
	}
	// Method errors propagate to the caller.
	if _, err := service.Call(ctx, "fail"); err == nil || err.Error() != "counter failure" {
		t.Fatalf("fail error = %v", err)
	}
	// Kind mismatches are typed errors.
	if _, err := service.Call(ctx, "state"); !IsRemoteServiceErrorCode(err, ErrServiceMemberMismatch) {
		t.Fatalf("calling a state member: %v", err)
	}
	if _, err := service.Call(ctx, "missing"); !IsRemoteServiceErrorCode(err, ErrServiceMemberNotFound) {
		t.Fatalf("calling a missing member: %v", err)
	}
	if err := fixture.binding.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Call(ctx, "add", 1, "x"); err == nil {
		t.Fatal("call after dispose succeeded")
	}
	select {
	case err := <-fixture.errs:
		t.Fatalf("unexpected binding error: %v", err)
	default:
	}
}

func TestProviderClassificationUsesContractMethodSet(t *testing.T) {
	counter := newCounter(t)
	classified, err := classifyImplementation[Counter]("test.counter", counter)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"add", "fail", "state"}; !reflect.DeepEqual(classified.names, want) {
		t.Fatalf("members = %v, want %v", classified.names, want)
	}
	type bad interface{ Nope(int) string }
	if _, err := classifyImplementation[bad]("test.bad", badImpl{}); err == nil {
		t.Fatal("non-remote member accepted")
	}
}

type badImpl struct{}

func (badImpl) Nope(int) string { return "" }

func TestWireUpdateRoundTrip(t *testing.T) {
	updates := []ServiceProviderUpdate{
		{Type: UpdateState, Member: "state", Sequence: 0, Ops: []pico3.Op{{"r", map[string]any{"a": 1.0}}}},
		{Type: UpdateState, Address: &ServiceInstanceAddress{Key: "k", Generation: 2}, Member: "state", Sequence: 3, Ops: []pico3.Op{{"s", []any{"a"}, 2.0}}},
		{Type: UpdateUnavailable},
		{Type: UpdateReplaced, Snapshot: &ServiceInstanceSnapshot{Members: []ServiceMemberSnapshot{{Name: "m", Kind: MemberMethod}}}},
		{Type: UpdateSpawned, Snapshot: &ServiceInstanceSnapshot{Instance: &ServiceInstanceAddress{Key: "k", Generation: 1}, Members: []ServiceMemberSnapshot{{Name: "s", Kind: MemberState, Sequence: 0, Ops: []pico3.Op{{"r", []any{}}}}}}},
		{Type: UpdateClosed, Address: &ServiceInstanceAddress{Key: "k", Generation: 1}},
	}
	wantJSON := []string{
		`{"type":"state","member":"state","sequence":0,"ops":[["r",{"a":1}]]}`,
		`{"type":"state","instance":{"key":"k","generation":2},"member":"state","sequence":3,"ops":[["s",["a"],2]]}`,
		`{"type":"unavailable"}`,
		`{"type":"replaced","snapshot":{"members":[{"name":"m","kind":"method"}]}}`,
		`{"type":"spawned","instance":{"instance":{"key":"k","generation":1},"members":[{"name":"s","kind":"state","sequence":0,"ops":[["r",[]]]}]}}`,
		`{"type":"closed","instance":{"key":"k","generation":1}}`,
	}
	for index, update := range updates {
		encoded, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != wantJSON[index] {
			t.Errorf("update %d = %s, want %s", index, encoded, wantJSON[index])
		}
		var decoded ServiceProviderUpdate
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded, update) {
			t.Errorf("update %d round trip = %+v, want %+v", index, decoded, update)
		}
	}
}

// counterView is the guarded service view a service lane registers for its
// contract (see RegisterServiceView).
type counterView struct {
	resolve func() (Counter, error)
}

func (view counterView) State() pico3.ReplicatedStateOf[*counterState] {
	return StateView(func() (pico3.ReplicatedStateOf[*counterState], error) {
		target, err := view.resolve()
		if err != nil {
			return nil, err
		}
		return target.State(), nil
	})
}

func (view counterView) Add(ctx context.Context, amount int, label string) (int, error) {
	target, err := view.resolve()
	if err != nil {
		return 0, err
	}
	return target.Add(ctx, amount, label)
}

func (view counterView) Fail(ctx context.Context) error {
	target, err := view.resolve()
	if err != nil {
		return err
	}
	return target.Fail(ctx)
}

func init() {
	newView := func(resolve func() (Counter, error)) Counter { return counterView{resolve: resolve} }
	RegisterServiceView(counterDefinition, newView)
	RegisterServiceView(keyedCounterDefinition, newView)
	RegisterRemoteClient(counterDefinition, func(service *RemoteService) Counter { return remoteCounter{service} })
}
