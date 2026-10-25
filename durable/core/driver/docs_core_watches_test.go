package driver_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports of packages/durable/test/session-watches.test.ts through driver.Session over the REAL core (docs_core_test.go rig).

var watchStateDoc = defineDoc("test.state", 1, func() obj { return delta.JsonObjectOf("value", 0) })

type frame struct {
	value int
	ops   []durable.Op
	null  bool
}

func (r *coreRig) setValue(t *testing.T, value int) {
	t.Helper()
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(watchStateDoc)
		if err != nil {
			return err
		}
		return o.Set("value", value)
	})
}

func (r *coreRig) watchState(t *testing.T) durable.WatchHandle[durable.JsonObject] {
	t.Helper()
	r.commit(t, func(tx durable.Tx) error { return touch(tx, watchStateDoc) })
	w, err := r.s.WatchDocErased(t.Context(), watchStateDoc)
	if err != nil || w == nil {
		t.Fatalf("watch: %v %v", w, err)
	}
	return w
}

func collect(w durable.WatchHandle[durable.JsonObject], gate <-chan struct{}) chan frame {
	frames := make(chan frame, 256)
	n := 0
	w.Start(func(_ context.Context, value durable.JsonObject, ops []durable.Op) error {
		f := frame{ops: ops, null: value == nil}
		if value != nil {
			f.value = toInt(value.Value("value"))
		}
		frames <- f
		if n++; n == 1 && gate != nil {
			<-gate
		}
		return nil
	})
	return frames
}

func next(t *testing.T, frames chan frame) frame {
	t.Helper()
	select {
	case f := <-frames:
		return f
	case <-time.After(10 * time.Second):
		t.Fatal("no frame")
	}
	return frame{}
}

func quiet(t *testing.T, frames chan frame) {
	t.Helper()
	select {
	case f := <-frames:
		t.Fatalf("unexpected frame %+v", f)
	case <-time.After(300 * time.Millisecond):
	}
}

func isRoot(op durable.Op) bool { return len(op) > 0 && op[0] == "r" }

func isRootReplacement(f frame, value int) bool {
	return len(f.ops) == 1 && f.value == value && isRoot(f.ops[0])
}

// session-watches.test.ts:142 "collapses 101 pending commits to one root replacement".
func TestCoreWatchCollapsesPendingCommitsToOneRootReplacement(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	for v := 1; v <= 101; v++ {
		r.setValue(t, v)
	}
	frames := collect(w, nil)
	f := next(t, frames)
	if !isRootReplacement(f, 101) {
		t.Fatalf("delivery = %+v, want one root replacement to 101", f)
	}
	quiet(t, frames)
	_, _ = w.Stop()
}

// session-watches.test.ts:159 "never folds the in-flight frame into an overflow reset".
func TestCoreWatchNeverFoldsTheInFlightFrameIntoAnOverflowReset(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	release := make(chan struct{})
	frames := collect(w, release)
	r.setValue(t, 1)
	first := next(t, frames)
	if first.value != 1 {
		t.Fatalf("first = %+v", first)
	}
	for v := 2; v <= 102; v++ {
		r.setValue(t, v)
	}
	close(release)
	second := next(t, frames)
	if !isRootReplacement(second, 102) {
		t.Fatalf("second = %+v, want one root replacement to 102", second)
	}
	quiet(t, frames)
	_, _ = w.Stop()
}

// session-watches.test.ts:189 "folds retirement into an overflow reset and then closes".
func TestCoreWatchFoldsRetirementIntoAnOverflowResetThenCloses(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	for v := 1; v <= 100; v++ {
		r.setValue(t, v)
	}
	r.commit(t, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
	frames := collect(w, nil)
	f := next(t, frames)
	if !f.null || len(f.ops) != 1 || !isRoot(f.ops[0]) {
		t.Fatalf("delivery = %+v, want one null root replacement", f)
	}
	select {
	case <-w.Closed():
	case <-time.After(10 * time.Second):
		t.Fatal("the watch closes after retirement")
	}
	if end := w.End(); end.Reason != durable.WatchRetired {
		t.Fatalf("end = %+v", end)
	}
}

// session-watches.test.ts:279 "delivers retirement and does not follow recreation".
func TestCoreWatchDeliversRetirementAndDoesNotFollowRecreation(t *testing.T) {
	r := openCoreDB(t)
	old := r.watchState(t)
	frames := collect(old, nil)
	r.commit(t, func(tx durable.Tx) error {
		if err := tx.RetireDoc(watchStateDoc); err != nil {
			return err
		}
		o, err := tx.Doc(watchStateDoc)
		if err != nil {
			return err
		}
		return o.Set("value", 10)
	})
	if f := next(t, frames); !f.null {
		t.Fatalf("the old watch delivers retirement: %+v", f)
	}
	select {
	case <-old.Closed():
	case <-time.After(10 * time.Second):
		t.Fatal("the old watch closes")
	}
	if end := old.End(); end.Reason != durable.WatchRetired {
		t.Fatalf("end = %+v", end)
	}
	if old.Value() != nil {
		t.Fatalf("a retired watch holds null: %v", old.Value())
	}
	replacement, err := r.s.WatchDocErased(t.Context(), watchStateDoc)
	if err != nil || replacement == nil || toInt(replacement.Value().Value("value")) != 10 {
		t.Fatalf("replacement = %v %v", replacement, err)
	}
	r.setValue(t, 11)
	quiet(t, frames)
	if old.Value() != nil {
		t.Fatal("the old watch stays null")
	}
	_, _ = replacement.Stop()
}

func expectPanicContaining(t *testing.T, text string, use func()) {
	t.Helper()
	defer func() {
		t.Helper()
		got := recover()
		if got == nil || !strings.Contains(fmt.Sprint(got), text) {
			t.Fatalf("panic = %v, want one containing %q", got, text)
		}
	}()
	use()
}

func noopListener(context.Context, durable.JsonObject, []durable.Op) error { return nil }

func awaitClosed(t *testing.T, w durable.WatchHandle[durable.JsonObject]) durable.WatchEnd {
	t.Helper()
	select {
	case <-w.Closed():
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not close")
	}
	return w.End()
}

// session-watches.test.ts:338 "supports idempotent stop and rejects repeated or late start".
func TestCoreWatchStopIsIdempotentAndStartIsRejectedTwiceOrLate(t *testing.T) {
	r := openCoreDB(t)
	started := r.watchState(t)
	started.Start(noopListener)
	expectPanicContaining(t, "already started", func() { started.Start(noopListener) })
	first, _ := started.Stop()
	second, _ := started.Stop()
	if first != second || first.Reason != durable.WatchStopped {
		t.Fatalf("stop = %+v then %+v", first, second)
	}
	stopped, err := r.s.WatchDocErased(t.Context(), watchStateDoc)
	if err != nil || stopped == nil {
		t.Fatalf("watch: %v %v", stopped, err)
	}
	_, _ = stopped.Stop()
	expectPanicContaining(t, "stopped", func() { stopped.Start(noopListener) })
}

// session-watches.test.ts:409 "settles listener failure on only the affected watch".
func TestCoreWatchListenerFailureSettlesOnlyTheAffectedWatch(t *testing.T) {
	r := openCoreDB(t)
	failed := r.watchState(t)
	healthy, err := r.s.WatchDocErased(t.Context(), watchStateDoc)
	if err != nil || healthy == nil {
		t.Fatalf("watch: %v %v", healthy, err)
	}
	failed.Start(func(context.Context, durable.JsonObject, []durable.Op) error { return errors.New("listener failed") })
	calls := make(chan struct{}, 8)
	healthy.Start(func(context.Context, durable.JsonObject, []durable.Op) error { calls <- struct{}{}; return nil })
	r.setValue(t, 1)
	end := awaitClosed(t, failed)
	if end.Reason != durable.WatchListenerError || end.Error == nil || end.Error.Error() != "listener failed" {
		t.Fatalf("end = %+v", end)
	}
	select {
	case <-calls:
	case <-time.After(10 * time.Second):
		t.Fatal("the healthy watch still delivers")
	}
	select {
	case <-healthy.Closed():
		t.Fatal("the healthy watch stays open")
	default:
	}
	_, _ = healthy.Stop()
}

// session-watches.test.ts:304 "Session close discards retirement buffered before start".
func TestCoreSessionCloseDiscardsRetirementBufferedBeforeStart(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	baseline := w.Value()
	r.commit(t, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
	if err := r.s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if end := awaitClosed(t, w); end.Reason != durable.WatchEnded {
		t.Fatalf("end = %+v", end)
	}
	if !reflect.DeepEqual(w.Value(), baseline) {
		t.Fatalf("the value stays the acquisition revision: %v, want %v", w.Value(), baseline)
	}
	expectPanicContaining(t, "stopped", func() { w.Start(noopListener) })
}

// session-watches.test.ts:315 "Session close discards retirement behind an in-flight callback".
func TestCoreSessionCloseDiscardsRetirementBehindAnInFlightCallback(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	release := make(chan struct{})
	frames := collect(w, release)
	r.setValue(t, 1)
	if f := next(t, frames); f.value != 1 {
		t.Fatalf("first = %+v", f)
	}
	r.commit(t, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
	if err := r.s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if end := awaitClosed(t, w); end.Reason != durable.WatchEnded {
		t.Fatalf("end = %+v", end)
	}
	close(release)
	quiet(t, frames)
}

// session-watches.test.ts:388 "Session close stops future delivery without joining an in-flight callback".
func TestCoreSessionCloseDoesNotJoinAnInFlightCallbackNorCancelItsContext(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	w.Start(func(ctx context.Context, _ durable.JsonObject, _ []durable.Op) error {
		entered <- ctx
		<-release
		return nil
	})
	r.setValue(t, 1)
	var delivered context.Context
	select {
	case delivered = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no delivery")
	}
	closed := make(chan error, 1)
	go func() { closed <- r.s.Close(t.Context()) }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Session.Close joined the in-flight callback")
	}
	if delivered.Err() != nil {
		t.Fatalf("the callback's context is not cancelled by close: %v", delivered.Err())
	}
	if end := awaitClosed(t, w); end.Reason != durable.WatchEnded {
		t.Fatalf("end = %+v", end)
	}
	close(release)
}

// session-watches.test.ts:467 "can replay every delivered exact operation batch from the acquisition revision".
func TestCoreWatchFramesReplayFromTheAcquisitionRevision(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	replica := any(w.Value())
	type applied struct{ ok bool }
	results := make(chan applied, 4)
	w.Start(func(_ context.Context, value durable.JsonObject, ops []durable.Op) error {
		next, err := delta.ApplyImmutable(replica, ops)
		replica = next
		results <- applied{err == nil && reflect.DeepEqual(next, any(value))}
		return nil
	})
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(watchStateDoc)
		if err != nil {
			return err
		}
		if err := o.Set("value", 3); err != nil {
			return err
		}
		return o.Set("extra", []any{"a", "b"})
	})
	select {
	case a := <-results:
		if !a.ok {
			t.Fatalf("replaying the delivered ops does not reproduce the delivered value: %v", replica)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no frame")
	}
	_, _ = w.Stop()
}

var watchItemsDoc = defineDoc("test.watch-items", 1, func() obj { return delta.JsonObjectOf("value", 0, "items", []any{"a", "b"}) })

// session-watches.test.ts:206 "delivers replayable structural no-op commits instead of suppressing them".
func TestCoreWatchDeliversStructuralNoOpCommits(t *testing.T) {
	r := openCoreDB(t)
	r.commit(t, func(tx durable.Tx) error { return touch(tx, watchItemsDoc) })
	w, err := r.s.WatchDocErased(t.Context(), watchItemsDoc)
	if err != nil || w == nil {
		t.Fatalf("watch: %v %v", w, err)
	}
	initial := w.Value()
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(watchItemsDoc)
		if err != nil {
			return err
		}
		items := o.Array("items")
		first := items.Shift()
		_, err = items.Unshift(first)
		return err
	})
	frames := make(chan []durable.Op, 8)
	w.Start(func(_ context.Context, value durable.JsonObject, ops []durable.Op) error {
		if !reflect.DeepEqual(value, initial) {
			t.Errorf("the delivered value equals the initial one: %v vs %v", value, initial)
		}
		frames <- ops
		return nil
	})
	select {
	case ops := <-frames:
		if len(ops) == 0 {
			t.Fatal("a no-op-looking commit still delivers replayable ops")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no frame: the structural no-op commits were suppressed")
	}
	_, _ = w.Stop()
}

// session-watches.test.ts:431 "hydrates migration without writing and observes the later exact edit".
func TestCoreWatchHydratesMigrationWithoutWritingAndSeesTheLaterEdit(t *testing.T) {
	r := openCoreDB(t)
	old := defineDoc("watch.migration", 1, func() obj { return delta.JsonObjectOf("value", 3) })
	current := defineDoc("watch.migration", 2, func() obj { return delta.JsonObjectOf("value", 0, "migrated", false) },
		withMigrate(func(v obj, _ int) obj { return delta.JsonObjectOf("value", v.Value("value"), "migrated", true) }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, old) })
	before := r.revisions(t, "watch.migration")
	w, err := r.s.WatchDocErased(t.Context(), current)
	if err != nil || w == nil {
		t.Fatalf("watch: %v %v", w, err)
	}
	if v := w.Value(); toInt(v.Value("value")) != 3 || v.Value("migrated") != true {
		t.Fatalf("hydrated value = %v", v)
	}
	eq(t, "revisions after the watch", r.revisions(t, "watch.migration"), before)
	values := make(chan int, 8)
	w.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error {
		values <- toInt(value.Value("value"))
		return nil
	})
	// The migration-only base reaches no observer of the new shape.
	r.commit(t, func(tx durable.Tx) error { return touch(tx, current) })
	quietInts(t, values)
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(current)
		if err != nil {
			return err
		}
		return o.Set("value", 4)
	})
	select {
	case v := <-values:
		if v != 4 {
			t.Fatalf("delivered %d, want 4", v)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the later edit is not delivered")
	}
	_, _ = w.Stop()
}

func quietInts(t *testing.T, values chan int) {
	t.Helper()
	select {
	case v := <-values:
		t.Fatalf("unexpected delivery %d", v)
	case <-time.After(300 * time.Millisecond):
	}
}

// session-watches.test.ts:260 "keeps earlier immutable revisions stable".
func TestCoreWatchKeepsEarlierRevisionsStable(t *testing.T) {
	r := openCoreDB(t)
	r.commit(t, func(tx durable.Tx) error { return touch(tx, watchItemsDoc) })
	w, err := r.s.WatchDocErased(t.Context(), watchItemsDoc)
	if err != nil || w == nil {
		t.Fatalf("watch: %v %v", w, err)
	}
	initial := w.Value()
	got := make(chan durable.JsonObject, 2)
	w.Start(func(_ context.Context, value durable.JsonObject, _ []durable.Op) error { got <- value; return nil })
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(watchItemsDoc)
		if err != nil {
			return err
		}
		return o.Set("value", 1)
	})
	select {
	case v := <-got:
		if toInt(v.Value("value")) != 1 || toInt(initial.Value("value")) != 0 {
			t.Fatalf("delivered %v, initial %v", v, initial)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no frame")
	}
	_, _ = w.Stop()
}

// session-watches.test.ts:366 "cancels future delivery without aborting an in-flight callback".
func TestCoreWatchCancellationEndsFutureDeliveryWithoutAbortingTheInFlightCallback(t *testing.T) {
	r := openCoreDB(t)
	r.commit(t, func(tx durable.Tx) error { return touch(tx, watchStateDoc) })
	child, cancel := context.WithCancel(t.Context())
	w, err := r.s.WatchDocErased(child, watchStateDoc)
	if err != nil || w == nil {
		t.Fatalf("watch: %v %v", w, err)
	}
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	w.Start(func(ctx context.Context, _ durable.JsonObject, _ []durable.Op) error {
		entered <- ctx
		<-release
		return nil
	})
	r.setValue(t, 1)
	var delivered context.Context
	select {
	case delivered = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no delivery")
	}
	cancel()
	if end := awaitClosed(t, w); end.Reason != durable.WatchCancelled {
		t.Fatalf("end = %+v", end)
	}
	if delivered.Err() != nil {
		t.Fatalf("the in-flight callback's context is not cancelled: %v", delivered.Err())
	}
	close(release)
}

type watchKey struct{}

// session-watches.test.ts:229 "preserves commit Context values without inheriting producer cancellation": the commit that holds the
// Session line is the producer of the publications that arrive meanwhile (watchHub.deliveryContext).
func TestCoreWatchDeliveryCarriesTheCommitContextValuesButNotItsCancellation(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	parent, cancelParent := context.WithCancel(context.WithValue(t.Context(), watchKey{}, "newest-commit"))
	entered := make(chan context.Context, 1)
	release := make(chan struct{})
	w.Start(func(ctx context.Context, _ durable.JsonObject, _ []durable.Op) error {
		entered <- ctx
		<-release
		return nil
	})
	if _, err := r.s.Commit(parent, func(tx durable.Tx) (any, error) {
		o, err := tx.Doc(watchStateDoc)
		if err != nil {
			return nil, err
		}
		return nil, o.Set("value", 1)
	}); err != nil {
		t.Fatal(err)
	}
	var delivered context.Context
	select {
	case delivered = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no delivery")
	}
	if got := delivered.Value(watchKey{}); got != "newest-commit" {
		t.Fatalf("the delivery context value = %v, want newest-commit", got)
	}
	cancelParent()
	if end, _ := w.Stop(); end.Reason != durable.WatchStopped {
		t.Fatalf("end = %+v", end)
	}
	if delivered.Err() != nil {
		t.Fatalf("the delivery context does not inherit the producer's cancellation: %v", delivered.Err())
	}
	close(release)
}

// session-watches.test.ts:485 "continues after the tracker cache unloads": the native core keeps no tracker cache the host can
// unload, so the observable half is that a snapshot read between commits does not disturb the watch, which then sees the edit.
func TestCoreWatchContinuesAfterASnapshotRead(t *testing.T) {
	r := openCoreDB(t)
	w := r.watchState(t)
	baseline := w.Value()
	got, err := durable.Snapshot[obj](t.Context(), r.s, watchStateDoc)
	if err != nil || got == nil || !reflect.DeepEqual(obj(*got), baseline) {
		t.Fatalf("snapshot = %v, %v; watch baseline %v", got, err, baseline)
	}
	frames := collect(w, nil)
	r.setValue(t, 7)
	if f := next(t, frames); f.value != 7 {
		t.Fatalf("frame = %+v", f)
	}
	if toInt(w.Value().Value("value")) != 7 {
		t.Fatalf("watch value = %v", w.Value())
	}
	_, _ = w.Stop()
}
