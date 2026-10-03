package chord

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Ports packages/chord/test/services.test.ts. TypeScript compile-time contract checks (case 1) and object-identity assertions on values have no Go form: remote contracts are checked by classification at provide time, and typed state hands out detached copies, so identity is asserted on the stored JSON revisions through core.snapshot.

type modelRef struct {
	Provider string `json:"provider"`
	ModelId  string `json:"modelId"`
}

type modelsState struct {
	Selected *modelRef `json:"selected"`
	Revision int       `json:"revision"`
}

type Models interface {
	State() ReplicatedStateOf[*modelsState]
	Select(ctx context.Context, model modelRef) error
}

var modelsDefinition = DefineService[Models]("test.models")

type modelsImpl struct {
	state    *MutableReplicatedState[*modelsState]
	selected func(ctx context.Context, model modelRef) error
}

func (models *modelsImpl) State() ReplicatedStateOf[*modelsState] { return models.state }
func (models *modelsImpl) Select(ctx context.Context, model modelRef) error {
	if models.selected == nil {
		return nil
	}
	return models.selected(ctx, model)
}

func newModels(t *testing.T, revision int) *modelsImpl {
	t.Helper()
	state, err := NewReplicatedState(&modelsState{Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	return &modelsImpl{state: state}
}

func (models *modelsImpl) setRevision(t *testing.T, revision int) {
	t.Helper()
	if err := models.state.Change(context.Background(), func(draft *modelsState) error { draft.Revision = revision; return nil }); err != nil {
		t.Fatal(err)
	}
}

type Question struct {
	Question string `json:"question"`
}

type QuestionDialogs interface {
	Request() ReplicatedStateOf[*Question]
	Submit(ctx context.Context, answer string) (submitResult, error)
}

type submitResult struct {
	Accepted bool `json:"accepted"`
}

var questionDialogsDefinition = DefineService[QuestionDialogs]("test.question-dialog")

type questionDialogsImpl struct {
	request *MutableReplicatedState[*Question]
	submit  func(ctx context.Context, answer string) (submitResult, error)
}

func (dialogs *questionDialogsImpl) Request() ReplicatedStateOf[*Question] { return dialogs.request }
func (dialogs *questionDialogsImpl) Submit(ctx context.Context, answer string) (submitResult, error) {
	return dialogs.submit(ctx, answer)
}

func newQuestion(t *testing.T, text string, submit func(context.Context, string) (submitResult, error)) *questionDialogsImpl {
	t.Helper()
	state, err := NewReplicatedState(&Question{Question: text})
	if err != nil {
		t.Fatal(err)
	}
	if submit == nil {
		submit = func(context.Context, string) (submitResult, error) { return submitResult{Accepted: true}, nil }
	}
	return &questionDialogsImpl{request: state, submit: submit}
}

type Timeline interface {
	State() ReplicatedStateOf[*timelineState]
}

type timelineState struct {
	Entries  []map[string]any `json:"entries"`
	Retained map[string]any   `json:"retained"`
}

var timelineDefinition = DefineService[Timeline]("test.timeline")

type timelineImpl struct {
	state ReplicatedStateOf[*timelineState]
}

func (timeline *timelineImpl) State() ReplicatedStateOf[*timelineState] { return timeline.state }

type Echo interface {
	Echo(ctx context.Context, payload map[string]any) (map[string]any, error)
}

var echoDefinition = DefineService[Echo]("test.echo")

type echoImpl func(ctx context.Context, payload map[string]any) (map[string]any, error)

func (echo echoImpl) Echo(ctx context.Context, payload map[string]any) (map[string]any, error) {
	return echo(ctx, payload)
}

func modelsReplica(t *testing.T, service *RemoteService) ReplicaOf[*modelsState] {
	t.Helper()
	replica, err := service.State("state")
	if err != nil {
		t.Fatal(err)
	}
	return TypedReplica[*modelsState](replica)
}

func revisionOf(t *testing.T, replica ReplicaOf[*modelsState]) (int, bool) {
	t.Helper()
	value, hydrated, err := replica.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !hydrated {
		return 0, false
	}
	return value.Revision, true
}

func loopbackBinding(t *testing.T, provider *RemoteServiceProvider, options RemoteServiceBindingOptions) *RemoteServiceBinding {
	t.Helper()
	if options.Transport == nil {
		options.Transport = NewLoopbackTransport(provider)
	}
	binding, err := CreateRemoteServiceBinding(options)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func provideModels(t *testing.T, provider *RemoteServiceProvider, models *modelsImpl) {
	t.Helper()
	if err := Provide[Models](provider, modelsDefinition, models); err != nil {
		t.Fatal(err)
	}
}

func use(t *testing.T, binding *RemoteServiceBinding, id string) *RemoteService {
	t.Helper()
	service, err := binding.Use(id)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func selectModel(ctx context.Context, service *RemoteService, model modelRef) error {
	_, err := service.Call(ctx, "select", model)
	return err
}

func TestRemoteServices(t *testing.T) {
	ctx := context.Background()

	t.Run("checks remote JSON contracts only at compile time", func(t *testing.T) {
		// services.test.ts:69-81 asserts TypeScript type errors. Go keeps the observable half: a remote token is not local, an explicit local token is, and non-JSON members are rejected when the service is provided.
		if DefineService[Echo]("test.json-passthrough").Local() {
			t.Fatal("a remote service token is local")
		}
		if !DefineServiceWithOptions[Echo]("test.local-non-json", ServiceOptions{Local: true}).Local() {
			t.Fatal("a local service token is remote")
		}
	})

	t.Run("marks services remotable by default and reserves Chord service IDs", func(t *testing.T) {
		local := DefineServiceWithOptions[Echo]("test.local", ServiceOptions{Local: true})
		if modelsDefinition.Local() || !local.Local() {
			t.Fatal("service locality")
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != "Service IDs beginning with $chord. are reserved" {
					t.Fatalf("recovered = %v", recovered)
				}
			}()
			DefineServiceWithOptions[Echo]("$chord.internal", ServiceOptions{Local: true})
		}()
		if _, err := NewRemoteServiceProvider(SingletonService(local)); err == nil || !strings.Contains(err.Error(), "cannot be published remotely") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("tracks mutable source state while publishing immutable revisions", func(t *testing.T) {
		state, err := NewReplicatedState(&modelsState{})
		if err != nil {
			t.Fatal(err)
		}
		var delivered []modelsState
		var kinds []string
		stop, err := state.Subscribe(func(value *modelsState, _ context.Context, delivery ReplicatedStateDelivery) {
			delivered = append(delivered, *value)
			kinds = append(kinds, delivery.Kind)
		})
		if err != nil {
			t.Fatal(err)
		}
		initial := stored2(state)
		if err := state.Change(ctx, func(draft *modelsState) error {
			draft.Selected = &modelRef{Provider: "test", ModelId: "one"}
			draft.Revision = 1
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		want := modelsState{Selected: &modelRef{Provider: "test", ModelId: "one"}, Revision: 1}
		if got := state.Value(); !reflect.DeepEqual(*got, want) || container(stored2(state)) == container(initial) {
			t.Fatalf("value = %+v", got)
		}
		if !reflect.DeepEqual(delivered, []modelsState{{Revision: 0}, want}) || !reflect.DeepEqual(kinds, []string{DeliveryHydrate, DeliveryUpdate}) {
			t.Fatalf("delivered = %+v kinds = %v", delivered, kinds)
		}
		stop()
	})

	t.Run("does not publish when a transaction restores the prior value", func(t *testing.T) {
		state, err := NewReplicatedState(map[string]any{"value": 1.0})
		if err != nil {
			t.Fatal(err)
		}
		var deliveries []ReplicatedStateDelivery
		if _, err := state.Subscribe(func(_ map[string]any, _ context.Context, delivery ReplicatedStateDelivery) {
			deliveries = append(deliveries, delivery)
		}); err != nil {
			t.Fatal(err)
		}
		if err := state.Change(ctx, func(draft map[string]any) error { draft["value"] = 2.0; draft["value"] = 1.0; return nil }); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.Value(), map[string]any{"value": 1.0}) || !reflect.DeepEqual(deliveries, []ReplicatedStateDelivery{{Kind: DeliveryHydrate, Sequence: 0}}) {
			t.Fatalf("deliveries = %v", deliveries)
		}
	})

	t.Run("hydrates a new subscriber from the latest atomic revision", func(t *testing.T) {
		state, err := NewReplicatedState(map[string]any{"entries": []any{map[string]any{"id": "one"}}})
		if err != nil {
			t.Fatal(err)
		}
		var first, second []any
		if _, err := state.Subscribe(func(value map[string]any, _ context.Context, _ ReplicatedStateDelivery) {
			first = append(first, canon(t, value))
		}); err != nil {
			t.Fatal(err)
		}
		if err := state.Change(ctx, func(draft map[string]any) error {
			draft["entries"] = append(draft["entries"].([]any), map[string]any{"id": "two"})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := state.Subscribe(func(value map[string]any, _ context.Context, _ ReplicatedStateDelivery) {
			second = append(second, canon(t, value))
		}); err != nil {
			t.Fatal(err)
		}
		one, two := map[string]any{"id": "one"}, map[string]any{"id": "two"}
		if !reflect.DeepEqual(first, []any{map[string]any{"entries": []any{one}}, map[string]any{"entries": []any{one, two}}}) || !reflect.DeepEqual(second, []any{map[string]any{"entries": []any{one, two}}}) {
			t.Fatalf("first = %v second = %v", first, second)
		}
	})

	t.Run("does not defensively clone method arguments or results", func(t *testing.T) {
		// services.test.ts:145-168 asserts object identity across the loopback. Go decodes arguments and encodes results at the method boundary, so the observable contract is value equality and a successful round trip.
		provider := newDeliveryProvider(t, SingletonService(echoDefinition))
		var received map[string]any
		if err := Provide[Echo](provider, echoDefinition, echoImpl(func(_ context.Context, payload map[string]any) (map[string]any, error) {
			received = payload
			return map[string]any{"value": "response"}, nil
		})); err != nil {
			t.Fatal(err)
		}
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{echoDefinition.Id()}})
		echo := use(t, binding, echoDefinition.Id())
		if err := binding.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		response, err := CallResult[map[string]any](ctx, echo, "echo", map[string]any{"value": "request"})
		if err != nil || !reflect.DeepEqual(response, map[string]any{"value": "response"}) || !reflect.DeepEqual(received, map[string]any{"value": "request"}) {
			t.Fatalf("response = %v received = %v err = %v", response, received, err)
		}
		_ = binding.Dispose(ctx)
		_ = provider.Dispose()
	})

	t.Run("provides and consumes one singleton with replicated state", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		if got := provider.Catalogue(); !reflect.DeepEqual(got, []ServiceCatalogueEntry{{ServiceId: modelsDefinition.Id(), Mode: ServiceSingleton}}) {
			t.Fatalf("catalogue = %v", got)
		}
		models := newModels(t, 0)
		models.selected = func(ctx context.Context, model modelRef) error {
			return models.state.Change(ctx, func(draft *modelsState) error {
				draft.Selected = &model
				draft.Revision++
				return nil
			})
		}
		provideModels(t, provider, models)
		errs := &locked[error]{}
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id()}, OnError: func(err error) { errs.add(err) }})
		first, second := use(t, binding, modelsDefinition.Id()), use(t, binding, modelsDefinition.Id())
		if first.facade != second.facade {
			t.Fatal("a service has two facades")
		}
		if _, hydrated := revisionOf(t, modelsReplica(t, first)); hydrated {
			t.Fatal("state is hydrated before ready")
		}
		if err := binding.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		if revision, hydrated := revisionOf(t, modelsReplica(t, first)); !hydrated || revision != 0 {
			t.Fatalf("revision = %d", revision)
		}
		var updates []modelsState
		stop, err := modelsReplica(t, second).Subscribe(func(value *modelsState, _ context.Context, _ ReplicatedStateDelivery) {
			updates = append(updates, *value)
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := selectModel(ctx, first, modelRef{"test", "one"}); err != nil {
			t.Fatal(err)
		}
		want := modelsState{Selected: &modelRef{"test", "one"}, Revision: 1}
		if value, _, _ := modelsReplica(t, first).Load(); !reflect.DeepEqual(*value, want) {
			t.Fatalf("value = %+v", value)
		}
		if !reflect.DeepEqual(updates, []modelsState{{Revision: 0}, want}) || len(errs.get()) != 0 {
			t.Fatalf("updates = %+v errors = %v", updates, errs.get())
		}
		late := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id()}})
		lateModels := use(t, late, modelsDefinition.Id())
		if err := late.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		if revision, _ := revisionOf(t, modelsReplica(t, lateModels)); revision != 1 {
			t.Fatalf("late revision = %d", revision)
		}
		stop()
		_ = binding.Dispose(ctx)
		_ = late.Dispose(ctx)
		_ = provider.Dispose()
	})

	t.Run("publishes compact tracked operations through the remote provider", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(timelineDefinition))
		source := newDocState(t, docState{"entries": []any{map[string]any{"id": "one"}}, "retained": map[string]any{"value": 1.0}})
		typed, err := NewReplicatedState(&timelineState{Entries: []map[string]any{{"id": "one"}}, Retained: map[string]any{"value": 1.0}})
		if err != nil {
			t.Fatal(err)
		}
		_ = source
		if err := Provide[Timeline](provider, timelineDefinition, &timelineImpl{state: typed}); err != nil {
			t.Fatal(err)
		}
		log := &updateLog{}
		raw, err := provider.Subscribe(timelineDefinition.Id(), ServiceSingleton, log.listen)
		if err != nil {
			t.Fatal(err)
		}
		members := raw.Snapshot().Instances[0].Members
		wantBase := `[{"name":"state","kind":"state","sequence":0,"ops":[["r",{"entries":[{"id":"one"}],"retained":{"value":1}}]]}]`
		if got := mustMarshal(t, members); got != wantBase {
			t.Fatalf("snapshot members = %s", got)
		}
		activate(t, raw)
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{timelineDefinition.Id()}})
		service := use(t, binding, timelineDefinition.Id())
		if err := binding.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		replica, _ := service.State("state")
		previous, _, _ := TypedReplica[*timelineState](replica).Load()
		push := func(id string) {
			if err := typed.Change(ctx, func(draft *timelineState) error {
				draft.Entries = append(draft.Entries, map[string]any{"id": id})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		push("two")
		if got := mustMarshal(t, log.updates); !strings.Contains(got, `{"type":"state","member":"state","sequence":1,"ops":[["p",["entries"],1,0,[{"id":"two"}]]]}`) {
			t.Fatalf("updates = %s", got)
		}
		if len(previous.Entries) != 1 {
			t.Fatalf("a retained revision changed: %+v", previous)
		}
		if current, _, _ := TypedReplica[*timelineState](replica).Load(); len(current.Entries) != 2 {
			t.Fatalf("replica = %+v", current)
		}
		push("three")
		late, err := provider.Subscribe(timelineDefinition.Id(), ServiceSingleton, func(context.Context, ServiceProviderUpdate) {})
		if err != nil {
			t.Fatal(err)
		}
		if got := mustMarshal(t, log.updates[len(log.updates)-1]); got != `{"type":"state","member":"state","sequence":2,"ops":[["p",["entries"],2,0,[{"id":"three"}]]]}` {
			t.Fatalf("last update = %s", got)
		}
		wantLate := `[{"name":"state","kind":"state","sequence":2,"ops":[["r",{"entries":[{"id":"one"},{"id":"two"},{"id":"three"}],"retained":{"value":1}}]]}]`
		if got := mustMarshal(t, late.Snapshot().Instances[0].Members); got != wantLate {
			t.Fatalf("late snapshot = %s", got)
		}
		_ = late.Close(ctx)
		_ = raw.Close(ctx)
		_ = binding.Dispose(ctx)
		_ = provider.Dispose()
	})

	t.Run("publishes authoritative source references through services without re-diffing", func(t *testing.T) {
		initial := map[string]any{"entries": []any{map[string]any{"id": "one"}}, "retained": map[string]any{"value": 1.0}}
		var publish func(ReplicatedStateSourceFrame)
		disposed := false
		source := &funcSource{attach: func() ReplicatedStateSourceAttachment {
			return &funcAttachment{snapshot: ReplicatedStateSourceSnapshot{Value: initial, Cursor: 20}, activate: func(l func(ReplicatedStateSourceFrame)) { publish = l }, dispose: func() { disposed = true }}
		}}
		state, err := AttachReplicatedState[*timelineState](source, ReplicatedStateSourceOptions{})
		if err != nil {
			t.Fatal(err)
		}
		provider := newDeliveryProvider(t, SingletonService(timelineDefinition))
		if err := Provide[Timeline](provider, timelineDefinition, &timelineImpl{state: state}); err != nil {
			t.Fatal(err)
		}
		log := &updateLog{}
		subscription, err := provider.Subscribe(timelineDefinition.Id(), ServiceSingleton, log.listen)
		if err != nil {
			t.Fatal(err)
		}
		member := subscription.Snapshot().Instances[0].Members[0]
		if member.Kind != MemberState || container(member.Ops[0][1]) != container(initial) {
			t.Fatalf("snapshot member = %+v", member)
		}
		activate(t, subscription)
		next := map[string]any{"entries": []any{map[string]any{"id": "one"}, map[string]any{"id": "two"}}, "retained": initial["retained"]}
		ops := []Op{{"p", []any{"entries"}, 1, 0, []any{map[string]any{"id": "two"}}}}
		publish(ReplicatedStateSourceFrame{Cursor: 21, Value: next, Ops: ops, Context: ctx})
		if len(log.updates) != 1 || log.updates[0].Type != UpdateState || &log.updates[0].Ops[0] != &ops[0] {
			t.Fatalf("updates = %+v", log.updates)
		}
		_ = subscription.Close(ctx)
		_ = provider.Dispose()
		state.Dispose()
		if !disposed {
			t.Fatal("source attachment was not disposed")
		}
	})

	t.Run("keeps singleton facades stable when their provider is replaced", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		provideModels(t, provider, newModels(t, 1))
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id()}})
		models := use(t, binding, modelsDefinition.Id())
		replica := modelsReplica(t, models)
		if err := binding.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		if revision, _ := revisionOf(t, replica); revision != 1 {
			t.Fatalf("revision = %d", revision)
		}
		if err := Withdraw[Models](provider, modelsDefinition); err != nil {
			t.Fatal(err)
		}
		if _, hydrated := revisionOf(t, replica); hydrated {
			t.Fatal("state survived withdrawal")
		}
		if err := selectModel(ctx, models, modelRef{"test", "unavailable"}); !IsRemoteServiceErrorCode(err, ErrServiceNotFound) {
			t.Fatalf("select error = %v", err)
		}
		replacement := newModels(t, 2)
		calls := 0
		replacement.selected = func(context.Context, modelRef) error { calls++; return nil }
		if err := Replace[Models](provider, modelsDefinition, replacement); err != nil {
			t.Fatal(err)
		}
		if again := use(t, binding, modelsDefinition.Id()); again.facade != models.facade {
			t.Fatal("replacement created a new facade")
		}
		if revision, _ := revisionOf(t, replica); revision != 2 {
			t.Fatalf("revision = %d", revision)
		}
		if err := selectModel(ctx, models, modelRef{"test", "replacement"}); err != nil || calls != 1 {
			t.Fatalf("calls = %d err = %v", calls, err)
		}
		if err := Replace[stateOnlyModelsContract](provider, DefineService[stateOnlyModelsContract](modelsDefinition.Id()), &stateOnlyModels{newModels(t, 9).state}); err == nil || !strings.Contains(err.Error(), "replacement must preserve its member shape") {
			t.Fatalf("shape error = %v", err)
		}
		if err := Replace[methodStateModelsContract](provider, DefineService[methodStateModelsContract](modelsDefinition.Id()), &methodStateModels{}); err == nil || !strings.Contains(err.Error(), "replacement must preserve its member shape") {
			t.Fatalf("shape error = %v", err)
		}
		if revision, _ := revisionOf(t, replica); revision != 2 {
			t.Fatalf("revision = %d", revision)
		}
		_ = binding.Dispose(ctx)
		_ = provider.Dispose()
	})

	t.Run("delivers active subscriber updates before reporting listener failures", func(t *testing.T) {
		failure := errors.New("listener failed")
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		provideModels(t, provider, newModels(t, 1))
		delivered := 0
		failing := subscribeTo(t, provider, modelsDefinition.Id(), func(context.Context, ServiceProviderUpdate) { panic(failure) })
		succeeding := subscribeTo(t, provider, modelsDefinition.Id(), func(context.Context, ServiceProviderUpdate) { delivered++ })
		activate(t, failing)
		activate(t, succeeding)
		if err := Replace[Models](provider, modelsDefinition, newModels(t, 2)); !errors.Is(err, failure) {
			t.Fatalf("error = %v", err)
		}
		if delivered != 1 {
			t.Fatalf("delivered = %d", delivered)
		}
		_ = failing.Close(ctx)
		_ = succeeding.Close(ctx)
		_ = provider.Dispose()
	})

	t.Run("does not replay a queued revision already covered by a new subscription snapshot", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		models := newModels(t, 0)
		provideModels(t, provider, models)
		var lateUpdates []int
		var late ServiceSubscription
		first := subscribeTo(t, provider, modelsDefinition.Id(), func(_ context.Context, update ServiceProviderUpdate) {
			if update.Type != UpdateState || update.Sequence != 1 {
				return
			}
			models.setRevision(t, 2)
			late = subscribeTo(t, provider, modelsDefinition.Id(), func(_ context.Context, next ServiceProviderUpdate) {
				if next.Type == UpdateState {
					lateUpdates = append(lateUpdates, next.Sequence)
				}
			})
			want := `[{"name":"select","kind":"method"},{"name":"state","kind":"state","sequence":2,"ops":[["r",{"selected":null,"revision":2}]]}]`
			if got := canon(t, late.Snapshot().Instances[0].Members); !reflect.DeepEqual(got, jsonValue(t, want)) {
				t.Errorf("late members = %s", mustMarshal(t, got))
			}
			activate(t, late)
		})
		activate(t, first)
		models.setRevision(t, 1)
		if len(lateUpdates) != 0 {
			t.Fatalf("lateUpdates = %v", lateUpdates)
		}
		models.setRevision(t, 3)
		if !reflect.DeepEqual(lateUpdates, []int{3}) {
			t.Fatalf("lateUpdates = %v", lateUpdates)
		}
		_ = late.Close(ctx)
		_ = first.Close(ctx)
		_ = provider.Dispose()
	})

	t.Run("replays every buffered update before reporting listener failures", func(t *testing.T) {
		failure := errors.New("listener failed")
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		models := newModels(t, 0)
		provideModels(t, provider, models)
		delivered := 0
		subscription := subscribeTo(t, provider, modelsDefinition.Id(), func(context.Context, ServiceProviderUpdate) { delivered++; panic(failure) })
		models.setRevision(t, 1)
		models.setRevision(t, 2)
		if err := subscription.Activate(); err == nil || !strings.Contains(err.Error(), "Failed to activate remote service subscription") {
			t.Fatalf("activate error = %v", err)
		}
		if delivered != 2 {
			t.Fatalf("delivered = %d", delivered)
		}
		_ = subscription.Close(ctx)
		_ = provider.Dispose()
	})

	t.Run("clears retained facades when providers and bindings are disposed", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		provideModels(t, provider, newModels(t, 1))
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id()}})
		models := use(t, binding, modelsDefinition.Id())
		replica := modelsReplica(t, models)
		if err := binding.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		if revision, _ := revisionOf(t, replica); revision != 1 {
			t.Fatalf("revision = %d", revision)
		}
		_ = provider.Dispose()
		if _, hydrated := revisionOf(t, replica); hydrated {
			t.Fatal("state survived provider disposal")
		}
		if err := selectModel(ctx, models, modelRef{"test", "one"}); err == nil || !strings.Contains(err.Error(), "Remote service provider is disposed") {
			t.Fatalf("select error = %v", err)
		}
		_ = binding.Dispose(ctx)
		if _, _, err := replica.Load(); err == nil || !strings.Contains(err.Error(), "Remote service binding is disposed") {
			t.Fatalf("load error = %v", err)
		}
	})

	t.Run("applies provider disposal buffered while subscriptions are starting", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition), KeyedService(questionDialogsDefinition))
		provideModels(t, provider, newModels(t, 1))
		if _, err := Spawn[QuestionDialogs](provider, questionDialogsDefinition, "pending", newQuestion(t, "Pending?", nil)); err != nil {
			t.Fatal(err)
		}
		// Upstream's loopback subscribe runs its provider subscription synchronously, so the disposal lands while the consumer is still starting. The gate holds each start between the provider subscription and the consumer's snapshot install to make that ordering explicit.
		gated := &gatedTransport{RemoteServiceTransport: NewLoopbackTransport(provider), subscribed: make(chan struct{}, 2), release: make(chan struct{})}
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id(), questionDialogsDefinition.Id()}, Transport: gated})
		models := use(t, binding, modelsDefinition.Id())
		observed := &locked[*RemoteService]{}
		if _, err := binding.Observe(questionDialogsDefinition.Id(), func(_ context.Context, service *RemoteService) error { observed.add(service); return nil }); err != nil {
			t.Fatal(err)
		}
		<-gated.subscribed
		<-gated.subscribed
		_ = provider.Dispose()
		close(gated.release)
		if err := binding.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		if _, hydrated := revisionOf(t, modelsReplica(t, models)); hydrated || len(observed.get()) != 0 {
			t.Fatalf("hydrated=%v observed=%d", hydrated, len(observed.get()))
		}
		_ = binding.Dispose(ctx)
	})

	t.Run("keeps deferred service handles inaccessible until host activation", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		provideModels(t, provider, newModels(t, 0))
		var mu sync.Mutex
		active := false
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{
			Services: []string{modelsDefinition.Id()}, Unbound: true,
			AssertAccess: func() error {
				mu.Lock()
				defer mu.Unlock()
				if !active {
					return errors.New("Service handles are not active")
				}
				return nil
			},
		})
		models := use(t, binding, modelsDefinition.Id())
		if _, err := models.State("state"); err == nil || !strings.Contains(err.Error(), "Service handles are not active") {
			t.Fatalf("State error = %v", err)
		}
		if _, err := models.Call(ctx, "select", modelRef{"test", "one"}); err == nil || !strings.Contains(err.Error(), "Service handles are not active") {
			t.Fatalf("Call error = %v", err)
		}
		if err := binding.Rebind(ctx, true); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		active = true
		mu.Unlock()
		if err := binding.Ready(ctx); err != nil {
			t.Fatal(err)
		}
		if revision, _ := revisionOf(t, modelsReplica(t, models)); revision != 0 {
			t.Fatalf("revision = %d", revision)
		}
		_ = binding.Dispose(ctx)
		_ = provider.Dispose()
	})

	t.Run("rejects namespace readiness when initial hydration fails", func(t *testing.T) {
		failure := errors.New("initial hydration failed")
		errs := &locked[error]{}
		binding := loopbackBinding(t, nil, RemoteServiceBindingOptions{
			Services:  []string{modelsDefinition.Id()},
			Transport: failingTransport{failure},
			OnError:   func(err error) { errs.add(err) },
		})
		models := use(t, binding, modelsDefinition.Id())
		if err := binding.Ready(ctx); !errors.Is(err, failure) {
			t.Fatalf("Ready error = %v", err)
		}
		if _, hydrated := revisionOf(t, modelsReplica(t, models)); hydrated {
			t.Fatal("state is hydrated")
		}
		if got := errs.get(); len(got) != 1 || !errors.Is(got[0], failure) {
			t.Fatalf("errors = %v", got)
		}
		_ = binding.Dispose(ctx)
	})

	t.Run("buffers state updates that race subscription hydration", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		models := newModels(t, 0)
		provideModels(t, provider, models)
		transport := racingTransport{provider: provider, race: func() { models.setRevision(t, 1) }}
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id()}, Transport: transport})
		service := use(t, binding, modelsDefinition.Id())
		var revisions locked[int]
		if _, err := modelsReplica(t, service).Subscribe(func(value *modelsState, _ context.Context, _ ReplicatedStateDelivery) { revisions.add(value.Revision) }); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "hydration then update", func() bool { return reflect.DeepEqual(revisions.get(), []int{0, 1}) })
		if revision, _ := revisionOf(t, modelsReplica(t, service)); revision != 1 {
			t.Fatalf("revision = %d", revision)
		}
		_ = binding.Dispose(ctx)
		_ = provider.Dispose()
	})

	for _, tc := range []struct {
		kind     string
		sequence int
	}{{"duplicate", 0}, {"gap", 2}} {
		t.Run("clears replicated state after a "+tc.kind+" operation sequence", func(t *testing.T) {
			var send UpdateListener
			errs := &locked[error]{}
			transport := scriptedTransport{onSubscribe: func(listener UpdateListener) { send = listener }, snapshot: ServiceSubscriptionSnapshot{
				ServiceId: modelsDefinition.Id(), Mode: ServiceSingleton,
				Instances: []ServiceInstanceSnapshot{{Members: []ServiceMemberSnapshot{
					{Name: "select", Kind: MemberMethod},
					{Name: "state", Kind: MemberState, Sequence: 0, Ops: []Op{{"r", map[string]any{"selected": nil, "revision": 0.0}}}},
				}}},
			}}
			binding := loopbackBinding(t, nil, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id()}, Transport: transport, OnError: func(err error) { errs.add(err) }})
			models := use(t, binding, modelsDefinition.Id())
			if err := binding.Ready(ctx); err != nil {
				t.Fatal(err)
			}
			if revision, _ := revisionOf(t, modelsReplica(t, models)); revision != 0 {
				t.Fatalf("revision = %d", revision)
			}
			send(ctx, ServiceProviderUpdate{Type: UpdateState, Member: "state", Sequence: tc.sequence, Ops: []Op{{"r", map[string]any{"selected": nil, "revision": float64(tc.sequence)}}}})
			if _, hydrated := revisionOf(t, modelsReplica(t, models)); hydrated {
				t.Fatal("replica kept its value")
			}
			if got := errs.get(); len(got) != 1 || !strings.Contains(got[0].Error(), "sequence has a gap") {
				t.Fatalf("errors = %v", got)
			}
			_ = binding.Dispose(ctx)
		})
	}

	t.Run("hydrates cold ReplicatedState replicas and replaces them across rebinds", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition))
		models := newModels(t, 0)
		provideModels(t, provider, models)
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{modelsDefinition.Id()}, Unbound: true})
		service := use(t, binding, modelsDefinition.Id())
		replica := modelsReplica(t, service)
		var revisions locked[int]
		if _, err := replica.Subscribe(func(value *modelsState, _ context.Context, _ ReplicatedStateDelivery) { revisions.add(value.Revision) }); err != nil {
			t.Fatal(err)
		}
		if _, hydrated := revisionOf(t, replica); hydrated || len(revisions.get()) != 0 {
			t.Fatal("cold replica is hydrated")
		}
		models.setRevision(t, 1)
		if err := binding.Rebind(ctx, true); err != nil {
			t.Fatal(err)
		}
		if revision, _ := revisionOf(t, replica); revision != 1 || !reflect.DeepEqual(revisions.get(), []int{1}) {
			t.Fatalf("revision = %d revisions = %v", revision, revisions.get())
		}
		models.setRevision(t, 2)
		if !reflect.DeepEqual(revisions.get(), []int{1, 2}) {
			t.Fatalf("revisions = %v", revisions.get())
		}
		if err := binding.Rebind(ctx, false); err != nil {
			t.Fatal(err)
		}
		if _, hydrated := revisionOf(t, replica); hydrated {
			t.Fatal("replica survived unbind")
		}
		models.setRevision(t, 3)
		if !reflect.DeepEqual(revisions.get(), []int{1, 2}) {
			t.Fatalf("revisions = %v", revisions.get())
		}
		if err := binding.Rebind(ctx, true); err != nil {
			t.Fatal(err)
		}
		if revision, _ := revisionOf(t, replica); revision != 3 || !reflect.DeepEqual(revisions.get(), []int{1, 2, 3}) {
			t.Fatalf("revision = %d revisions = %v", revision, revisions.get())
		}
		_ = binding.Dispose(ctx)
		_ = provider.Dispose()
	})

	t.Run("hydrates keyed state before observe handlers and fences reused keys", func(t *testing.T) {
		provider := newDeliveryProvider(t, KeyedService(questionDialogsDefinition))
		if got := provider.Catalogue(); !reflect.DeepEqual(got, []ServiceCatalogueEntry{{ServiceId: questionDialogsDefinition.Id(), Mode: ServiceKeyed}}) {
			t.Fatalf("catalogue = %v", got)
		}
		errs := &locked[error]{}
		binding := loopbackBinding(t, provider, RemoteServiceBindingOptions{Services: []string{questionDialogsDefinition.Id()}, OnError: func(err error) { errs.add(err) }})
		type observation struct {
			question *Question
			service  *RemoteService
			ctx      context.Context
		}
		observed := &locked[observation]{}
		stop, err := binding.Observe(questionDialogsDefinition.Id(), func(ctx context.Context, service *RemoteService) error {
			replica, err := service.State("request")
			if err != nil {
				return err
			}
			question, _, err := TypedReplica[*Question](replica).Load()
			if err != nil {
				return err
			}
			observed.add(observation{question, service, ctx})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := errs.get(); len(got) != 0 {
			t.Fatalf("errors = %v", got)
		}
		var firstAnswers []string
		first := newQuestion(t, "First?", func(_ context.Context, answer string) (submitResult, error) {
			firstAnswers = append(firstAnswers, answer)
			return submitResult{Accepted: true}, nil
		})
		closeFirst, err := Spawn[QuestionDialogs](provider, questionDialogsDefinition, "invocation-1", first)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "first observation", func() bool { return len(observed.get()) == 1 })
		if got := observed.get()[0].question; got == nil || got.Question != "First?" {
			t.Fatalf("question = %+v", got)
		}
		firstService := observed.get()[0].service
		if err := first.request.Change(ctx, func(draft *Question) error { draft.Question = "Updated?"; return nil }); err != nil {
			t.Fatal(err)
		}
		firstReplica, _ := firstService.State("request")
		if value, _, _ := TypedReplica[*Question](firstReplica).Load(); value.Question != "Updated?" {
			t.Fatalf("question = %+v", value)
		}
		if result, err := CallResult[submitResult](ctx, firstService, "submit", "yes"); err != nil || !result.Accepted || !reflect.DeepEqual(firstAnswers, []string{"yes"}) {
			t.Fatalf("submit = %+v, %v", result, err)
		}
		_ = closeFirst()
		if observed.get()[0].ctx.Err() == nil {
			t.Fatal("observation context was not aborted")
		}
		if _, _, err := TypedReplica[*Question](firstReplica).Load(); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("closed state error = %v", err)
		}
		if _, err := firstService.Call(ctx, "submit", "late"); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("closed call error = %v", err)
		}

		second := newQuestion(t, "Again?", func(context.Context, string) (submitResult, error) { return submitResult{}, nil })
		closeSecond, err := Spawn[QuestionDialogs](provider, questionDialogsDefinition, "invocation-1", second)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "second observation", func() bool { return len(observed.get()) == 2 })
		if got := observed.get()[1].question; got == nil || got.Question != "Again?" || observed.get()[1].service.facade == firstService.facade {
			t.Fatalf("second observation = %+v", observed.get()[1])
		}
		secondService := observed.get()[1].service
		secondReplica, _ := secondService.State("request")
		stop()
		if observed.get()[1].ctx.Err() == nil {
			t.Fatal("second observation context was not aborted")
		}
		if _, _, err := TypedReplica[*Question](secondReplica).Load(); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("closed state error = %v", err)
		}
		if _, err := secondService.Call(ctx, "submit", "late"); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("closed call error = %v", err)
		}
		_ = closeSecond()
		_ = binding.Dispose(ctx)
		_ = provider.Dispose()
		if got := errs.get(); len(got) != 0 {
			t.Fatalf("errors = %v", got)
		}
	})

	t.Run("rejects mode mixing and unsupported members", func(t *testing.T) {
		provider := newDeliveryProvider(t, SingletonService(modelsDefinition), KeyedService(questionDialogsDefinition))
		provideModels(t, provider, newModels(t, 0))
		if _, err := Spawn[Models](provider, modelsDefinition, "wrong", newModels(t, 0)); err == nil || !strings.Contains(err.Error(), "singleton") {
			t.Fatalf("spawn error = %v", err)
		}
		if _, err := Spawn[badRequestDialogsContract](provider, DefineService[badRequestDialogsContract](questionDialogsDefinition.Id()), "invalid", &badRequestDialogs{}); err == nil || !strings.Contains(err.Error(), "not remotely exposable") {
			t.Fatalf("spawn error = %v", err)
		}
		_ = provider.Dispose()
	})
}

func jsonValue(t *testing.T, text string) any {
	t.Helper()
	var out any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func stored2(state *MutableReplicatedState[*modelsState]) any {
	_, value := state.core.snapshot()
	return value
}

func subscribeTo(t *testing.T, provider *RemoteServiceProvider, serviceId string, listener UpdateListener) ServiceSubscription {
	t.Helper()
	subscription, err := provider.Subscribe(serviceId, ServiceSingleton, listener)
	if err != nil {
		t.Fatal(err)
	}
	return subscription
}

type stateOnlyModelsContract interface {
	State() ReplicatedStateOf[*modelsState]
}

type methodStateModelsContract interface {
	State(context.Context) error
	Select(context.Context, modelRef) error
}

type badRequestDialogsContract interface {
	Request() string
	Submit(context.Context, string) (submitResult, error)
}

// stateOnlyModels has the state member but no select method.
type stateOnlyModels struct {
	state *MutableReplicatedState[*modelsState]
}

func (models *stateOnlyModels) State() ReplicatedStateOf[*modelsState] { return models.state }

// methodStateModels turns the state member into a method.
type methodStateModels struct{}

func (methodStateModels) State(context.Context) error            { return nil }
func (methodStateModels) Select(context.Context, modelRef) error { return nil }

// badRequestDialogs exposes a request member that is not a replicated state.
type badRequestDialogs struct{}

func (badRequestDialogs) Request() string { return "not state" }
func (badRequestDialogs) Submit(context.Context, string) (submitResult, error) {
	return submitResult{}, nil
}

type funcSource struct {
	attach func() ReplicatedStateSourceAttachment
}

func (source *funcSource) Attach() ReplicatedStateSourceAttachment { return source.attach() }

type funcAttachment struct {
	snapshot ReplicatedStateSourceSnapshot
	activate func(func(ReplicatedStateSourceFrame))
	dispose  func()
}

func (attachment *funcAttachment) Snapshot() ReplicatedStateSourceSnapshot {
	return attachment.snapshot
}
func (attachment *funcAttachment) Activate(listener func(ReplicatedStateSourceFrame)) {
	attachment.activate(listener)
}
func (attachment *funcAttachment) Dispose() { attachment.dispose() }

type failingTransport struct{ failure error }

func (transport failingTransport) Invoke(context.Context, ServiceCall) (json.RawMessage, error) {
	return nil, errors.New("unexpected invocation")
}
func (transport failingTransport) Subscribe(context.Context, string, ServiceMode, UpdateListener) (ServiceSubscription, error) {
	return nil, transport.failure
}

// racingTransport publishes a revision between the provider subscription and the consumer's snapshot install.
type racingTransport struct {
	provider *RemoteServiceProvider
	race     func()
}

func (transport racingTransport) Invoke(ctx context.Context, call ServiceCall) (json.RawMessage, error) {
	return transport.provider.Invoke(ctx, call)
}
func (transport racingTransport) Subscribe(_ context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	subscription, err := transport.provider.Subscribe(serviceId, mode, listener)
	if err != nil {
		return nil, err
	}
	transport.race()
	return subscription, nil
}

// scriptedTransport returns a fixed snapshot and hands the listener to the test.
type scriptedTransport struct {
	onSubscribe func(UpdateListener)
	snapshot    ServiceSubscriptionSnapshot
}

func (transport scriptedTransport) Invoke(context.Context, ServiceCall) (json.RawMessage, error) {
	return nil, errors.New("unexpected invocation")
}
func (transport scriptedTransport) Subscribe(_ context.Context, _ string, _ ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	transport.onSubscribe(listener)
	return scriptedSubscription{transport.snapshot}, nil
}

type scriptedSubscription struct{ snapshot ServiceSubscriptionSnapshot }

func (subscription scriptedSubscription) Snapshot() ServiceSubscriptionSnapshot {
	return subscription.snapshot
}
func (subscription scriptedSubscription) Activate() error             { return nil }
func (subscription scriptedSubscription) Close(context.Context) error { return nil }

// gatedTransport holds each subscription between the provider's subscribe and the consumer's use of the snapshot.
type gatedTransport struct {
	RemoteServiceTransport
	subscribed chan struct{}
	release    chan struct{}
}

func (transport *gatedTransport) Subscribe(ctx context.Context, serviceId string, mode ServiceMode, listener UpdateListener) (ServiceSubscription, error) {
	subscription, err := transport.RemoteServiceTransport.Subscribe(ctx, serviceId, mode, listener)
	transport.subscribed <- struct{}{}
	<-transport.release
	return subscription, err
}
