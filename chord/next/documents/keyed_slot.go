package documents

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/next/documents/keyed-slot.ts
//
// Keyed slots (slice 6a §1.2, §4, §10, §11): the documents of one keyed name on one object, by key. In memory, every
// present document stays; persisted, a document is loaded on demand (a cold read is a job on the object's line, one
// load in flight per key, shared) and leaves memory when unpinned and idle for the retention (residency.go). A key
// read through `at(key)` or a watch has its own source, which exists while the key is pinned.
//
// Async contract: Get, Watch, Ensure, Load and the key iterator block on the line, the store or a shared load (never
// inside Do); a caller's ctx ends only its own wait (`awaitWithContext`). A cold load is shared by every reader of the
// key: it runs once on the line under the owner's context, as one goroutine whose result every waiter reads. Pin,
// Hydrate, Adopt, Retire, Evict and Drop are sections.

// Access is whom a read is for: a handle's seal token and the version it was declared with (R15).
type Access struct {
	Token   *SealToken
	Version int
}

// keysPage is the keys `Keys` reads per page from the store.
const keysPage = 100

// sharedLoad is a cold load in flight, shared by its waiters: their handles' tokens, and those already sealed when the
// load took the line (nil until then), whose waiters it refuses (rule 4).
type sharedLoad struct {
	done    chan struct{}
	err     error
	tokens  map[*SealToken]struct{}
	refused map[*SealToken]struct{}
	// value is the loaded document's value (nil: the store has none), kept for the waiters: an ending object drops its
	// residents once the line settled, which can be before a waiter resumed (Pi's microtask order puts the waiter first).
	value *JsonObject
}

// KeyedSlot is a keyed slot: one resident per document in memory, one source per pinned key.
type KeyedSlot struct {
	owner       *Documents
	definition  *SlotDefinition
	persistence *Persistence
	residents   map[string]*Resident
	// sources are the sources of pinned keys: a key is pinned while it has one.
	sources map[string]*KeySource
	// loads are the cold loads in flight, by key: shared by every reader of the key.
	loads map[string]*sharedLoad
	// keyWhat names a refused key.
	keyWhat string
}

// NewKeyedSlot is the keyed slot of definition on owner, persisted when persistence is not nil.
func NewKeyedSlot(owner *Documents, definition AnyDefinition, persistence *Persistence) *KeyedSlot {
	return &KeyedSlot{
		owner: owner, definition: NewSlotDefinition(definition), persistence: persistence,
		residents: map[string]*Resident{}, sources: map[string]*KeySource{}, loads: map[string]*sharedLoad{},
		keyWhat: definition.Name + ": key",
	}
}

// Owner is the object's documents.
func (slot *KeyedSlot) Owner() *Documents { return slot.owner }

// Definition is the slot's definition state.
func (slot *KeyedSlot) Definition() *SlotDefinition { return slot.definition }

// Persistence is the store side of a persisted slot.
func (slot *KeyedSlot) Persistence() *Persistence { return slot.persistence }

// Persisted is the slot itself when it is persisted, nil otherwise.
func (slot *KeyedSlot) Persisted() PersistedSlot {
	if slot.persistence == nil {
		return nil
	}
	return slot
}

// Get is the value at key, nil when absent. A document in memory is read at once (also through a sealed handle); a cold
// persisted one is loaded on the line (refused `released` once the access's token is sealed), and ctx ends only this
// caller's wait. An absent key is not cached (rule 20).
func (slot *KeyedSlot) Get(raw string, access Access, ctx context.Context) (*JsonObject, error) {
	key, err := slot.Key(raw)
	if err != nil {
		return nil, err
	}
	return slot.get(key, access, ctx)
}

// get is Get of a key that went through Key already.
func (slot *KeyedSlot) get(key string, access Access, ctx context.Context) (*JsonObject, error) {
	var value *JsonObject
	var failure error
	var settled bool
	slot.owner.Do(func() {
		if failure = slot.owner.AssertLive(); failure != nil {
			return
		}
		if failure = slot.definition.AssertVersion(access.Version); failure != nil {
			return
		}
		resident, err := slot.resident(key)
		if err != nil {
			failure = err
			return
		}
		if resident != nil {
			slot.touch(resident)
			value, settled = resident.Tracker.Value(), true
			return
		}
		settled = slot.persistence == nil
	})
	if failure != nil || settled {
		return value, failure
	}
	loaded, err := slot.loadShared(key, access.Token, ctx)
	if err != nil {
		return nil, err
	}
	slot.owner.Do(func() {
		if resident := slot.residents[key]; resident != nil {
			value = resident.Tracker.Value()
		} else {
			value = loaded
		}
	})
	return value, nil
}

// KeyIterator is the present keys in code point order (Pi's `AsyncIterable<string>`): in memory, the residents;
// persisted, the store's, a page per line job. The access checks run at the first Next, as the generator's body does.
type KeyIterator struct {
	slot    *KeyedSlot
	access  Access
	ctx     context.Context
	started bool
	done    bool
	buffer  []string
	after   *string
}

// Keys is the iterator of the present keys.
func (slot *KeyedSlot) Keys(access Access, ctx context.Context) *KeyIterator {
	return &KeyIterator{slot: slot, access: access, ctx: ctx}
}

// Next is the next key; ok is false once every key was read.
func (it *KeyIterator) Next(ctx context.Context) (key string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, context.Cause(ctx)
	}
	slot := it.slot
	if !it.started {
		it.started = true
		var failure error
		slot.owner.Do(func() {
			if failure = slot.owner.AssertLive(); failure != nil {
				return
			}
			if failure = slot.definition.AssertVersion(it.access.Version); failure != nil {
				return
			}
			if slot.persistence == nil {
				for key := range slot.residents {
					it.buffer = append(it.buffer, key)
				}
				slices.SortFunc(it.buffer, store.CompareText)
				it.done = true
			}
		})
		if failure != nil {
			it.done = true
			return "", false, failure
		}
	}
	for len(it.buffer) == 0 {
		if it.done {
			return "", false, nil
		}
		var page []string
		err := slot.owner.OnLine(it.ctx, func() error {
			var err error
			page, err = slot.persistence.Keys(it.after, keysPage, it.ctx)
			return err
		}, it.access.Token)
		if err != nil {
			it.done = true
			return "", false, err
		}
		it.buffer = page
		if len(page) < keysPage {
			it.done = true
		} else {
			last := page[len(page)-1]
			it.after = &last
		}
	}
	key = it.buffer[0]
	it.buffer = it.buffer[1:]
	return key, true, nil
}

// Close ends the iteration.
func (it *KeyIterator) Close() {
	it.done = true
	it.buffer = nil
}

// Watch is a watch of the document at key, nil when absent (after a cold load); it follows the address (a retirement
// is a frame with the `retired` flag) and pins the key until it ends.
func (slot *KeyedSlot) Watch(raw string, access Access, ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	key, err := slot.Key(raw)
	if err != nil {
		return nil, err
	}
	value, err := slot.get(key, access, ctx)
	if err != nil || value == nil {
		return nil, err
	}
	var watch *chord.Watch[delta.JsonValue]
	var failure error
	slot.owner.Do(func() {
		release, err := slot.Pin(key, ctx)
		if err != nil {
			failure = err
			return
		}
		// Retired between the load and now: absent.
		if _, present := slot.residents[key]; !present {
			release()
			return
		}
		opened, err := slot.sources[key].OpenWatch(ctx)
		if err != nil {
			release()
			failure = err
			return
		}
		watch = opened
		go func() {
			<-opened.Closed()
			slot.owner.Do(release)
		}()
	})
	return watch, failure
}

// Pin pins key: its source (made by the first pin) and its document stay in memory until the returned release
// (idempotent) runs; a persisted key not in memory is loaded by the next activation load (LoadDeclared). A section.
func (slot *KeyedSlot) Pin(raw string, ctx context.Context) (func(), error) {
	key, err := slot.Key(raw)
	if err != nil {
		return nil, err
	}
	source := slot.sources[key]
	if source == nil {
		_, inMemory := slot.residents[key]
		initial, err := slot.definition.current.Initial(ctx)
		if err != nil {
			return nil, err
		}
		source = newKeySource(slot, key, initial, slot.persistence == nil || inMemory)
		slot.sources[key] = source
	}
	source.pins++
	if resident := slot.residents[key]; resident != nil {
		slot.owner.Residency().Forget(resident)
	}
	pinned := source
	released := false
	return func() {
		slot.owner.Do(func() {
			if released {
				return
			}
			released = true
			pinned.pins--
			if pinned.pins > 0 || slot.sources[key] != pinned {
				return
			}
			delete(slot.sources, key)
			if idle := slot.residents[key]; idle != nil {
				slot.touch(idle)
			}
		})
	}, nil
}

// Ensure loads pinned key on the line unless it was loaded since it was pinned (a consumer's `at(key)`, slice 6a §12).
func (slot *KeyedSlot) Ensure(raw string, ctx context.Context) error {
	key, err := slot.Key(raw)
	if err != nil {
		return err
	}
	var pending bool
	slot.owner.Do(func() {
		source := slot.sources[key]
		pending = source != nil && !source.ready
	})
	if !pending {
		return nil
	}
	return slot.owner.OnLine(ctx, func() error { return slot.Load(key, ctx, nil) }, nil)
}

// Source is the source of pinned key.
func (slot *KeyedSlot) Source(raw string) state.DeriveSource {
	key, _ := slot.Key(raw)
	if source := slot.sources[key]; source != nil {
		return source
	}
	return nil
}

// KeySourceOf is the source of pinned key as a KeySource; nil when the key is not pinned.
func (slot *KeyedSlot) KeySourceOf(raw string) *KeySource {
	key, _ := slot.Key(raw)
	return slot.sources[key]
}

// Key is key as the slot and the store keep it: the `str()` text rules (slice 6a §8), a lone surrogate replaced by
// U+FFFD, NUL refused `invalid/args`. Plugin keys are a trust boundary; every entry point takes them through here, so
// memory and the store agree on one key.
func (slot *KeyedSlot) Key(key string) (string, error) { return store.StoredText(key, slot.keyWhat) }

// LoadDeclared loads every pinned key not loaded since it was pinned (activation, holding the line).
func (slot *KeyedSlot) LoadDeclared(ctx context.Context) error {
	var pending []string
	slot.owner.Do(func() {
		for key, source := range slot.sources {
			if !source.ready {
				pending = append(pending, key)
			}
		}
	})
	slices.SortFunc(pending, store.CompareText)
	for _, key := range pending {
		if err := slot.Load(key, ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

// Load loads the document at key (the caller holds the line) and installs it while it is still absent from memory (a
// commit's adopt always wins). A stored version newer than the code's, another fingerprint or a failing migrate fails
// the owner (slice 6a §1.4) and returns the cause.
func (slot *KeyedSlot) Load(key string, ctx context.Context, token *SealToken) error {
	var present bool
	slot.owner.Do(func() { _, present = slot.residents[key] })
	if present {
		return nil
	}
	// A store error is the store's (`storage_failed`, `aborted`).
	document, err := slot.persistence.Read(key, ctx)
	if err != nil {
		return err
	}
	// The transaction was cut meanwhile: what it read may be stale already.
	if token != nil && token.Sealed() {
		return nil
	}
	var failure error
	slot.owner.Do(func() {
		var loaded *Loaded
		if document != nil {
			result, err := slot.persistence.Decode(document, slot.definition.current, ctx)
			if err != nil {
				// What the code cannot read (a failing migrate, whatever it fails with) fails the owner.
				failure = slot.owner.failWith(err)
				return
			}
			loaded = &result
		}
		if _, present := slot.residents[key]; loaded != nil && !present {
			stored := loaded.Stored
			resident := NewResident(slot, key, delta.Track(loaded.Value), &stored, slot.definition.current.Version)
			slot.residents[key] = resident
			slot.touch(resident)
		}
		if source := slot.sources[key]; source != nil {
			source.ready = true
		}
	})
	return failure
}

// Cold reports whether the document at key is persisted and not in memory.
func (slot *KeyedSlot) Cold(key string) bool {
	_, present := slot.residents[key]
	return slot.persistence != nil && !present
}

// Stored is what is stored of the document at key as memory knows it.
func (slot *KeyedSlot) Stored(key string) *Stored {
	if resident := slot.residents[key]; resident != nil {
		return resident.Stored
	}
	return nil
}

// Stage stages the commit's writes of the document at key.
func (slot *KeyedSlot) Stage(key string, change DocumentChange, was *Stored, writes *[]store.StagedWrite, _ CommitPersistence) (StoredChange, error) {
	return slot.persistence.Stage(key, slot.definition.current, was, change, writes, nil)
}

// Hydrate takes the attempt's declaration: a newer version migrates each document in memory on its next access
// (persisted ones store a base with their next change); the pinned keys are watched, so they migrate now, all or none,
// and take `initial()` again.
func (slot *KeyedSlot) Hydrate(definition AnyDefinition, ctx context.Context) error {
	from := slot.definition.Hydrated()
	slot.definition.Hydrate(definition)
	if from == nil || from.Version == definition.Version {
		return nil
	}
	var pinned []*Resident
	for key := range slot.sources {
		if resident := slot.residents[key]; resident != nil {
			pinned = append(pinned, resident)
		}
	}
	trackers := make([]*delta.Tracker, len(pinned))
	for i, resident := range pinned {
		tracker, err := slot.migrated(resident, ctx)
		if err != nil {
			return err
		}
		trackers[i] = tracker
	}
	for i, resident := range pinned {
		resident.Tracker = trackers[i]
		resident.Version = definition.Version
	}
	return slot.owner.Batch().Commit(func() error {
		for _, source := range slot.sources {
			initial, err := definition.Initial(ctx)
			if err != nil {
				return err
			}
			if err := source.retake(initial); err != nil {
				return err
			}
		}
		return nil
	}, ctx)
}

// Current is the tracker of the document at key, migrated first when older (migration on access); nil while absent.
func (slot *KeyedSlot) Current(key string) (*delta.Tracker, error) {
	resident, err := slot.resident(key)
	if err != nil || resident == nil {
		return nil, err
	}
	return resident.Tracker, nil
}

// Initial is the value a draft of an absent document starts from: a fresh `initial()`.
func (slot *KeyedSlot) Initial(ctx context.Context) (*JsonObject, error) {
	return slot.definition.current.Initial(ctx)
}

// Adopt adopts a commit of the document at key.
func (slot *KeyedSlot) Adopt(key string, tracker *delta.Tracker, prepared *delta.Prepared, change *StoredChange) error {
	var stored *Stored
	if change != nil {
		stored = StoredAfter(*change)
	}
	source := slot.sources[key]
	var before *JsonObject
	if source != nil {
		before = source.Peek()
	}
	if err := tracker.Adopt(prepared); err != nil {
		return err
	}
	resident := slot.residents[key]
	version := slot.definition.current.Version
	if resident == nil {
		resident = NewResident(slot, key, tracker, stored, version)
		slot.residents[key] = resident
	} else {
		resident.Tracker, resident.Stored, resident.Version = tracker, stored, version
	}
	if source == nil {
		slot.touch(resident)
	} else {
		source.Written(before, prepared)
	}
	return nil
}

// Retire retires the document at key (inside the batch): out of memory, its source reads `initial()` with the flag.
func (slot *KeyedSlot) Retire(key string) {
	if resident := slot.residents[key]; resident != nil {
		delete(slot.residents, key)
		slot.owner.Residency().Forget(resident)
	}
	if source := slot.sources[key]; source != nil {
		source.Retire()
	}
}

// Evict is: the sweep found resident idle past the retention: it leaves memory.
func (slot *KeyedSlot) Evict(resident *Resident) {
	if slot.residents[resident.Key] == resident {
		delete(slot.residents, resident.Key)
	}
}

// Drop is: its object ended or no attempt declares it any more: the documents go and the keys' watches end.
func (slot *KeyedSlot) Drop() {
	for _, resident := range slot.residents {
		slot.owner.Residency().Forget(resident)
	}
	clear(slot.residents)
	for _, source := range slot.sources {
		source.End()
	}
	clear(slot.sources)
}

// loadShared is a cold persisted key's load, one in flight per key, on the line under the owner's context (rule 11).
// The load is every waiter's: when it takes the line, a waiter whose handle was sealed meanwhile (its attempt failed
// live, the object still open) is refused `released`, and with no other waiter the load does not run (rule 4).
func (slot *KeyedSlot) loadShared(key string, token *SealToken, ctx context.Context) (*JsonObject, error) {
	var load *sharedLoad
	var failure error
	slot.owner.Do(func() {
		// Also when a load is in flight: a sealed handle's cold read is refused (rule 4).
		if failure = slot.owner.AssertAdmitted(token); failure != nil {
			return
		}
		load = slot.loads[key]
		if load == nil {
			load = slot.startLoad(key)
		}
		load.tokens[token] = struct{}{}
	})
	if failure != nil {
		return nil, failure
	}
	parking.Park(ctx)
	select {
	case <-load.done:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	if load.err != nil {
		return nil, load.err
	}
	var refused bool
	slot.owner.Do(func() { _, refused = load.refused[token] })
	if refused {
		return nil, slot.owner.Released()
	}
	return load.value, nil
}

// startLoad starts the shared load of key; it leaves `loads` when it settles. A section.
func (slot *KeyedSlot) startLoad(key string) *sharedLoad {
	load := &sharedLoad{done: make(chan struct{}), tokens: map[*SealToken]struct{}{}}
	slot.loads[key] = load
	go func() {
		err := slot.owner.OnLine(slot.owner.Context(), func() error {
			var failure error
			slot.owner.Do(func() {
				refused := map[*SealToken]struct{}{}
				for token := range load.tokens {
					if token.Sealed() {
						refused[token] = struct{}{}
					}
				}
				load.refused = refused
				if len(refused) == len(load.tokens) {
					failure = slot.owner.Released()
				}
			})
			if failure != nil {
				return failure
			}
			if failure = slot.Load(key, slot.owner.Context(), nil); failure != nil {
				return failure
			}
			slot.owner.Do(func() {
				if resident := slot.residents[key]; resident != nil {
					load.value = resident.Tracker.Value()
				}
			})
			return nil
		}, nil)
		slot.owner.Do(func() {
			load.err = err
			delete(slot.loads, key)
		})
		close(load.done)
	}()
	return load
}

// resident is the resident at key, migrated to the slot's version first when it is older (migration on access).
func (slot *KeyedSlot) resident(key string) (*Resident, error) {
	resident := slot.residents[key]
	if resident == nil || resident.Version == slot.definition.current.Version {
		return resident, nil
	}
	tracker, err := slot.migrated(resident, slot.owner.Context())
	if err != nil {
		return nil, err
	}
	resident.Tracker = tracker
	resident.Version = slot.definition.current.Version
	return resident, nil
}

// migrated is resident's value migrated from its version to the slot's (checked, R8). A failing migrate fails the live
// owner `broken` (slice 6a §1.4) and returns the failure (an activation fails with it).
func (slot *KeyedSlot) migrated(resident *Resident, ctx context.Context) (*delta.Tracker, error) {
	value, err := slot.definition.current.Migrate(resident.Tracker.Value(), resident.Version, ctx)
	if err != nil {
		return nil, slot.owner.failWith(err)
	}
	return delta.Track(value), nil
}

// touch is a use of resident while unpinned: persisted, it is idle from now (Durable's granule rule).
func (slot *KeyedSlot) touch(resident *Resident) {
	if _, pinned := slot.sources[resident.Key]; slot.persistence != nil && !pinned {
		slot.owner.Residency().Touch(resident)
	}
}

// KeySource is one pinned key of a keyed slot as a source (`handle.at(key)`, a keyed member's `at(key)`,
// `watch(key)`): the document's value, `initial()` while it is absent; retirements arrive with the `retired` flag.
type KeySource struct {
	AddressSource
	slot *KeyedSlot
	// Key is the key.
	Key string
	// pins: at 0 the slot forgets it.
	pins int
	// ready is whether the key was loaded since it was pinned (always, in memory): its value is known.
	ready   bool
	initial *JsonObject
}

func newKeySource(slot *KeyedSlot, key string, initial *JsonObject, ready bool) *KeySource {
	source := &KeySource{slot: slot, Key: key, ready: ready, initial: initial}
	source.Init(slot.owner.Batch(), source)
	return source
}

// Owner is the object's documents.
func (source *KeySource) Owner() *Documents { return source.slot.owner }

// Pins is how many pins hold the key (the slot forgets it at 0): the count a host's tests read where Pi's reach `pins`.
func (source *KeySource) Pins() int { return source.pins }

// Value is the document's value, `initial()` while it is absent.
func (source *KeySource) Value() (delta.JsonValue, error) {
	if err := source.slot.owner.AssertLive(); err != nil {
		return nil, err
	}
	if !source.ready {
		message := source.slot.definition.current.Name + "(" + source.Key + "): read before it was loaded"
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: message}, nil)
	}
	return source.Peek(), nil
}

// Peek is the value without the checks (inside its slot's commits).
func (source *KeySource) Peek() *JsonObject {
	if tracker, _ := source.slot.Current(source.Key); tracker != nil {
		return tracker.Value()
	}
	return source.initial
}

// Retire is: the document was retired: `initial()` from now, with the flag.
func (source *KeySource) Retire() { source.RetiredTo(source.initial) }

func (source *KeySource) retake(initial *JsonObject) error {
	source.initial = initial
	if source.ready {
		return source.Replaced()
	}
	return nil
}

// End is: its slot was dropped: the watches end.
func (source *KeySource) End() { source.EndWatches() }

var (
	_ DocumentSlot  = (*KeyedSlot)(nil)
	_ PersistedSlot = (*KeyedSlot)(nil)
)
