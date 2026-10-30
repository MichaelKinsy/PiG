package host

import (
	"context"
	"math"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/jobs/run-states.ts
//
// A job's run state documents (§15, slice 6b §4.1, §4.3): one per run by its id, Chord's own slot apart from the
// object's `self.state` slots. In memory, or persisted in the object's store (namespace `chord.job-states`), always in
// memory while the run is unsettled (`ctx.state.value` reads it synchronously). A persisted run's deferred changes (D8)
// are adopted and published at once and held, stored before the next change's own, at the outcome, at close or when
// their deadline passes.
//
// Forced Go differences. A time that Pi spells `Infinity` (a held change with no deadline) is `math.MaxInt64`
// milliseconds, as Loop.AfterFunc treats a MaxInt64 duration (DESIGN-go-layout.md section 9). Every member is used
// inside a section.

// heldState is a held state's ops, folded as they come (PendingOps: repeated writes of one leaf keep one op; past its
// bound only "store a base" stays), whether a held change asked for a base, and when it must be stored at the latest.
type heldState struct {
	pending  delta.PendingOps
	base     bool
	deadline int64
}

// runStatesOwner is what the run states tell their job: a document changed (`running` follows), a held change's
// deadline.
type runStatesOwner struct {
	documents *documents.Documents
	// changed is: run `id`'s document changed by ops, inside the commit step.
	changed func(id string, ops []delta.Op)
	// held is: a held change must be stored by deadline (`Date.now()` time).
	held func(deadline int64)
}

// defaultWithinMs is how long a deferred change is held at most by default (Durable's `progress.persistWithinMs`).
const defaultWithinMs = 1_000

// noDeadline is Pi's `Infinity` deadline: none held, or none with a deadline.
const noDeadline = int64(math.MaxInt64)

// runStates are a run's state documents. Created with the run, retired with its record; never drafted while absent.
type runStates struct {
	loop        eventLoop
	owner       *documents.Documents
	definition  *documents.SlotDefinition
	persistence *documents.Persistence
	states      runStatesOwner
	trackers    map[string]*delta.Tracker
	stored      map[string]documents.Stored
	held        map[string]*heldState
	// heldOrder is the ids of held, in the order the changes were first held (a Map's insertion order).
	heldOrder []string
}

var (
	_ documents.DocumentSlot  = (*runStates)(nil)
	_ documents.PersistedSlot = (*runStates)(nil)
)

// newRunStates are the states of a job whose documents `definition` declares; persistence is the store side when the job
// persists, nil in memory.
func newRunStates(loop eventLoop, definition *documents.StateDefinition, states runStatesOwner, persistence *documents.Persistence) *runStates {
	return &runStates{
		loop: loop, owner: states.documents, definition: documents.NewSlotDefinition(definition), persistence: persistence,
		states: states, trackers: map[string]*delta.Tracker{}, stored: map[string]documents.Stored{}, held: map[string]*heldState{},
	}
}

// Owner implements documents.DocumentSlot.
func (states *runStates) Owner() *documents.Documents { return states.owner }

// Definition implements documents.PersistedSlot.
func (states *runStates) Definition() *documents.SlotDefinition { return states.definition }

// Persisted implements documents.DocumentSlot: the store side when the job persists; nil in memory.
func (states *runStates) Persisted() documents.PersistedSlot {
	if states.persistence == nil {
		return nil
	}
	return states
}

// Persistence implements documents.PersistedSlot.
func (states *runStates) Persistence() *documents.Persistence {
	// Only reached through `Persisted`, which a persisted job's states alone have.
	return states.persistence
}

// value is the value of run `id`'s document; nil once retired.
func (states *runStates) value(id string) *delta.JsonObject {
	if tracker := states.trackers[id]; tracker != nil {
		return tracker.Value()
	}
	return nil
}

// Current implements documents.DocumentSlot.
func (states *runStates) Current(key string) (*delta.Tracker, error) {
	return states.trackers[key], nil
}

// Initial implements documents.DocumentSlot.
func (states *runStates) Initial(context.Context) (*delta.JsonObject, error) {
	return nil, data.NewChordError(data.ChordErrorInit{
		Code: data.CodeReleased, Reason: data.ReasonSettled,
		Message: states.definition.Current().Name + ": the run's document is retired",
	}, nil)
}

// Cold implements documents.PersistedSlot: pinned in memory while the run is unsettled: never cold.
func (states *runStates) Cold(string) bool { return false }

// Stored implements documents.PersistedSlot.
func (states *runStates) Stored(key string) *documents.Stored {
	if stored, found := states.stored[key]; found {
		return &stored
	}
	return nil
}

// Load implements documents.PersistedSlot: nothing to load, the documents are in memory.
func (states *runStates) Load(string, context.Context, *documents.SealToken) error { return nil }

// Stage implements documents.PersistedSlot: a change of run `key`'s document: a deferred one of a stored document is
// held (nothing written); an immediate one stores what is held with its own ops, as one delta on the stored revision or
// a base.
func (states *runStates) Stage(key string, change documents.DocumentChange, was *documents.Stored, writes *[]store.StagedWrite, persistence documents.CommitPersistence) (documents.StoredChange, error) {
	if persistence.Kind == documents.CommitDeferred && was != nil && !change.Retires && change.Prepared != nil {
		within := float64(defaultWithinMs)
		if persistence.WithinMs != nil {
			within = *persistence.WithinMs
		}
		return documents.StoredChange{Kind: documents.StoredHeld, Stored: *was, WithinMs: within}, nil
	}
	var held *documents.Held
	if each := states.held[key]; each != nil {
		held = heldOps(each)
	}
	return states.persistence.Stage(key, states.definition.Current(), was, change, writes, held)
}

// Adopt implements documents.DocumentSlot.
func (states *runStates) Adopt(key string, tracker *delta.Tracker, prepared *delta.Prepared, change *documents.StoredChange) error {
	if err := tracker.Adopt(prepared); err != nil {
		return err
	}
	switch {
	case change != nil && change.Kind == documents.StoredHeld:
		states.hold(key, prepared, *change)
	case change != nil:
		states.settle(key, documents.StoredAfter(*change))
	}
	states.states.changed(key, prepared.Ops())
	return nil
}

// Retire implements documents.DocumentSlot: retires run `key`'s document (its record's deletion): what is held goes
// with it.
func (states *runStates) Retire(key string) {
	delete(states.trackers, key)
	delete(states.stored, key)
	states.unhold(key)
}

// create makes run `id`'s document with value, as its start created it or a load found it (stored: persisted).
func (states *runStates) create(id string, value *delta.JsonObject, stored *documents.Stored) {
	states.trackers[id] = delta.Track(value)
	if stored != nil {
		states.stored[id] = *stored
	}
}

// stageCreate stages the creation of run `id`'s document with value into writes (its start); it returns the adoption,
// which creates it in memory with what is stored.
func (states *runStates) stageCreate(id string, value *delta.JsonObject, writes func() *[]store.StagedWrite) (func(), error) {
	if states.persistence == nil {
		return func() { states.create(id, value, nil) }, nil
	}
	definition := states.definition.Current()
	version := int64(definition.Version)
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		return nil, err
	}
	created := &documents.CreatedID{}
	base := store.DocumentBase{Version: version, Fingerprint: definition.Fingerprint, Value: value}
	target := writes()
	*target = append(*target, store.CreateDocument(states.persistence.At(id), base, string(text), func(made int64) { created.ID = made }))
	return func() {
		states.create(id, value, &documents.Stored{ID: created.ID, Revision: 1, Version: version})
	}, nil
}

// deadline is when run `id`'s held changes must be stored at the latest (noDeadline: none held, or none with a
// deadline).
func (states *runStates) deadline(id string) int64 {
	if each := states.held[id]; each != nil {
		return each.deadline
	}
	return noDeadline
}

// heldIds are the runs whose documents hold changes not stored yet.
func (states *runStates) heldIds() []string { return append([]string(nil), states.heldOrder...) }

// stageHeld stages what run `id`'s document holds into writes (a flush: its deadline, the outcome, the close writer), as
// one delta on its stored revision or a base; it returns the adoption, nil when nothing is held.
func (states *runStates) stageHeld(id string, writes *[]store.StagedWrite) (func(), error) {
	each := states.held[id]
	stored, found := states.stored[id]
	tracker := states.trackers[id]
	if each == nil || !found || tracker == nil {
		return nil, nil
	}
	definition := states.definition.Current()
	version := int64(definition.Version)
	value := tracker.Value()
	held := heldOps(each)
	base := held.Base || stored.Version != version
	var content store.DocumentContent
	var payload any
	if base {
		content = store.DocumentContent{Kind: store.ContentBase, Version: version, Fingerprint: definition.Fingerprint, Value: value}
		payload = value
	} else {
		content = store.DocumentContent{Kind: store.ContentDelta, Version: version, Ops: held.Ops}
		payload = held.Ops
	}
	text, err := jsstring.MarshalJSON(payload)
	if err != nil {
		return nil, err
	}
	*writes = append(*writes, store.ChangeDocument(stored.ID, stored.Revision, content, string(text)))
	deltas := stored.DeltasSinceBase + 1
	if base {
		deltas = 0
	}
	after := documents.Stored{ID: stored.ID, Revision: stored.Revision + 1, Version: version, DeltasSinceBase: deltas}
	return func() {
		if states.held[id] == each {
			states.settle(id, &after)
		}
	}, nil
}

// drop drops every document (the object ended): what is held is lost, as a crash loses it.
func (states *runStates) drop() {
	clear(states.trackers)
	clear(states.stored)
	clear(states.held)
	states.heldOrder = nil
}

// settle is: what is stored of run `key`'s document after a commit stored it: nothing is held any more.
func (states *runStates) settle(key string, stored *documents.Stored) {
	if stored == nil {
		delete(states.stored, key)
	} else {
		states.stored[key] = *stored
	}
	states.unhold(key)
}

func (states *runStates) unhold(key string) {
	if _, found := states.held[key]; !found {
		return
	}
	delete(states.held, key)
	for index, id := range states.heldOrder {
		if id == key {
			states.heldOrder = append(states.heldOrder[:index], states.heldOrder[index+1:]...)
			break
		}
	}
}

// hold is: a deferred change adopted: its ops held (a base when `checkpointWhen` asks), its deadline told.
func (states *runStates) hold(key string, prepared *delta.Prepared, change documents.StoredChange) {
	each := states.held[key]
	if each == nil {
		each = &heldState{deadline: noDeadline}
	}
	deltasSinceBase := change.Stored.DeltasSinceBase
	if each.pending.Size() > 0 {
		deltasSinceBase++
	}
	if states.definition.Current().CheckpointWhen(prepared.Value(), prepared.Ops(), documents.CheckpointInfo{DeltasSinceBase: deltasSinceBase}) {
		each.base = true
	}
	// A base stores the whole value: no op needs keeping for it.
	if each.base {
		each.pending.Take()
	} else {
		each.pending.Push(prepared.Ops())
	}
	each.deadline = min(each.deadline, addMillis(states.loop.Now(), change.WithinMs))
	if _, found := states.held[key]; !found {
		states.heldOrder = append(states.heldOrder, key)
	}
	states.held[key] = each
	if each.deadline != noDeadline {
		states.states.held(each.deadline)
	}
}

// addMillis is now + within milliseconds, noDeadline when that is not finite.
func addMillis(now int64, within float64) int64 {
	if math.IsInf(within, 1) || math.IsNaN(within) || within >= float64(noDeadline-now) {
		return noDeadline
	}
	return now + int64(within)
}

// heldOps is what each stores, its queue left as it was (a commit may still be refused): its folded ops, or a base once
// a held change asked for one or the queue overflowed. Taking and pushing back one batch keeps it by reference: no copy.
func heldOps(each *heldState) *documents.Held {
	if each.base {
		return &documents.Held{Base: true}
	}
	taken := each.pending.Take()
	if taken.Replace {
		each.base = true
		return &documents.Held{Base: true}
	}
	each.pending.Push(taken.Ops)
	return &documents.Held{Ops: taken.Ops}
}
