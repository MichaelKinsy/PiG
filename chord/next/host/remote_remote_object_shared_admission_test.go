package host

import (
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// pi: packages/chord/src/remote/remote-object.ts

// Ports the describe "admission (accepted difference 2)" of 'references sharing one wire hold (decision "References
// share one wire hold")' of packages/chord/test/remote/remote-object.test.ts (Pi 47e9d74f9 + 90ed3e3b1).

func TestReferencesSharingOneWireHoldAdmission(t *testing.T) {
	// remote-object.test.ts: references sharing one wire hold › admission (accepted difference 2) › a relay's authorize turned false after A is held: B joins with no hold upstream and no authorization
	t.Run("a relay's authorize turned false after A is held: B joins with no hold upstream and no authorization", func(t *testing.T) {
		var allowed atomic.Bool
		var asked atomic.Int32
		allowed.Store(true)
		rel := relay(t, relayedOptions{Authorize: func(data.Principal, contracts.Address) bool {
			asked.Add(1)
			return allowed.Load()
		}})
		get := func() *Reference {
			ref, err := rel.Tab.Get(background, PlacedNotes, rhNotes("S1"), GetOptions{})
			must(t, err)
			return ref
		}
		a := get()
		rel.Transported()
		holds := rhActivityReferences(t, rel.Server)
		allowed.Store(false)
		before := asked.Load()
		b := get()
		rel.Transported()
		if more, kind, references := asked.Load()-before, rhRefState(b), rhActivityReferences(t, rel.Server); more != 0 || kind != RefStateLive || references != holds {
			t.Fatalf("[asked, b, references] = [%d %v %v], want [0 live %v]", more, kind, references, holds)
		}
		if kind := rhRefState(a); kind != RefStateLive {
			t.Fatalf("a = %v, want live", kind)
		}
	})
}
