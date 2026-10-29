package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/contracts/fingerprint.test.ts.

// fingerprintOf is the fingerprint of a one-variant remote contract with state `s` and params `params`.
func fingerprintOf(t *testing.T, state contracts.TSchema, params contracts.Params) string {
	t.Helper()
	return remote(t, "n", 1, params, variant("a", []contracts.Property{contracts.Prop("s", state)})).Fingerprint
}

func TestFingerprints(t *testing.T) {
	t.Run("pin the normal form: a fixed contract has a fixed hash", func(t *testing.T) {
		if contracts.NormalFormVersion != 2 {
			t.Fatalf("NormalFormVersion = %d", contracts.NormalFormVersion)
		}
		note := func() contracts.TSchema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
		notes := remote(t, "notes", 1, props(contracts.Prop("session", contracts.Str())),
			variant("editor", state(contracts.Prop("notes", note())),
				contracts.Meth("add", contracts.NewMethod(note(), contracts.Str()))),
			variant("reader", state(contracts.Prop("notes", note()))))
		if notes.Fingerprint != "b3eda86070a7e319" {
			t.Fatalf("fingerprint = %s", notes.Fingerprint)
		}
	})

	t.Run("hash64 is two FNV-1a lanes over the UTF-8 bytes", func(t *testing.T) {
		// Lane values computed from the algorithm of fingerprint.ts for the empty text and "a".
		if got := contracts.Hash64(""); got != "cbf29ce4811c9dc5" {
			t.Fatalf("Hash64(\"\") = %s", got)
		}
		if a, b := contracts.Hash64("a"), contracts.Hash64("b"); a == b || len(a) != 16 {
			t.Fatalf("Hash64 = %s %s", a, b)
		}
	})

	t.Run("are stable across key order and annotations", func(t *testing.T) {
		plain := raw(t, `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}},"required":["a","b"]}`)
		reordered := raw(t, `{"required":["b","a"],"title":"T","properties":{"b":{"type":"integer","description":"d"},"a":{"$comment":"c","type":"string","examples":["x"]}},"type":"object","deprecated":true}`)
		if fingerprintOf(t, reordered, noParams()) != fingerprintOf(t, plain, noParams()) {
			t.Fatal("annotations or key order changed the fingerprint")
		}
	})

	for _, tc := range []struct {
		name string
		a, b contracts.TSchema
	}{
		{"defaults", contracts.Str(), contracts.Str(contracts.Default("x"))},
		{"bounds", contracts.Str(), contracts.Str(contracts.Max(3))},
		{"formats", contracts.Str(), raw(t, `{"type":"string","format":"email"}`)},
		{"union order", contracts.Union([]contracts.TSchema{contracts.Str(), contracts.Int()}), contracts.Union([]contracts.TSchema{contracts.Int(), contracts.Str()})},
		{"additionalProperties", tbObject(), tbClosedObject()},
		{"readOnly", contracts.Str(), raw(t, `{"type":"string","readOnly":true}`)},
	} {
		t.Run("are sensitive to "+tc.name, func(t *testing.T) {
			if fingerprintOf(t, tc.a, noParams()) == fingerprintOf(t, tc.b, noParams()) {
				t.Fatal("the fingerprints are equal")
			}
		})
	}

	t.Run("are sensitive to params order", func(t *testing.T) {
		ab := props(contracts.Prop("a", contracts.Str()), contracts.Prop("b", contracts.Int()))
		ba := props(contracts.Prop("b", contracts.Int()), contracts.Prop("a", contracts.Str()))
		if fingerprintOf(t, contracts.Str(), ab) == fingerprintOf(t, contracts.Str(), ba) {
			t.Fatal("params order does not change the fingerprint")
		}
	})

	t.Run("give the same v1 from a separate declaration, and evolve keeps v1's", func(t *testing.T) {
		declare := func() *contracts.Contract {
			return remote(t, "n", 1, noParams(), variant("a", state(contracts.Prop("s", contracts.Str()))))
		}
		evolve := func(contract *contracts.Contract) *contracts.Contract {
			return must(contracts.Evolve(contract, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
				return e.State("a", "s", contracts.Int())
			}))
		}
		v1 := declare()
		v2 := evolve(v1)
		if declare().Fingerprint != v1.Fingerprint {
			t.Fatal("a separate declaration has another fingerprint")
		}
		if v2.Fingerprint == v1.Fingerprint {
			t.Fatal("an edit left the fingerprint")
		}
		if evolve(declare()).Fingerprint != v2.Fingerprint {
			t.Fatal("evolve is not deterministic")
		}
	})

	t.Run("differ between local and remote", func(t *testing.T) {
		asRemote := remote(t, "n", 1, noParams(), variant("members", state(contracts.Prop("s", contracts.Str()))))
		asLocal := local(t, "n", 1, noParams(), contracts.Mem("s", contracts.Str()))
		if asLocal.Fingerprint == asRemote.Fingerprint {
			t.Fatal("local and remote fingerprints are equal")
		}
	})

	t.Run("do not depend on the name or version", func(t *testing.T) {
		a := remote(t, "a", 1, noParams(), variant("v", nil))
		b := remote(t, "b", 7, noParams(), variant("v", nil))
		if a.Fingerprint != b.Fingerprint {
			t.Fatal("the name or version changed the fingerprint")
		}
	})
}
