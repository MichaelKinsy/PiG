package documents

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// TestKeyedSlotSealedHandleGetWhileHydrateWritesDoesNotRaceDefinition holds a cold load on the line
// (through holdNextLoad), seals the old attempt, and releases the successor's Hydrate write and the
// sealed handle's Get from the same barrier (no sleep). Hydrate already holds the object's section
// when the barrier opens; on unmodified code KeyOf reads slot.definition.current before entering Do,
// racing Hydrate's write under -race.
func TestKeyedSlotSealedHandleGetWhileHydrateWritesDoesNotRaceDefinition(t *testing.T) {
	chordStore, db := ksFreshStore(t)
	ksSeed(t, chordStore, ksEntry{"b", "y"})
	p := ksPersistedOver(t, chordStore, nil)
	gate := db.holdNextLoad()
	old := p.notes()
	first := ksGo(func() (*JsonObject, error) { return old.Get(background, "b") })
	ksWaitClosed(t, gate.entered)
	p.current().Fail()

	hydrateReady := make(chan struct{})
	getReady := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }

	def := old.Slot.Definition()
	p.o.Do(func() {
		def.beforeHydrate = func() {
			close(hydrateReady)
			<-release
		}
	})
	defer func() {
		unblock()
		gate.open()
		p.o.Do(func() { def.beforeHydrate = nil })
		p.close()
	}()

	restarting := ksGo(func() (*JsonObject, error) { return nil, p.start() })
	getting := ksGo(func() (*JsonObject, error) {
		close(getReady)
		<-release
		return old.Get(background, "b")
	})
	ksWaitClosed(t, hydrateReady)
	ksWaitClosed(t, getReady)
	unblock()

	_ = ksRequireCode(t, ksRecv(t, getting).err, data.CodeReleased)
	gate.open()
	res := ksRecv(t, first)
	if res.err != nil {
		t.Fatalf("first: %v", res.err)
	}
	ksRequireDoc(t, res.value, "y")
	if res := ksRecv(t, restarting); res.err != nil {
		t.Fatalf("restart: %v", res.err)
	}
}

// TestKeyedSlotEndedHandleRefusesWrongKindKeyBeforeEndedPinsPiOrder pins Pi's error precedence in
// packages/chord/src/documents/handles.ts:116-135 (get: `this.slot.get(this.slot.keyOf(key), this, context)`;
// watch: `this.slot.watch(this.slot.keyOf(key), this, context)`): `keyOf` runs before `assertLive` and
// `assertVersion`, so an ended handle given a wrong-kind address still rejects with `invalid/args` first.
func TestKeyedSlotEndedHandleRefusesWrongKindKeyBeforeEndedPinsPiOrder(t *testing.T) {
	session := handlesSession(t)
	var meta *Keyed
	o, _ := activated(t, objectOptions{}, func(a *attempt) { meta = a.Keyed(handlesMeta(t, session)) })
	o.End()
	want := obj("code", "invalid", "reason", "args", "message", "meta: key: expected an address of session")
	_, err := meta.Get(background, contracts.Address{Kind: "user", Params: obj("id", "S1")})
	nexttest.RequireJSONEqual(t, rejection(t, err), want)
	_, err = meta.Watch(background, contracts.Address{Kind: "user", Params: obj("id", "S1")})
	nexttest.RequireJSONEqual(t, rejection(t, err), want)
	_, err = meta.Get(background, "session|S1")
	nexttest.RequireJSONEqual(t, rejection(t, err), want)
	_, err = meta.Watch(background, "session|S1")
	nexttest.RequireJSONEqual(t, rejection(t, err), want)
}

// TestKeyedSlotSealedHandleSiblingsWhileHydrateWritesDoNotRaceDefinition exercises Watch,
// Keys (HandleKeys), and exported KeyOf from another goroutine against Hydrate at the same
// per-slot barrier (no sleep).
func TestKeyedSlotSealedHandleSiblingsWhileHydrateWritesDoNotRaceDefinition(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(old *Keyed) error
	}{
		{"Watch", func(old *Keyed) error {
			_, err := old.Watch(background, "b")
			return err
		}},
		{"Keys", func(old *Keyed) error {
			it := old.Keys(background)
			defer it.Close()
			_, _, err := it.Next(background)
			return err
		}},
		{"KeyOf", func(old *Keyed) error {
			_, err := old.Slot.KeyOf("b")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chordStore, db := ksFreshStore(t)
			ksSeed(t, chordStore, ksEntry{"b", "y"})
			p := ksPersistedOver(t, chordStore, nil)
			gate := db.holdNextLoad()
			old := p.notes()
			first := ksGo(func() (*JsonObject, error) { return old.Get(background, "b") })
			ksWaitClosed(t, gate.entered)
			p.current().Fail()

			hydrateReady := make(chan struct{})
			callReady := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }

			def := old.Slot.Definition()
			p.o.Do(func() {
				def.beforeHydrate = func() {
					close(hydrateReady)
					<-release
				}
			})
			defer func() {
				unblock()
				gate.open()
				p.o.Do(func() { def.beforeHydrate = nil })
				p.close()
			}()

			restarting := ksGo(func() (*JsonObject, error) { return nil, p.start() })
			calling := ksGo(func() (*JsonObject, error) {
				close(callReady)
				<-release
				return nil, tc.run(old)
			})
			ksWaitClosed(t, hydrateReady)
			ksWaitClosed(t, callReady)
			unblock()

			if tc.name == "Keys" {
				// Keys queues a page job on the line behind first; open the load gate so it resolves released.
				gate.open()
			}
			err := ksRecv(t, calling).err
			if tc.name == "KeyOf" {
				if err != nil {
					t.Fatalf("KeyOf: %v", err)
				}
			} else {
				_ = ksRequireCode(t, err, data.CodeReleased)
			}
			gate.open()
			if res := ksRecv(t, first); res.err != nil {
				t.Fatalf("first: %v", res.err)
			} else {
				ksRequireDoc(t, res.value, "y")
			}
			if res := ksRecv(t, restarting); res.err != nil {
				t.Fatalf("restart: %v", res.err)
			}
		})
	}
}

func BenchmarkKeyedGetOffLoop(b *testing.B) {
	var notes *Keyed
	o, _ := activated(b, objectOptions{}, func(a *attempt) { notes = a.Keyed(ksMemo(b)) })
	defer o.End()
	ksChange(b, notes, "k", "v")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := notes.Get(background, "k"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKeyedGetInSection(b *testing.B) {
	var notes *Keyed
	o, _ := activated(b, objectOptions{}, func(a *attempt) { notes = a.Keyed(ksMemo(b)) })
	defer o.End()
	ksChange(b, notes, "k", "v")
	b.ReportAllocs()
	b.ResetTimer()
	o.Do(func() {
		for b.Loop() {
			if _, err := notes.Get(background, "k"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
