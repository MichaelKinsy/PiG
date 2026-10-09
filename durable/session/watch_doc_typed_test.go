package session_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// WatchDoc is Session.watchDoc with a typed value: the acquisition revision, each later committed revision decoded as
// T, nil once the document retires, and a nil watch for an absent document (types.ts DocumentObserver.watchDoc).
func TestWatchDocTypedDecodesRevisionsAndReportsRetirementAsNil(t *testing.T) {
	harness := createWatchHarness(t)
	ctx := context.Background()
	watch, err := durable.WatchDoc[obj](ctx, harness.Session, watchStateDoc)
	if err != nil || watch == nil {
		t.Fatalf("WatchDoc = %v, %v", watch, err)
	}
	if initial := watch.Value(); initial == nil || (*initial).Value("value") != float64(0) || len((*initial).Value("items").([]any)) != 2 {
		t.Fatalf("acquisition value = %+v", initial)
	}
	var mu sync.Mutex
	var seen []*obj
	watch.Start(func(_ context.Context, value *obj, _ []durable.Op) error {
		mu.Lock()
		seen = append(seen, value)
		mu.Unlock()
		return nil
	})
	setWatchValue(t, harness, 7)
	eventuallyTrue(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 1 })
	mu.Lock()
	if seen[0] == nil || (*seen[0]).Value("value") != float64(7) || len((*seen[0]).Value("items").([]any)) != 2 {
		t.Fatalf("delivered %+v", seen[0])
	}
	mu.Unlock()
	// Retirement delivers a nil value and ends the watch (observation.ts advance/#drain: a null value retires it).
	commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
	eventuallyTrue(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) == 2 })
	mu.Lock()
	if seen[1] != nil {
		t.Fatalf("retirement delivered %+v", seen[1])
	}
	mu.Unlock()
	if watch.Value() != nil {
		t.Fatalf("after retirement Value = %+v", watch.Value())
	}
	if end, err := watch.Stop(); err != nil || end.Reason != durable.WatchRetired {
		t.Fatalf("end after retirement = %+v, %v", end, err)
	}

	absent, err := durable.WatchDoc[obj](ctx, harness.Session, defineDoc("watch.never", 1, sessionScope, func() obj { return delta.NewJsonObject(0) }))
	if err != nil || absent != nil {
		t.Fatalf("absent document: %v, %v", absent, err)
	}
}

func eventuallyTrue(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

type typedSeed struct {
	Label string `json:"label"`
}

var typedSeedFamily = defineFamily("test.typed-seed", 1, sessionScope, func(seed typedSeed) obj { return delta.JsonObjectOf("label", seed.Label) })

// A family member's seed may be a typed Go value: it is stored through its JSON encoding, as CreateTask input and
// Snapshot values are (Pi seeds are JSON values; a struct is Go's way to write one).
func TestFamilyDocSeedAcceptsATypedGoValue(t *testing.T) {
	harness := open()
	commit(t, harness.Session, func(tx durable.Tx) error {
		draft, err := tx.Doc(typedSeedFamily, "member", typedSeed{Label: "from a struct"})
		if err != nil {
			return err
		}
		if draft.Get("label") != "from a struct" {
			t.Errorf("seeded member: %v", draft.Snapshot())
		}
		return nil
	})
}

// A waiter woken from a commit listener resumes after the listeners that follow it, as a resolved promise's
// continuation runs after upstream's synchronous listener loop.
func TestDeferUntilPublishedRunsAfterEveryListener(t *testing.T) {
	harness := open()
	var order []string
	var mu sync.Mutex
	note := func(what string) { mu.Lock(); order = append(order, what); mu.Unlock() }
	harness.Session.SubscribeCommits(func(context.Context, durable.CommitPublication) {
		harness.Session.DeferUntilPublished(func() { note("deferred") })
		note("first listener")
	})
	harness.Session.SubscribeCommits(func(context.Context, durable.CommitPublication) { note("second listener") })
	setWatchValue(t, harness, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 3 || order[0] != "first listener" || order[1] != "second listener" || order[2] != "deferred" {
		t.Fatalf("order = %v", order)
	}
	var immediate bool
	harness.Session.DeferUntilPublished(func() { immediate = true })
	if !immediate {
		t.Fatal("outside a publication the function runs at once")
	}
}

// A listener that panics propagates out of the commit, as upstream's throwing listener rejects commit(), but work an
// earlier listener deferred still runs (upstream's promise was already resolved) and the next publication is clean.
func TestDeferUntilPublishedSurvivesAPanickingListener(t *testing.T) {
	harness := open()
	var ran []string
	var mu sync.Mutex
	note := func(what string) { mu.Lock(); ran = append(ran, what); mu.Unlock() }
	panicking := true
	harness.Session.SubscribeCommits(func(context.Context, durable.CommitPublication) {
		harness.Session.DeferUntilPublished(func() { note("deferred") })
	})
	harness.Session.SubscribeCommits(func(context.Context, durable.CommitPublication) {
		if panicking {
			panicking = false
			panic("listener failed")
		}
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the listener's panic did not reach the committer")
			}
		}()
		setWatchValue(t, harness, 1)
	}()
	mu.Lock()
	if len(ran) != 1 {
		t.Fatalf("deferred work after a panicking listener: %v", ran)
	}
	mu.Unlock()
	var immediate bool
	harness.Session.DeferUntilPublished(func() { immediate = true })
	if !immediate {
		t.Fatal("a panicking publication left the Session publishing")
	}
	setWatchValue(t, harness, 2)
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 {
		t.Fatalf("deferred work of the next publication: %v", ran)
	}
}

// DocumentStateOf is Session.documentState with a typed value: the attachment snapshot, each later committed
// revision decoded as T with its delivery, nil once the document retires, and a nil state for an absent document.
func TestDocumentStateOfDecodesRevisionsAndRetirement(t *testing.T) {
	harness := createWatchHarness(t)
	state, err := durable.DocumentStateOf[obj](ctx, harness.Session, watchStateDoc)
	if err != nil || state == nil {
		t.Fatalf("DocumentStateOf = %v, %v", state, err)
	}
	defer state.Dispose()
	if initial := state.Value(); initial == nil || (*initial).Value("value") != float64(0) {
		t.Fatalf("attachment value = %v", initial)
	}
	var mu sync.Mutex
	var seen []*obj
	unsubscribe, err := state.Subscribe(func(value *obj, _ context.Context, _ chord.ReplicatedStateDelivery) {
		mu.Lock()
		seen = append(seen, value)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	setWatchValue(t, harness, 3)
	commit(t, harness.Session, func(tx durable.Tx) error { return tx.RetireDoc(watchStateDoc) })
	eventuallyTrue(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(seen) >= 3 })
	mu.Lock()
	defer mu.Unlock()
	// A subscriber first receives the current value, then each revision; the retirement is nil.
	if seen[0] == nil || (*seen[0]).Value("value") != float64(0) || seen[1] == nil || (*seen[1]).Value("value") != float64(3) || seen[len(seen)-1] != nil {
		t.Fatalf("deliveries = %v", seen)
	}
	if state.Value() != nil {
		t.Fatalf("after retirement Value = %v", state.Value())
	}
	absent, err := durable.DocumentStateOf[obj](ctx, harness.Session, defineDoc("watch.never-state", 1, sessionScope, func() obj { return delta.NewJsonObject(0) }))
	if err != nil || absent != nil {
		t.Fatalf("absent document: %v, %v", absent, err)
	}
}

type watchedNumber struct {
	Value float64 `json:"value"`
}

type watchedText struct {
	Value string `json:"value"`
}

// typedWatchState is the token of watch.state read as T, the same stored document through another Go type.
func typedWatchState[T any]() durable.DocToken[T] {
	return durable.DefineDoc(durable.DocDefinition[T]{
		CommonDocDefinition: durable.CommonDocDefinition[T]{Kind: "watch.state", Version: 1, Initial: func() T { var zero T; return zero }},
		DocumentSemantics:   sessionScope,
	})
}

// Upstream's Value is a getter that cannot fail. A document whose current value does not decode as the token's type
// is refused when the watch is acquired, as Snapshot refuses it; a later revision that does not decode ends Start's
// listener with listener_error and leaves Value at the latest revision that decoded, never a panic.
func TestWatchDocTypedValueDoesNotPanicOnAMismatchedRevision(t *testing.T) {
	harness := createWatchHarness(t)
	ctx := context.Background()
	if watch, err := durable.WatchDoc[watchedText](ctx, harness.Session, typedWatchState[watchedText]()); err == nil || watch != nil {
		t.Fatalf("acquiring a mismatched value = %v, %v, want an error", watch, err)
	}
	watch, err := durable.WatchDoc[watchedNumber](ctx, harness.Session, typedWatchState[watchedNumber]())
	if err != nil || watch == nil {
		t.Fatalf("WatchDoc = %v, %v", watch, err)
	}
	var mu sync.Mutex
	var delivered int
	watch.Start(func(context.Context, *watchedNumber, []durable.Op) error {
		mu.Lock()
		delivered++
		mu.Unlock()
		return nil
	})
	setWatchValue(t, harness, 3)
	eventuallyTrue(t, func() bool { mu.Lock(); defer mu.Unlock(); return delivered == 1 })
	setWatchValue(t, harness, "three")
	select {
	case <-watch.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not end")
	}
	if end := watch.End(); end.Reason != durable.WatchListenerError {
		t.Fatalf("end %+v, want listener_error", end)
	}
	if value := watch.Value(); value == nil || value.Value != 3 {
		t.Fatalf("Value after a mismatched revision = %+v, want the latest that decoded", value)
	}
}
