package session_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-watches.test.ts

var watchStateDoc = defineDoc("watch.state", 1, sessionScope, func() obj {
	return delta.JsonObjectOf("value", 0, "items", []any{"a", "b"}, "retained", delta.JsonObjectOf("label", "stable"))
})

var watchKeysDoc = defineDoc("watch.keys", 1, sessionScope, func() obj {
	return delta.JsonObjectOf("keys", delta.JsonObjectOf())
})

func createWatchHarness(t *testing.T) sessiontest.Harness {
	t.Helper()
	harness := open()
	commit(t, harness.Session, func(tx durable.Tx) error {
		_, err := tx.Doc(watchStateDoc)
		return err
	})
	return harness
}

func watchDoc(t *testing.T, harness sessiontest.Harness, watchCtx context.Context, token durable.AnyDocToken) durable.WatchHandle[obj] {
	t.Helper()
	watch, err := harness.Session.WatchDocErased(watchCtx, token)
	if err != nil {
		t.Fatalf("watchDoc: %v", err)
	}
	return watch
}

func setWatchValue(t *testing.T, harness sessiontest.Harness, value any) {
	t.Helper()
	setStateValue(t, harness, watchStateDoc, value)
}

func setStateValue(t *testing.T, harness sessiontest.Harness, token durable.AnyDocToken, value any) {
	t.Helper()
	commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, token).Set("value", value) })
}

type watchDelivery struct {
	value obj
	ops   []durable.Op
}

// changeRecorder records each change a watch delivers, retirement flag included.
type changeRecorder struct {
	mu    sync.Mutex
	items []durable.WatchChange[obj]
}

func (recorder *changeRecorder) listener() func(context.Context, durable.WatchChange[obj]) error {
	return func(_ context.Context, change durable.WatchChange[obj]) error {
		recorder.mu.Lock()
		recorder.items = append(recorder.items, change)
		recorder.mu.Unlock()
		return nil
	}
}

func (recorder *changeRecorder) all() []durable.WatchChange[obj] {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]durable.WatchChange[obj](nil), recorder.items...)
}

type watchRecorder struct {
	mu    sync.Mutex
	items []watchDelivery
}

func (recorder *watchRecorder) record(value obj, ops []durable.Op) {
	recorder.mu.Lock()
	recorder.items = append(recorder.items, watchDelivery{value, ops})
	recorder.mu.Unlock()
}

func (recorder *watchRecorder) all() []watchDelivery {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]watchDelivery(nil), recorder.items...)
}

func (recorder *watchRecorder) values() []any {
	var values []any
	for _, delivery := range recorder.all() {
		if delivery.value == nil {
			values = append(values, -1)
			continue
		}
		values = append(values, delivery.value.Value("value"))
	}
	return values
}

func closedEnd(t *testing.T, watch durable.WatchHandle[obj]) durable.WatchEnd {
	t.Helper()
	waitClosed(t, watch.Closed())
	return watch.End()
}

func TestSessionDocumentWatches(t *testing.T) {
	t.Run("never creates an absent document", func(t *testing.T) {
		harness := open()
		commits := len(harness.Storage.Commits())
		if watchDoc(t, harness, ctx, watchStateDoc) != nil {
			t.Fatal("an absent document has no watch")
		}
		if len(harness.Storage.Commits()) != commits || harness.Storage.MintCount() != 0 {
			t.Fatal("watchDoc never creates")
		}
	})

	t.Run("keeps the acquisition revision until start, then delivers the commits since in one call", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		initial := watch.Value()
		setWatchValue(t, harness, 1)
		flush(harness)
		firstPublished := lastPublishedDocument(harness)
		setWatchValue(t, harness, 2)
		flush(harness)
		secondPublished := lastPublishedDocument(harness)
		if !same(watch.Value(), initial) {
			t.Fatal("the value stays at acquisition before start")
		}
		var mu sync.Mutex
		inline := true
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			mu.Lock()
			wasInline := inline
			mu.Unlock()
			if wasInline {
				t.Error("the listener never runs inline")
			}
			if !same(watch.Value(), value) {
				t.Error("the watch value is the delivered value")
			}
			recorder.record(value, ops)
			return nil
		})
		mu.Lock()
		inline = false
		mu.Unlock()
		flush(harness)
		deliveries := recorder.all()
		// Both commits set value: folded, the second set replaces the first.
		expectEqual(t, firstPublished.Ops, []any{[]any{"s", []any{"value"}, 1}})
		expectEqual(t, recorder.values(), []any{2})
		if len(deliveries) != 1 || !same(deliveries[0].value, secondPublished.Value) {
			t.Fatal("the delivery is the newest published value")
		}
		expectEqual(t, deliveries[0].ops, secondPublished.Ops)
		expectEqual(t, initial.Value("value"), 0)

		// A listener that keeps up gets each commit's ops as published.
		setWatchValue(t, harness, 3)
		flush(harness)
		thirdPublished := lastPublishedDocument(harness)
		deliveries = recorder.all()
		if len(deliveries) != 2 || !same(deliveries[1].value, thirdPublished.Value) || !same(deliveries[1].ops, thirdPublished.Ops) {
			t.Fatalf("deliveries %v", recorder.values())
		}
		_, _ = watch.Stop()
	})

	t.Run("serializes callbacks and folds the commits made while one is in flight into the next", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		entered := make(chan struct{})
		release := make(chan struct{})
		var mu sync.Mutex
		var values []any
		var batches []any
		active, maxActive := 0, 0
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			mu.Lock()
			active++
			maxActive = max(maxActive, active)
			values = append(values, value.Value("value"))
			batches = append(batches, ops)
			first := len(values) == 1
			mu.Unlock()
			if first {
				close(entered)
				<-release
			}
			mu.Lock()
			active--
			mu.Unlock()
			return nil
		})
		setWatchValue(t, harness, 1)
		waitClosed(t, entered)
		for value := 2; value <= 20; value++ {
			setWatchValue(t, harness, value)
		}
		mu.Lock()
		expectEqual(t, values, []any{1})
		mu.Unlock()
		close(release)
		flush(harness)
		mu.Lock()
		defer mu.Unlock()
		if maxActive != 1 {
			t.Fatal("callbacks are serialized")
		}
		expectEqual(t, values, []any{1, 20})
		expectEqual(t, batches[1], []durable.Op{{"s", []any{"value"}, 20}})
		_, _ = watch.Stop()
	})

	t.Run("allows a listener to initiate a later Session commit", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		completed := make(chan struct{})
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			recorder.record(value, ops)
			if num(value.Value("value")) == 1 {
				setWatchValue(t, harness, 2)
			} else {
				close(completed)
			}
			return nil
		})
		setWatchValue(t, harness, 1)
		waitClosed(t, completed)
		expectEqual(t, recorder.values(), []any{1, 2})
		_, _ = watch.Stop()
	})

	t.Run("folds 101 pending commits of one field into one set", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		for value := 1; value <= 101; value++ {
			setWatchValue(t, harness, value)
		}
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			recorder.record(value, ops)
			return nil
		})
		flush(harness)
		deliveries := recorder.all()
		if len(deliveries) != 1 || num(deliveries[0].value.Value("value")) != 101 {
			t.Fatalf("deliveries %v", recorder.values())
		}
		expectEqual(t, deliveries[0].ops, []durable.Op{{"s", []any{"value"}, 101}})
		_, _ = watch.Stop()
	})

	t.Run("replaces the root once more than 10,000 ops are pending after folding", func(t *testing.T) {
		harness := open()
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(watchKeysDoc)
			return err
		})
		watch := watchDoc(t, harness, ctx, watchKeysDoc)
		// Sets of distinct keys, in commits small enough to stay ops: 4,000 + 4,000 + 2,001 pending ops.
		for _, span := range [][2]int{{0, 4_000}, {4_000, 8_000}, {8_000, 10_001}} {
			commit(t, harness.Session, func(tx durable.Tx) error {
				keys := mustDoc(t, tx, watchKeysDoc).Object("keys")
				for index := span[0]; index < span[1]; index++ {
					if err := keys.Set(fmt.Sprintf("k%d", index), index); err != nil {
						return err
					}
				}
				return nil
			})
		}
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			recorder.record(value, ops)
			return nil
		})
		flush(harness)
		deliveries := recorder.all()
		if len(deliveries) != 1 || !same(deliveries[0].value, watch.Value()) {
			t.Fatalf("deliveries %d", len(deliveries))
		}
		expectEqual(t, deliveries[0].ops, []any{[]any{"r", watch.Value()}})
		if keys, _ := watch.Value().Value("keys").(*delta.JsonObject); keys == nil || keys.Len() != 10_001 {
			t.Fatal("the newest value holds all 10,001 keys")
		}
		_, _ = watch.Stop()
	})

	t.Run("never folds the in-flight frame into the next call", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		entered := make(chan struct{})
		release := make(chan struct{})
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			recorder.record(value, ops)
			if len(recorder.all()) == 1 {
				close(entered)
				<-release
			}
			return nil
		})
		setWatchValue(t, harness, 1)
		waitClosed(t, entered)
		for value := 2; value <= 102; value++ {
			setWatchValue(t, harness, value)
		}
		close(release)
		flush(harness)
		deliveries := recorder.all()
		if len(deliveries) != 2 || num(deliveries[0].value.Value("value")) != 1 || num(deliveries[1].value.Value("value")) != 102 {
			t.Fatalf("deliveries %v", recorder.values())
		}
		expectEqual(t, deliveries[1].ops, []durable.Op{{"s", []any{"value"}, 102}})
		_, _ = watch.Stop()
	})

	t.Run("folds a retirement into the pending commits' call, which reports it with the last value, and goes on", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		for value := 1; value <= 100; value++ {
			setWatchValue(t, harness, value)
		}
		last := snapshot(t, harness.Session, watchStateDoc)
		commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
		recorder := &changeRecorder{}
		watch.Start(recorder.listener())
		flush(harness)
		changes := recorder.all()
		if len(changes) != 1 || !changes[0].Retired || !same(changes[0].Value, last) {
			t.Fatalf("changes %v", changes)
		}
		expectEqual(t, changes[0].Ops, []durable.Op{{"s", []any{"value"}, 100}})
		if !same(watch.Value(), last) {
			t.Fatal("the watch value is the last value")
		}
		commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, watchStateDoc).Set("value", 7) })
		flush(harness)
		changes = recorder.all()
		recreated := delta.JsonObjectOf("value", 7, "items", []any{"a", "b"}, "retained", delta.JsonObjectOf("label", "stable"))
		if len(changes) != 2 || changes[1].Retired {
			t.Fatalf("changes %v", changes)
		}
		expectEqual(t, changes[1].Value, recreated)
		expectEqual(t, changes[1].Ops, []durable.Op{{"r", recreated}})
		_, _ = watch.Stop()
	})

	t.Run("delivers replayable structural no-op commits instead of suppressing them", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		initial := watch.Value()
		commit(t, harness.Session, func(tx durable.Tx) error {
			items := mustDoc(t, tx, watchStateDoc).Array("items")
			_, err := items.Unshift(items.Shift())
			return err
		})
		newest := snapshot(t, harness.Session, watchStateDoc)
		if same(newest, initial) || !equal(newest, initial) {
			t.Fatal("a structural no-op is a new equal revision")
		}
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			if !same(value, newest) {
				t.Error("the frame value is the newest revision")
			}
			recorder.record(value, ops)
			return nil
		})
		flush(harness)
		deliveries := recorder.all()
		if len(deliveries) != 1 || len(deliveries[0].ops) == 0 {
			t.Fatal("one nonempty batch")
		}
		_, _ = watch.Stop()
	})

	t.Run("preserves commit Context values without inheriting producer cancellation", func(t *testing.T) {
		type watchKey struct{}
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		parent, cancelParent := context.WithCancelCause(context.Background())
		commitContext := context.WithValue(parent, watchKey{}, "newest-commit")
		entered := make(chan struct{})
		release := make(chan struct{})
		deliveryContext := make(chan context.Context, 1)
		watch.Start(func(delivered context.Context, change durable.WatchChange[obj]) error {
			deliveryContext <- delivered
			close(entered)
			<-release
			return nil
		})
		_, err := harness.Session.Commit(commitContext, func(tx durable.Tx) (any, error) {
			return nil, mustDoc(t, tx, watchStateDoc).Set("value", 1)
		})
		must(t, err)
		waitClosed(t, entered)
		delivered := <-deliveryContext
		if delivered.Value(watchKey{}) != "newest-commit" || delivered.Done() != nil {
			t.Fatal("the delivery keeps commit values without cancellation")
		}
		cancelParent(errors.New("caller finished"))
		end, _ := watch.Stop()
		expectEqual(t, end, durable.WatchEnd{Reason: durable.WatchStopped})
		if delivered.Done() != nil || delivered.Err() != nil {
			t.Fatal("producer cancellation does not reach the delivery")
		}
		close(release)
	})

	t.Run("keeps earlier immutable revisions stable", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		initial := watch.Value()
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			recorder.record(value, ops)
			return nil
		})
		setWatchValue(t, harness, 1)
		flush(harness)
		delivered := recorder.all()[0].value
		if !same(delivered, watch.Value()) || same(delivered, initial) || !same(delivered.Value("retained"), initial.Value("retained")) {
			t.Fatal("revisions are immutable and structurally shared")
		}
		expectEqual(t, initial.Value("value"), 0)
		_, _ = watch.Stop()
	})

	t.Run("reports a change and a retirement in one commit as the retirement, with the value it was given before", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		before := watch.Value()
		recorder := &changeRecorder{}
		watch.Start(recorder.listener())
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, mustDoc(t, tx, watchStateDoc).Set("value", 5))
			return tx.RetireDoc(watchStateDoc)
		})
		flush(harness)
		// Storage got the change, then the retirement; the commit publishes the retirement only.
		var types []string
		for _, write := range harness.Storage.LastCommit() {
			types = append(types, durable.StorageWriteType(write))
		}
		expectEqual(t, types, []string{"document.change", "document.retire"})
		changes := recorder.all()
		if len(changes) != 1 || !changes[0].Retired || len(changes[0].Ops) != 0 || !same(changes[0].Value, before) {
			t.Fatalf("changes %v", changes)
		}
		_, _ = watch.Stop()
	})

	t.Run("follows its address across a retirement and a recreation in one commit, in one call", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		recorder := &changeRecorder{}
		watch.Start(recorder.listener())
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, tx.RetireDoc(watchStateDoc))
			return mustDoc(t, tx, watchStateDoc).Set("value", 10)
		})
		flush(harness)
		recreated := snapshot(t, harness.Session, watchStateDoc)
		changes := recorder.all()
		if len(changes) != 1 || !changes[0].Retired || !equal(changes[0].Value, recreated) {
			t.Fatalf("changes %v", changes)
		}
		expectEqual(t, changes[0].Ops, []durable.Op{{"r", recreated}})
		setWatchValue(t, harness, 11)
		flush(harness)
		changes = recorder.all()
		if len(changes) != 2 || changes[1].Retired || !same(changes[1].Value, watch.Value()) {
			t.Fatalf("changes %v", changes)
		}
		expectEqual(t, changes[1].Ops, []durable.Op{{"s", []any{"value"}, 11}})
		expectEqual(t, watch.Value().Value("value"), 11)
		select {
		case <-watch.Closed():
			t.Fatalf("the watch ended: %+v", watch.End())
		default:
		}
		_, _ = watch.Stop()
	})

	t.Run("Session close discards retirement buffered before start", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		baseline := watch.Value()
		commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
		must(t, harness.Session.Close(ctx))
		expectEqual(t, closedEnd(t, watch), durable.WatchEnd{Reason: durable.WatchEnded})
		if !same(watch.Value(), baseline) {
			t.Fatal("the value stays at acquisition")
		}
		expectPanic(t, "stopped", func() { watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error { return nil }) })
	})

	t.Run("Session close discards retirement behind an in-flight callback", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		entered := make(chan struct{})
		release := make(chan struct{})
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			recorder.record(value, ops)
			close(entered)
			<-release
			return nil
		})
		setWatchValue(t, harness, 1)
		waitClosed(t, entered)
		commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
		must(t, harness.Session.Close(ctx))
		expectEqual(t, closedEnd(t, watch), durable.WatchEnd{Reason: durable.WatchEnded})
		close(release)
		flush(harness)
		expectEqual(t, recorder.values(), []any{1})
	})

	t.Run("supports idempotent stop and rejects repeated or late start", func(t *testing.T) {
		harness := createWatchHarness(t)
		started := watchDoc(t, harness, ctx, watchStateDoc)
		noop := func(_ context.Context, change durable.WatchChange[obj]) error { return nil }
		started.Start(noop)
		expectPanic(t, "already started", func() { started.Start(noop) })
		first, _ := started.Stop()
		second, _ := started.Stop()
		if first != second {
			t.Fatal("stop is idempotent")
		}
		expectEqual(t, first, durable.WatchEnd{Reason: durable.WatchStopped})
		stopped := watchDoc(t, harness, ctx, watchStateDoc)
		_, _ = stopped.Stop()
		expectPanic(t, "stopped", func() { stopped.Start(noop) })
	})

	t.Run("cancels acquisition without leaking a registered watch", func(t *testing.T) {
		harness := createWatchHarness(t)
		must(t, harness.Session.UnloadDocuments())
		gate := harness.Storage.HoldFindDocument()
		child, cancel := context.WithCancelCause(ctx)
		acquisition := make(chan error, 1)
		go func() {
			_, err := harness.Session.WatchDocErased(child, watchStateDoc)
			acquisition <- err
		}()
		waitClosed(t, gate.Entered())
		cancel(errors.New("cancel acquisition"))
		gate.Release()
		expectErrorContains(t, <-acquisition, "cancel acquisition")
		must(t, harness.Session.Close(ctx))
	})

	t.Run("cancels future delivery without aborting an in-flight callback", func(t *testing.T) {
		harness := createWatchHarness(t)
		child, cancel := context.WithCancel(ctx)
		watch := watchDoc(t, harness, child, watchStateDoc)
		entered := make(chan struct{})
		release := make(chan struct{})
		deliveryContext := make(chan context.Context, 1)
		watch.Start(func(delivered context.Context, change durable.WatchChange[obj]) error {
			deliveryContext <- delivered
			close(entered)
			<-release
			return nil
		})
		setWatchValue(t, harness, 1)
		waitClosed(t, entered)
		cancel()
		expectEqual(t, closedEnd(t, watch), durable.WatchEnd{Reason: durable.WatchCancelled})
		if (<-deliveryContext).Done() != nil {
			t.Fatal("the in-flight callback is not cancelled")
		}
		close(release)
	})

	t.Run("Session close stops future delivery without joining an in-flight callback", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		entered := make(chan struct{})
		release := make(chan struct{})
		deliveryContext := make(chan context.Context, 1)
		watch.Start(func(delivered context.Context, change durable.WatchChange[obj]) error {
			deliveryContext <- delivered
			close(entered)
			<-release
			return nil
		})
		setWatchValue(t, harness, 1)
		waitClosed(t, entered)
		must(t, harness.Session.Close(ctx))
		if (<-deliveryContext).Done() != nil {
			t.Fatal("the delivery carries no cancellation")
		}
		expectEqual(t, closedEnd(t, watch), durable.WatchEnd{Reason: durable.WatchEnded})
		close(release)
	})

	t.Run("settles listener failure on only the affected watch", func(t *testing.T) {
		harness := createWatchHarness(t)
		failed := watchDoc(t, harness, ctx, watchStateDoc)
		healthy := watchDoc(t, harness, ctx, watchStateDoc)
		failed.Start(func(_ context.Context, change durable.WatchChange[obj]) error { return errors.New("listener failed") })
		var mu sync.Mutex
		healthyCalls := 0
		healthy.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			mu.Lock()
			healthyCalls++
			mu.Unlock()
			return nil
		})
		setWatchValue(t, harness, 1)
		end := closedEnd(t, failed)
		if end.Reason != durable.WatchListenerError || end.Error == nil || end.Error.Error() != "listener failed" {
			t.Fatalf("end %+v", end)
		}
		flush(harness)
		mu.Lock()
		if healthyCalls != 1 {
			t.Fatal("the healthy watch continues")
		}
		mu.Unlock()
		_, _ = healthy.Stop()
	})

	t.Run("reports a listener's failure through the Session while it ends only that watch", func(t *testing.T) {
		// Pi chord watch.ts Watch #drain: `this.#report(error)` runs before `#terminate({ reason: "listener_error" })`.
		var mu sync.Mutex
		var reported []error
		harness := sessiontest.OpenReportingTestSession(func(err error) {
			mu.Lock()
			reported = append(reported, err)
			mu.Unlock()
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			_, err := tx.Doc(watchStateDoc)
			return err
		})
		failed := watchDoc(t, harness, ctx, watchStateDoc)
		failure := errors.New("listener failed")
		failed.Start(func(_ context.Context, change durable.WatchChange[obj]) error { return failure })
		setWatchValue(t, harness, 1)
		if end := closedEnd(t, failed); end.Reason != durable.WatchListenerError {
			t.Fatalf("end %+v", end)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(reported) != 1 || !errors.Is(reported[0], failure) {
			t.Fatalf("reported %v, want the listener's error once", reported)
		}
		if harness.Session.Failure() != nil {
			t.Fatalf("a watch listener's failure failed the Session: %v", harness.Session.Failure())
		}
	})

	t.Run("hydrates migration without writing and observes the later exact edit", func(t *testing.T) {
		old := defineDoc("watch.migration", 1, sessionScope, func() obj { return delta.JsonObjectOf("value", 3) })
		current := defineDoc("watch.migration", 2, sessionScope, func() obj { return delta.JsonObjectOf("value", 0, "migrated", false) },
			withMigrate(func(value obj, _ int) obj { return delta.JsonObjectOf("value", value.Value("value"), "migrated", true) }))
		harness := open()
		commit(t, harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(old); return err })
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		watch := watchDoc(t, harness, ctx, current)
		expectEqual(t, watch.Value(), delta.JsonObjectOf("value", 3, "migrated", true))
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("hydration writes nothing")
		}
		recorder := &watchRecorder{}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			recorder.record(value, ops)
			return nil
		})
		commit(t, harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(current); return err })
		flush(harness)
		if len(recorder.all()) != 0 {
			t.Fatal("a migration-only base changes nothing for the new shape")
		}
		setStateValue(t, harness, current, 4)
		flush(harness)
		expectEqual(t, recorder.values(), []any{4})
		_, _ = watch.Stop()
	})

	t.Run("can replay every delivered exact operation batch from the acquisition revision", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		var mu sync.Mutex
		var replica any = watch.Value()
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
			value, ops := change.Value, change.Ops
			mu.Lock()
			defer mu.Unlock()
			next, err := delta.ApplyImmutable(replica, ops)
			if err != nil {
				return err
			}
			replica = next
			if !equal(replica, value) {
				t.Error("the replica equals the delivered value")
			}
			return nil
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			state := mustDoc(t, tx, watchStateDoc)
			must(t, state.Set("value", 3))
			_, err := state.Array("items").Splice(0, 1)
			return err
		})
		flush(harness)
		mu.Lock()
		expectEqual(t, replica, watch.Value())
		mu.Unlock()
		_, _ = watch.Stop()
	})

	t.Run("continues after the tracker cache unloads", func(t *testing.T) {
		harness := createWatchHarness(t)
		watch := watchDoc(t, harness, ctx, watchStateDoc)
		baseline := watch.Value()
		reads := harness.Storage.DocumentReadCount()
		must(t, harness.Session.UnloadDocuments())
		reloaded := snapshot(t, harness.Session, watchStateDoc)
		if same(reloaded, baseline) || !equal(reloaded, baseline) || harness.Storage.DocumentReadCount() <= reads {
			t.Fatal("unload cold-loads a detached equal value")
		}
		watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error { return nil })
		setWatchValue(t, harness, 7)
		flush(harness)
		expectEqual(t, watch.Value().Value("value"), 7)
		_, _ = watch.Stop()
	})
}

// Upstream session.ts #runCommit publishes synchronously before its promise resolves, and chord watch.ts
// Watch.advance queues the drain microtask there, so a started watch with no callback in flight enters the
// listener with the committed frame, and watch.value is that frame, before the commit's caller resumes. A stop after
// the commit returns therefore cannot discard the frame (chord-guide.test.ts "runs the job output watch until the
// producer retires its document" stops right after the producer's last commit).
func TestSessionWatchEntersTheCommittedFrameBeforeTheCommitReturns(t *testing.T) {
	harness := createWatchHarness(t)
	watch := watchDoc(t, harness, ctx, watchStateDoc)
	recorder := &watchRecorder{}
	watch.Start(func(_ context.Context, change durable.WatchChange[obj]) error {
		value, ops := change.Value, change.Ops
		recorder.record(value, ops)
		return nil
	})
	setWatchValue(t, harness, 1)
	published := lastPublishedDocument(harness)
	if !same(watch.Value(), published.Value) {
		t.Fatal("the watch value is the committed frame when the commit returns")
	}
	end, err := watch.Stop()
	if err != nil || end.Reason != durable.WatchStopped {
		t.Fatalf("stop: %+v %v", end, err)
	}
	flush(harness)
	deliveries := recorder.all()
	if len(deliveries) != 1 || !same(deliveries[0].value, published.Value) || !same(deliveries[0].ops, published.Ops) {
		t.Fatalf("a stop after the commit returns keeps the frame already in delivery: %v", recorder.values())
	}
}

// Ports "ignores the same kind in another conversation and under another family key" of session-watches.test.ts.
func TestSessionDocumentWatchIgnoresTheSameKindInAnotherConversationAndUnderAnotherFamilyKey(t *testing.T) {
	notes := defineDoc("watch.scoped-notes", 1, latestScope(durable.ForkInitial), func() obj { return delta.JsonObjectOf("text", "") })
	members := defineFamily("watch.members", 1, sessionScope, func(seed int) obj { return delta.JsonObjectOf("text", "") })
	harness := open()
	watched := createConversation(t, harness)
	other := createConversation(t, harness)
	setText := func(tx durable.Tx, text string) {
		must(t, mustDoc(t, tx, notes, watched).Set("text", text))
	}
	commit(t, harness.Session, func(tx durable.Tx) error {
		setText(tx, "a")
		return mustDoc(t, tx, members, "watched", 0).Set("text", "a")
	})
	noteWatch, err := harness.Session.WatchDocErased(ctx, notes, watched)
	must(t, err)
	memberWatch, err := harness.Session.WatchDocErased(ctx, members, "watched")
	must(t, err)
	recorder := &changeRecorder{}
	noteWatch.Start(recorder.listener())
	memberWatch.Start(recorder.listener())
	// The same kinds at other addresses: created, changed, retired, and created again.
	commit(t, harness.Session, func(tx durable.Tx) error {
		must(t, mustDoc(t, tx, notes, other).Set("text", "other"))
		return mustDoc(t, tx, members, "other", 0).Set("text", "other")
	})
	commit(t, harness.Session, func(tx durable.Tx) error {
		must(t, tx.RetireDoc(notes, other))
		return tx.RetireDoc(members, "other")
	})
	commit(t, harness.Session, func(tx durable.Tx) error {
		must(t, mustDoc(t, tx, notes, other).Set("text", "again"))
		return mustDoc(t, tx, members, "other", 0).Set("text", "again")
	})
	flush(harness)
	if changes := recorder.all(); len(changes) != 0 {
		t.Fatalf("changes %v, want none", changes)
	}
	if attached := harness.Session.DocumentCacheSizes().Attached; attached != 2 {
		t.Fatalf("attached %d, want 2", attached)
	}
	_, _ = noteWatch.Stop()
	_, _ = memberWatch.Stop()
	must(t, harness.Session.Close(ctx))
}

// Ports "takes the version of a recreation written by a newer definition, then its ops".
func TestSessionDocumentWatchTakesTheVersionOfARecreationWrittenByANewerDefinitionThenItsOps(t *testing.T) {
	old := defineDoc("watch.recreated-version", 1, sessionScope, func() obj { return delta.JsonObjectOf("value", 1) })
	current := defineDoc("watch.recreated-version", 2, sessionScope, func() obj { return delta.JsonObjectOf("value", 0, "migrated", false) },
		withMigrate(func(value obj, _ int) obj { return delta.JsonObjectOf("value", value.Value("value"), "migrated", true) }))
	harness := open()
	commit(t, harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(old); return err })
	watch := watchDoc(t, harness, ctx, old)
	recorder := &changeRecorder{}
	watch.Start(recorder.listener())
	commit(t, harness.Session, func(tx durable.Tx) error {
		must(t, tx.RetireDoc(current))
		return mustDoc(t, tx, current).Set("value", 5)
	})
	flush(harness)
	setStateValue(t, harness, current, 6)
	flush(harness)
	changes := recorder.all()
	first := delta.JsonObjectOf("value", 5, "migrated", false)
	if len(changes) != 2 || !changes[0].Retired || changes[1].Retired {
		t.Fatalf("changes %v", changes)
	}
	expectEqual(t, changes[0].Value, first)
	expectEqual(t, changes[0].Ops, []durable.Op{{"r", first}})
	expectEqual(t, changes[1].Value, delta.JsonObjectOf("value", 6, "migrated", false))
	expectEqual(t, changes[1].Ops, []durable.Op{{"s", []any{"value"}, 6}})
	_, _ = watch.Stop()
	must(t, harness.Session.Close(ctx))
}
