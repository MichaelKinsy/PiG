package session_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Ports packages/durable/test/session-states.test.ts

var stateStateDoc = defineDoc("state.state", 1, sessionScope, func() obj {
	return obj{"value": 0, "retained": obj{"label": "stable"}}
})

var stateFamilyDoc = defineFamily("state.family", 1, sessionScope, func(seed string) obj { return obj{"value": len(seed)} })

func createStateHarness(t *testing.T) sessiontest.Harness {
	t.Helper()
	harness := open()
	commit(t, harness.Session, func(tx durable.Tx) error {
		_, err := tx.Doc(stateStateDoc)
		return err
	})
	flush(harness)
	return harness
}

func setStateValue(t *testing.T, harness sessiontest.Harness, token durable.AnyDocToken, value any) {
	t.Helper()
	commit(t, harness.Session, func(tx durable.Tx) error { return mustDoc(t, tx, token).Set("value", value) })
}

func documentState(t *testing.T, harness sessiontest.Harness, token durable.AnyDocToken, args ...any) *chord.AttachedReplicatedState[obj] {
	t.Helper()
	state, err := harness.Session.DocumentStateErased(ctx, token, args...)
	if err != nil {
		t.Fatalf("documentState: %v", err)
	}
	return state
}

type stateDelivery struct {
	value    obj
	sequence int
}

type deliveries struct {
	mu    sync.Mutex
	items []stateDelivery
}

func (recorded *deliveries) listener(value obj, _ context.Context, delivery chord.ReplicatedStateDelivery) {
	recorded.mu.Lock()
	recorded.items = append(recorded.items, stateDelivery{value, delivery.Sequence})
	recorded.mu.Unlock()
}

func (recorded *deliveries) all() []stateDelivery {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	return append([]stateDelivery(nil), recorded.items...)
}

func TestSessionDocumentStates(t *testing.T) {
	t.Run("never creates an absent document", func(t *testing.T) {
		harness := open()
		commits := len(harness.Storage.Commits())
		if state := documentState(t, harness, stateStateDoc); state != nil {
			t.Fatal("absent singleton has no state")
		}
		if state := documentState(t, harness, stateFamilyDoc, "missing"); state != nil {
			t.Fatal("absent member has no state")
		}
		if len(harness.Storage.Commits()) != commits || harness.Storage.MintCount() != 0 {
			t.Fatal("documentState must not create")
		}
	})

	t.Run("returns an immediately hydrated read-only state with contiguous Chord deliveries", func(t *testing.T) {
		harness := createStateHarness(t)
		baseline := snapshot(t, harness.Session, stateStateDoc)
		state := documentState(t, harness, stateStateDoc)
		recorded := &deliveries{}
		_, _ = state.Subscribe(recorded.listener)
		if !same(state.Value(), baseline) {
			t.Fatal("state value is the committed snapshot")
		}
		setStateValue(t, harness, stateStateDoc, 1)
		setStateValue(t, harness, stateStateDoc, 2)
		flush(harness)
		var sequences, values []any
		for _, delivery := range recorded.all() {
			sequences = append(sequences, delivery.sequence)
			values = append(values, delivery.value["value"])
		}
		expectEqual(t, sequences, []any{0, 1, 2})
		expectEqual(t, values, []any{0, 1, 2})
		if !same(state.Value(), snapshot(t, harness.Session, stateStateDoc)) {
			t.Fatal("state value follows the committed snapshot")
		}
		state.Dispose()
	})

	t.Run("creates independent disposable states for one incarnation", func(t *testing.T) {
		harness := createStateHarness(t)
		first := documentState(t, harness, stateStateDoc)
		second := documentState(t, harness, stateStateDoc)
		if first == second {
			t.Fatal("states are independent")
		}
		setStateValue(t, harness, stateStateDoc, 1)
		flush(harness)
		expectEqual(t, []any{first.Value()["value"], second.Value()["value"]}, []any{1, 1})
		first.Dispose()
		setStateValue(t, harness, stateStateDoc, 2)
		flush(harness)
		expectEqual(t, []any{first.Value()["value"], second.Value()["value"]}, []any{1, 2})
		second.Dispose()
	})

	t.Run("shares exact committed value and operation references with Chord", func(t *testing.T) {
		harness := createStateHarness(t)
		state := documentState(t, harness, stateStateDoc)
		var mu sync.Mutex
		var receivedOps []durable.Op
		state.SubscribeSource(func(ops []delta.Op, _ int, _ context.Context) {
			mu.Lock()
			receivedOps = ops
			mu.Unlock()
		})
		setStateValue(t, harness, stateStateDoc, 4)
		flush(harness)
		published := sessiontest.DocumentChanges(harness.Publications.Last())[0]
		if !same(state.Value(), published.Value) {
			t.Fatal("state value is the published revision")
		}
		mu.Lock()
		defer mu.Unlock()
		if !same(receivedOps, published.Ops) {
			t.Fatal("Chord receives the published operation batch")
		}
		if !same(state.Value()["retained"], snapshot(t, harness.Session, stateStateDoc)["retained"]) {
			t.Fatal("unchanged subtrees are shared")
		}
		state.Dispose()
	})

	t.Run("captures a late baseline without redelivering an already covered commit", func(t *testing.T) {
		harness := createStateHarness(t)
		setStateValue(t, harness, stateStateDoc, 1)
		state := documentState(t, harness, stateStateDoc)
		recorded := &deliveries{}
		_, _ = state.Subscribe(recorded.listener)
		flush(harness)
		expectEqual(t, state.Value()["value"], 1)
		if got := recorded.all(); len(got) != 1 || got[0].sequence != 0 {
			t.Fatalf("deliveries %v", got)
		}
		state.Dispose()
	})

	t.Run("publishes null retirement and never follows a replacement incarnation", func(t *testing.T) {
		harness := createStateHarness(t)
		oldState := documentState(t, harness, stateStateDoc)
		var mu sync.Mutex
		var retirementOps []durable.Op
		oldState.SubscribeSource(func(ops []delta.Op, _ int, _ context.Context) {
			mu.Lock()
			retirementOps = ops
			mu.Unlock()
		})
		commit(t, harness.Session, func(tx durable.Tx) error {
			must(t, tx.RetireDoc(stateStateDoc))
			return mustDoc(t, tx, stateStateDoc).Set("value", 10)
		})
		flush(harness)
		if oldState.Value() != nil {
			t.Fatal("retired state value is null")
		}
		mu.Lock()
		expectEqual(t, retirementOps, []any{[]any{"r", nil}})
		mu.Unlock()
		replacement := documentState(t, harness, stateStateDoc)
		expectEqual(t, replacement.Value()["value"], 10)
		setStateValue(t, harness, stateStateDoc, 11)
		flush(harness)
		if oldState.Value() != nil {
			t.Fatal("the old state does not follow the replacement")
		}
		expectEqual(t, replacement.Value()["value"], 11)
		oldState.Dispose()
		replacement.Dispose()
	})

	t.Run("cold-loads a definition-free fork copy", func(t *testing.T) {
		copied := defineDoc("state.copied", 1, latestScope(durable.ForkCurrent), func() obj { return obj{"value": 0} })
		harness := open()
		parentId := createConversation(t, harness)
		var at durable.EntryId
		commit(t, harness.Session, func(tx durable.Tx) error {
			entry, err := tx.AppendEntry(parentId, durable.EntryDraft{Kind: "point"})
			at = entry.Id
			must(t, err)
			return mustDoc(t, tx, copied, parentId).Set("value", 7)
		})
		var childId durable.ConversationId
		commit(t, harness.Session, func(tx durable.Tx) error {
			child, err := tx.ForkConversation(parentId, at, ownerless())
			childId = child.Id
			return err
		})
		reads := harness.Storage.DocumentReadCount()
		state := documentState(t, harness, copied, childId)
		expectEqual(t, state.Value(), obj{"value": 7})
		if harness.Storage.DocumentReadCount() <= reads {
			t.Fatal("a fork copy cold-loads from Storage")
		}
		state.Dispose()
	})

	t.Run("hydrates a migrated tracker without writing and skips an equal version-base update", func(t *testing.T) {
		old := defineDoc("state.migration", 1, sessionScope, func() obj { return obj{"value": 3} })
		current := defineDoc("state.migration", 2, sessionScope, func() obj { return obj{"value": 0, "migrated": false} },
			withMigrate(func(value obj, _ int) obj { return obj{"value": value["value"], "migrated": true} }))
		harness := open()
		commit(t, harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(old); return err })
		must(t, harness.Session.UnloadDocuments())
		commits := len(harness.Storage.Commits())
		state := documentState(t, harness, current)
		expectEqual(t, state.Value(), obj{"value": 3, "migrated": true})
		if len(harness.Storage.Commits()) != commits {
			t.Fatal("hydration writes nothing")
		}
		baseline := state.Value()
		commit(t, harness.Session, func(tx durable.Tx) error { _, err := tx.Doc(current); return err })
		flush(harness)
		if len(harness.Storage.Commits()) != commits+1 {
			t.Fatal("the first transaction writes the version base")
		}
		if !same(state.Value(), baseline) {
			t.Fatal("an equal version base does not reach the state")
		}
		setStateValue(t, harness, current, 4)
		flush(harness)
		expectEqual(t, state.Value(), obj{"value": 4, "migrated": true})
		state.Dispose()
	})

	t.Run("continues from exact committed values after the tracker cache unloads", func(t *testing.T) {
		harness := createStateHarness(t)
		state := documentState(t, harness, stateStateDoc)
		baseline := state.Value()
		reads := harness.Storage.DocumentReadCount()
		must(t, harness.Session.UnloadDocuments())
		reloaded := snapshot(t, harness.Session, stateStateDoc)
		expectEqual(t, reloaded, baseline)
		if same(reloaded, baseline) || harness.Storage.DocumentReadCount() <= reads {
			t.Fatal("unload cold-loads a detached value")
		}
		setStateValue(t, harness, stateStateDoc, 6)
		flush(harness)
		expectEqual(t, state.Value()["value"], 6)
		state.Dispose()
	})

	// Upstream also asserts Object.isFrozen is false; Go values have no frozen state, so the shared-reference half is
	// the whole contract here.
	t.Run("exposes trusted shared immutable values without freezing", func(t *testing.T) {
		harness := createStateHarness(t)
		value := snapshot(t, harness.Session, stateStateDoc)
		state := documentState(t, harness, stateStateDoc)
		if !same(state.Value(), value) {
			t.Fatal("the state shares the committed snapshot")
		}
		state.Dispose()
	})
}

// Upstream's Session commit publishes before its promise resolves, and SessionSourceAttachment.publish drains in a
// microtask that runs the state's subscribers before the committer resumes (session/observation.ts). A subscriber has
// the committed frame when Commit returns, with no wait for deliveries; a subscriber that commits queues behind the
// Session line and its own frame reaches the same subscriber before the outer Commit returns.
func TestSessionStateSubscribersRunBeforeTheCommitReturns(t *testing.T) {
	t.Run("a committed value is delivered before the commit returns", func(t *testing.T) {
		harness := createStateHarness(t)
		state := documentState(t, harness, stateStateDoc)
		recorded := &deliveries{}
		_, err := state.Subscribe(recorded.listener)
		if err != nil {
			t.Fatal(err)
		}
		setStateValue(t, harness, stateStateDoc, 1)
		got := recorded.all()
		if len(got) != 2 || got[1].sequence != 1 || got[1].value["value"] != float64(1) {
			t.Fatalf("deliveries when Commit returned = %+v, want hydrate then update 1", got)
		}
	})

	t.Run("a subscriber that commits does not deadlock and is delivered its own frame", func(t *testing.T) {
		harness := createStateHarness(t)
		state := documentState(t, harness, stateStateDoc)
		recorded := &deliveries{}
		var once sync.Once
		_, err := state.Subscribe(func(value obj, c context.Context, delivery chord.ReplicatedStateDelivery) {
			recorded.listener(value, c, delivery)
			if delivery.Kind != chord.DeliveryUpdate {
				return
			}
			once.Do(func() { setStateValue(t, harness, stateStateDoc, 2) })
		})
		if err != nil {
			t.Fatal(err)
		}
		setStateValue(t, harness, stateStateDoc, 1)
		got := recorded.all()
		if len(got) != 3 || got[1].sequence != 1 || got[2].sequence != 2 {
			t.Fatalf("deliveries when Commit returned = %+v, want hydrate, update 1, update 2", got)
		}
	})
}

// Upstream's flush() resolves after the drain microtask, which runs the state's subscribers to completion in one turn
// (session-support.ts flush, session/observation.ts SessionSourceAttachment.publish). WaitDeliveries is that flush: a
// state drain another goroutine's commit already took off the queue and is still running counts as a scheduled delivery.
func TestWaitDeliveriesJoinsAStateDrainAnotherCommitIsRunning(t *testing.T) {
	harness := createStateHarness(t)
	state := documentState(t, harness, stateStateDoc)
	entered := make(chan struct{})
	release := make(chan struct{})
	var finished sync.Mutex
	done := false
	_, err := state.Subscribe(func(_ obj, _ context.Context, delivery chord.ReplicatedStateDelivery) {
		if delivery.Kind != chord.DeliveryUpdate {
			return
		}
		close(entered)
		<-release
		finished.Lock()
		done = true
		finished.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	committed := make(chan struct{})
	go func() {
		defer close(committed)
		setStateValue(t, harness, stateStateDoc, 1)
	}()
	<-entered
	waited := make(chan bool)
	go func() {
		harness.Session.WaitDeliveries()
		finished.Lock()
		waited <- done
		finished.Unlock()
	}()
	var deliveredWhenWaitReturned bool
	select {
	case deliveredWhenWaitReturned = <-waited:
		close(release)
	case <-time.After(100 * time.Millisecond):
		close(release)
		deliveredWhenWaitReturned = <-waited
	}
	<-committed
	if !deliveredWhenWaitReturned {
		t.Fatal("WaitDeliveries returned while a state subscriber was still running")
	}
}
