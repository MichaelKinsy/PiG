package host

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// GO-ONLY (Pi has no case for it): the reader a parked attempt opens is followed by the attempt in the turn that found it not
// ready. Pi's `openReaders` awaits `reader.open(context)`, which ends with `if (source.ready) return { kind: "ready" }`
// (query-reader.ts:91-94), and adds the wake right after it (`reader.source.dependents.add(wake)`, read-declarations.ts:43-45):
// two microtasks of one turn, so an adoption, which arrives as a task of its own, cannot land between the check and the
// follow. The Go reader opens on a task off the loop (reader.Open blocks), and its check and its follow were two sections with
// that task's goroutine between them: an adoption that landed there made the reader ready, told nobody, and the attempt parked
// on a ready reader for good (the start barrier of a host never ended).
//
// The adoption here is a task of the loop (a store's adoption: a task in Pi): it runs at the first task boundary after the
// reading was asked whether it is ready, when nothing else is queued behind the section that asked. A section that ends at the
// question, as the check of a reader that has a section of its own, lets it land before the next section; a section that goes
// on with the follow (a reaction queued behind the question) lets it land after the follow, as Pi's turn does.

// rlReading is a reading that is not ready when it is first asked, and is adopted (ready, its dependents told) as a task of the
// loop after the section that asked.
type rlReading struct {
	rdReading
	loop *Loop
	// asked is whether it was asked yet; adopted is closed when its adoption ran.
	asked   bool
	adopted chan struct{}
}

func newRlReading(host *chordHost) *rlReading {
	return &rlReading{loop: host.loop().(*Loop), adopted: make(chan struct{})}
}

// Ready is the question whose section the adoption follows.
func (f *rlReading) Ready() bool {
	if !f.asked {
		f.asked = true
		f.loop.Queue(f.lands)
	}
	return f.ready
}

// lands is a task of the loop: it waits behind the microtasks queued in the section that ran it, and runs once none is.
func (f *rlReading) lands() {
	f.loop.mu.Lock()
	idle := len(f.loop.queue) == 0
	f.loop.mu.Unlock()
	if !idle {
		f.loop.Queue(f.lands)
		return
	}
	f.ready = true
	for _, each := range f.deps.Snapshot() {
		each.Invalidate()
	}
	close(f.adopted)
}

// rlWake is the dependent a parked attempt follows its readers with: it counts the times it was told.
type rlWake struct{ told int }

func (w *rlWake) Invalidate()                              { w.told++ }
func (*rlWake) SourceRetired(map[state.Dependent]struct{}) {}

// rlReaderOver is a reader of host over reading, of an own query that reads nothing; wake and spawn are its attempt's.
func rlReaderOver(host *chordHost, reading Reading, wake func(), spawn func(task func())) *reader {
	table := mustTableDef("doc", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("n", store.Int())}, Key: []string{"n"}})
	snapshot := nqMust(contracts.SnapshotArguments(contracts.Obj(), `app.doc: query "all": args`))
	own := newOwnQuery("app.doc", "all", store.NewTableHandle(table, "app.doc:doc", nil, nil), func(delta.JsonValue) *store.Query {
		return store.NewQuery(table)
	}, QueriesOptions{}, snapshot)
	given := NewGivenQuery(own, GivenArguments{Kind: GivenFixed, Value: jsonObject()}, host.batch)
	return newReader(given, reading, wake, spawn)
}

func TestOpeningReadersFollowsAReaderInTheSectionThatFoundItNotReadyNotAfterAnAdoptionCouldLandBetween(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}}).(*chordHost)
	late := newRlReading(host)
	readers := []*reader{rlReaderOver(host, late, func() {}, host.loop().Go)}
	wake := &rlWake{}
	settled := make(chan parkResult, 1)
	host.enter(func() {
		openReaders(host, readers, wake, &rdBlockers{}, "app.doc: a live query", background, func(found parkResult) { settled <- found })
	})
	got := receive(t, settled)
	receive(t, late.adopted)
	var told int
	var ready bool
	host.enter(func() { told, ready = wake.told, late.ready })
	if !ready {
		t.Fatal("the reading was never adopted: the case asks nothing")
	}
	if got.kind != parkingReady && told == 0 {
		t.Fatalf("the step parked (kind %d, ready is %d) and the wake was told nothing, though the reader has its value now: the attempt "+
			"it parks follows a reader that is ready and nothing will tell it", got.kind, parkingReady)
	}
}

// rlContract is a host-lifetime type (empty params): the start barrier of Load waits for its first outcome.
var rlContract = mustContract(declareLocal("test.late", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("ping", contracts.NewMethod())},
}))

// rlFacet serves rlContract, whose attempt reads `late` before it goes live: the reader an attempt's List or LiveQuery makes,
// over a reading that is adopted as a task after the section that found it not ready.
func rlFacet(late *rlReading) Facet {
	return DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(rlContract, func(_ *delta.JsonObject, plain Self) (contracts.MembersImplementation, error) {
			a := plain.(*attempt)
			a.readers = append(a.readers, rlReaderOver(a.object.host, late, a.dependencyMoved, func(task func()) { goTask(a.loop(), task, task) }))
			return contracts.MembersImplementation{Methods: map[string]contracts.MethodFunction{
				"ping": exMethod(func(*delta.JsonObject, context.Context) (delta.JsonValue, error) { return nil, nil }),
			}}, nil
		}, ServeOptions{})
	})
}

// The start barrier (host.ts:730) ends once every host-lifetime object has its first outcome. The object parked on a reader
// that was adopted before its attempt followed it never went live: Load never returned. The case asks the object, once the
// host is quiet, so a first outcome that never comes fails by assertion and not by a Load that never returns.
func TestStartBarrierAHostObjectWhoseReaderWasAdoptedBetweenItsCheckAndItsFollowGoesLive(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}}).(*chordHost)
	late := newRlReading(host)
	loaded := make(chan error, 1)
	go func() {
		_, err := host.Load([]HostFacet{{Name: "@test/late", Facet: rlFacet(late)}}, background)
		loaded <- err
	}()
	receive(t, late.adopted)
	deliveredOn(host)
	var phase objectStateKind
	var first, found bool
	host.enter(func() {
		for _, object := range host.objects {
			if object.served.contract.Name == rlContract.Name {
				phase, first, found = object.phase.kind, object.hasFirstOutcome, true
			}
		}
	})
	if !found || phase != objectLive || !first {
		t.Fatalf("the host-lifetime object: found = %v, phase = %d (live is %d), first outcome = %v, want live with its first outcome: "+
			"its reader was adopted before the attempt followed it, and nothing told the attempt", found, phase, objectLive, first)
	}
	must(t, <-loaded)
	must(t, host.Close(background))
}
