package host

import (
	"context"
	"errors"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Tests of packages/chord/test/next/host/keyed-members.test.ts. Every case of the upstream file serves objects through a
// whole host (`loaded`, `serve`, `ChordObject` attempts, `failLive`, `defineState` keyed documents, `createHost`), so none
// is ported yet: they are `missing` for the host core. The tests below are own tests of keyedMembers over fakes of its
// two seams (keyedSlot, and keyedProvider as an object with keyedMembers of its own, as Pi's ChordObject.pinKey is); each
// names the upstream case whose behavior it mirrors.

// keyedFakeSlot is a keyed document handle's slot: its keys' values (the store), the keys in memory with their pins, and
// the loads it can hold. Its state belongs to the loop. A key with no pin leaves memory (its value stays).
type keyedFakeSlot struct {
	h      *memberHarness
	values map[string]float64
	keys   map[string]*keyedFakeKey
	// gate, when set, holds every Ensure after its read until it is closed; entered gets a value once one reached it.
	gate      chan struct{}
	entered   chan struct{}
	ensureErr error
	releases  int
	// pinErr is what Pin returns, when set.
	pinErr error
}

type keyedFakeKey struct {
	pins int
	doc  *memberDoc
}

func newKeyedFakeSlot(h *memberHarness) *keyedFakeSlot {
	return &keyedFakeSlot{h: h, values: map[string]float64{}, keys: map[string]*keyedFakeKey{}, entered: make(chan struct{}, 16)}
}

var _ keyedSlot = (*keyedFakeSlot)(nil)

// Pin pins key; the release is idempotent, and the last one drops the key from memory.
func (s *keyedFakeSlot) Pin(key string, _ context.Context) (func(), error) {
	if s.pinErr != nil {
		return nil, s.pinErr
	}
	entry, ok := s.keys[key]
	if !ok {
		entry = &keyedFakeKey{}
		s.keys[key] = entry
	}
	entry.pins++
	released := false
	return func() {
		if released {
			return
		}
		released = true
		s.releases++
		entry.pins--
		if entry.pins == 0 && s.keys[key] == entry {
			delete(s.keys, key)
		}
	}, nil
}

// Ensure loads key from the store when it is not in memory.
func (s *keyedFakeSlot) Ensure(key string, _ context.Context) error {
	var gate chan struct{}
	var err error
	s.h.loop.Run(func() { gate, err = s.gate, s.ensureErr })
	if gate != nil {
		s.entered <- struct{}{}
		<-gate
	}
	if err != nil {
		return err
	}
	s.h.loop.Run(func() {
		if entry := s.keys[key]; entry != nil && entry.doc == nil {
			entry.doc = newMemberDoc(s.h.batch, memberCount(s.values[key]))
		}
	})
	return nil
}

func (s *keyedFakeSlot) Source(key string) state.DeriveSource { return s.keys[key].doc }

// set changes the value of key in the store and in memory (the caller is not in a section).
func (s *keyedFakeSlot) set(t *testing.T, key string, n float64) {
	t.Helper()
	s.h.loop.Run(func() {
		s.values[key] = n
		if entry := s.keys[key]; entry != nil && entry.doc != nil {
			entry.doc.set(t, memberCount(n))
		}
	})
}

// load makes key a key in memory with no pin, as the handle's own `change` leaves it (the caller is not in a section).
func (s *keyedFakeSlot) pinsOf(key string) (pins int, inMemory bool) {
	s.h.loop.Run(func() {
		if entry := s.keys[key]; entry != nil {
			pins, inMemory = entry.pins, true
		}
	})
	return pins, inMemory
}

// keyedFakeObject is an object with keyed members: its keyedMembers and what each member is on its live attempt;
// PinKey is `ChordObject.pinKey`, which a re-exposing object reaches through a Binding.
type keyedFakeObject struct {
	keys     *keyedMembers
	bindings *contracts.NamedMap[*keyedBinding]
}

var _ keyedProvider = (*keyedFakeObject)(nil)

func (h *memberHarness) keyedObject(binding *keyedBinding) *keyedFakeObject {
	bindings := contracts.NewNamedMap[*keyedBinding](1)
	bindings.Set("notes", binding)
	return &keyedFakeObject{keys: newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground), bindings: bindings}
}

// PinKey pins key of member on the object's live attempt.
func (o *keyedFakeObject) PinKey(member, key string, ctx context.Context) (pinnedKey, error) {
	return o.keys.pin(member, key, o.bindings.Value(member), ctx)
}

func ownBinding(slot keyedSlot) *keyedBinding { return &keyedBinding{kind: keyedOwn, slot: slot} }

func reexposedBinding(provider *keyedFakeObject) *keyedBinding {
	return &keyedBinding{kind: keyedReexposed, binding: provider, member: "notes"}
}

// bindingsOf is the keyed members of an attempt: notes is binding.
func bindingsOf(binding *keyedBinding) *contracts.NamedMap[*keyedBinding] {
	bindings := contracts.NewNamedMap[*keyedBinding](1)
	bindings.Set("notes", binding)
	return bindings
}

// pinAsync starts keys.pin(...) on a task of the loop, as a reference's call (an awaited Promise of Pi) waiting meanwhile.
func (h *memberHarness) pinAsync(keys *keyedMembers, binding *keyedBinding, key string, ctx context.Context) <-chan pinResult {
	result := make(chan pinResult, 1)
	h.loop.Go(func() {
		pin, err := keys.pin("notes", key, binding, ctx)
		result <- pinResult{pin: pin, err: err}
	})
	return result
}

// pinned pins key and fails the test when it fails.
func (h *memberHarness) pinned(t *testing.T, keys *keyedMembers, binding *keyedBinding, key string) pinnedKey {
	t.Helper()
	pin, err := keys.pin("notes", key, binding, memberBackground)
	if err != nil {
		t.Fatalf("pin %q: %v", key, err)
	}
	return pin
}

func asHandle(t *testing.T, source state.DeriveSource) state.SourceHandle {
	t.Helper()
	handle, ok := source.(state.SourceHandle)
	if !ok {
		t.Fatalf("%T is not a source", source)
	}
	return handle
}

// doubled is a derive over source of twice its `n`.
func (h *memberHarness) doubled(t *testing.T, source state.DeriveSource) *state.Derive {
	t.Helper()
	return h.derive(t, []any{source}, func(values []any) (delta.JsonValue, error) { return memberCountOf(values[0]) * 2, nil })
}

var keyedBoom = data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil)

// fail is the live attempt of keys failing: the keys following another object's member freeze at once, then every key.
func (h *memberHarness) fail(keys *keyedMembers) {
	h.loop.Run(func() {
		keys.freeze(freezeForeign, keyedBoom)
		keys.freeze(freezeAll, keyedBoom)
	})
}

// Own test, mirrors keyed-members.test.ts › a failed object's held keys › keep their last values and get no frames (at,
// watch, a derive over at); the provider goes on.
func TestKeyedMembersAFailedObjectsHeldKeysKeepTheirLastValuesAndGetNoFramesTheProviderGoesOn(t *testing.T) {
	h := newMemberHarness(t)
	slot := newKeyedFakeSlot(h)
	slot.values["k"] = 1
	provider := h.keyedObject(ownBinding(slot))
	exposer := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	viewed := h.pinned(t, exposer, reexposedBinding(provider), "k")
	double := h.doubled(t, viewed.source)
	values := nexttest.ChangesOf(h.watch(t, asHandle(t, viewed.source)))
	doubles := nexttest.ChangesOf(h.watch(t, double))
	// The provider's own watch pins the key, as a handle's `watch` does: the provider goes on while the exposer lets go.
	h.loop.Run(func() {
		if _, err := slot.Pin("k", memberBackground); err != nil {
			t.Errorf("pin: %v", err)
		}
	})
	direct := nexttest.ChangesOf(h.watch(t, asHandle(t, slot.keys["k"].doc)))
	h.fail(exposer)
	slot.set(t, "k", 3)
	h.delivered()
	expectEqual(t, "value", h.read(t, viewed.source), delta.JsonValue(memberCount(1)))
	if v, _ := h.value(double); v != float64(2) {
		t.Fatalf("double = %v, want 2", v)
	}
	expectEqual(t, "frames while failed", [2]int{len(values.Items()), len(doubles.Items())}, [2]int{0, 0})
	expectEqual(t, "the provider's own watch", nexttest.ValuesOf(direct.Items()), []any{delta.JsonValue(memberCount(3))})
}

// Own test, mirrors keyed-members.test.ts › re-binding when a new attempt goes live › follows the new attempt's member of
// the same name (one frame); the old provider's pin is released.
func TestKeyedMembersRebindingFollowsTheNewAttemptsMemberOfTheSameNameOneFrameAndTheOldProvidersPinIsReleased(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newKeyedFakeSlot(h), newKeyedFakeSlot(h)
	a.values["k"], b.values["k"] = 1, 2
	providerA, providerB := h.keyedObject(ownBinding(a)), h.keyedObject(ownBinding(b))
	exposer := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	viewed := h.pinned(t, exposer, reexposedBinding(providerA), "k")
	double := h.doubled(t, viewed.source)
	values := nexttest.ChangesOf(h.watch(t, asHandle(t, viewed.source)))
	doubles := nexttest.ChangesOf(h.watch(t, double))
	if pins, _ := a.pinsOf("k"); pins != 1 {
		t.Fatalf("pins of a = %d, want 1", pins)
	}
	h.fail(exposer)
	next := bindingsOf(reexposedBinding(providerB))
	if _, err := h.prepare(exposer, next, memberBackground).await(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	h.within(t, exposer.rebind)
	h.delivered()
	expectEqual(t, "value", h.read(t, viewed.source), delta.JsonValue(memberCount(2)))
	if v, _ := h.value(double); v != float64(4) {
		t.Fatalf("double = %v, want 4", v)
	}
	_, aInMemory := a.pinsOf("k")
	bPins, _ := b.pinsOf("k")
	expectEqual(t, "a in memory, pins of b", []any{aInMemory, bPins}, []any{false, 1})
	b.set(t, "k", 4)
	a.set(t, "k", 5)
	h.delivered()
	expectEqual(t, "values", nexttest.ValuesOf(values.Items()), []any{delta.JsonValue(memberCount(2)), delta.JsonValue(memberCount(4))})
	expectEqual(t, "doubles", nexttest.ValuesOf(doubles.Items()), []any{float64(4), float64(8)})
}

// Own test, mirrors keyed-members.test.ts › re-binding when a new attempt goes live › loads a cold persisted key of the new
// member before the new attempt goes live: prepare loads the key, which the live key follows only at rebind.
func TestKeyedMembersRebindingLoadsAColdPersistedKeyOfTheNewMemberBeforeTheNewAttemptGoesLive(t *testing.T) {
	h := newMemberHarness(t)
	memory, cold := newKeyedFakeSlot(h), newKeyedFakeSlot(h)
	memory.values["k"], cold.values["k"] = 1, 10
	providerMemory, providerCold := h.keyedObject(ownBinding(memory)), h.keyedObject(ownBinding(cold))
	exposer := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	viewed := h.pinned(t, exposer, reexposedBinding(providerMemory), "k")
	if _, inMemory := cold.pinsOf("k"); inMemory {
		t.Fatalf("the cold key is in memory before the new attempt")
	}
	h.fail(exposer)
	if _, err := h.prepare(exposer, bindingsOf(reexposedBinding(providerCold)), memberBackground).await(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// Loaded before it goes live; the key still shows what the failed attempt had.
	if pins, inMemory := cold.pinsOf("k"); !inMemory || pins != 1 {
		t.Fatalf("the cold key after prepare: pins %d, in memory %v, want 1, true", pins, inMemory)
	}
	expectEqual(t, "value before the attempt is live", h.read(t, viewed.source), delta.JsonValue(memberCount(1)))
	h.within(t, exposer.rebind)
	expectEqual(t, "value once live", h.read(t, viewed.source), delta.JsonValue(memberCount(10)))
}

// keyedLoading is a cold key of an exposer's provider whose first pin is held at the load, and the exposer pinning it.
type keyedLoading struct {
	h        *memberHarness
	cold     *keyedFakeSlot
	warm     *keyedFakeSlot
	exposer  *keyedMembers
	provider *keyedFakeObject
	other    *keyedFakeObject
}

func (h *memberHarness) loading() *keyedLoading {
	cold, warm := newKeyedFakeSlot(h), newKeyedFakeSlot(h)
	cold.values["k"], warm.values["k"] = 1, 2
	cold.gate = make(chan struct{})
	return &keyedLoading{
		h: h, cold: cold, warm: warm,
		exposer:  newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground),
		provider: h.keyedObject(ownBinding(cold)),
		other:    h.keyedObject(ownBinding(warm)),
	}
}

// settle lets the held load finish and waits for every task the pins started.
func (l *keyedLoading) settle() {
	close(l.cold.gate)
	l.h.loop.Wait()
}

// Own test, mirrors keyed-members.test.ts › a key whose first pin still loads › the object fails and re-binds meanwhile:
// the consumer retries on the new member, the late pin is released.
func TestKeyedMembersAKeyWhoseFirstPinStillLoadsTheObjectFailsAndRebindsMeanwhileTheConsumerRetriesOnTheNewMemberTheLatePinIsReleased(t *testing.T) {
	h := newMemberHarness(t)
	l := h.loading()
	loading := h.pinAsync(l.exposer, reexposedBinding(l.provider), "k", memberBackground)
	<-l.cold.entered
	h.fail(l.exposer)
	if first := <-loading; !errors.Is(first.err, keyedBoom) {
		t.Fatalf("the wait on the failed object = %v, want its failure", first.err)
	}
	l.settle()
	if _, inMemory := l.cold.pinsOf("k"); inMemory {
		t.Fatalf("the late pin of the failed object was not released")
	}
	// The consumer retries on the new attempt's member.
	viewed := h.pinned(t, l.exposer, reexposedBinding(l.other), "k")
	expectEqual(t, "value", h.read(t, viewed.source), delta.JsonValue(memberCount(2)))
	_, coldInMemory := l.cold.pinsOf("k")
	warmPins, _ := l.warm.pinsOf("k")
	expectEqual(t, "cold in memory, pins of the new member", []any{coldInMemory, warmPins}, []any{false, 1})
	values := nexttest.ChangesOf(h.watch(t, asHandle(t, viewed.source)))
	l.cold.set(t, "k", 3)
	l.warm.set(t, "k", 4)
	h.delivered()
	expectEqual(t, "values", nexttest.ValuesOf(values.Items()), []any{delta.JsonValue(memberCount(4))})
}

// Own test, mirrors keyed-members.test.ts › a key whose first pin still loads › the object fails and stays failed: the
// pin's wait rejects with the failure, the late pin is released, and a wait that was aborted rejects with its reason.
func TestKeyedMembersAKeyWhoseFirstPinStillLoadsTheObjectFailsAndStaysFailedTheWaitsRejectAndTheLatePinIsReleased(t *testing.T) {
	h := newMemberHarness(t)
	l := h.loading()
	abort, cancel := chordctx.WithCancel(memberBackground)
	loading := h.pinAsync(l.exposer, reexposedBinding(l.provider), "k", abort)
	<-l.cold.entered
	h.fail(l.exposer)
	if first := <-loading; !errors.Is(first.err, keyedBoom) {
		t.Fatalf("the wait on the failed object = %v, want its failure", first.err)
	}
	cancel(errors.New("viewer released"))
	l.settle()
	if _, inMemory := l.cold.pinsOf("k"); inMemory {
		t.Fatalf("the late pin of the failed object was not released")
	}
	if !l.exposer.empty() {
		t.Fatalf("a failed first pin is kept")
	}
}

// Own test, mirrors keyed-members.test.ts › a key whose first pin still loads › the object ends meanwhile: the consumer's
// wait rejects, the late pin is released.
func TestKeyedMembersAKeyWhoseFirstPinStillLoadsTheObjectEndsMeanwhileTheWaitRejectsAndTheLatePinIsReleased(t *testing.T) {
	h := newMemberHarness(t)
	l := h.loading()
	loading := h.pinAsync(l.exposer, reexposedBinding(l.provider), "k", memberBackground)
	<-l.cold.entered
	ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.exposer: ended"}, nil)
	h.loop.Run(func() { l.exposer.end(ended) })
	if first := <-loading; !errors.Is(first.err, ended) {
		t.Fatalf("the wait on the ended object = %v, want its end", first.err)
	}
	l.settle()
	if _, inMemory := l.cold.pinsOf("k"); inMemory {
		t.Fatalf("the late pin of the ended object was not released")
	}
	if !l.exposer.empty() {
		t.Fatalf("an ended object keeps pinned keys")
	}
}

// Own test, mirrors keyed-members.ts `pin` (`context` ends only this caller's wait): one user aborts its wait and gets its
// abort reason; the other still gets the key, and the entry stays for it.
func TestKeyedMembersAContextEndsOnlyItsCallersWait(t *testing.T) {
	h := newMemberHarness(t)
	l := h.loading()
	binding := reexposedBinding(l.provider)
	abort, cancel := chordctx.WithCancel(memberBackground)
	aborted := h.pinAsync(l.exposer, binding, "k", abort)
	patient := h.pinAsync(l.exposer, binding, "k", memberBackground)
	<-l.cold.entered
	reason := errors.New("viewer left")
	cancel(reason)
	if got := <-aborted; !errors.Is(got.err, reason) {
		t.Fatalf("the aborted wait = %v, want its reason", got.err)
	}
	l.settle()
	got := <-patient
	if got.err != nil {
		t.Fatalf("the patient wait = %v", got.err)
	}
	expectEqual(t, "value", h.read(t, got.pin.source), delta.JsonValue(memberCount(1)))
	if pins, _ := l.cold.pinsOf("k"); pins != 1 {
		t.Fatalf("pins = %d, want 1", pins)
	}
	got.pin.release()
	if _, inMemory := l.cold.pinsOf("k"); inMemory {
		t.Fatalf("the last release left the key pinned")
	}
	if !l.exposer.empty() {
		t.Fatalf("the last release left the entry")
	}
}

// Own test: a wait on a context aborted before the pin returns its reason and leaves nothing pinned (keyed-members.ts
// `pin`, `awaitWithContext`: an aborted signal rejects at once).
func TestKeyedMembersAContextAbortedBeforeThePinRejectsAtOnceAndLeavesNothingPinned(t *testing.T) {
	h := newMemberHarness(t)
	slot := newKeyedFakeSlot(h)
	slot.values["k"] = 1
	keys := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	abort, cancel := chordctx.WithCancel(memberBackground)
	reason := errors.New("already aborted")
	cancel(reason)
	_, err := keys.pin("notes", "k", ownBinding(slot), abort)
	if !errors.Is(err, reason) {
		t.Fatalf("pin = %v, want the abort reason", err)
	}
	h.loop.Wait()
	if _, inMemory := slot.pinsOf("k"); inMemory || !keys.empty() {
		t.Fatalf("an aborted pin left a pin or an entry behind")
	}
}

// Own test, mirrors keyed-members.test.ts › a keyed watch whose object closes while it is set up › rejects with the
// object's end error, whichever microtask the close lands in: the pin settles with the key or the end error, and every
// pin is released exactly once.
func TestKeyedMembersAKeyedWatchWhoseObjectClosesWhileItIsSetUpSettlesWithTheKeyOrTheEndWhicheverMicrotaskTheCloseLandsIn(t *testing.T) {
	ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.exposer: ended"}, nil)
	for gap := range 40 {
		h := newMemberHarness(t)
		slot := newKeyedFakeSlot(h)
		slot.values["a"] = 1
		keys := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
		watching := h.pinAsync(keys, ownBinding(slot), "a", memberBackground)
		var hop func(turns int)
		hop = func(turns int) {
			if turns == 0 {
				keys.end(ended)
				return
			}
			h.loop.Queue(func() { hop(turns - 1) })
		}
		h.loop.Run(func() { hop(gap) })
		got := <-watching
		if got.err != nil && !errors.Is(got.err, ended) {
			t.Fatalf("gap %d: pin = %v, want the key or the object's end", gap, got.err)
		}
		if got.err == nil {
			got.pin.release()
		}
		h.loop.Wait()
		pins, inMemory := slot.pinsOf("a")
		if inMemory {
			t.Fatalf("gap %d: %d pins left on the slot", gap, pins)
		}
	}
}

// Own test, mirrors keyed-members.test.ts › chains and releases › a key re-exposed through two objects freezes with the
// middle one and follows its next attempt.
func TestKeyedMembersAKeyReexposedThroughTwoObjectsFreezesWithTheMiddleOneAndFollowsItsNextAttempt(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newKeyedFakeSlot(h), newKeyedFakeSlot(h)
	a.values["k"], b.values["k"] = 1, 2
	providerA, providerB := h.keyedObject(ownBinding(a)), h.keyedObject(ownBinding(b))
	middle := h.keyedObject(reexposedBinding(providerA))
	outer := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	viewed := h.pinned(t, outer, reexposedBinding(middle), "k")
	values := nexttest.ChangesOf(h.watch(t, asHandle(t, viewed.source)))
	h.fail(middle.keys)
	a.set(t, "k", 3)
	h.delivered()
	expectEqual(t, "value and frames while the middle one is failed", []any{h.read(t, viewed.source), len(values.Items())}, []any{delta.JsonValue(memberCount(1)), 0})
	if _, err := h.prepare(middle.keys, bindingsOf(reexposedBinding(providerB)), memberBackground).await(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	h.within(t, middle.keys.rebind)
	h.delivered()
	expectEqual(t, "value", h.read(t, viewed.source), delta.JsonValue(memberCount(2)))
	expectEqual(t, "values", nexttest.ValuesOf(values.Items()), []any{delta.JsonValue(memberCount(2))})
}

// Own test, mirrors keyed-members.test.ts › chains and releases › a consumer released while the object re-activates leaves
// no hold behind; nothing is kept for it.
func TestKeyedMembersAConsumerReleasedWhileTheObjectReactivatesLeavesNoHoldBehindAndNothingIsKeptForIt(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newKeyedFakeSlot(h), newKeyedFakeSlot(h)
	a.values["k"], b.values["k"] = 1, 2
	providerA, providerB := h.keyedObject(ownBinding(a)), h.keyedObject(ownBinding(b))
	exposer := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	var doc weak.Pointer[memberDoc]
	func() {
		viewed := h.pinned(t, exposer, reexposedBinding(providerA), "k")
		doc = weak.Make(a.keys["k"].doc)
		h.fail(exposer)
		viewed.release()
	}()
	// The re-activating attempt has no key to prepare: nothing was kept for the released consumer.
	if preparing := h.prepare(exposer, bindingsOf(reexposedBinding(providerB)), memberBackground); preparing != nil {
		t.Fatalf("prepare with no pinned key returned a promise")
	}
	h.within(t, exposer.rebind)
	_, aInMemory := a.pinsOf("k")
	_, bInMemory := b.pinsOf("k")
	expectEqual(t, "a and b in memory", []bool{aInMemory, bInMemory}, []bool{false, false})
	if !exposer.empty() || !providerA.keys.empty() || !providerB.keys.empty() {
		t.Fatalf("keys are kept for a released consumer")
	}
	nexttest.ExpectCollected(t, doc)
}

// Own test, mirrors keyed-members.ts `prepare`/`discard`: what prepare pinned on a new attempt that did not go live is
// released, and the keys go on following the failed attempt's member; a load that fails rejects prepare.
func TestKeyedMembersDiscardReleasesWhatPreparePinnedAndAFailedLoadRejectsPrepare(t *testing.T) {
	h := newMemberHarness(t)
	a, b := newKeyedFakeSlot(h), newKeyedFakeSlot(h)
	a.values["k"], b.values["k"] = 1, 2
	exposer := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	viewed := h.pinned(t, exposer, ownBinding(a), "k")
	h.fail(exposer)
	next := bindingsOf(ownBinding(b))
	if _, err := h.prepare(exposer, next, memberBackground).await(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if pins, _ := b.pinsOf("k"); pins != 1 {
		t.Fatalf("pins of the prepared member = %d, want 1", pins)
	}
	h.loop.Run(exposer.discard)
	if _, inMemory := b.pinsOf("k"); inMemory {
		t.Fatalf("discard left the prepared pin")
	}
	expectEqual(t, "value", h.read(t, viewed.source), delta.JsonValue(memberCount(1)))
	// A failing load rejects prepare; the key keeps what it has.
	loadFailed := errors.New("store failed")
	b.ensureErr = loadFailed
	if _, err := h.prepare(exposer, next, memberBackground).await(); !errors.Is(err, loadFailed) {
		t.Fatalf("prepare = %v, want the failed load", err)
	}
	if _, inMemory := b.pinsOf("k"); inMemory {
		t.Fatalf("a failed load left its pin")
	}
}

// Own test: a pin on a slot that refuses it rejects with its error (keyed-members.ts `pinned`: `slot.pin` throws), and a
// failed load releases the pin and rejects every user.
func TestKeyedMembersAFailedPinOrLoadRejectsAndReleasesThePin(t *testing.T) {
	h := newMemberHarness(t)
	slot := newKeyedFakeSlot(h)
	slot.values["k"] = 1
	keys := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	refused := errors.New("slot closed")
	slot.pinErr = refused
	if _, err := keys.pin("notes", "k", ownBinding(slot), memberBackground); !errors.Is(err, refused) {
		t.Fatalf("pin = %v, want the slot's error", err)
	}
	slot.pinErr = nil
	slot.ensureErr = errors.New("store failed")
	if _, err := keys.pin("notes", "k", ownBinding(slot), memberBackground); !errors.Is(err, slot.ensureErr) {
		t.Fatalf("pin = %v, want the failed load", err)
	}
	h.loop.Wait()
	if _, inMemory := slot.pinsOf("k"); inMemory || !keys.empty() {
		t.Fatalf("a failed pin left a pin or an entry behind")
	}
	if slot.releases != 1 {
		t.Fatalf("releases = %d, want 1", slot.releases)
	}
}

// Own test, mirrors keyed-members.ts `end`: the object ended: every key ends with its error (the watches of its member
// source end `ended`), releases what it pins once, and a release of the pin after it is a no-op.
func TestKeyedMembersEndEndsEveryKeyAndReleasesWhatItPinsOnce(t *testing.T) {
	h := newMemberHarness(t)
	slot := newKeyedFakeSlot(h)
	slot.values["k"], slot.values["j"] = 1, 2
	keys := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	k, j := h.pinned(t, keys, ownBinding(slot), "k"), h.pinned(t, keys, ownBinding(slot), "j")
	watch := h.watch(t, asHandle(t, k.source))
	ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonEnded, Message: "app.exposer: ended"}, nil)
	h.within(t, func() { keys.end(ended) })
	<-watch.Closed()
	expectEqual(t, "end", watch.End(), chord.WatchEnd{Kind: chord.WatchEnded})
	if _, err := h.value(k.source); !errors.Is(err, ended) {
		t.Fatalf("read after the end = %v, want its error", err)
	}
	k.release()
	j.release()
	j.release()
	expectEqual(t, "releases", slot.releases, 2)
	if _, inMemory := slot.pinsOf("k"); inMemory || !keys.empty() {
		t.Fatalf("an ended object keeps pins or keys")
	}
}

// Own test, mirrors keyed-members.ts `freeze`: `foreign` takes only the keys of re-exposed members, `all` every key; the
// release of a pin is idempotent and the last user drops the entry.
func TestKeyedMembersFreezeForeignTakesOnlyReexposedMembersAndTheLastReleaseDropsTheEntry(t *testing.T) {
	h := newMemberHarness(t)
	own, foreign := newKeyedFakeSlot(h), newKeyedFakeSlot(h)
	own.values["k"], foreign.values["j"] = 1, 2
	provider := h.keyedObject(ownBinding(foreign))
	keys := newKeyedMembers(h.batch, h.scheduler, h.loop, memberBackground)
	mine := h.pinned(t, keys, ownBinding(own), "k")
	theirs := h.pinned(t, keys, reexposedBinding(provider), "j")
	h.loop.Run(func() { keys.freeze(freezeForeign, keyedBoom) })
	expectEqual(t, "releases after foreign", []int{own.releases, foreign.releases}, []int{0, 1})
	// The own key still follows its slot; the foreign one kept its value.
	own.set(t, "k", 5)
	h.delivered()
	expectEqual(t, "own value", h.read(t, mine.source), delta.JsonValue(memberCount(5)))
	foreign.set(t, "j", 6)
	expectEqual(t, "foreign value", h.read(t, theirs.source), delta.JsonValue(memberCount(2)))
	h.loop.Run(func() { keys.freeze(freezeAll, keyedBoom) })
	expectEqual(t, "releases after all", []int{own.releases, foreign.releases}, []int{1, 1})
	mine.release()
	mine.release()
	theirs.release()
	if !keys.empty() || !provider.keys.empty() {
		t.Fatalf("the last releases left entries")
	}
}

// Own test of keyedPromise, the stored Promise of Pi's `ready` (several awaiters): its reactions run as microtasks of the
// loop, after the section that settled it, in the order they were registered.
func TestKeyedPromiseCallbacksRunAsMicrotasksInTheOrderTheyWereRegistered(t *testing.T) {
	h := newMemberHarness(t)
	promise := newKeyedPromise[int](h.loop)
	var order []int
	h.loop.Run(func() {
		for i := range 3 {
			promise.then(func(value int, err error) { order = append(order, value*10+i) })
		}
		promise.resolve(7)
		// Reactions run after the section that settled the promise, not inside it.
		expectEqual(t, "reactions inside the settling section", len(order), 0)
		promise.resolve(8)
		promise.then(func(value int, _ error) { order = append(order, value*10+3) })
	})
	expectEqual(t, "order", order, []int{70, 71, 72, 73})
	value, err := promise.await()
	expectEqual(t, "await", []any{value, err}, []any{7, nil})
}

// Own test, mirrors keyed-members.ts `awaitWithContext(entry.ready, context)`: the abort reason wins when the signal is
// aborted first, a result wins when the promise settled first, and either way the callback runs once.
func TestKeyedPromiseAwaitWithContextCallsBackOnceWhicheverComesFirst(t *testing.T) {
	h := newMemberHarness(t)
	reason := errors.New("aborted")
	t.Run("the abort first", func(t *testing.T) {
		promise := newKeyedPromise[int](h.loop)
		abort, cancel := chordctx.WithCancel(memberBackground)
		var got []error
		done := make(chan struct{})
		h.loop.Run(func() {
			promise.thenWithContext(abort, func(_ int, err error) { got = append(got, err); close(done) })
		})
		cancel(reason)
		<-done
		h.loop.Run(func() { promise.resolve(1) })
		expectEqual(t, "calls", got, []error{reason})
	})
	t.Run("the result first", func(t *testing.T) {
		promise := newKeyedPromise[int](h.loop)
		abort, cancel := chordctx.WithCancel(memberBackground)
		var got []any
		h.loop.Run(func() {
			promise.thenWithContext(abort, func(value int, err error) { got = append(got, value, err) })
			promise.resolve(1)
		})
		cancel(reason)
		h.loop.Wait()
		h.loop.Run(func() {})
		expectEqual(t, "calls", got, []any{1, nil})
	})
	t.Run("an aborted context rejects at once, a context with no abort signal waits on the promise alone", func(t *testing.T) {
		promise := newKeyedPromise[int](h.loop)
		abort, cancel := chordctx.WithCancel(memberBackground)
		cancel(reason)
		var got []any
		h.loop.Run(func() {
			promise.thenWithContext(abort, func(value int, err error) { got = append(got, value, err) })
			promise.thenWithContext(memberBackground, func(value int, err error) { got = append(got, value, err) })
			promise.resolve(2)
		})
		expectEqual(t, "calls", got, []any{0, reason, 2, nil})
	})
}

// Own test of keyedMap, the Map of Pi's `#entries`: insertion order, and iterators that are live as a JavaScript Map's.
func TestKeyedMapIteratesLiveInInsertionOrder(t *testing.T) {
	var m keyedMap[int]
	for i, key := range []string{"a", "b", "c"} {
		m.set(key, i)
	}
	m.set("a", 9)
	expectEqual(t, "order", m.values(), []int{9, 1, 2})
	it := m.iterate()
	first, _ := it.next()
	// An entry added meanwhile is visited, one deleted before its turn is not; a deleted one re-added goes last.
	m.set("d", 3)
	m.delete("b")
	m.delete("a")
	m.set("a", 4)
	var visited []int
	visited = append(visited, first)
	for value, ok := it.next(); ok; value, ok = it.next() {
		visited = append(visited, value)
	}
	expectEqual(t, "visited", visited, []int{9, 2, 3, 4})
	expectEqual(t, "after", m.values(), []int{2, 3, 4})
	if m.iterators != 0 {
		t.Fatalf("a finished iterator is still counted")
	}
	// A cleared map goes on with what is added after it.
	it = m.iterate()
	m.clear()
	m.set("e", 5)
	value, ok := it.next()
	expectEqual(t, "after clear", []any{value, ok, m.len()}, []any{5, true, 1})
	it.close()
	it.close()
	expectEqual(t, "iterators", m.iterators, 0)
	// Compaction drops the removed entries only when nothing iterates.
	for i := range 100 {
		m.set(string(rune('A'+i%26))+string(rune('a'+i/26)), i)
	}
	for i := range 100 {
		m.delete(string(rune('A'+i%26)) + string(rune('a'+i/26)))
	}
	if len(m.nodes) > 40 {
		t.Fatalf("removed entries are kept: %d nodes for %d entries", len(m.nodes), m.len())
	}
	expectEqual(t, "survivor", m.values(), []int{5})
}
