package chord

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// upstream: packages/chord/src/facets/host.ts:562-573; packages/chord/src/services/handle.ts:26-35.
func TestFacetServiceFollowsLocalCutover(t *testing.T) {
	t.Parallel()
	type value struct{ generation string }
	def := DefineServiceWithOptions[*value]("test.facet-dynamic.local", ServiceOptions{Local: true})
	provider := func(generation string) Facet {
		return Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
			return ProvideService(env, def, &value{generation})
		}}
	}
	var retained *FacetService
	consumer := Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		var err error
		retained, err = UseFacetService(env, def.Id(), true)
		if err != nil {
			return err
		}
		if _, err := retained.Resolve(); err == nil {
			return errors.New("service access succeeded during setup")
		}
		return nil
	}}
	host, err := CreateFacetHost(t.Context(), FacetOptions{Facets: []Facet{consumer, provider("A")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, generation := range []string{"A", "B"} {
		if generation == "B" {
			if err := host.Reload(t.Context(), []Facet{provider(generation)}); err != nil {
				t.Fatal(err)
			}
		}
		implementation, err := retained.Resolve()
		if err != nil || implementation.(*value).generation != generation {
			t.Fatalf("resolve = %#v, %v; want %s", implementation, err, generation)
		}
	}
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := retained.Resolve(); err == nil {
		t.Fatal("retained service survived disposal")
	}
}

// upstream: packages/chord/src/facets/host.ts:710-733. Runtime-only consumers use the actual loopback facade and leave unrelated typed adapter configuration untouched.
func TestFacetDynamicConsumerUsesLoopback(t *testing.T) {
	t.Parallel()
	def := DefineService[*FacetServiceImplementation]("test.facet-dynamic.loopback")
	state, err := NewFacetState(map[string]any{"generation": "A"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	provider := Facet{Id: "provider", Setup: func(env *FacetEnvironment) error {
		return ProvideService(env, def, &FacetServiceImplementation{Members: []FacetServiceMember{{Name: "state", State: state}, {
			Name: "read", Invoke: func(context.Context, []json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`"A"`), nil },
		}}})
	}}
	var service *FacetService
	consumer := Facet{Id: "consumer", Setup: func(env *FacetEnvironment) error {
		var err error
		service, err = UseFacetService(env, def.Id(), false)
		return err
	}}
	options := map[string]func(*RemoteService) any{"other": func(service *RemoteService) any { return service }}
	host, err := CreateFacetHost(t.Context(), FacetOptions{Facets: []Facet{consumer, provider}, RemoteClients: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, leaked := options[def.Id()]; leaked {
		t.Fatal("dynamic adapter changed caller-owned options")
	}
	resolved, err := service.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	remote, ok := resolved.(*RemoteService)
	if !ok {
		t.Fatalf("resolved service = %T, want actual loopback facade", resolved)
	}
	value, err := remote.Call(t.Context(), "read")
	if err != nil || string(value) != `"A"` {
		t.Fatalf("read = %s, %v", value, err)
	}
	replica, err := remote.State("state")
	if err != nil {
		t.Fatal(err)
	}
	initial, hydrated, err := replica.Load()
	if err != nil || !hydrated || !reflect.DeepEqual(initial, map[string]any{"generation": "A"}) {
		t.Fatalf("state = %#v, hydrated=%v, error=%v", initial, hydrated, err)
	}
	if err := state.Apply(t.Context(), 1, []Op{{"r", map[string]any{"generation": "B"}}}); err != nil {
		t.Fatal(err)
	}
	updated, hydrated, err := replica.Load()
	if err != nil || !hydrated || !reflect.DeepEqual(updated, map[string]any{"generation": "B"}) {
		t.Fatalf("updated state = %#v, hydrated=%v, error=%v", updated, hydrated, err)
	}
}

// upstream: packages/chord/src/services/provider.ts:574 uses JavaScript's UTF-16 Object.keys(...).sort().
func TestFacetMemberSortUsesUTF16(t *testing.T) {
	t.Parallel()
	invoke := func(context.Context, []json.RawMessage) (json.RawMessage, error) { return nil, nil }
	classified, err := classifyFacetImplementation("unicode", &FacetServiceImplementation{Members: []FacetServiceMember{
		{Name: "\ue000", Invoke: invoke}, {Name: "😀", Invoke: invoke}, {Name: "a", Invoke: invoke},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "😀", "\ue000"}; !reflect.DeepEqual(classified.names, want) {
		t.Fatalf("member order = %q, want %q", classified.names, want)
	}
}

// upstream: packages/chord/src/services/provider.ts:209-236,566-593 and services/state.ts:87-126.
func TestFacetRuntimeMembersUseExistingProvider(t *testing.T) {
	t.Parallel()
	def := DefineService[*FacetServiceImplementation]("test.facet-dynamic.remote")
	state, err := NewFacetState(map[string]any{"count": float64(7)}, 3)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("remote callback failed")
	implementation := &FacetServiceImplementation{Members: []FacetServiceMember{
		{Name: "state", State: state},
		{Name: "echo", Invoke: func(ctx context.Context, args []json.RawMessage) (json.RawMessage, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(args) != 1 {
				return nil, failure
			}
			return args[0], nil
		}},
		{Name: "void", Invoke: func(context.Context, []json.RawMessage) (json.RawMessage, error) { return nil, nil }},
	}}
	provider, err := NewRemoteServiceProvider(SingletonService(def))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Dispose(); err != nil {
			t.Error(err)
		}
	})
	if err := Provide(provider, def, implementation); err != nil {
		t.Fatal(err)
	}
	var updates []ServiceProviderUpdate
	subscription, err := provider.Subscribe(def.Id(), ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) { updates = append(updates, update) })
	if err != nil {
		t.Fatal(err)
	}
	want := ServiceSubscriptionSnapshot{ServiceId: def.Id(), Mode: ServiceSingleton, Instances: []ServiceInstanceSnapshot{{Members: []ServiceMemberSnapshot{
		{Name: "echo", Kind: MemberMethod},
		{Name: "state", Kind: MemberState, Sequence: 3, Ops: []Op{{"r", map[string]any{"count": float64(7)}}}},
		{Name: "void", Kind: MemberMethod},
	}}}}
	if got := subscription.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
	ops := []Op{{"r", map[string]any{"count": float64(9)}}}
	if err := state.Apply(t.Context(), 4, ops); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updates, []ServiceProviderUpdate{{Type: UpdateState, Member: "state", Sequence: 4, Ops: ops}}) {
		t.Fatalf("updates = %#v", updates)
	}
	if err := state.Apply(t.Context(), 6, ops); err == nil || !strings.Contains(err.Error(), "gap") {
		t.Fatalf("gap error = %v", err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`{"name":"Armin"}`)} {
		got, err := provider.Invoke(t.Context(), ServiceCall{ServiceId: def.Id(), Member: "echo", Args: []json.RawMessage{raw}})
		if err != nil || string(got) != string(raw) {
			t.Fatalf("echo = %s, %v; want %s", got, err, raw)
		}
	}
	got, err := provider.Invoke(t.Context(), ServiceCall{ServiceId: def.Id(), Member: "void"})
	if err != nil || got != nil {
		t.Fatalf("void = %s, %v; want absent result", got, err)
	}
	_, err = provider.Invoke(t.Context(), ServiceCall{ServiceId: def.Id(), Member: "echo"})
	if !errors.Is(err, failure) {
		t.Fatalf("callback error = %v, want %v", err, failure)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = provider.Invoke(cancelled, ServiceCall{ServiceId: def.Id(), Member: "echo", Args: []json.RawMessage{json.RawMessage(`1`)}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if err := Replace(provider, def, &FacetServiceImplementation{Members: implementation.Members[:1]}); err == nil {
		t.Fatal("replacement changed the remote member shape")
	}
}
