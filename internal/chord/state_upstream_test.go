package chord

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/chord/test/state.test.ts. Typed state decodes a detached draft, so the JavaScript cases that observe Proxy revocation, mutation after placement, or object identity of the returned value have no Go form; the PromiseLike case is an equivalence test (a draft settles when its callback returns); the structural-sharing and identity guarantees are asserted on the stored JSON revisions through core.snapshot.

type docState = map[string]any

func newDocState(t *testing.T, initial docState) *MutableReplicatedState[docState] {
	t.Helper()
	state, err := NewReplicatedState(initial)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func stored(state *MutableReplicatedState[docState]) any {
	_, value := state.core.snapshot()
	return value
}

func container(value any) uintptr { return reflect.ValueOf(value).Pointer() }

func sub(t *testing.T, value any, key string) any {
	t.Helper()
	typed, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %T", value)
	}
	return typed[key]
}

func changeDoc(t *testing.T, state *MutableReplicatedState[docState], mutate func(docState)) {
	t.Helper()
	if err := state.Change(context.Background(), func(draft docState) error { mutate(draft); return nil }); err != nil {
		t.Fatal(err)
	}
}

func opsJSON(t *testing.T, ops []Op) string {
	t.Helper()
	encoded, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestTransactionalReplicatedState(t *testing.T) {
	ctx := context.Background()

	t.Run("publishes one immutable structurally shared revision", func(t *testing.T) {
		state := newDocState(t, docState{"changed": map[string]any{"value": 1.0}, "retained": map[string]any{"value": 2.0}})
		previous := stored(state)
		var deliveries []int
		if _, err := state.Subscribe(func(_ docState, _ context.Context, delivery ReplicatedStateDelivery) {
			deliveries = append(deliveries, delivery.Sequence)
		}); err != nil {
			t.Fatal(err)
		}
		changeDoc(t, state, func(draft docState) {
			draft["changed"].(map[string]any)["value"] = 3.0
			draft["changed"].(map[string]any)["value"] = 4.0
		})
		current := stored(state)
		if !reflect.DeepEqual(current, map[string]any{"changed": map[string]any{"value": 4.0}, "retained": map[string]any{"value": 2.0}}) {
			t.Fatalf("value = %v", current)
		}
		if container(current) == container(previous) || container(sub(t, current, "changed")) == container(sub(t, previous, "changed")) {
			t.Fatal("changed branches were not copied")
		}
		if container(sub(t, current, "retained")) != container(sub(t, previous, "retained")) {
			t.Fatal("retained branch was not shared")
		}
		if !reflect.DeepEqual(deliveries, []int{0, 1}) {
			t.Fatalf("deliveries = %v", deliveries)
		}
	})

	t.Run("rolls back callback failures", func(t *testing.T) {
		state := newDocState(t, docState{"nested": map[string]any{"value": 1.0}})
		previous := stored(state)
		err := state.Change(ctx, func(draft docState) error {
			draft["nested"].(map[string]any)["value"] = 2.0
			return errors.New("stop")
		})
		if err == nil || err.Error() != "stop" {
			t.Fatalf("error = %v", err)
		}
		if container(stored(state)) != container(previous) || state.Sequence() != 0 {
			t.Fatal("failed change was published")
		}
	})

	t.Run("rejects nested changes and replacements without losing the outer rollback", func(t *testing.T) {
		state := newDocState(t, docState{"left": 0.0, "right": 0.0})
		err := state.Change(ctx, func(draft docState) error {
			draft["left"] = 1.0
			return state.Change(ctx, func(nested docState) error { nested["right"] = 2.0; return nil })
		})
		if err == nil || !strings.Contains(err.Error(), "reentrantly") {
			t.Fatalf("nested change error = %v", err)
		}
		if !reflect.DeepEqual(stored(state), map[string]any{"left": 0.0, "right": 0.0}) {
			t.Fatalf("value = %v", stored(state))
		}
		err = state.Change(ctx, func(docState) error {
			return state.Replace(ctx, docState{"left": 1.0, "right": 2.0})
		})
		if err == nil || !strings.Contains(err.Error(), "change callback") {
			t.Fatalf("nested replace error = %v", err)
		}
		if !reflect.DeepEqual(stored(state), map[string]any{"left": 0.0, "right": 0.0}) {
			t.Fatalf("value = %v", stored(state))
		}
	})

	t.Run("queues listener-triggered changes in sequence order", func(t *testing.T) {
		state := newDocState(t, docState{"value": 0.0})
		var sourceSequences []int
		var deliveries [][2]int
		var lateDeliveries []ReplicatedStateDelivery
		nested := false
		state.core.subscribeOps(func(_ context.Context, _ []Op, _ int) {
			if nested {
				return
			}
			nested = true
			changeDoc(t, state, func(draft docState) { draft["value"] = 2.0 })
			if _, err := state.Subscribe(func(_ docState, _ context.Context, delivery ReplicatedStateDelivery) {
				lateDeliveries = append(lateDeliveries, delivery)
			}); err != nil {
				t.Error(err)
			}
		})
		state.core.subscribeOps(func(_ context.Context, _ []Op, sequence int) { sourceSequences = append(sourceSequences, sequence) })
		if _, err := state.Subscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				deliveries = append(deliveries, [2]int{int(value["value"].(float64)), delivery.Sequence})
			}
		}); err != nil {
			t.Fatal(err)
		}
		changeDoc(t, state, func(draft docState) { draft["value"] = 1.0 })
		if !reflect.DeepEqual(sourceSequences, []int{1, 2}) {
			t.Fatalf("source sequences = %v", sourceSequences)
		}
		if !reflect.DeepEqual(deliveries, [][2]int{{1, 1}, {2, 2}}) {
			t.Fatalf("deliveries = %v", deliveries)
		}
		if !reflect.DeepEqual(lateDeliveries, []ReplicatedStateDelivery{{Kind: DeliveryHydrate, Sequence: 2}}) {
			t.Fatalf("late deliveries = %v", lateDeliveries)
		}
	})

	t.Run("isolates listener failures after committing the revision", func(t *testing.T) {
		state := newDocState(t, docState{"value": 0.0})
		var received []int
		state.core.subscribeOps(func(context.Context, []Op, int) { panic(errors.New("listener failed")) })
		state.core.subscribeOps(func(_ context.Context, _ []Op, sequence int) { received = append(received, sequence) })
		err := state.Change(ctx, func(draft docState) error { draft["value"] = 1.0; return nil })
		if err == nil || err.Error() != "listener failed" {
			t.Fatalf("error = %v", err)
		}
		if !reflect.DeepEqual(stored(state), map[string]any{"value": 1.0}) || !reflect.DeepEqual(received, []int{1}) {
			t.Fatalf("value = %v received = %v", stored(state), received)
		}
	})

	t.Run("copies assigned values by value", func(t *testing.T) {
		// state.test.ts:137-150 edits through the alias after assigning; Go aliases share edits, so the case keeps the independence of the stored containers and the untouched source.
		type pair struct {
			Left  *struct{ Value int } `json:"left"`
			Right *struct{ Value int } `json:"right"`
		}
		state, err := NewReplicatedState(&pair{})
		if err != nil {
			t.Fatal(err)
		}
		external := &struct{ Value int }{1}
		if err := state.Change(ctx, func(draft *pair) error { draft.Left, draft.Right = external, external; return nil }); err != nil {
			t.Fatal(err)
		}
		_, value := state.core.snapshot()
		if external.Value != 1 || !reflect.DeepEqual(value, map[string]any{"left": map[string]any{"Value": 1.0}, "right": map[string]any{"Value": 1.0}}) {
			t.Fatalf("value = %v external = %v", value, external)
		}
		if container(sub(t, value, "left")) == container(sub(t, value, "right")) {
			t.Fatal("placements alias")
		}
	})

	t.Run("takes immutable ownership of alias-free replacements", func(t *testing.T) {
		state := newDocState(t, docState{"left": map[string]any{"value": 1.0}, "right": map[string]any{"value": 2.0}})
		if err := state.Replace(ctx, docState{"left": map[string]any{"value": 1.0}, "right": map[string]any{"value": 1.0}}); err != nil {
			t.Fatal(err)
		}
		current := stored(state)
		if container(sub(t, current, "left")) == container(sub(t, current, "right")) {
			t.Fatal("replacement containers alias")
		}
		changeDoc(t, state, func(draft docState) { draft["left"].(map[string]any)["value"] = 9.0 })
		if !reflect.DeepEqual(stored(state), map[string]any{"left": map[string]any{"value": 9.0}, "right": map[string]any{"value": 1.0}}) {
			t.Fatalf("value = %v", stored(state))
		}
	})

	t.Run("preserves compact string, splice, and permutation operations", func(t *testing.T) {
		state := newDocState(t, docState{"text": "abcdefgh", "values": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}, map[string]any{"id": "c"}}})
		var batches []string
		state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) { batches = append(batches, opsJSON(t, ops)) })
		changeDoc(t, state, func(draft docState) {
			draft["text"] = "defghxyz"
			draft["values"] = draft["values"].([]any)[1:]
		})
		changeDoc(t, state, func(draft docState) {
			values := draft["values"].([]any)
			values[0], values[1] = values[1], values[0]
		})
		want := []string{`[["t",["text"],3],["a",["text"],"xyz"],["p",["values"],0,1,[]]]`, `[["m",["values"],[1,0]]]`}
		if !reflect.DeepEqual(batches, want) {
			t.Fatalf("batches = %v\nwant     %v", batches, want)
		}
	})

	t.Run("validates replica revisions without freezing shared immutable payloads", func(t *testing.T) {
		replica := newReplica(func(error) {})
		initial := map[string]any{"rows": []any{map[string]any{"value": 1.0}}}
		if err := replica.hydrate(ctx, 0, []Op{{"r", initial}}, nil); err != nil {
			t.Fatal(err)
		}
		value, _ := replica.snapshotValue()
		if container(value) != container(initial) {
			t.Fatal("hydrate copied the payload")
		}
		inserted := map[string]any{"value": 2.0}
		if err := replica.update(ctx, 1, []Op{{"p", []any{"rows"}, 1, 0, []any{inserted}}}, nil); err != nil {
			t.Fatal(err)
		}
		next, _ := replica.snapshotValue()
		rows := sub(t, next, "rows").([]any)
		if container(next) == container(initial) || len(rows) != 2 {
			t.Fatalf("value = %v", next)
		}
		if container(rows[0]) != container(initial["rows"].([]any)[0]) || container(rows[1]) != container(inserted) {
			t.Fatal("update did not share unchanged and inserted containers")
		}
		if len(initial["rows"].([]any)) != 1 {
			t.Fatal("update mutated the previous revision")
		}
	})

	t.Run("clears a replica when an adopted update is invalid", func(t *testing.T) {
		var errs []error
		replica := newReplica(func(err error) { errs = append(errs, err) })
		if err := replica.hydrate(ctx, 0, []Op{{"r", map[string]any{"value": 0.0}}}, nil); err != nil {
			t.Fatal(err)
		}
		err := replica.update(ctx, 1, []Op{{"s", []any{"value"}, nanValue()}}, nil)
		if err == nil || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("error = %v", err)
		}
		if _, hydrated := replica.snapshotValue(); hydrated {
			t.Fatal("replica kept a value after an invalid update")
		}
		if err := replica.update(ctx, 2, []Op{{"s", []any{"value"}, 2.0}}, nil); err == nil || !strings.Contains(err.Error(), "before hydration") {
			t.Fatalf("error = %v", err)
		}

		malformed := newReplica(func(err error) { errs = append(errs, err) })
		if err := malformed.hydrate(ctx, 0, []Op{{"r", map[string]any{"values": []any{1.0, 2.0}}}}, nil); err != nil {
			t.Fatal(err)
		}
		if err := malformed.update(ctx, 1, []Op{{"m", []any{"values"}, []any{0.0}}}, nil); err == nil {
			t.Fatal("malformed permutation accepted")
		}
		if _, hydrated := malformed.snapshotValue(); hydrated || len(errs) != 0 {
			t.Fatalf("hydrated=%v errors=%v", hydrated, errs)
		}
	})

	t.Run("replaces atomically and ignores deeply equal replacements", func(t *testing.T) {
		state := newDocState(t, docState{"value": map[string]any{"nested": 1.0}, "retained": map[string]any{"nested": 2.0}})
		previous := stored(state)
		var batches []string
		state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) { batches = append(batches, opsJSON(t, ops)) })
		if err := state.Replace(ctx, docState{"value": map[string]any{"nested": 1.0}, "retained": map[string]any{"nested": 2.0}}); err != nil {
			t.Fatal(err)
		}
		if container(stored(state)) != container(previous) || len(batches) != 0 || state.Sequence() != 0 {
			t.Fatal("a deeply equal replacement published")
		}
		if err := state.Replace(ctx, docState{"value": map[string]any{"nested": 2.0}, "retained": map[string]any{"nested": 2.0}}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored(state), map[string]any{"value": map[string]any{"nested": 2.0}, "retained": map[string]any{"nested": 2.0}}) {
			t.Fatalf("value = %v", stored(state))
		}
		if len(batches) != 1 || !strings.HasPrefix(batches[0], `[["r",`) {
			t.Fatalf("a replacement must publish one root replacement: %v", batches)
		}
	})
}

func nanValue() float64 { var zero float64; return zero / zero }

// source tests

type testSource struct {
	attachments map[*testAttachment]struct{}
	onAttach    func()
	value       JsonValue
	cursor      int
}

func newTestSource(value JsonValue, cursor int) *testSource {
	return &testSource{attachments: map[*testAttachment]struct{}{}, value: value, cursor: cursor}
}

type testAttachment struct {
	snapshot            ReplicatedStateSourceSnapshot
	onDispose           func()
	buffer              []ReplicatedStateSourceFrame
	listener            func(ReplicatedStateSourceFrame)
	activated, disposed bool
}

func (source *testSource) Attach() ReplicatedStateSourceAttachment {
	attachment := &testAttachment{snapshot: ReplicatedStateSourceSnapshot{Value: source.value, Cursor: source.cursor}}
	attachment.onDispose = func() { delete(source.attachments, attachment) }
	source.attachments[attachment] = struct{}{}
	if source.onAttach != nil {
		source.onAttach()
	}
	return attachment
}

func (source *testSource) commit(value JsonValue, ops []Op) {
	source.commitAt(value, ops, context.Background(), source.cursor+1)
}

func (source *testSource) commitAt(value JsonValue, ops []Op, ctx context.Context, cursor int) {
	source.value, source.cursor = value, cursor
	frame := ReplicatedStateSourceFrame{Cursor: cursor, Value: value, Ops: ops, Context: ctx}
	for attachment := range source.attachments {
		attachment.publish(frame)
	}
}

func (attachment *testAttachment) Snapshot() ReplicatedStateSourceSnapshot {
	return attachment.snapshot
}

func (attachment *testAttachment) Activate(listener func(ReplicatedStateSourceFrame)) {
	if attachment.activated {
		panic(errors.New("attachment is already active"))
	}
	attachment.activated, attachment.listener = true, listener
	buffered := attachment.buffer
	attachment.buffer = nil
	for _, frame := range buffered {
		listener(frame)
	}
}

func (attachment *testAttachment) publish(frame ReplicatedStateSourceFrame) {
	if attachment.disposed {
		return
	}
	if attachment.listener == nil {
		attachment.buffer = append(attachment.buffer, frame)
		return
	}
	attachment.listener(frame)
}

func (attachment *testAttachment) Dispose() {
	if attachment.disposed {
		return
	}
	attachment.disposed = true
	attachment.buffer = nil
	attachment.onDispose()
}

func valueDoc(value float64) map[string]any { return map[string]any{"value": value} }

func setValueOps(value float64) []Op { return []Op{{"s", []any{"value"}, value}} }

func attach(t *testing.T, source *testSource, options ReplicatedStateSourceOptions) *AttachedReplicatedState[docState] {
	t.Helper()
	state, err := AttachReplicatedState[docState](source, options)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestAuthoritativeReplicatedStateSources(t *testing.T) {
	t.Run("captures before activation and drains queued commits in order", func(t *testing.T) {
		initial, first, second := valueDoc(0), valueDoc(1), valueDoc(2)
		source := newTestSource(initial, 10)
		source.onAttach = func() {
			source.commit(first, setValueOps(1))
			source.commit(second, setValueOps(2))
		}
		state := attach(t, source, ReplicatedStateSourceOptions{})
		var deliveries [][2]int
		if _, err := state.Subscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
			deliveries = append(deliveries, [2]int{int(value["value"].(float64)), delivery.Sequence})
		}); err != nil {
			t.Fatal(err)
		}
		sequence, value := state.core.snapshot()
		if container(value) != container(second) || sequence != 2 || !reflect.DeepEqual(deliveries, [][2]int{{2, 2}}) {
			t.Fatalf("sequence=%d deliveries=%v", sequence, deliveries)
		}
	})

	t.Run("hydrates at sequence zero when attaching after existing commits", func(t *testing.T) {
		source := newTestSource(valueDoc(0), 40)
		current := valueDoc(1)
		source.commit(current, setValueOps(1))
		state := attach(t, source, ReplicatedStateSourceOptions{})
		var deliveries []int
		if _, err := state.Subscribe(func(_ docState, _ context.Context, delivery ReplicatedStateDelivery) {
			deliveries = append(deliveries, delivery.Sequence)
		}); err != nil {
			t.Fatal(err)
		}
		if _, value := state.core.snapshot(); container(value) != container(current) || !reflect.DeepEqual(deliveries, []int{0}) {
			t.Fatalf("deliveries = %v", deliveries)
		}
	})

	t.Run("publishes exact source value and operation references without applying or re-diffing", func(t *testing.T) {
		source := newTestSource(valueDoc(0), 0)
		state := attach(t, source, ReplicatedStateSourceOptions{})
		next, ops := valueDoc(1), setValueOps(1)
		var publishedOps []Op
		state.core.subscribeOps(func(_ context.Context, received []Op, _ int) { publishedOps = received })
		var publishedValue docState
		if _, err := state.Subscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				publishedValue = value
			}
		}); err != nil {
			t.Fatal(err)
		}
		source.commit(next, ops)
		if _, value := state.core.snapshot(); container(value) != container(next) || publishedValue["value"] != 1.0 {
			t.Fatal("value was not published as given")
		}
		if &publishedOps[0] != &ops[0] {
			t.Fatal("operation batch was copied")
		}
	})

	t.Run("buffers reentrant frames and skips updates covered by a late hydration", func(t *testing.T) {
		source := newTestSource(valueDoc(0), 0)
		state := attach(t, source, ReplicatedStateSourceOptions{})
		var received []float64
		type late struct {
			kind     string
			sequence int
			value    float64
		}
		var lateDeliveries []late
		nested := false
		state.core.subscribeOps(func(_ context.Context, _ []Op, sequence int) {
			if sequence != 1 || nested {
				return
			}
			nested = true
			source.commit(valueDoc(2), setValueOps(2))
			if _, err := state.Subscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
				lateDeliveries = append(lateDeliveries, late{delivery.Kind, delivery.Sequence, value["value"].(float64)})
			}); err != nil {
				t.Error(err)
			}
		})
		if _, err := state.Subscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				received = append(received, value["value"].(float64))
			}
		}); err != nil {
			t.Fatal(err)
		}
		source.commit(valueDoc(1), setValueOps(1))
		if !reflect.DeepEqual(received, []float64{1, 2}) || !reflect.DeepEqual(lateDeliveries, []late{{DeliveryHydrate, 2, 2}}) {
			t.Fatalf("received=%v late=%v", received, lateDeliveries)
		}
	})

	t.Run("reports listener failures without throwing them into the source", func(t *testing.T) {
		source := newTestSource(valueDoc(0), 0)
		var errs []error
		state := attach(t, source, ReplicatedStateSourceOptions{OnError: func(err error) { errs = append(errs, err) }})
		var received []float64
		if _, err := state.Subscribe(func(_ docState, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				panic(errors.New("listener failed"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := state.Subscribe(func(value docState, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				received = append(received, value["value"].(float64))
			}
		}); err != nil {
			t.Fatal(err)
		}
		source.commit(valueDoc(1), setValueOps(1))
		source.commit(valueDoc(2), setValueOps(2))
		messages := make([]string, len(errs))
		for at, err := range errs {
			messages[at] = err.Error()
		}
		if !reflect.DeepEqual(received, []float64{1, 2}) || !reflect.DeepEqual(messages, []string{"listener failed", "listener failed"}) {
			t.Fatalf("received=%v errors=%v", received, messages)
		}
	})

	t.Run("reports cursor gaps, disposes the broken attachment, and ignores later frames", func(t *testing.T) {
		source := newTestSource(valueDoc(0), 5)
		var errs []error
		state := attach(t, source, ReplicatedStateSourceOptions{OnError: func(err error) { errs = append(errs, err) }})
		source.commitAt(valueDoc(2), setValueOps(2), context.Background(), 7)
		if len(errs) == 0 || !strings.Contains(errs[0].Error(), "expected 6, received 7") {
			t.Fatalf("errors = %v", errs)
		}
		if len(source.attachments) != 0 || !reflect.DeepEqual(state.Value(), docState{"value": 0.0}) {
			t.Fatal("broken attachment was not disposed or value changed")
		}
		source.commitAt(valueDoc(3), setValueOps(3), context.Background(), 8)
		if !reflect.DeepEqual(state.Value(), docState{"value": 0.0}) {
			t.Fatal("a frame after the failure was published")
		}
	})

	t.Run("keeps attachments independent and disposes each idempotently", func(t *testing.T) {
		source := newTestSource(valueDoc(0), 0)
		first, second := attach(t, source, ReplicatedStateSourceOptions{}), attach(t, source, ReplicatedStateSourceOptions{})
		if len(source.attachments) != 2 {
			t.Fatalf("attachments = %d", len(source.attachments))
		}
		source.commit(valueDoc(1), setValueOps(1))
		if first.Value()["value"] != 1.0 || second.Value()["value"] != 1.0 {
			t.Fatal("frame did not reach both attachments")
		}
		first.Dispose()
		first.Dispose()
		if len(source.attachments) != 1 {
			t.Fatalf("attachments = %d", len(source.attachments))
		}
		source.commit(valueDoc(2), setValueOps(2))
		if first.Value()["value"] != 1.0 || second.Value()["value"] != 2.0 {
			t.Fatalf("first=%v second=%v", first.Value(), second.Value())
		}
		second.Dispose()
		if len(source.attachments) != 0 {
			t.Fatal("attachments remain")
		}
	})
}

func TestChangeWorkAfterTheCallbackReturnsCannotPublish(t *testing.T) {
	// Equivalence port of state.test.ts:49 "rejects PromiseLike callbacks and aborts their draft". A Go change callback is
	// synchronous by signature, so it cannot return a Promise. The invariant is that the draft settles when the callback
	// returns: a callback that reports unfinished work (an error) aborts its draft, the value keeps its previous
	// identity, and an edit made to the draft after the callback returned never reaches the published value.
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		result  error
		publish string
	}{
		{"an unfinished callback aborts its draft", errors.New("change callback did not finish synchronously"), `{"value":0}`},
		{"a finished callback publishes only what it wrote before returning", nil, `{"value":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newDocState(t, docState{"value": 0.0})
			previous := stored(state)
			returned, late := make(chan struct{}), make(chan struct{})
			err := state.Change(ctx, func(draft docState) error {
				draft["value"] = 1.0
				go func() {
					defer close(late)
					<-returned
					draft["value"] = 2.0
					draft["late"] = true
				}()
				return test.result
			})
			close(returned)
			<-late
			if !errors.Is(err, test.result) {
				t.Fatalf("Change returned %v, want %v", err, test.result)
			}
			if test.result != nil && container(stored(state)) != container(previous) {
				t.Fatal("an aborted change must keep the previous value identity")
			}
			if got, _ := json.Marshal(stored(state)); string(got) != test.publish {
				t.Fatalf("published %s, want %s", got, test.publish)
			}
		})
	}
}
