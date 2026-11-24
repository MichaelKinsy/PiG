package host

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Go tests of packages/chord/src/host/read-declarations.ts's pure checks, which Pi covers through the refusals of
// test/host/named-query.test.ts and query-reader.test.ts (they wait for the store's read engine). Pi's checks of the
// builders' and the sources' shapes (an object of functions, `[[query, args]]`) are typed in Go and have no check.

// pi: packages/chord/src/host/read-declarations.ts
func TestReadDeclarationRefusals(t *testing.T) {
	cases := []struct {
		name string
		got  error
		want string
	}{
		{"a table given to a reader", namedQueryExpected("app.todos", "liveQuery"), "app.todos: self.liveQuery reads a named query; a table of this object is read with self.query"},
		{"a list's reader names the list", namedQueryExpected("app.todos", `list "all"`), `app.todos: self.list "all" reads a named query; a table of this object is read with self.query`},
		{"a table the factory did not declare", undeclaredTable("app.todos", "queries"), "app.todos: self.queries: the table is not one this factory declared"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got == nil || c.got.Error() != c.want {
				t.Fatalf("error = %v, want %q", c.got, c.want)
			}
		})
	}
}

func TestFirstOf(t *testing.T) {
	for _, first := range []int64{1, 25, 1<<53 - 1} {
		if err := firstOf("app.todos", "liveQuery", first); err != nil {
			t.Fatalf("firstOf(%d) = %v, want accepted", first, err)
		}
	}
	for _, first := range []int64{0, -1, 1 << 53} {
		err := firstOf("app.todos", "list", first)
		if err == nil || err.Error() != "app.todos: self.list: { first: n } is required, a positive integer" {
			t.Fatalf("firstOf(%d) = %v, want the refusal", first, err)
		}
	}
}

func TestListName(t *testing.T) {
	listed := map[string]struct{}{}
	if err := listName("app.todos", "all", listed); err != nil {
		t.Fatalf("a first name: %v", err)
	}
	if err := listName("app.todos", "all", listed); err == nil || err.Error() != `app.todos: list "all" is declared twice` {
		t.Fatalf("a second declaration: %v", err)
	}
	if err := listName("app.todos", "", listed); err == nil || !strings.HasSuffix(err.Error(), "self.list: a list has a name") {
		t.Fatalf("an empty name: %v", err)
	}
	if _, kept := listed[""]; kept {
		t.Fatal("a refused name was remembered")
	}
	if err := listName("app.todos", "open", listed); err != nil {
		t.Fatalf("another name: %v", err)
	}
}

// Ports packages/chord/test/host/read-declarations.test.ts ("opening readers", 4 cases): `openReaders`, the step a parked
// attempt or an admission opens its readers of named queries with. Pi's reader is a fake of `open` and `source`; here a
// real reader over a fake Reading that is ready, has failed, has not run yet, or whose builder breaks: the same four
// outcomes of `Open`. Pi's `await openReaders(...)` is the wait for `settle`, which openReaders calls once it has decided.

// rdReading is a Reading with the outcome of its first run set by the test: a run that settled with a value, with a
// failure, or not yet.
type rdReading struct {
	ready   bool
	failure error
	deps    state.DependentSet
}

func (f *rdReading) Value() (delta.JsonValue, error)               { return nil, nil }
func (f *rdReading) DeriveValue() (any, error)                     { return nil, nil }
func (f *rdReading) DependentSet() *state.DependentSet             { return &f.deps }
func (f *rdReading) Ready() bool                                   { return f.ready }
func (f *rdReading) Failure() error                                { return f.failure }
func (f *rdReading) Open(store.LiveTarget, context.Context) func() { return func() {} }
func (f *rdReading) Fail(error, context.Context)                   {}
func (f *rdReading) Detach(error)                                  {}

// rdWake is the dependent a parked attempt follows its readers with.
type rdWake struct{}

func (rdWake) Invalidate()                                {}
func (rdWake) SourceRetired(map[state.Dependent]struct{}) {}

// rdBlockers records the blockers openReaders names to its long wait.
type rdBlockers struct{ named []*blocker }

func (b *rdBlockers) blockedBy(found *blocker) { b.named = append(b.named, found) }

// rdOpening is Pi's `opening`: a host, readers over fakes, and what openReaders settled.
type rdOpening struct {
	host     *chordHost
	wake     rdWake
	blockers *rdBlockers
	readings []*rdReading
	readers  []*reader
}

func newRdOpening(t *testing.T) *rdOpening {
	t.Helper()
	return &rdOpening{host: newHost(t, HostOptions{OnError: func(HostError) {}}).(*chordHost), blockers: &rdBlockers{}}
}

// reader adds a reader whose reading has the given outcome; builds, when set, is what its builder throws.
func (o *rdOpening) reader(t *testing.T, reading *rdReading, builds error) *rdReading {
	t.Helper()
	table := mustTableDef("doc", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("n", store.Int())}, Key: []string{"n"}})
	snapshot := nqMust(contracts.SnapshotArguments(contracts.Obj(), `app.doc: query "all": args`))
	own := newOwnQuery("app.doc", "all", store.NewTableHandle(table, "app.doc:doc", nil, nil), func(delta.JsonValue) *store.Query {
		if builds != nil {
			panic(builds)
		}
		return store.NewQuery(table)
	}, QueriesOptions{}, snapshot)
	given := NewGivenQuery(own, GivenArguments{Kind: GivenFixed, Value: jsonObject()}, o.host.batch)
	o.readings = append(o.readings, reading)
	o.readers = append(o.readers, newReader(given, reading, func() {}, o.host.loop().Go))
	return reading
}

// open runs openReaders over the readers, and waits for the step's decision.
func (o *rdOpening) open() parkResult {
	settled := make(chan parkResult, 1)
	// A step runs in the host's section (a parked attempt's check).
	o.host.enter(func() {
		openReaders(o.host, o.readers, o.wake, o.blockers, "app.doc: a live query", background, func(result parkResult) { settled <- result })
	})
	return <-settled
}

func TestOpeningReadersIsReadyOnceEveryReaderHasAValueAndFollowsNone(t *testing.T) {
	o := newRdOpening(t)
	a, b := o.reader(t, &rdReading{ready: true}, nil), o.reader(t, &rdReading{ready: true}, nil)
	if got := o.open(); got.kind != parkingReady {
		t.Fatalf("parking = %+v, want ready", got)
	}
	if sizes := [2]int{a.deps.Len(), b.deps.Len()}; sizes != [2]int{0, 0} {
		t.Fatalf("dependents = %v, want none", sizes)
	}
}

func TestOpeningReadersParksWaitingOnAReadThatFailedWithTheCauseAReloadsReportNamesAndFollowsIt(t *testing.T) {
	o := newRdOpening(t)
	failed := o.reader(t, &rdReading{failure: errors.New("no such set")}, nil)
	later := o.reader(t, &rdReading{ready: true}, nil)
	got := o.open()
	const message = "waiting for app.doc: a live query: app.doc: a live query: no such set"
	if got.kind != parkingParked || !got.firstPass || got.shown == nil || got.shown.Kind != RefStateFailed || got.shown.Cause.Kind != data.FailureWaiting ||
		got.cause == nil || got.cause.Kind != data.FailureWaiting || got.cause.Error.Message != message {
		t.Fatalf("parking = %+v, want parked, first pass, failed { waiting } with %q", got, message)
	}
	if followed := [2]int{failed.deps.Len(), later.deps.Len()}; followed != [2]int{1, 0} {
		t.Fatalf("dependents = %v, want [1 0]", followed)
	}
	if last := o.blockers.named[len(o.blockers.named)-1]; *last != (blocker{label: "app.doc: a live query", state: blockerFailed}) {
		t.Fatalf("last blocker = %+v, want failed", *last)
	}
}

func TestOpeningReadersParksLoadingOnAReadThatHasNotRunYetWithACauseTooAndHoldsUpTheFirstPass(t *testing.T) {
	o := newRdOpening(t)
	o.reader(t, &rdReading{ready: true}, nil)
	pending := o.reader(t, &rdReading{}, nil)
	got := o.open()
	const message = "waiting for app.doc: a live query: app.doc: a live query is loading"
	if got.kind != parkingParked || got.firstPass || got.shown == nil || got.shown.Kind != RefStateLoading ||
		got.cause == nil || got.cause.Kind != data.FailureWaiting || got.cause.Error.Message != message {
		t.Fatalf("parking = %+v, want parked, not first pass, loading with %q", got, message)
	}
	if pending.deps.Len() != 1 {
		t.Fatalf("dependents of the pending reader = %d, want the wake", pending.deps.Len())
	}
}

func TestOpeningReadersBreaksOnAReadersOwnCode(t *testing.T) {
	o := newRdOpening(t)
	o.reader(t, &rdReading{}, errors.New("bad arguments"))
	got := o.open()
	if got.kind != parkingBroken || got.err == nil || got.err.Message != "app.doc: a live query: bad arguments" {
		t.Fatalf("parking = %+v, want broken with %q", got, "app.doc: a live query: bad arguments")
	}
}
