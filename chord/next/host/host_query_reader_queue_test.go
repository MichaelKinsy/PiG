package host

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Go's own tests (Pi has none: they test the Go form of the promises of query-reader.ts, see the file's header): a reader
// opens each read in the section of the move (Pi's `read` registers its query when it is called, in call order) and keeps
// ONE pending load behind the one running, drained by one owned task (an overtaken read's load does nothing there). The
// reader is given a fake Reading, which blocks each load until the test lets it go, and a real OwnQuery over a table
// nothing reads.

// qqArguments are the arguments a reader follows: a source that is no store's (its value is what the test set).
type qqArguments struct{ n atomic.Int64 }

func (a *qqArguments) Value() (delta.JsonValue, error) {
	return jsonObject("n", float64(a.n.Load())), nil
}
func (a *qqArguments) DeriveValue() (any, error) { return a.Value() }

// DependentSet is nil: the test moves the reader itself.
func (a *qqArguments) DependentSet() *state.DependentSet { return nil }

// qqRead is one read whose load the fake reading ran, held until release.
type qqRead struct {
	target  store.LiveTarget
	release chan struct{}
}

// qqReading is a Reading that blocks each Read on the test.
type qqReading struct {
	state.DeriveSource
	reads    chan *qqRead
	detached atomic.Pointer[error]
	// opens counts the reads opened (each move opens one); count the loads that ran.
	opens atomic.Int64
	count atomic.Int64
}

func (f *qqReading) Ready() bool    { return true }
func (f *qqReading) Failure() error { return nil }

func (f *qqReading) Open(target store.LiveTarget, _ context.Context) func() {
	f.opens.Add(1)
	return func() {
		read := &qqRead{target: target, release: make(chan struct{})}
		f.count.Add(1)
		f.reads <- read
		<-read.release
	}
}

func (f *qqReading) Fail(error, context.Context) {}

func (f *qqReading) Detach(err error) { f.detached.Store(&err) }

// qqFixture is a reader of a table's query "all" for the arguments `{ n }`, over a host without a store.
type qqFixture struct {
	host    *chordHost
	args    *qqArguments
	reading *qqReading
	reader  *reader
	woken   atomic.Int64
	mu      sync.Mutex
	built   map[int64]*store.Query
}

func newQQ(t *testing.T) *qqFixture {
	t.Helper()
	f := &qqFixture{args: &qqArguments{}, reading: &qqReading{reads: make(chan *qqRead, 8)}, built: map[int64]*store.Query{}}
	f.host = newHost(t, HostOptions{OnError: func(HostError) {}}).(*chordHost)
	table := mustTableDef("queue", store.TableOptions{Version: 1, Columns: []store.ColumnProp{store.Col("n", store.Int())}, Key: []string{"n"}})
	snapshot := nqMust(contracts.SnapshotArguments(contracts.Obj(contracts.Prop("n", contracts.Int())), "app.queue: query \"all\": args"))
	own := newOwnQuery("app.queue", "all", store.NewTableHandle(table, "app.queue:queue", nil, nil), func(args delta.JsonValue) *store.Query {
		n := qrInt(args.(*delta.JsonObject).Value("n"))
		query := store.NewQuery(table).Where(func(row, _ store.ColumnRefs) store.Condition { return store.Eq(row["n"], n) })
		f.mu.Lock()
		f.built[n] = query
		f.mu.Unlock()
		return query
	}, QueriesOptions{}, snapshot)
	given := NewGivenQuery(own, GivenArguments{Kind: GivenFollowing, Source: f.args}, f.host.batch)
	f.reader = newReader(given, f.reading, func() { f.woken.Add(1) }, f.host.loop().Go)
	return f
}

// move is: the arguments became n, and the reader was told, in a section as a batch's flush tells it.
func (f *qqFixture) move(n int64) {
	f.args.n.Store(n)
	f.host.batch.Section(f.reader.moved)
}

func (f *qqFixture) queryFor(n int64) *store.Query {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.built[n]
}

// settled waits until the reads the reader queued ran.
func (f *qqFixture) settled() { f.reader.settle() }

// open is `await reader.open(context)`: the reader's Open on this goroutine, and its tail run in a section, as awaitTail does.
func (f *qqFixture) open() Opened {
	tail := f.reader.Open(background)
	var got Opened
	f.host.batch.Section(func() { got = tail() })
	return got
}

func TestQueryReaderQueueTwoMovesWhileAReadRunsReadOnceWithTheLaterTarget(t *testing.T) {
	f := newQQ(t)
	f.args.n.Store(1)
	opened := make(chan Opened, 1)
	go func() { opened <- f.open() }()
	first := <-f.reading.reads
	if first.target.Spec.Query != f.queryFor(1) {
		t.Fatal("the first read is not of the arguments the reader opened with")
	}
	select {
	case <-opened:
		t.Fatal("Open returned before the first run settled")
	default:
	}
	// Two moves while the first read runs: one pending read, the later's target.
	f.move(2)
	f.move(3)
	close(first.release)
	if got := <-opened; got.Kind != OpenedReady {
		t.Fatalf("Open = %+v, want ready", got)
	}
	second := <-f.reading.reads
	if second.target.Spec.Query != f.queryFor(3) {
		t.Fatal("the second read is not of the later arguments")
	}
	close(second.release)
	f.settled()
	if loads, opens := f.reading.count.Load(), f.reading.opens.Load(); loads != 2 || opens != 3 {
		t.Fatalf("%d loads of %d opened reads, want 2 of 3 (every move opens its read; the overtaken one's load is dropped)", loads, opens)
	}
}

func TestQueryReaderQueueACloseDropsTheQueuedReadAndDetachesTheReading(t *testing.T) {
	f := newQQ(t)
	f.args.n.Store(1)
	opened := make(chan Opened, 1)
	go func() { opened <- f.open() }()
	first := <-f.reading.reads
	f.move(2)
	ended := context.Canceled
	f.host.batch.Section(func() { f.reader.Close(ended) })
	close(first.release)
	<-opened
	f.settled()
	if loads, opens := f.reading.count.Load(), f.reading.opens.Load(); loads != 1 || opens != 2 {
		t.Fatalf("%d loads of %d opened reads, want 1 of 2 (the move opened its read at once; the close dropped its load)", loads, opens)
	}
	if detached := f.reading.detached.Load(); detached == nil || !errors.Is(*detached, ended) {
		t.Fatalf("detached with %v, want %v", detached, ended)
	}
	// A move after the close reads nothing.
	f.move(3)
	f.settled()
	if loads, opens := f.reading.count.Load(), f.reading.opens.Load(); loads != 1 || opens != 2 {
		t.Fatalf("%d loads of %d opened reads after a move of a closed reader, want 1 of 2", loads, opens)
	}
}

func TestQueryReaderQueueAMoveBeforeTheFirstReadOnlyWakesItsAttempt(t *testing.T) {
	f := newQQ(t)
	f.move(2)
	f.settled()
	if f.woken.Load() != 1 || f.reading.opens.Load() != 0 {
		t.Fatalf("woken %d, opened reads %d; want 1, 0", f.woken.Load(), f.reading.opens.Load())
	}
}
