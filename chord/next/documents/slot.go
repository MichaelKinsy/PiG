package documents

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/next/documents/slot.ts
//
// Document slots (slice 6a §1.2): one per declared document name on its object, across the object's attempts. A slot
// holds the documents' trackers, the version and definition its values are of, what is stored of persisted ones, and
// the watches; each attempt's handles are thin views of it. This file: what every slot is, and the singleton's slot
// (the keyed one is keyed_slot.go).
//
// Async contract: Load blocks on the store's read (outside the host's sections, on the object's line); every other
// method is a section (inside Do) and does not block.

// AnyDefinition is a document definition, whatever its keyedness.
type AnyDefinition = *StateDefinition

// DocumentSlot is what a transaction drafts and adopts: documents of one object by key (a singleton ignores the key).
// The slots of `self.state`, and Chord's own (a job's run state documents).
type DocumentSlot interface {
	Owner() *Documents
	// Current is the tracker of the document at key, nil while absent (a keyed document older than the slot migrates on
	// this access, which may fail).
	Current(key string) (*delta.Tracker, error)
	// Initial is the value a draft of an absent document starts from.
	Initial(ctx context.Context) (*JsonObject, error)
	// Persisted is the persisted slot's store side; nil for documents in memory only.
	Persisted() PersistedSlot
	// Adopt adopts a commit of the document at key (inside the batch), with what the commit stored of it.
	Adopt(key string, tracker *delta.Tracker, prepared *delta.Prepared, stored *StoredChange) error
	// Retire retires the document at key (inside the batch).
	Retire(key string)
}

// PersistedSlot is what a transaction needs of a persisted slot (slice 6a §4, §10).
type PersistedSlot interface {
	Persistence() *Persistence
	Definition() *SlotDefinition
	// Cold reports whether the document at key may be stored without being in memory (a persisted keyed document not
	// loaded).
	Cold(key string) bool
	// Stored is what is stored of the document at key as memory knows it; nil when absent or cold.
	Stored(key string) *Stored
	// Stage stages one commit's writes of the document at key into writes (was: what is stored of it, a cold one's as
	// the transaction found it); persistence is the transaction's (a deferred change of a run's state is held, D8).
	Stage(key string, change DocumentChange, was *Stored, writes *[]store.StagedWrite, persistence CommitPersistence) (StoredChange, error)
	// Load loads the document at key (its caller holds the line): installed when present and still absent from memory,
	// and, for a transaction's load (token not nil), while the token is not sealed (a cut transaction released the line,
	// so a successor may have changed or retired the key meanwhile).
	Load(key string, ctx context.Context, token *SealToken) error
}

// SlotDefinition is the definition a slot's values are of (the one that hydrated it last; the first declaration until
// then): the checks of a new attempt's declaration and the old handles' version fence (R15).
type SlotDefinition struct {
	current  AnyDefinition
	hydrated bool
}

// NewSlotDefinition is the slot definition of a slot first declared with definition.
func NewSlotDefinition(definition AnyDefinition) *SlotDefinition {
	return &SlotDefinition{current: definition}
}

// Current is the definition the values are of.
func (slot *SlotDefinition) Current() AnyDefinition { return slot.current }

// Hydrated is the definition the values were hydrated with, nil before the first hydration.
func (slot *SlotDefinition) Hydrated() AnyDefinition {
	if !slot.hydrated {
		return nil
	}
	return slot.current
}

// Check checks a declaration of this name by a new attempt: a flipped `keyed` or `persist`, a version older than the
// values (old code) or the same version with another fingerprint (a schema changed without a bump) fail (the attempt
// fails broken).
func (slot *SlotDefinition) Check(definition AnyDefinition) error {
	current := slot.current
	name := current.Name
	if definition.Keyed != current.Keyed {
		kind := "singleton"
		if definition.Keyed {
			kind = "keyed"
		}
		return fmt.Errorf("%s: declared %s, its documents are not", name, kind)
	}
	if definition.Persist != current.Persist {
		kind := "in memory"
		if definition.Persist {
			kind = "persisted"
		}
		return fmt.Errorf("%s: declared %s, its documents are not", name, kind)
	}
	if definition.Version < current.Version {
		return fmt.Errorf("%s: version %d is older than its documents' (%d)", name, definition.Version, current.Version)
	}
	if definition.Version == current.Version && definition.Fingerprint != current.Fingerprint {
		return fmt.Errorf("%s: the schema changed without a version bump (version %d)", name, current.Version)
	}
	return nil
}

// Hydrate records the hydration with definition.
func (slot *SlotDefinition) Hydrate(definition AnyDefinition) {
	slot.current = definition
	slot.hydrated = true
}

// AssertVersion fails `released` for a handle of version once the values migrated past it (old code reads no new
// shape), and `not_ready` for a handle of a newer version until its activation migrated them (new code reads no old
// shape: a retry's factory).
func (slot *SlotDefinition) AssertVersion(version int) error {
	current := slot.current
	if version == current.Version {
		return nil
	}
	message := fmt.Sprintf("%s: a handle of version %d; the documents are at version %d", current.Name, version, current.Version)
	if version < current.Version {
		return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonVersion, Message: message}, nil)
	}
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: message + " until the activation migrates them"}, nil)
}

// SingletonSlot is a singleton's slot: one tracker while written, `initial()` while absent; the source its handles and
// members watch. Persisted, it loads once per object (again after a failed load) and stays in memory while the object
// lives.
type SingletonSlot struct {
	AddressSource
	owner       *Documents
	definition  *SlotDefinition
	persistence *Persistence
	// initial is `initial()`, taken at hydration; nil before the first.
	initial *JsonObject
	tracker *delta.Tracker
	// stored is what is stored of it (persisted); nil while absent.
	stored *Stored
	// loaded is: in memory always; persisted once loaded.
	loaded bool
}

// NewSingletonSlot is the slot of definition on owner, persisted when persistence is not nil.
func NewSingletonSlot(owner *Documents, definition AnyDefinition, persistence *Persistence) *SingletonSlot {
	slot := &SingletonSlot{owner: owner, definition: NewSlotDefinition(definition), persistence: persistence, loaded: persistence == nil}
	slot.Init(owner.Batch(), slot)
	return slot
}

// Owner is the object's documents.
func (slot *SingletonSlot) Owner() *Documents { return slot.owner }

// Definition is the slot's definition state.
func (slot *SingletonSlot) Definition() *SlotDefinition { return slot.definition }

// Persistence is the store side of a persisted slot.
func (slot *SingletonSlot) Persistence() *Persistence { return slot.persistence }

// Persisted is the slot itself when it is persisted, nil otherwise.
func (slot *SingletonSlot) Persisted() PersistedSlot {
	if slot.persistence == nil {
		return nil
	}
	return slot
}

// Object is the value as the document's root: the tracker's, `initial()` while absent. It fails `released` once the
// object ended and `not_ready` before the activation hydrated it (and, persisted, loaded it).
func (slot *SingletonSlot) Object() (*JsonObject, error) {
	if err := slot.owner.AssertLive(); err != nil {
		return nil, err
	}
	if slot.initial == nil || !slot.loaded {
		message := slot.definition.current.Name + ": read before activation"
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: message}, nil)
	}
	if slot.tracker != nil {
		return slot.tracker.Value(), nil
	}
	return slot.initial, nil
}

// Value is Object as a JSON value.
func (slot *SingletonSlot) Value() (delta.JsonValue, error) {
	value, err := slot.Object()
	if err != nil {
		return nil, err
	}
	return value, nil
}

// Hydrate takes the attempt's declaration definition (Check accepted it): the first takes `initial()`; a newer version
// migrates the value in memory (a persisted one stores a base with its next change) and takes `initial()` again, and a
// value that changed reaches watches as a root replacement; the same version keeps everything.
func (slot *SingletonSlot) Hydrate(definition AnyDefinition, ctx context.Context) error {
	from := slot.definition.Hydrated()
	if from != nil && from.Version == definition.Version {
		slot.definition.Hydrate(definition)
		return nil
	}
	var before *JsonObject
	if from != nil && slot.loaded {
		value, err := slot.Object()
		if err != nil {
			return err
		}
		before = value
	}
	tracker := slot.tracker
	if tracker != nil && from != nil {
		migrated, err := definition.Migrate(slot.tracker.Value(), from.Version, ctx)
		if err != nil {
			return err
		}
		tracker = delta.Track(migrated)
	}
	initial, err := definition.Initial(ctx)
	if err != nil {
		return err
	}
	slot.initial = initial
	slot.tracker = tracker
	slot.definition.Hydrate(definition)
	if before != nil {
		return slot.owner.Batch().Commit(func() error { return slot.Replaced() }, ctx)
	}
	return nil
}

// Load loads the persisted document unless it is loaded already (the caller holds the line).
func (slot *SingletonSlot) Load(_ string, ctx context.Context, _ *SealToken) error {
	var loaded bool
	slot.owner.Do(func() { loaded = slot.loaded })
	if loaded {
		return nil
	}
	document, err := slot.persistence.Read("", ctx)
	if err != nil {
		return err
	}
	var failure error
	slot.owner.Do(func() {
		// Singletons load at activation, on the line: what the code cannot read fails the activation.
		if document != nil {
			result, err := slot.persistence.Decode(document, slot.definition.current, ctx)
			if err != nil {
				failure = err
				return
			}
			slot.tracker = delta.Track(result.Value)
			stored := result.Stored
			slot.stored = &stored
		}
		slot.loaded = true
	})
	return failure
}

// LoadDeclared loads the persisted document unless it is loaded already (activation, holding the line).
func (slot *SingletonSlot) LoadDeclared(ctx context.Context) error { return slot.Load("", ctx, nil) }

// Key is a singleton has one document: its key is always "".
func (slot *SingletonSlot) Key(string) (string, error) { return "", nil }

// Cold is false: a singleton loads at activation.
func (slot *SingletonSlot) Cold(string) bool { return false }

// Stored is what is stored of the document.
func (slot *SingletonSlot) Stored(string) *Stored { return slot.stored }

// Stage stages the commit's writes of the document.
func (slot *SingletonSlot) Stage(key string, change DocumentChange, was *Stored, writes *[]store.StagedWrite, _ CommitPersistence) (StoredChange, error) {
	return slot.persistence.Stage(key, slot.definition.current, was, change, writes, nil)
}

// Current is the tracker while written.
func (slot *SingletonSlot) Current(string) (*delta.Tracker, error) { return slot.tracker, nil }

// Initial is the value a draft of the absent document starts from: the `initial()` taken at hydration.
func (slot *SingletonSlot) Initial(context.Context) (*JsonObject, error) { return slot.initial, nil }

// Adopt adopts a commit of the document.
func (slot *SingletonSlot) Adopt(_ string, tracker *delta.Tracker, prepared *delta.Prepared, stored *StoredChange) error {
	// Unchecked: a step admitted before the object ended adopts after its seal (slice 6a §1.3).
	before := slot.initial
	if slot.tracker != nil {
		before = slot.tracker.Value()
	}
	if err := tracker.Adopt(prepared); err != nil {
		return err
	}
	slot.tracker = tracker
	slot.stored = nil
	if stored != nil {
		slot.stored = StoredAfter(*stored)
	}
	slot.Written(before, prepared)
	return nil
}

// Retire retires the document: it reads `initial()` again, and the frame says it was retired.
func (slot *SingletonSlot) Retire(string) {
	slot.tracker = nil
	slot.stored = nil
	slot.RetiredTo(slot.initial)
}

// Drop is: its object ended or no attempt declares it any more: the document goes and its watches end.
func (slot *SingletonSlot) Drop() {
	slot.tracker = nil
	slot.stored = nil
	slot.EndWatches()
}

var (
	_ DocumentSlot       = (*SingletonSlot)(nil)
	_ PersistedSlot      = (*SingletonSlot)(nil)
	_ state.SourceHandle = (*SingletonSlot)(nil)
)
