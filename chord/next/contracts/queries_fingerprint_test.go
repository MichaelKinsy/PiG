package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Interop of the fingerprint of a `queries` member (Pi 92e1db6ca, contracts/fingerprint.ts variantNormal; slice 14 §3.1) with Pi:
// every value is Pi's own, from the run of testdata/queries_fingerprint_oracle.mjs over Pi 0db9861ec018 described at
// TestFingerprintsOfContractsWithoutQueriesMatchPi.
//
// Order: Pi sorts. A variant's members and a `queries` member's named queries both go through sortedDict (UTF-16 code unit
// order), so two queries declared in opposite orders have one fingerprint; the declaration order is kept only in the data
// (`args.keys()`, JavaScript's own-key order).
func TestQueriesFingerprintsMatchPi(t *testing.T) {
	note := func() contracts.Schema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
	ofBoard := func() contracts.Schema { return contracts.Obj(contracts.Prop("board", contracts.Str())) }
	one := func() contracts.Schema { return contracts.Obj(contracts.Prop("id", contracts.Int())) }
	store := func(members ...contracts.LocalEntry) *contracts.Contract {
		return local(t, "app.store", 1, noParams(), members...)
	}
	queries := func(item contracts.Schema, args ...contracts.Property) contracts.LocalMember {
		return contracts.Queries(item, append([]contracts.Property{}, args...))
	}
	mem := contracts.Mem

	for _, tc := range []struct {
		name     string
		contract func() *contracts.Contract
		want     string
	}{
		// Contracts with one.
		{"a queries member without a named query", func() *contracts.Contract {
			return store(mem("notes", queries(note())))
		}, "0fd1d8af6abcfa14"},
		{"one query with no arguments", func() *contracts.Contract {
			return store(mem("notes", queries(note(), contracts.Prop("all", contracts.Obj()))))
		}, "b4c67a855091b16a"},
		{"one query with arguments", func() *contracts.Contract {
			return store(mem("notes", queries(note(), contracts.Prop("ofBoard", ofBoard()))))
		}, "90b8254f20644760"},
		{"two queries, ofBoard first", func() *contracts.Contract {
			return store(mem("notes", queries(note(), contracts.Prop("ofBoard", ofBoard()), contracts.Prop("one", one()))))
		}, "eb8da76bfa245a34"},
		{"two queries, the opposite order", func() *contracts.Contract {
			return store(mem("notes", queries(note(), contracts.Prop("one", one()), contracts.Prop("ofBoard", ofBoard()))))
		}, "eb8da76bfa245a34"},
		{"an optional argument", func() *contracts.Contract {
			return store(mem("notes", queries(note(), contracts.Prop("page", contracts.Obj(contracts.Prop("board", contracts.Str()), contracts.Prop("after", contracts.Opt(contracts.Str())))))))
		}, "2956c8a61c34b847"},
		{"another item shape", func() *contracts.Contract {
			item := contracts.Obj(contracts.Prop("text", contracts.Str()), contracts.Prop("tags", contracts.Arr(contracts.Str())))
			return store(mem("notes", queries(item, contracts.Prop("all", contracts.Obj()))))
		}, "25b3351f70ff4772"},
		{"queries among state, method, keyed and event members, two queries members", func() *contracts.Contract {
			return store(
				mem("events", contracts.Event(contracts.Str())),
				mem("notes", queries(note(), contracts.Prop("ofBoard", ofBoard()), contracts.Prop("one", one()))),
				mem("add", contracts.NewMethod(note(), contracts.Str())),
				mem("drafts", contracts.Keyed(note())),
				mem("title", contracts.Str()),
				mem("other", queries(contracts.Str(), contracts.Prop("z", contracts.Obj()), contracts.Prop("a", contracts.Obj(contracts.Prop("k", contracts.Int()))))))
		}, "116dd767d4a97578"},
		{"evolved, with a queries member", func() *contracts.Contract {
			base := store(mem("notes", queries(note(), contracts.Prop("ofBoard", ofBoard()), contracts.Prop("one", one()))), mem("add", contracts.NewMethod()))
			return must(contracts.Evolve(base, func(e *contracts.LocalEditor) *contracts.LocalEditor {
				return e.AddMethod("clear", contracts.Str(), contracts.Str())
			}))
		}, "07866fc506fbdf12"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.contract().Fingerprint; got != tc.want {
				t.Fatalf("fingerprint = %s, Pi's = %s", got, tc.want)
			}
		})
	}
}
