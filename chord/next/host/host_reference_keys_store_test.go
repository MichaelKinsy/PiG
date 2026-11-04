package host

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Own test of reference-keys.test.ts › a key whose first pin still loads › the object fails and re-binds meanwhile, for a
// new member whose key is in the store only. Pi's test re-binds to a member in memory, whose pin has landed when the old
// pin's load settles. Here the new pin's load has not finished when the old pin settles: Pi's hold has returned by then
// with the key's source, which has no value, and the consumer's derive reads it: its get rejects with `not_ready`,
// "app.exposer: not live yet", and the failure is reported as `activation_failed` (run against Pi v1.5.0 by the review of
// this fix).

// kmColdOther is a second provider with a persisted key `k`, to which the exposer re-binds.
var kmColdOther = mustContract(contracts.Local("app.cold-other", contracts.LocalDeclaration{
	Params:  contracts.Params{},
	Version: 1, Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
}))

// kmStoreGates holds the first two reads of the store after arm: the first at entered[0] until let(0), the second at
// entered[1] until let(1).
type kmStoreGates struct {
	armed   atomic.Bool
	reads   atomic.Int32
	entered [2]chan struct{}
	release [2]chan struct{}
	freed   [2]sync.Once
}

func newKmStoreGates() *kmStoreGates {
	g := &kmStoreGates{}
	for i := range g.entered {
		g.entered[i], g.release[i] = make(chan struct{}), make(chan struct{})
	}
	return g
}

func (g *kmStoreGates) hit() {
	if !g.armed.Load() {
		return
	}
	i := g.reads.Add(1) - 1
	if i > 1 {
		return
	}
	close(g.entered[i])
	<-g.release[i]
}

func (g *kmStoreGates) let(i int) { g.freed[i].Do(func() { close(g.release[i]) }) }

// kmStoreReads is a database whose reads, once the gates are armed, wait at them after they ran.
type kmStoreReads struct {
	sqlite.SqliteExecutor
	gates *kmStoreGates
}

func (e *kmStoreReads) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	row, err := e.SqliteExecutor.Get(sql, params...)
	e.gates.hit()
	return row, err
}

func (e *kmStoreReads) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	rows, err := e.SqliteExecutor.All(sql, params...)
	e.gates.hit()
	return rows, err
}

type kmStoreDatabase struct {
	sqlite.SqliteDatabase
	gates *kmStoreGates
}

func (db *kmStoreDatabase) Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	return (&kmStoreReads{SqliteExecutor: db.SqliteDatabase, gates: db.gates}).Get(sql, params...)
}

func (db *kmStoreDatabase) All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	return (&kmStoreReads{SqliteExecutor: db.SqliteDatabase, gates: db.gates}).All(sql, params...)
}

func (db *kmStoreDatabase) Transaction(callback func(transaction sqlite.SqliteExecutor) error) error {
	return db.SqliteDatabase.Transaction(func(transaction sqlite.SqliteExecutor) error {
		return callback(&kmStoreReads{SqliteExecutor: transaction, gates: db.gates})
	})
}

func TestReferenceKeysAKeyWhoseFirstPinStillLoadsTheObjectRebindsToAKeyInTheStoreOnlyTheConsumerIsRejectedNotReadyAsInPi(t *testing.T) {
	gates := newKmStoreGates()
	t.Cleanup(func() { gates.let(0); gates.let(1) })
	st := openMemoryStore(t, func(db sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		return &kmStoreDatabase{SqliteDatabase: db, gates: gates}
	}, nil)
	providers := map[string]*contracts.Contract{"a": kmColdProvider, "c": kmColdOther}
	var seeded sync.Map
	seed, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		for id, contract := range providers {
			if err := h.Serve(contract, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				notes := self.State(kmStored).(*documents.Keyed)
				seeded.Store(id, notes)
				return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
		}
		return nil
	}, loadedOptions{Store: st})
	for id, n := range map[string]float64{"a": 1, "c": 3} {
		_, err := seed.Get(background, providers[id], jsonObject(), GetOptions{})
		must(t, err)
		notes, _ := seeded.Load(id)
		kmSet(t, notes.(*documents.Keyed), "k", n)
	}
	must(t, seed.Close(background))
	// A new host: both keys are in the store only.
	registry := newKmRegistry("a")
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		for id, contract := range providers {
			if err := h.Serve(contract, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				notes := self.State(kmStored).(*documents.Keyed)
				registry.setProvider(id, notes)
				return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
			}, ServeOptions{}); err != nil {
				return err
			}
		}
		if err := h.Serve(kmExposer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			registry.addExposer(self)
			self.State(kmOwn)
			notes := self.Get(providers[registry.current()], jsonObject()).Keyed("notes")
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(kmViewer, kmViewerFactory, ServeOptions{})
	}, loadedOptions{Store: st})
	for _, contract := range providers {
		_, err := host.Get(background, contract, jsonObject(), GetOptions{})
		must(t, err)
	}
	exposer, err := host.Get(background, kmExposer, jsonObject(), GetOptions{})
	must(t, err)
	gates.armed.Store(true)
	loading := kmGetViewer(host, background)
	<-gates.entered[0]
	failLive(registry.exposer(-1), data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil))
	registry.setWhich("c")
	kmRetry(t, host, exposer)
	// The viewer's pin on the new member reads the store, and waits there.
	<-gates.entered[1]
	gates.let(0)
	got := <-loading
	if got.err == nil {
		t.Fatalf("the get of the viewer = %v, want the rejection Pi gives", got.ref)
	}
	if chordErr, ok := data.IsChordError(got.err); !ok || chordErr.Code != data.CodeNotReady || got.err.Error() != "app.exposer: not live yet" {
		t.Fatalf("the get of the viewer = %v, want not_ready \"app.exposer: not live yet\"", got.err)
	}
	gates.let(1)
	must(t, host.Close(background))
	if got := failures(errs.All()); len(got) != 1 {
		t.Fatalf("activation failures = %v, want the viewer's", got)
	}
}
