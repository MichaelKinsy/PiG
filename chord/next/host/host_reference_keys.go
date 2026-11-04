package host

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/reference-keys.ts
//
// The keys a reference holds (slice 6a §12; decision "References are the only indirection" (4)): its key watches and
// its factory's pins (`at(key)`) of its object's keyed members, one source per member and key shared by them, pinned on
// the object's live instance (loaded, mapped to the reference's version). While the object still follows the instance a
// key is pinned on (a swap's old instance, a restart's drain, a live failure's admitted commits) the key follows it on,
// as members do. When the reference moves to a new instance or a new object it pins the key there again, keeping the
// last value until that pin loads, then one frame; while it points at nothing the key keeps its last value (one
// `failed` call when its target failed, none while it loads).

// keyHolder is what a reference's keys need of it.
type keyHolder interface {
	sourceOwner
	// keysBatch is the reference's batch.
	keysBatch() *state.Batch
	// keysScheduler is the host's delivery scheduler, for the sources' watches.
	keysScheduler() *chord.DeliveryScheduler
	// keysLoop is the host's loop: a pin's settlement is a section of it.
	keysLoop() sectionLoop
	// keysVersion is its contract version: keys are mapped to it.
	keysVersion() int
	// keysTarget is the object it is bound to; nil while unbound.
	keysTarget() target
	// keysInMemory is whether the load of a pin of key of the keyed member has finished, or needs none (its slot, however
	// the object re-exposes it, holds the key loaded): false when the object is not live, or is not a local one.
	keysInMemory(member, key string) bool
}

// keyEntry is one key of one keyed member held by the reference: its source, its users, and the instance pin it follows.
type keyEntry struct {
	member string
	key    string
	source *referenceSource
	// holders counts its users.
	holders int
	// instance is the live instance its pin is (or is being) taken on; nil while it points at nothing.
	instance any
	// pin is the pin it follows; nil when none.
	pin *pinnedKey
	// loading is the pin in flight, settled once the source follows it; nil when none.
	loading *keyedPromise[struct{}]
}

// referenceKeys is a reference's held keys; every instance pin below them released exactly once. Its methods run in
// sections of the host's loop, except hold, which blocks.
type referenceKeys struct {
	holder  keyHolder
	entries keyedMap[*keyedMap[*keyEntry]]
}

func newReferenceKeys(holder keyHolder) *referenceKeys { return &referenceKeys{holder: holder} }

// hold holds key of keyed member: its source once pinned and loaded on the live instance; ctx ends only this caller's
// wait. release (once, in a section) lets it go with its last holder. It blocks, outside a section: its sections are
// the lookup and the resumption after the pin loaded.
//
// The pin it waits for may leave its instance while it loads (the object failed and went live again): its load then
// ends without a pin for the key, and the entry follows the pin on the instance it points at now. Pi's `hold` awaits the
// first pin only, and so returns the entry's source at once, with no value if the new pin has not landed. In Pi's test it
// has: a new pin whose load has finished when the old pin settles lands before the old pin's waiter resumes
// (reference-keys.test.ts "the object fails and re-binds meanwhile"). Pins here are tasks, so nothing orders them, and
// hold waits for the new pin to settle when its load has finished (keysInMemory), which is when Pi's has landed already;
// it returns the source whichever way that pin settled. A new pin whose load has not finished when the old pin settles is
// not waited for: Pi's hold has returned by then, with the key's source as it is.
//
// Residual: "finished" is the slot's key marked loaded, and the load's end and that mark are a few steps apart. A load
// that ends within that window before the old pin settles is not yet seen as finished (hold returns at once where Pi's new
// pin has landed first: PiG fails where Pi succeeds), and one that ends within the window after Pi's old pin settled is
// seen as finished (hold waits where Pi's has returned: PiG succeeds where Pi fails).
func (k *referenceKeys) hold(member, key string, ctx context.Context) (pinnedKey, error) {
	loop := k.holder.keysLoop()
	var entry *keyEntry
	var wait *keyedPromise[struct{}]
	var release func()
	loop.Run(func() {
		entry = k.entry(member, key)
		entry.holders++
		released := false
		release = func() {
			if released {
				return
			}
			released = true
			k.release(entry)
		}
		if entry.pin == nil {
			wait = k.pin(entry)
		}
	})
	for first := true; wait != nil; first = false {
		result := make(chan error, 1)
		loop.Run(func() {
			wait.thenWithContext(ctx, func(_ struct{}, err error) { result <- err })
		})
		parking.Park(ctx)
		// The first pin's failure is the hold's; a later pin's is not (it shows on the key), but ctx still ends the wait.
		if err := <-result; err != nil && (first || ctx.Err() != nil) {
			loop.Run(release)
			return pinnedKey{}, err
		}
		waited := wait
		wait = nil
		loop.Run(func() {
			// A pin the entry followed has set entry.pin; one it left leaves the pin of the new instance, in flight.
			if entry.pin == nil && entry.loading != nil && entry.loading != waited && k.holder.keysInMemory(member, key) {
				wait = entry.loading
			}
		})
	}
	return pinnedKey{source: entry.source, release: release}, nil
}

// follow: its reference's target changed, inside a batch: a key pins again on a new live instance (last value
// meanwhile); while the target is not live it follows the instance it is pinned on as long as the object does (a commit
// admitted there still shows), then keeps its last value (one `failed` call for a failure) and its pin goes.
func (k *referenceKeys) follow(ctx context.Context) {
	goal := k.holder.keysTarget()
	var instance any
	var attachable attachable
	if goal != nil {
		instance = goal.instance()
		attachable = goal.attachable()
	}
	for _, byKey := range k.entries.values() {
		for _, entry := range byKey.values() {
			if instance != nil {
				// A failed load shows on the key (`failed`); nobody waits on this pin.
				if entry.instance != instance {
					k.pin(entry).then(func(struct{}, error) {})
				}
				continue
			}
			if attachable != nil && entry.instance == any(attachable) {
				continue
			}
			if goal != nil {
				if current := goal.state(); current.Kind == RefStateFailed {
					entry.source.fail(current.Cause.Error, ctx)
					k.unpin(entry)
					continue
				}
			}
			entry.source.hold()
			k.unpin(entry)
		}
	}
}

// fail: its reference left its object (err: why it waits), inside a batch: keys keep their last values, pins go.
func (k *referenceKeys) fail(err *data.ChordError, ctx context.Context) {
	for _, byKey := range k.entries.values() {
		for _, entry := range byKey.values() {
			entry.source.fail(err, ctx)
			k.unpin(entry)
		}
	}
}

// end: its reference ended, inside a batch: every key ends with err and releases its pin.
func (k *referenceKeys) end(err *data.ChordError) {
	for _, byKey := range k.entries.values() {
		for _, entry := range byKey.values() {
			entry.source.end(err)
			k.unpin(entry)
		}
	}
	k.entries.clear()
}

// entry is the entry of member and key, made by the first holder.
func (k *referenceKeys) entry(member, key string) *keyEntry {
	byKey, found := k.entries.get(member)
	if !found {
		byKey = &keyedMap[*keyEntry]{}
		k.entries.set(member, byKey)
	}
	entry, found := byKey.get(key)
	if !found {
		source := newReferenceSource(k.holder.keysBatch(), k.holder.keysScheduler(), k.holder)
		entry = &keyEntry{member: member, key: key, source: source}
		byKey.set(key, entry)
	}
	return entry
}

// pin pins entry's key on the target's live instance (once per instance): the source keeps its last value meanwhile,
// follows the pin when it lands (one frame) and releases the previous one; a failed load shows as its failure.
func (k *referenceKeys) pin(entry *keyEntry) *keyedPromise[struct{}] {
	loop := k.holder.keysLoop()
	batch := k.holder.keysBatch()
	version := k.holder.keysVersion()
	goal := k.holder.keysTarget()
	var instance any
	if goal != nil {
		instance = goal.instance()
	}
	if goal == nil || instance == nil {
		rejected := newKeyedPromise[struct{}](loop)
		rejected.reject(k.holder.none())
		return rejected
	}
	if entry.instance == instance && entry.loading != nil {
		return entry.loading
	}
	entry.instance = instance
	// No frames from the old instance while the new one loads the key.
	entry.source.hold()
	loading := newKeyedPromise[struct{}](loop)
	loop.Go(func() {
		pin, err := goal.pinKey(entry.member, entry.key, version, chordctx.Background())
		loop.Run(func() {
			if err != nil {
				if entry.instance == instance {
					entry.instance = nil
					entry.loading = nil
					failure := data.AsChordError(err, goal.label())
					_ = batch.Within(func() error { entry.source.fail(failure, chordctx.Background()); return nil }, chordctx.Background())
				}
				loading.reject(err)
				return
			}
			if entry.instance != instance || entry.holders == 0 {
				pin.release()
				loading.resolve(struct{}{})
				return
			}
			previous := entry.pin
			entry.pin = &pin
			_ = batch.Within(func() error { entry.source.bind(pin.source); return nil }, chordctx.Background())
			if previous != nil {
				previous.release()
			}
			loading.resolve(struct{}{})
		})
	})
	entry.loading = loading
	return loading
}

// release: one holder of entry left; the last drops it and its pin.
func (k *referenceKeys) release(entry *keyEntry) {
	entry.holders--
	if entry.holders > 0 {
		return
	}
	byKey, found := k.entries.get(entry.member)
	if !found {
		return
	}
	if current, found := byKey.get(entry.key); !found || current != entry {
		return
	}
	byKey.delete(entry.key)
	if byKey.len() == 0 {
		k.entries.delete(entry.member)
	}
	// Its source ends: it leaves the document, whose other holders would keep it and its reference (Astra, GC).
	released := data.NewChordError(data.ChordErrorInit{
		Code: data.CodeReleased, Message: fmt.Sprintf("%s.at(%s): released", entry.member, entry.key),
	}, nil)
	end := func() { entry.source.end(released) }
	if entry.source.inUse() {
		_ = k.holder.keysBatch().Within(func() error { end(); return nil }, chordctx.Background())
	} else {
		end()
	}
	k.unpin(entry)
}

// unpin releases the pin entry follows or awaits: it points at nothing until pinned again.
func (k *referenceKeys) unpin(entry *keyEntry) {
	if entry.pin != nil {
		entry.pin.release()
	}
	entry.pin = nil
	entry.instance = nil
	entry.loading = nil
}
