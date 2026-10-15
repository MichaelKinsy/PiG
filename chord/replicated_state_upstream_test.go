package chord

// Ports packages/chord/test/replicated-state.test.ts at Pi 0104fef1.

import (
	"context"
	"errors"
	"strings"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// captureUncaught replaces the uncaught reporter for one test, as replicated-state.test.ts spies on queueMicrotask.
func captureUncaught(t *testing.T) *[]error {
	t.Helper()
	var reports []error
	previous := SetUncaughtErrorReporter(func(err error) { reports = append(reports, err) })
	t.Cleanup(func() { SetUncaughtErrorReporter(previous) })
	return &reports
}

func valueOf(object *delta.JsonObject) any { return object.Value("value") }

func TestTransactionalReplicatedState(t *testing.T) {
	background := chordctx.Background()

	t.Run("publishes one immutable structurally shared revision", func(t *testing.T) {
		state := NewReplicatedState(delta.JsonObjectOf("changed", delta.JsonObjectOf("value", float64(1)), "retained", delta.JsonObjectOf("value", float64(2))))
		previous := state.Value()
		var deliveries []int
		state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			deliveries = append(deliveries, delivery.Sequence)
		})
		if err := state.Change(background, func(draft *delta.Object) error {
			if err := draft.Object("changed").Set("value", float64(3)); err != nil {
				return err
			}
			return draft.Object("changed").Set("value", float64(4))
		}); err != nil {
			t.Fatal(err)
		}
		value := state.Value()
		if got := mustJSON(t, value); got != `{"changed":{"value":4},"retained":{"value":2}}` {
			t.Fatalf("value %s", got)
		}
		if value == previous || value.Value("changed") == previous.Value("changed") {
			t.Fatal("the changed path was not copied")
		}
		if value.Value("retained") != previous.Value("retained") {
			t.Fatal("the unchanged subtree is not shared")
		}
		if !equalInts(deliveries, []int{0, 1}) {
			t.Fatalf("deliveries %v", deliveries)
		}
	})

	t.Run("rolls back callback failures and revokes escaped drafts", func(t *testing.T) {
		state := NewReplicatedState(delta.JsonObjectOf("nested", delta.JsonObjectOf("value", float64(1))))
		previous := state.Value()
		var escaped *delta.Object
		err := state.Change(background, func(draft *delta.Object) error {
			escaped = draft.Object("nested")
			if err := escaped.Set("value", float64(2)); err != nil {
				return err
			}
			return errors.New("stop")
		})
		if err == nil || err.Error() != "stop" {
			t.Fatalf("err = %v, want stop", err)
		}
		if state.Value() != previous {
			t.Fatal("the failed change was published")
		}
		func() {
			defer func() {
				if recovered := recover(); !errors.Is(toError(recovered), delta.ErrRevoked) {
					t.Fatalf("escaped draft read: %v, want ErrRevoked", recovered)
				}
			}()
			escaped.Get("value")
		}()
	})

	t.Run("rolls back a panicking callback and returns the panic", func(t *testing.T) {
		state := NewReplicatedState(delta.JsonObjectOf("value", float64(0)))
		previous := state.Value()
		err := state.Change(background, func(draft *delta.Object) error {
			_ = draft.Set("value", float64(1))
			panic("stop")
		})
		if err == nil || err.Error() != "stop" || state.Value() != previous {
			t.Fatalf("err = %v, value changed %v", err, state.Value() != previous)
		}
	})

	t.Run("rejects nested changes and replacements without losing the outer rollback", func(t *testing.T) {
		state := NewReplicatedState(delta.JsonObjectOf("left", float64(0), "right", float64(0)))
		err := state.Change(background, func(draft *delta.Object) error {
			_ = draft.Set("left", float64(1))
			return state.Change(background, func(nested *delta.Object) error { return nested.Set("right", float64(2)) })
		})
		if err == nil || !strings.Contains(err.Error(), "reentrantly") {
			t.Fatalf("nested change err = %v", err)
		}
		if got := mustJSON(t, state.Value()); got != `{"left":0,"right":0}` {
			t.Fatalf("value %s", got)
		}
		err = state.Change(background, func(*delta.Object) error {
			return state.Replace(background, delta.JsonObjectOf("left", float64(1), "right", float64(2)))
		})
		if err == nil || !strings.Contains(err.Error(), "change callback") {
			t.Fatalf("nested replace err = %v", err)
		}
		if got := mustJSON(t, state.Value()); got != `{"left":0,"right":0}` {
			t.Fatalf("value %s", got)
		}
	})

	t.Run("queues listener-triggered changes in sequence order", func(t *testing.T) {
		state := NewReplicatedState(delta.JsonObjectOf("value", float64(0)))
		type entry struct {
			value    any
			sequence int
		}
		var deliveries []entry
		var late []ReplicatedStateDelivery
		nested := false
		state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind != DeliveryUpdate || nested {
				return
			}
			nested = true
			if err := state.Change(background, func(draft *delta.Object) error { return draft.Set("value", float64(2)) }); err != nil {
				t.Error(err)
			}
			state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
				late = append(late, delivery)
			})
		})
		state.Subscribe(func(value *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				deliveries = append(deliveries, entry{valueOf(value), delivery.Sequence})
			}
		})
		if err := state.Change(background, func(draft *delta.Object) error { return draft.Set("value", float64(1)) }); err != nil {
			t.Fatal(err)
		}
		if len(deliveries) != 2 || deliveries[0] != (entry{float64(1), 1}) || deliveries[1] != (entry{float64(2), 2}) {
			t.Fatalf("deliveries %v", deliveries)
		}
		if len(late) != 1 || late[0] != (ReplicatedStateDelivery{Kind: DeliveryHydrate, Sequence: 2}) {
			t.Fatalf("late deliveries %v", late)
		}
	})

	// 0104fef1: a mutable state has no exact-op subscription, so a listener failure never reaches Change; it goes to the
	// asynchronous reporter after the revision committed, and the other subscribers still receive it.
	t.Run("isolates listener failures after committing the revision", func(t *testing.T) {
		reports := captureUncaught(t)
		state := NewReplicatedState(delta.JsonObjectOf("value", float64(0)))
		var received []int
		var order []string
		state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				order = append(order, "failing listener")
				panic(errors.New("listener failed"))
			}
		})
		state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			order = append(order, "second listener")
			received = append(received, delivery.Sequence)
		})
		order = nil
		err := state.Change(background, func(draft *delta.Object) error { return draft.Set("value", float64(1)) })
		order = append(order, "change returned")
		if err != nil {
			t.Fatalf("Change returned the listener failure: %v", err)
		}
		if got := mustJSON(t, state.Value()); got != `{"value":1}` {
			t.Fatalf("value %s", got)
		}
		if !equalInts(received, []int{0, 1}) {
			t.Fatalf("received %v", received)
		}
		if len(*reports) != 1 || (*reports)[0].Error() != "listener failed" {
			t.Fatalf("reports %v", *reports)
		}
		if strings.Join(order, ",") != "failing listener,second listener,change returned" {
			t.Fatalf("order %v", order)
		}
	})

	t.Run("copies assigned values by value", func(t *testing.T) {
		external := delta.JsonObjectOf("value", float64(1))
		state := NewReplicatedState(delta.JsonObjectOf("left", nil, "right", nil))
		if err := state.Change(background, func(draft *delta.Object) error {
			if err := draft.Set("left", external); err != nil {
				return err
			}
			if err := draft.Set("right", external); err != nil {
				return err
			}
			return draft.Object("left").Set("value", float64(2))
		}); err != nil {
			t.Fatal(err)
		}
		if external.Value("value") != float64(1) {
			t.Fatal("the external value was mutated")
		}
		if got := mustJSON(t, state.Value()); got != `{"left":{"value":2},"right":{"value":1}}` {
			t.Fatalf("value %s", got)
		}
		if state.Value().Value("left") == state.Value().Value("right") {
			t.Fatal("left and right alias")
		}
	})

	t.Run("takes immutable ownership of alias-free replacements", func(t *testing.T) {
		state := NewReplicatedState(delta.JsonObjectOf("left", delta.JsonObjectOf("value", float64(1)), "right", delta.JsonObjectOf("value", float64(2))))
		left := state.Value().Value("left").(*delta.JsonObject)
		replacement := delta.JsonObjectOf("left", left.Clone(), "right", left.Clone())
		if err := state.Replace(background, replacement); err != nil {
			t.Fatal(err)
		}
		if state.Value() != replacement {
			t.Fatal("the replacement was not adopted as is")
		}
		if state.Value().Value("left") == state.Value().Value("right") {
			t.Fatal("left and right alias")
		}
		if err := state.Change(background, func(draft *delta.Object) error { return draft.Object("left").Set("value", float64(9)) }); err != nil {
			t.Fatal(err)
		}
		if got := mustJSON(t, state.Value()); got != `{"left":{"value":9},"right":{"value":1}}` {
			t.Fatalf("value %s", got)
		}
	})

	t.Run("replaces atomically and ignores deeply equal replacements", func(t *testing.T) {
		state := NewReplicatedState(delta.JsonObjectOf("value", delta.JsonObjectOf("nested", float64(1)), "retained", delta.JsonObjectOf("nested", float64(2))))
		previous := state.Value()
		if err := state.Replace(background, delta.JsonObjectOf("value", delta.JsonObjectOf("nested", float64(1)), "retained", delta.JsonObjectOf("nested", float64(2)))); err != nil {
			t.Fatal(err)
		}
		if state.Value() != previous {
			t.Fatal("a deeply equal replacement was adopted")
		}
		if err := state.Replace(background, delta.JsonObjectOf("value", delta.JsonObjectOf("nested", float64(2)), "retained", previous.Value("retained"))); err != nil {
			t.Fatal(err)
		}
		if got := mustJSON(t, state.Value()); got != `{"value":{"nested":2},"retained":{"nested":2}}` {
			t.Fatalf("value %s", got)
		}
		if state.Value().Value("retained") != previous.Value("retained") {
			t.Fatal("the retained subtree is not shared")
		}
	})
}

// testSource is replicated-state.test.ts TestSource: attachments buffer frames until activation.
type testSource[T any] struct {
	attachments []*testAttachment[T]
	onAttach    func()
	value       T
	cursor      int
}

type testAttachment[T any] struct {
	snapshot  ReplicatedStateSnapshot[T]
	onDispose func()
	buffer    []ReplicatedStateSourceFrame[T]
	listener  func(ReplicatedStateSourceFrame[T])
	activated bool
	disposed  bool
}

func (source *testSource[T]) Attach() (ReplicatedStateSourceAttachment[T], error) {
	attachment := &testAttachment[T]{snapshot: ReplicatedStateSnapshot[T]{Value: source.value, Cursor: source.cursor}}
	attachment.onDispose = func() {
		for index, candidate := range source.attachments {
			if candidate == attachment {
				source.attachments = append(source.attachments[:index:index], source.attachments[index+1:]...)
				return
			}
		}
	}
	source.attachments = append(source.attachments, attachment)
	if source.onAttach != nil {
		source.onAttach()
	}
	return attachment, nil
}

func (source *testSource[T]) commit(value T, ops []delta.Op) {
	source.commitAt(value, ops, source.cursor+1)
}

func (source *testSource[T]) commitAt(value T, ops []delta.Op, cursor int) {
	source.value, source.cursor = value, cursor
	frame := ReplicatedStateSourceFrame[T]{Cursor: cursor, Value: value, Ops: ops, Context: chordctx.Background()}
	for _, attachment := range append([]*testAttachment[T](nil), source.attachments...) {
		attachment.publish(frame)
	}
}

func (attachment *testAttachment[T]) Snapshot() ReplicatedStateSnapshot[T] {
	return attachment.snapshot
}

func (attachment *testAttachment[T]) Activate(listener func(ReplicatedStateSourceFrame[T])) error {
	if attachment.activated {
		return errors.New("attachment is already active")
	}
	attachment.activated = true
	attachment.listener = listener
	buffered := attachment.buffer
	attachment.buffer = nil
	for _, frame := range buffered {
		listener(frame)
	}
	return nil
}

func (attachment *testAttachment[T]) publish(frame ReplicatedStateSourceFrame[T]) {
	if attachment.disposed {
		return
	}
	if attachment.listener == nil {
		attachment.buffer = append(attachment.buffer, frame)
	} else {
		attachment.listener(frame)
	}
}

func (attachment *testAttachment[T]) Dispose() {
	if attachment.disposed {
		return
	}
	attachment.disposed = true
	attachment.buffer = nil
	attachment.onDispose()
}

func setOp(value float64) []delta.Op { return []delta.Op{{"s", []any{"value"}, value}} }

func TestAuthoritativeReplicatedStateSources(t *testing.T) {
	number := func(value float64) *delta.JsonObject { return delta.JsonObjectOf("value", value) }

	t.Run("captures before activation and drains queued commits in order", func(t *testing.T) {
		first, second := number(1), number(2)
		source := &testSource[*delta.JsonObject]{value: number(0), cursor: 10}
		source.onAttach = func() {
			source.commit(first, setOp(1))
			source.commit(second, setOp(2))
		}
		state := mustAttach(t, source, ReplicatedStateSourceOptions{})
		type entry struct {
			value    *delta.JsonObject
			sequence int
		}
		var deliveries []entry
		state.Subscribe(func(value *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			deliveries = append(deliveries, entry{value, delivery.Sequence})
		})
		if state.Value() != second || len(deliveries) != 1 || deliveries[0] != (entry{second, 2}) {
			t.Fatalf("value %v, deliveries %v", state.Value(), deliveries)
		}
	})

	t.Run("hydrates at sequence zero when attaching after existing commits", func(t *testing.T) {
		current := number(1)
		source := &testSource[*delta.JsonObject]{value: number(0), cursor: 40}
		source.commit(current, setOp(1))
		state := mustAttach(t, source, ReplicatedStateSourceOptions{})
		var deliveries []int
		state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			deliveries = append(deliveries, delivery.Sequence)
		})
		if state.Value() != current || !equalInts(deliveries, []int{0}) {
			t.Fatalf("value %v, deliveries %v", state.Value(), deliveries)
		}
	})

	t.Run("publishes exact source values without applying or re-diffing", func(t *testing.T) {
		source := &testSource[*delta.JsonObject]{value: number(0)}
		state := mustAttach(t, source, ReplicatedStateSourceOptions{})
		next := number(1)
		var published *delta.JsonObject
		state.Subscribe(func(value *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				published = value
			}
		})
		// Deliberately not the operation that produces next: Chord must publish the source value as is.
		source.commit(next, setOp(7))
		if state.Value() != next || published != next {
			t.Fatalf("value %v, published %v", state.Value(), published)
		}
	})

	t.Run("buffers reentrant frames and skips updates covered by a late hydration", func(t *testing.T) {
		source := &testSource[*delta.JsonObject]{value: number(0)}
		state := mustAttach(t, source, ReplicatedStateSourceOptions{})
		var received []any
		type entry struct {
			kind     DeliveryKind
			sequence int
			value    any
		}
		var late []entry
		nested := false
		state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind != DeliveryUpdate || delivery.Sequence != 1 || nested {
				return
			}
			nested = true
			source.commit(number(2), setOp(2))
			state.Subscribe(func(value *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
				late = append(late, entry{delivery.Kind, delivery.Sequence, valueOf(value)})
			})
		})
		state.Subscribe(func(value *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				received = append(received, valueOf(value))
			}
		})
		source.commit(number(1), setOp(1))
		if len(received) != 2 || received[0] != float64(1) || received[1] != float64(2) {
			t.Fatalf("received %v", received)
		}
		if len(late) != 1 || late[0] != (entry{DeliveryHydrate, 2, float64(2)}) {
			t.Fatalf("late %v", late)
		}
	})

	t.Run("reports listener failures without throwing them into the source", func(t *testing.T) {
		source := &testSource[*delta.JsonObject]{value: number(0)}
		var reported []string
		state := mustAttach(t, source, ReplicatedStateSourceOptions{OnError: func(err error) { reported = append(reported, err.Error()) }})
		var received []any
		state.Subscribe(func(_ *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				panic(errors.New("listener failed"))
			}
		})
		state.Subscribe(func(value *delta.JsonObject, _ context.Context, delivery ReplicatedStateDelivery) {
			if delivery.Kind == DeliveryUpdate {
				received = append(received, valueOf(value))
			}
		})
		source.commit(number(1), setOp(1))
		source.commit(number(2), setOp(2))
		if len(received) != 2 || received[0] != float64(1) || received[1] != float64(2) {
			t.Fatalf("received %v", received)
		}
		if strings.Join(reported, "|") != "listener failed|listener failed" {
			t.Fatalf("reported %v", reported)
		}
	})

	t.Run("reports cursor gaps, disposes the broken attachment, and ignores later frames", func(t *testing.T) {
		source := &testSource[*delta.JsonObject]{value: number(0), cursor: 5}
		var reported []error
		state := mustAttach(t, source, ReplicatedStateSourceOptions{OnError: func(err error) { reported = append(reported, err) }})
		source.commitAt(number(2), setOp(2), 7)
		if len(reported) == 0 || !strings.Contains(reported[0].Error(), "expected 6, received 7") {
			t.Fatalf("reported %v", reported)
		}
		if len(source.attachments) != 0 {
			t.Fatal("the broken attachment was not disposed")
		}
		if valueOf(state.Value()) != float64(0) {
			t.Fatalf("value %v", state.Value())
		}
		source.commitAt(number(3), setOp(3), 8)
		if valueOf(state.Value()) != float64(0) {
			t.Fatalf("a later frame was published: %v", state.Value())
		}
	})

	t.Run("keeps attachments independent and disposes each idempotently", func(t *testing.T) {
		source := &testSource[*delta.JsonObject]{value: number(0)}
		first := mustAttach(t, source, ReplicatedStateSourceOptions{})
		second := mustAttach(t, source, ReplicatedStateSourceOptions{})
		if len(source.attachments) != 2 {
			t.Fatalf("attachments %d", len(source.attachments))
		}
		source.commit(number(1), setOp(1))
		if valueOf(first.Value()) != float64(1) || valueOf(second.Value()) != float64(1) {
			t.Fatal("both attachments see the first commit")
		}
		first.Dispose()
		first.Dispose()
		if len(source.attachments) != 1 {
			t.Fatalf("attachments %d after disposing the first twice", len(source.attachments))
		}
		source.commit(number(2), setOp(2))
		if valueOf(first.Value()) != float64(1) || valueOf(second.Value()) != float64(2) {
			t.Fatal("a disposed attachment followed the source")
		}
		second.Dispose()
		if len(source.attachments) != 0 {
			t.Fatalf("attachments %d", len(source.attachments))
		}
	})
}

func mustAttach[T any](t *testing.T, source ReplicatedStateSource[T], options ReplicatedStateSourceOptions) *AttachedReplicatedState[T] {
	t.Helper()
	state, err := AttachReplicatedStateSource(source, options)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func mustJSON(t *testing.T, value *delta.JsonObject) string {
	t.Helper()
	encoded, err := value.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
