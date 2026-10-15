package driver_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// The Go port of session-states.test.ts through driver.Session over the real core.

var stateDoc = defineDoc("state.state", 1, func() obj { return delta.JsonObjectOf("value", 0, "retained", delta.JsonObjectOf("label", "stable")) })

func (r *coreRig) setStateValue(t *testing.T, n int) {
	t.Helper()
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(stateDoc)
		if err != nil {
			return err
		}
		return o.Set("value", n)
	})
}

func stateValue(state durable.DocumentState[obj]) string {
	v := state.Value()
	if v == nil {
		return "<null>"
	}
	return fmt.Sprint(v.Value("value"))
}

type deliveries struct {
	mu        sync.Mutex
	sequences []int
	values    []string
}

func (d *deliveries) record(v obj, delivery chord.ReplicatedStateDelivery) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sequences = append(d.sequences, delivery.Sequence)
	if v == nil {
		d.values = append(d.values, "<null>")
	} else {
		d.values = append(d.values, fmt.Sprint(v.Value("value")))
	}
}

func (d *deliveries) snapshot() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return fmt.Sprint(d.sequences, d.values)
}

func (r *coreRig) stateWithDoc(t *testing.T) durable.DocumentState[obj] {
	t.Helper()
	r.commit(t, func(tx durable.Tx) error { return touch(tx, stateDoc) })
	state, err := r.s.DocumentStateErased(t.Context(), stateDoc)
	if err != nil || state == nil {
		t.Fatalf("state: %v %v", state, err)
	}
	return state
}

// session-states.test.ts:31 "never creates an absent document".
func TestCoreDocumentStateNeverCreatesAnAbsentDocument(t *testing.T) {
	r := openCoreDB(t)
	before := r.revisions(t, "state.state")
	state, err := r.s.DocumentStateErased(t.Context(), stateDoc)
	if err != nil || state != nil {
		t.Fatalf("absent = %v, %v", state, err)
	}
	eq(t, "revisions", r.revisions(t, "state.state"), before)
	eq(t, "conversations", r.conversationCount(t), 0)
}

// session-states.test.ts:40 "returns an immediately hydrated read-only state with contiguous Chord deliveries": the hydration is
// sequence 0 and each later commit is the next sequence.
func TestCoreDocumentStateDeliversContiguousSequences(t *testing.T) {
	r := openCoreDB(t)
	state := r.stateWithDoc(t)
	var got deliveries
	off := state.Subscribe(func(v obj, _ context.Context, d chord.ReplicatedStateDelivery) { got.record(v, d) })
	defer off()
	if v := stateValue(state); v != "0" {
		t.Fatalf("hydrated value = %s", v)
	}
	r.setStateValue(t, 1)
	r.setStateValue(t, 2)
	waitFor(t, "three contiguous deliveries", func() bool { return got.snapshot() == "[0 1 2] [0 1 2]" })
	waitFor(t, "state follows", func() bool { return stateValue(state) == "2" })
	state.Dispose()
}

// session-states.test.ts:62 "creates independent disposable states for one incarnation".
func TestCoreDocumentStatesAreIndependentAndDisposable(t *testing.T) {
	r := openCoreDB(t)
	first := r.stateWithDoc(t)
	second, err := r.s.DocumentStateErased(t.Context(), stateDoc)
	if err != nil || second == nil || second == first {
		t.Fatalf("second = %v, %v", second, err)
	}
	r.setStateValue(t, 1)
	waitFor(t, "both follow 1", func() bool { return stateValue(first) == "1" && stateValue(second) == "1" })
	first.Dispose()
	r.setStateValue(t, 2)
	waitFor(t, "the live state follows 2", func() bool { return stateValue(second) == "2" })
	eq(t, "disposed state", stateValue(first), "1")
	second.Dispose()
}

// session-states.test.ts:102 "captures a late baseline without redelivering an already covered commit".
// Mutation evidence (core watch.go / driver watch.go): the baseline value of watch.open answered as null turns this and five
// other state tests red. A frame the core wrongly re-emitted for the baseline commit while the state is being mounted is
// folded into the baseline before any subscriber exists (watchHub.bind replays held frames into the source first), as Pi's
// state folds a covered commit, so that redelivery is unobservable to a subscriber and no mutation of it can go red here.
func TestCoreDocumentStateLateBaselineIsNotRedelivered(t *testing.T) {
	r := openCoreDB(t)
	r.commit(t, func(tx durable.Tx) error { return touch(tx, stateDoc) })
	r.setStateValue(t, 1)
	state, err := r.s.DocumentStateErased(t.Context(), stateDoc)
	if err != nil || state == nil {
		t.Fatalf("state: %v %v", state, err)
	}
	var got deliveries
	off := state.Subscribe(func(v obj, _ context.Context, d chord.ReplicatedStateDelivery) { got.record(v, d) })
	defer off()
	r.commit(t, func(tx durable.Tx) error { return touch(tx, stateDoc) }) // unrelated, no-op commit
	eq(t, "value", stateValue(state), "1")
	eq(t, "deliveries", got.snapshot(), "[0] [1]")
	state.Dispose()
}

// session-states.test.ts:116 "publishes null retirement and never follows a replacement incarnation".
// Mutation evidence: three independent guards keep the old state off the replacement incarnation (core watch.go: the watch
// is deleted on retirement, and a frame is delivered only to the watch of the changed document ID; the in-process
// CommittedStateSource: it ignores frames after a retired value). Removing any one leaves the test green; removing all three
// turns it red, so the test proves the behaviour, not one guard.
func TestCoreDocumentStatePublishesNullRetirementAndNeverFollowsAReplacement(t *testing.T) {
	r := openCoreDB(t)
	oldState := r.stateWithDoc(t)
	r.commit(t, func(tx durable.Tx) error {
		if err := tx.RetireDoc(stateDoc); err != nil {
			return err
		}
		o, err := tx.Doc(stateDoc)
		if err != nil {
			return err
		}
		return o.Set("value", 10)
	})
	waitFor(t, "retirement publishes null", func() bool { return stateValue(oldState) == "<null>" })
	replacement, err := r.s.DocumentStateErased(t.Context(), stateDoc)
	if err != nil || replacement == nil || stateValue(replacement) != "10" {
		t.Fatalf("replacement = %v, %v", replacement, err)
	}
	r.setStateValue(t, 11)
	waitFor(t, "replacement follows", func() bool { return stateValue(replacement) == "11" })
	eq(t, "old state", stateValue(oldState), "<null>")
	oldState.Dispose()
	replacement.Dispose()
}

// session-states.test.ts:143 "cold-loads a definition-free fork copy".
func TestCoreDocumentStateColdLoadsAForkCopy(t *testing.T) {
	r := openCoreDB(t)
	copied := forkDoc("state.copied", durable.HistoryLatest, durable.ForkCurrent)
	parentId := r.createConversation(t)
	entry := r.appendEntry(t, parentId, "point", func(tx durable.Tx) error { return setDoc(tx, copied, "7", parentId) })
	child := r.forkOf(t, parentId, entry, nil)
	state, err := r.s.DocumentStateErased(t.Context(), copied, child.Id)
	if err != nil || state == nil {
		t.Fatalf("state = %v, %v", state, err)
	}
	eq(t, "value", state.Value().Value("value"), "7")
	state.Dispose()
}

// session-states.test.ts:170 "hydrates a migrated tracker without writing and skips an equal version-base update": the state of
// the newer token hydrates the migrated value with no commit; touching the document stores the version base without publishing
// a new value; a real edit then arrives migrated.
func TestCoreDocumentStateHydratesAMigratedTrackerWithoutWriting(t *testing.T) {
	r := openCoreDB(t)
	old := defineDoc("state.migration", 1, func() obj { return delta.JsonObjectOf("value", 3) })
	current := defineDoc("state.migration", 2, func() obj { return delta.JsonObjectOf("value", 0, "migrated", false) },
		withMigrate(func(v obj, _ int) obj { return delta.JsonObjectOf("value", v.Value("value"), "migrated", true) }))
	r.commit(t, func(tx durable.Tx) error { return touch(tx, old) })
	before := r.revisions(t, "state.migration")
	state, err := r.s.DocumentStateErased(t.Context(), current)
	if err != nil || state == nil {
		t.Fatalf("state = %v, %v", state, err)
	}
	eq(t, "hydrated", plain(state.Value()), "map[migrated:true value:3]")
	eq(t, "hydration writes nothing", r.revisions(t, "state.migration"), before)
	var got deliveries
	off := state.Subscribe(func(v obj, _ context.Context, d chord.ReplicatedStateDelivery) { got.record(v, d) })
	defer off()
	r.commit(t, func(tx durable.Tx) error { return touch(tx, current) })
	eq(t, "the version base is stored", r.revisions(t, "state.migration"), []string{"base@2"})
	eq(t, "no new value was published", got.snapshot(), "[0] [3]")
	r.commit(t, func(tx durable.Tx) error {
		o, err := tx.Doc(current)
		if err != nil {
			return err
		}
		return o.Set("value", 4)
	})
	waitFor(t, "the edit arrives migrated", func() bool { return plain(state.Value()) == "map[migrated:true value:4]" })
	state.Dispose()
}
