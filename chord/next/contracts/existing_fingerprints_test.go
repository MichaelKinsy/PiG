package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contracts without a `queries` member keep the fingerprints they had before Pi 92e1db6ca (slice 14 §3.1; fingerprint.ts adds a
// node only per queries member): a PiG peer that declares no query interoperates with every peer. These tests were green on the
// Go code before the queries member existed.
//
// Upstream pins no hash for a contract with queries (declare.test.ts only requires it differ), and the chord/next tests have no
// live Pi oracle (the pinned mirror of .upstream/current predates slice 14), so every value is Pi's own, from one run of
// testdata/queries_fingerprint_oracle.mjs over Pi 0db9861ec018 (packages/chord/src/contracts and packages/schema, exported with
// `git -C ~/pi-main archive 0db9861ec018 packages/chord/src packages/schema`):
//
//	node --conditions=source --disable-warning=ExperimentalWarning chord/next/contracts/testdata/queries_fingerprint_oracle.mjs <export dir>
//
// with node_modules/@earendil-works/pi-schema linked to the exported packages/schema.
// pi: packages/chord/src/contracts/fingerprint.ts
func TestFingerprintsOfContractsWithoutQueriesMatchPi(t *testing.T) {
	note := func() contracts.Schema { return contracts.Obj(contracts.Prop("text", contracts.Str())) }
	store := func(members ...contracts.LocalEntry) *contracts.Contract {
		return local(t, "app.store", 1, noParams(), members...)
	}
	mem := contracts.Mem
	for _, tc := range []struct {
		name     string
		contract func() *contracts.Contract
		want     string
	}{
		{"no queries member", func() *contracts.Contract {
			return store(mem("notes", note()), mem("add", contracts.NewMethod()))
		}, "553e8ca0136cc14b"},
		{"a keyed member only", func() *contracts.Contract { return store(mem("notes", contracts.Keyed(note()))) }, "bf6ff30e1483251d"},
		{"an event member only", func() *contracts.Contract { return store(mem("ping", contracts.Event(contracts.Str()))) }, "affcf1d5cfd9629e"},
		{"evolved, no queries member", func() *contracts.Contract {
			return must(contracts.Evolve(store(mem("notes", note()), mem("add", contracts.NewMethod())), func(e *contracts.LocalEditor) *contracts.LocalEditor {
				return e.AddMethod("clear", contracts.Str(), contracts.Str())
			}))
		}, "dc1fb623a0d9fa34"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.contract().Fingerprint; got != tc.want {
				t.Fatalf("fingerprint = %s, Pi's = %s", got, tc.want)
			}
		})
	}
}
