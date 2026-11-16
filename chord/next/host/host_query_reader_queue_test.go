package host

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
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
	// The close dropped the queued read: none is left to run (kept, it would run, and block on the test's gate below).
	var queued bool
	f.host.batch.Section(func() { queued = f.reader.pending != nil })
	t.Cleanup(func() {
		select {
		case <-first.release:
		default:
			close(first.release)
		}
	})
	if queued {
		t.Fatal("a read is still queued after the close, which drops it")
	}
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

// qqSourced is a followed source with a dependent set of its own: what the reader's given query registers with.
type qqSourced struct {
	*qqArguments
	deps state.DependentSet
}

func (s *qqSourced) DependentSet() *state.DependentSet { return &s.deps }

// qqHandle counts the follows of the provider's other live instances the reader makes and releases.
type qqHandle struct {
	NamedQueryHandle
	follows, releases atomic.Int64
}

func (h *qqHandle) Follow(func()) func() {
	h.follows.Add(1)
	return func() { h.releases.Add(1) }
}

// Go-only (Pi's query-reader.ts `close` is `this.#closed = true; this.#given.release(); this.#reading.detach(error)`, line 98): a
// reader's close lets go of what its arguments follow: the source's dependent set no longer holds the given query, and the
// provider's instances are no longer followed, while the source itself lives on and keeps changing.
func TestQueryReaderQueueACloseReleasesTheSourceItFollowedAndTheProvidersInstances(t *testing.T) {
	f := newQQ(t)
	source := &qqSourced{qqArguments: f.args}
	handle := &qqHandle{NamedQueryHandle: f.reader.given.Handle}
	reader := newReader(NewGivenQuery(handle, GivenArguments{Kind: GivenFollowing, Source: source}, f.host.batch), f.reading, func() {}, f.host.loop().Go)
	f.args.n.Store(1)
	opened := make(chan Opened, 1)
	go func() {
		tail := reader.Open(background)
		var got Opened
		f.host.batch.Section(func() { got = tail() })
		opened <- got
	}()
	first := <-f.reading.reads
	close(first.release)
	if got := <-opened; got.Kind != OpenedReady {
		t.Fatalf("Open = %+v, want ready", got)
	}
	if holds, follows := source.deps.Len(), handle.follows.Load(); holds != 1 || follows != 1 {
		t.Fatalf("while open: the source holds %d dependents and the provider %d follows, want 1 and 1", holds, follows)
	}
	f.host.batch.Section(func() { reader.Close(context.Canceled) })
	if holds, released := source.deps.Len(), handle.releases.Load(); holds != 0 || released != 1 {
		t.Fatalf("after the close: the source holds %d dependents and the provider's follows released %d, want 0 and 1", holds, released)
	}
}

// Go-only: query-reader.ts `#value` turns only `not_ready` into the reader's own refusal (line 211: `error.code !== "not_ready"`
// rethrows); any other failure of the source, a chord error of another code or a plain one, is the read's failure as it is (Pi's
// case for the arguments without a value covers the `not_ready` side).
func TestQueryReaderArgumentsThatFailWithAnythingButNotReadyFailTheReadWithThatErrorAsItIs(t *testing.T) {
	f := newQQ(t)
	given := func(err error) *GivenQuery {
		return NewGivenQuery(f.reader.given.Handle, GivenArguments{Kind: GivenFollowing, Source: &qqFailing{err: err}}, f.host.batch)
	}
	for name, err := range map[string]error{
		"another code": data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "the source is down"}, nil),
		"a plain one":  errors.New("boom"),
	} {
		if _, got := given(err).value(); got != err { //nolint:errorlint // identity: the failure as it is, not one that wraps it
			t.Fatalf("%s: value failed with %v, want %v as it is", name, got, err)
		}
	}
	notReady := data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: "no value yet"}, nil)
	_, got := given(notReady).value()
	var refused *data.ChordError
	if !errors.As(got, &refused) || refused.Code != data.CodeInvalid || refused.Reason != data.ReasonArgs {
		t.Fatalf("not_ready: value failed with %v, want invalid/args", got)
	}
}

// qqFailing is a followed source whose value fails with err.
type qqFailing struct{ err error }

func (s *qqFailing) Value() (delta.JsonValue, error)   { return nil, s.err }
func (s *qqFailing) DeriveValue() (any, error)         { return nil, s.err }
func (s *qqFailing) DependentSet() *state.DependentSet { return nil }

// Go-only: Pi's `#awaitReaders` `finally { reader.source.dependents.delete(this.#readerWake) }` (attempt.ts:1606). The attempt of a
// page whose set is not served is parked on its reader (`failed { waiting }`) and follows the reader with its wake; the set bound
// again, the read runs, the reader is ready, the attempt builds. Pi's read-declarations.test.ts reads `openReaders` alone (it
// adds the wake and never takes it away): what takes the wake off the readers the attempt waited on is the attempt, which
// no later close does while the object lives; a wake left behind would flag the finished attempt at every run of the query.
func TestNamedQueryAnAttemptThatWaitedOnAReaderDoesNotFollowItOnceItIsBuilt(t *testing.T) {
	chordStore := memoryStore(t)
	club := nqClubPlugin(&nqProbe{})
	host := newHost(t, HostOptions{Store: chordStore})
	_, err := host.Load([]HostFacet{nqBareSessions(), club}, background)
	must(t, err)
	page := mustHold(t, host, nqTeamPage, jsonObject("team", "u12"))
	flushed(host)
	deliveredOn(host)
	if got := kindOf(page); got == RefStateLive {
		t.Fatalf("state = %s before the set is served, want the page waiting on its reader", got)
	}
	reloadMust(t)(host.Reload([]HostFacet{nqPublisher(nqRows), club}, background, ReloadOptions{}))
	flushed(host)
	deliveredOn(host)
	wantKind(t, page, RefStateLive)
	var following []string
	hostTurn(host, func() {
		for _, object := range host.(*chordHost).objectList() {
			for _, each := range []*attempt{object.phase.attempt, object.exposed, object.old, object.kept, object.showsReadiness} {
				if each == nil {
					continue
				}
				for _, reader := range each.readers {
					if reader.source().DependentSet().Delete(each.readerWake) {
						following = append(following, object.text)
					}
				}
			}
		}
	})
	if len(following) != 0 {
		t.Fatalf("built attempts of %v still follow the readers they waited on", following)
	}
}
