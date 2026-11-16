package store

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/store/reads.test.ts: the store's reads (slice 14 §3.2, §5): the index from a table to the
// reads over it, the bindings of column sets (one table per name, moved or dropped by their owner alone), tables held
// while they are not ready, and the refresh a commit stages: it takes its reads from the index inside the step and runs
// none over a held table. Test names share the prefix `Reads`.

func tagsTable(t *testing.T) *TableDefinition {
	t.Helper()
	tagSet, err := DefineColumns("tags.columns", ColumnSetOptions{
		Columns: []ColumnProp{Col("note", Int()), Col("tag", Str())},
		Key:     []string{"note", "tag"},
	})
	must(t, err)
	return qDefine(t, "tags", TableOptions{Version: 1, Columns: slices.Clone(tagSet.Schemas), Key: []string{"note", "tag"}, Exposes: tagSet})
}

func TestReadsBindings(t *testing.T) {
	tags := tagsTable(t)

	t.Run("bind one table per set name: another owner's other table is refused, naming both; only the owner unbinds", func(t *testing.T) {
		reads := NewReads()
		owner, other := tableOwner(), tableOwner()
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tags}, owner))
		if bound, _ := reads.Get("tags.columns"); bound.Table != "a:tags" {
			t.Fatalf("bound to %q", bound.Table)
		}
		err := reads.Bind("tags.columns", Bound{Table: "b:tags", Definition: tags}, other)
		if err == nil || err.Error() != `column set "tags.columns" is exposed by tables "a:tags" and "b:tags"` {
			t.Fatalf("err = %v", err)
		}
		reads.Unbind("tags.columns", other)
		if bound, found := reads.Get("tags.columns"); !found || bound.Table != "a:tags" {
			t.Fatal("another owner unbound the set")
		}
		qEqual(t, reads.BoundBy(owner), []string{"tags.columns"})
		// The owner moves the set to another of its tables, then drops it.
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags2", Definition: tags}, owner))
		if bound, _ := reads.Get("tags.columns"); bound.Table != "a:tags2" {
			t.Fatalf("moved to %q", bound.Table)
		}
		reads.Unbind("tags.columns", owner)
		if _, found := reads.Get("tags.columns"); found {
			t.Fatal("the set is still bound")
		}
		qEqual(t, reads.BoundBy(owner), []string(nil))
	})

	t.Run("another owner may bind a set to the table it is bound to: the set is its own from then on", func(t *testing.T) {
		reads := NewReads()
		owner, other := tableOwner(), tableOwner()
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tags}, owner))
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tags}, other))
		qEqual(t, reads.BoundBy(owner), []string(nil))
		qEqual(t, reads.BoundBy(other), []string{"tags.columns"})
		// Only the new owner drops it.
		reads.Unbind("tags.columns", owner)
		if _, found := reads.Get("tags.columns"); !found {
			t.Fatal("the previous owner unbound the set")
		}
	})

	t.Run("a new definition of the bound table is a new binding: the watchers are told; another table's readiness is not theirs", func(t *testing.T) {
		reads := NewReads()
		owner := tableOwner()
		told := 0
		reads.Watch("tags.columns", func() { told++ })
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tags}, owner))
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tagsTable(t)}, owner))
		if told != 2 {
			t.Fatalf("told %d times, want 2", told)
		}
		reads.Hold("a:other")
		reads.Ready("a:other")
		if told != 2 {
			t.Fatalf("told %d times of another table's readiness", told)
		}
	})

	t.Run("tell a set's watchers of a binding, a move, a drop and its table's readiness, until they release", func(t *testing.T) {
		reads := NewReads()
		owner := tableOwner()
		told := 0
		release := reads.Watch("tags.columns", func() { told++ })
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tags}, owner))
		// The same binding again changes nothing.
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tags}, owner))
		reads.Hold("a:tags")
		if !reads.IsHeld("a:tags") {
			t.Fatal("not held")
		}
		reads.Ready("a:tags")
		reads.Ready("a:tags")
		reads.Unbind("tags.columns", owner)
		if told != 3 {
			t.Fatalf("told %d times, want 3", told)
		}
		release()
		must(t, reads.Bind("tags.columns", Bound{Table: "a:tags", Definition: tags}, owner))
		if told != 3 {
			t.Fatalf("told %d times after the release, want 3", told)
		}
	})
}

// runLog is every run a read's sink adopted.
type runLog struct {
	mu   sync.Mutex
	runs []ReadRun
}

func (log *runLog) Adopt(run ReadRun) {
	log.mu.Lock()
	log.runs = append(log.runs, run)
	log.mu.Unlock()
}

func (log *runLog) len() int {
	log.mu.Lock()
	defer log.mu.Unlock()
	return len(log.runs)
}

func (log *runLog) from(first int) []ReadRun {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.runs[first:])
}

func (log *runLog) last() ReadRun {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.runs[len(log.runs)-1]
}

type readsWorld struct {
	store  *ChordStore
	tags   *TableHandle
	notes  *TableHandle
	set    *ColumnSet
	tagged *Query
}

func newReadsWorld(t *testing.T) *readsWorld {
	t.Helper()
	set, err := DefineColumns("tags.columns", ColumnSetOptions{
		Columns: []ColumnProp{Col("note", Int()), Col("tag", Str())},
		Key:     []string{"note", "tag"},
	})
	must(t, err)
	tags := qDefine(t, "tags", TableOptions{Version: 1, Columns: slices.Clone(set.Schemas), Key: []string{"note", "tag"}, Exposes: set})
	notes := qDefine(t, "notes", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Int()), Col("text", Str())}, Key: []string{"id"}})
	store := memoryStore(t)
	w := &readsWorld{store: store, set: set, tags: tableIn(t, store, tags), notes: tableIn(t, store, notes)}
	w.tagged = NewQuery(notes).Where(func(n, _ ColumnRefs) Condition {
		return Exists(set, Match{{"note", n["id"]}}, func(tag ColumnRefs) Condition { return Eq(tag["tag"], "red") })
	})
	must(t, w.tagged.Err())
	return w
}

func (w *readsWorld) bind(t *testing.T) {
	t.Helper()
	must(t, w.store.Reads.Bind(w.set.Name, Bound{Table: w.tags.Sql.Table, Definition: w.tags.Definition}, w.tags))
}

type openedReads struct {
	open  *OpenRead
	query *OpenQuery
	runs  *runLog
	batch *state.Batch
	// loading is the loads a rebinding started (Pi's `void open.load()`): `settle` waits until they ended, Go's form of
	// Pi's `await new Promise(setTimeout 0)` after a binding changed.
	loading sync.WaitGroup
}

func (o *openedReads) settle() { o.loading.Wait() }

// opened is an open query of q over handle's table with one read of it, and every run its sink adopted.
func (w *readsWorld) opened(t *testing.T, handle *TableHandle, q *Query) *openedReads {
	t.Helper()
	must(t, q.Err())
	result := &openedReads{runs: &runLog{}, batch: state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)}
	spec := QuerySpec{Query: q, Table: handle.Sql.Table, Projection: Projection{}, Owner: handle}
	// A binding changed: its one read runs again, as a live query's.
	result.query = NewOpenQuery(ReadSite{Store: w.store, Batch: result.batch}, spec, ReadRows, func() {
		result.loading.Go(func() {
			result.open.Load()
		})
	}, background)
	result.open = result.query.Read(ReadKind{Kind: "rows", Range: Range{Limit: 5}}, result.runs)
	result.open.Load()
	return result
}

// stage is a transaction's one put into handle's table with the refresh it stages.
func (w *readsWorld) stage(t *testing.T, handle *TableHandle, row Row, batch *state.Batch) ([]StagedWrite, *ReadRefresh, CommitFence) {
	t.Helper()
	var staged []StagedWrite
	changes := NewTableChanges(handle, &staged)
	must(t, changes.Put(row))
	refresh := NewReadRefresh(w.store.Reads, []string{handle.Sql.Table}, changes.Changes, background)
	staged = append(staged, refresh.Write)
	fence := CommitFence{
		Admit: func() bool { return true },
		Adopt: func(int64, []int64, int) error { return batch.Commit(refresh.Adopt, background) },
	}
	return staged, refresh, fence
}

// put commits one put into handle's table with the refresh a transaction stages.
func (w *readsWorld) put(t *testing.T, handle *TableHandle, row Row, batch *state.Batch) {
	t.Helper()
	staged, _, fence := w.stage(t, handle, row, batch)
	if _, err := w.store.Commit(staged, fence, background); err != nil {
		t.Fatal(err)
	}
}

// shown is what a run shows: its rows' ids, or its kind.
func shown(run ReadRun) any {
	if run.Kind != "rows" {
		return run.Kind
	}
	ids := []any{}
	for _, row := range run.Result.Rows {
		ids = append(ids, row["id"])
	}
	return ids
}

// pausedPut starts a commit of one put into handle's table whose step waits, after its reads ran, until `release`:
// what happens meanwhile happens between a read's run and its adoption. The commit's result arrives on `committed`.
func (w *readsWorld) pausedPut(t *testing.T, handle *TableHandle, row Row, batch *state.Batch) (release func(), committed <-chan error) {
	t.Helper()
	staged, _, fence := w.stage(t, handle, row, batch)
	ran, held := make(chan struct{}), make(chan struct{})
	staged = append(staged, pause{ran: ran, held: held})
	done := make(chan error, 1)
	go func() {
		_, err := w.store.Commit(staged, fence, background)
		done <- err
	}()
	<-ran
	return func() { close(held) }, done
}

// pause is the staged write that lets the step wait for its test.
type pause struct{ ran, held chan struct{} }

func (pause) StageOrder() WriteOrder { return OrderWrite }

func (write pause) Apply(*Step) ([]StoreWrite, error) {
	close(write.ran)
	<-write.held
	return nil, nil
}

func TestReadsRunBelongsToItsRegistration(t *testing.T) {
	t.Run("a commit's run of a plan the query no longer has is dropped: it cannot heal the failure that followed it", func(t *testing.T) {
		// Astra: another owner's commit ran the read; the set was withdrawn (a failed run, `unavailable`); the commit then
		// adopted the run its step had made with the old plan, and the read showed rows of a set nobody serves.
		w := newReadsWorld(t)
		w.bind(t)
		o := w.opened(t, w.notes, w.tagged)
		w.put(t, w.tags, Row{"note": int64(1), "tag": "red"}, o.batch)
		// The step runs the read: the new note passes its condition.
		before := o.runs.len()
		release, committed := w.pausedPut(t, w.notes, Row{"id": int64(1), "text": "t"}, o.batch)
		w.store.Reads.Unbind(w.set.Name, w.tags)
		o.settle()
		if failed := o.runs.from(before); len(failed) != 1 || failed[0].Kind != "failed" {
			t.Fatalf("runs since = %#v, want one failed run", failed)
		}
		failed := o.runs.len()
		release()
		must(t, <-committed)
		if o.runs.len() != failed {
			t.Fatalf("the commit adopted a run of the old plan: %d runs, want %d", o.runs.len(), failed)
		}
		// Bound again, the new registration reads for itself.
		w.bind(t)
		o.settle()
		qEqual(t, shown(o.runs.last()), any([]any{int64(1)}))
		o.query.Close()
	})

	t.Run("a run a commit made before the read closed still reaches its sink: a list's retired page", func(t *testing.T) {
		w := newReadsWorld(t)
		o := w.opened(t, w.notes, NewQuery(w.notes.Definition))
		release, committed := w.pausedPut(t, w.notes, Row{"id": int64(1), "text": "t"}, o.batch)
		o.open.Close()
		release()
		must(t, <-committed)
		qEqual(t, shown(o.runs.last()), any([]any{int64(1)}))
		o.query.Close()
	})
}

// Pi open-query.ts:202-207 (`load`): `if (this.#generation !== generation) return undefined; site.batch.within(() => read.adopt(run),
// context)`. The check and the adoption are one turn of Pi's one thread, so no commit's run can come between them. Go's
// adoption waits for a section of the host; a check made before that wait is stale when the section is entered: a commit's
// adoption that took the section first made the page's newer rows, and the first load's older run was adopted after them
// (a list's page lost the rows of a gate commit: "ids = [30 40], want [10 20 30 40]"). The test's section holds the load between
// its read and its adoption, as a busy host does, and lets a commit adopt meanwhile.
func TestReadsLoadIsOvertakenByACommitThatAdoptsWhileItWaitsForItsSection(t *testing.T) {
	w := newReadsWorld(t)
	o := w.opened(t, w.notes, NewQuery(w.notes.Definition))
	entered, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	o.batch.UseSection(func(work func()) {
		if held.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		work()
	}, nil)
	loaded := make(chan *ReadRun)
	go func() { loaded <- o.open.Load() }()
	<-entered
	// The load read no rows and waits for its section. A commit puts a row and adopts its run.
	before := o.runs.len()
	w.put(t, w.notes, Row{"id": int64(1), "text": "t"}, o.batch)
	qEqual(t, o.runs.len(), before+1)
	close(release)
	if run := <-loaded; run != nil {
		t.Fatalf("the load adopted its older run after the commit's: %#v", shown(*run))
	}
	qEqual(t, o.runs.len(), before+1)
	qEqual(t, shown(o.runs.last()), any([]any{int64(1)}))
	o.query.Close()
}

// Pi open-query.ts:194-198 (`load`): `if (this.#closed) return undefined; if ("error" in compiled) { ...; site.batch.within(() =>
// this.#sink.adopt(failed), context) }`: the closed check, the compile-error check and the adoption of the failed run are one
// turn, so a read closed before the adoption adopts nothing. Go's adoption waits for a section of the host; checks made before that
// wait are stale when the section is entered: a read closed meanwhile (a list's retired page) handed its sink a failed run, which
// fails the list. The test's section holds the adoption between its checks and its entry and closes the read meanwhile.
func TestReadsFailedCompileIsNotAdoptedByAReadClosedWhileItWaitsForItsSection(t *testing.T) {
	w := newReadsWorld(t)
	o := w.opened(t, w.notes, w.tagged)
	if runs := o.runs.len(); runs != 1 {
		t.Fatalf("runs = %d, want the one failed run of the open", runs)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	o.batch.UseSection(func(work func()) {
		if held.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		work()
	}, nil)
	loaded := make(chan *ReadRun)
	go func() { loaded <- o.open.Load() }()
	<-entered
	o.open.Close()
	close(release)
	if run := <-loaded; run != nil {
		t.Fatalf("a read closed while its failed run waited for the section loaded %#v", run)
	}
	if runs := o.runs.len(); runs != 1 {
		t.Fatalf("runs = %d after the close, want only the failed run of the open: the sink of a closed read was handed another", runs)
	}
	o.query.Close()
}

func TestReadsIndexAndRefresh(t *testing.T) {
	t.Run("enter a read under every table it uses: the other owner's commit runs it; closed, nothing does", func(t *testing.T) {
		w := newReadsWorld(t)
		w.bind(t)
		o := w.opened(t, w.notes, w.tagged)
		qEqual(t, []int{len(w.store.Reads.On(w.notes.Sql.Table)), len(w.store.Reads.On(w.tags.Sql.Table))}, []int{1, 1})
		w.put(t, w.notes, Row{"id": int64(1), "text": "t"}, o.batch)
		w.put(t, w.tags, Row{"note": int64(1), "tag": "red"}, o.batch)
		qEqual(t, shown(o.runs.last()), any([]any{int64(1)}))
		before := o.runs.len()
		o.query.Close()
		if on := w.store.Reads.On(w.tags.Sql.Table); on != nil {
			t.Fatalf("reads over tags after the close: %d", len(on))
		}
		w.put(t, w.tags, Row{"note": int64(1), "tag": "blue"}, o.batch)
		if o.runs.len() != before {
			t.Fatalf("a closed query adopted %d runs", o.runs.len()-before)
		}
	})

	t.Run("a query that does not compile is a failed run; the set bound later, it compiles and runs once", func(t *testing.T) {
		w := newReadsWorld(t)
		o := w.opened(t, w.notes, w.tagged)
		runs := o.runs.from(0)
		if len(runs) != 1 || runs[0].Kind != "failed" {
			t.Fatalf("runs = %#v, want one failed run", runs)
		}
		if message := rejected(t, runs[0].Err, data.CodeUnavailable, "").Message; message != `column set "tags.columns" is not served` {
			t.Fatalf("message = %q", message)
		}
		if o.open.Read() != nil {
			t.Fatal("a query that does not compile has a read")
		}
		w.bind(t)
		o.settle()
		qEqual(t, shown(o.runs.last()), any([]any{}))
		o.query.Close()
	})

	// Go-only: Pi's `load` tests `closed` before it looks at what the query compiled to (open-query.ts:194, then :195): a closed read
	// has nothing to load, and no failed run reaches its sink, though its query does not compile.
	t.Run("a closed read whose query does not compile loads nothing and adopts no failed run", func(t *testing.T) {
		w := newReadsWorld(t)
		o := w.opened(t, w.notes, w.tagged)
		if runs := o.runs.len(); runs != 1 {
			t.Fatalf("runs = %d, want the one failed run of the open", runs)
		}
		o.query.Close()
		if run := o.open.Load(); run != nil {
			t.Fatalf("a closed read loaded %#v", run)
		}
		o.open.Reload()
		if runs := o.runs.len(); runs != 1 {
			t.Fatalf("runs = %d after a load and a reload of the closed read, want the one", runs)
		}
	})

	t.Run("run no read over a held table, and one run when it is ready", func(t *testing.T) {
		w := newReadsWorld(t)
		w.bind(t)
		o := w.opened(t, w.notes, w.tagged)
		w.put(t, w.notes, Row{"id": int64(1), "text": "t"}, o.batch)
		before := o.runs.len()
		w.store.Reads.Hold(w.tags.Sql.Table)
		// Another owner commits between two chunks of the migration: the read keeps its rows.
		w.put(t, w.notes, Row{"id": int64(2), "text": "t"}, o.batch)
		w.put(t, w.tags, Row{"note": int64(2), "tag": "red"}, o.batch)
		if o.runs.len() != before {
			t.Fatalf("a read over a held table ran: %d runs, want %d", o.runs.len(), before)
		}
		w.store.Reads.Ready(w.tags.Sql.Table)
		o.settle()
		if o.runs.len() != before+1 {
			t.Fatalf("%d runs once ready, want %d", o.runs.len(), before+1)
		}
		qEqual(t, shown(o.runs.last()), any([]any{int64(2)}))
		o.query.Close()
	})

	t.Run("find the reads over tables with a host column of a kind, joined and exists tables included", func(t *testing.T) {
		w := newReadsWorld(t)
		session, err := contracts.NewHostKind("session", contracts.Prop("id", contracts.Str()))
		must(t, err)
		ownedSet, err := DefineColumns("owned.columns", ColumnSetOptions{
			Columns: []ColumnProp{Col("id", Int()), Col("session", Host(session))},
			Key:     []string{"id"},
		})
		must(t, err)
		ownedDefinition := qDefine(t, "owned", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Int()), Col("session", Host(session))}, Key: []string{"id"}})
		owned := tableIn(t, w.store, ownedDefinition)
		exposing := qDefine(t, "owned", TableOptions{Version: 1, Columns: slices.Clone(ownedSet.Schemas), Key: []string{"id"}, Exposes: ownedSet})
		must(t, w.store.Reads.Bind(ownedSet.Name, Bound{Table: owned.Sql.Table, Definition: exposing}, owned))
		direct := w.opened(t, owned, NewQuery(ownedDefinition))
		through := w.opened(t, w.notes, NewQuery(w.notes.Definition).Where(func(n, _ ColumnRefs) Condition {
			return Exists(ownedSet, Match{{"id", n["id"]}}, nil)
		}))
		plain := w.opened(t, w.notes, NewQuery(w.notes.Definition))
		if found := w.store.Reads.OnHostKind("session"); len(found) != 2 {
			t.Fatalf("OnHostKind(session) = %d reads, want 2", len(found))
		}
		if found := w.store.Reads.OnHostKind("server"); len(found) != 0 {
			t.Fatalf("OnHostKind(server) = %d reads, want none", len(found))
		}
		for _, each := range []*openedReads{direct, through, plain} {
			each.query.Close()
		}
	})

	t.Run("close the queries of an owner that retires: their reads leave the index, others stay", func(t *testing.T) {
		// Code review round 1 (Opus S1, Sol 3): a provider's attempt ended; no commit runs its queries again.
		w := newReadsWorld(t)
		retiring := w.opened(t, w.notes, NewQuery(w.notes.Definition))
		other := tableOwner()
		batch := state.NewBatch(func(state.BatchError) {}, telemetry.NoopTelemetryMetrics)
		spec := QuerySpec{Query: NewQuery(w.notes.Definition), Table: w.notes.Sql.Table, Owner: other}
		staying := NewOpenQuery(ReadSite{Store: w.store, Batch: batch}, spec, ReadRows, func() {}, background)
		staying.Read(ReadKind{Kind: "rows", Range: Range{Limit: 5}}, &runLog{})
		w.store.Reads.Retire(w.notes)
		qEqual(t, []any{retiring.query.Closed(), staying.Closed(), len(w.store.Reads.On(w.notes.Sql.Table))}, []any{true, false, 1})
		before := retiring.runs.len()
		w.put(t, w.notes, Row{"id": int64(1), "text": "t"}, retiring.batch)
		if retiring.runs.len() != before {
			t.Fatalf("a retired query adopted %d runs", retiring.runs.len()-before)
		}
		staying.Close()
	})

	t.Run("a read of a closed query is closed: never registered, never run", func(t *testing.T) {
		// Code review round 2 (Sol): a list whose provider retired asked its closed query for another page; the read of the
		// old plan stayed in the index for good.
		w := newReadsWorld(t)
		o := w.opened(t, w.notes, NewQuery(w.notes.Definition))
		w.store.Reads.Retire(w.notes)
		runs := &runLog{}
		late := o.query.Read(ReadKind{Kind: "rows", Range: Range{Limit: 5}}, runs)
		known := o.query.ReadKnown(Range{Limit: 5}, nil, runs)
		if late.Read() != nil || known.Read() != nil || w.store.Reads.On(w.notes.Sql.Table) != nil {
			t.Fatal("a read of a closed query is registered")
		}
		if run := late.Load(); run != nil {
			t.Fatalf("a closed read loaded %#v", run)
		}
		w.put(t, w.notes, Row{"id": int64(1), "text": "t"}, o.batch)
		if runs.len() != 0 {
			t.Fatalf("a closed read adopted %d runs", runs.len())
		}
	})

	t.Run("keep nothing of a closed read", func(t *testing.T) {
		w := newReadsWorld(t)
		o := w.opened(t, w.notes, NewQuery(w.notes.Definition))
		read := weak.Make(o.open.Read())
		o.query.Close()
		if len(o.query.reads) != 0 {
			t.Fatalf("a closed query keeps %d reads", len(o.query.reads))
		}
		nexttest.ExpectCollected(t, read)
	})

	t.Run("keep nothing of a closed query that watched a set's binding", func(t *testing.T) {
		w := newReadsWorld(t)
		w.bind(t)
		// Opened and closed in a function of its own: nothing of this test keeps the query.
		closed := func() weak.Pointer[OpenQuery] {
			o := w.opened(t, w.notes, w.tagged)
			o.query.Close()
			return weak.Make(o.query)
		}
		nexttest.ExpectCollected(t, closed())
		// The binding changes without a watcher left to tell.
		w.store.Reads.Unbind(w.set.Name, w.tags)
	})
}

// The cases below are not Pi's: Pi's reads.test.ts reaches `setsOf`, a re-applied refresh and a load over a held table only
// through the cases above; these state them.

func TestReadsSetsOf(t *testing.T) {
	w := newReadsWorld(t)
	other, err := DefineColumns("other.columns", ColumnSetOptions{Columns: []ColumnProp{Col("note", Int())}, Key: []string{"note"}})
	must(t, err)
	nested := NewQuery(w.notes.Definition).Where(func(n, _ ColumnRefs) Condition {
		return Not(Or(
			Exists(w.set, Match{{"note", n["id"]}}, func(tag ColumnRefs) Condition {
				return Exists(other, Match{{"note", tag["note"]}}, nil)
			}),
			Exists(w.set, Match{{"note", n["id"]}}, nil),
		))
	})
	if nested.Err() == nil {
		qEqual(t, SetsOf(nested), []string{"tags.columns", "other.columns"})
	} else {
		t.Fatalf("nested exists: %v", nested.Err())
	}
	qEqual(t, SetsOf(NewQuery(w.notes.Definition)), []string(nil))
	joined := NewQuery(w.tags.Definition).Join(other, func(tag ColumnRefs) Match { return Match{{"note", tag["note"]}} })
	must(t, joined.Err())
	qEqual(t, SetsOf(joined), []string{"other.columns"})
}

func TestReadsRefreshAppliedAgain(t *testing.T) {
	// A grouped commit that rolled back applies again alone: only the last application's runs count.
	w := newReadsWorld(t)
	o := w.opened(t, w.notes, NewQuery(w.notes.Definition))
	staged, refresh, _ := w.stage(t, w.notes, Row{"id": int64(1), "text": "t"}, o.batch)
	// A read without a result is never skipped: both applications run it.
	o.open.Forget()
	before := o.runs.len()
	meter := metered(t)
	_, err := Read(w.store, func(executor sqlite.SqliteExecutor) (struct{}, error) {
		for range 2 {
			// Each application is of another transaction: the first one's tentative run is not the second's latest.
			step := NewStep(&struct{ sqlite.SqliteExecutor }{executor}, 0, nil, nil)
			if _, err := staged[len(staged)-1].Apply(step); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	}, meter.Context)
	must(t, err)
	must(t, refresh.Adopt())
	if got := o.runs.len() - before; got != 1 {
		t.Fatalf("%d runs adopted after two applications, want 1", got)
	}
	o.query.Close()
}

func TestReadsLoadOverAHeldTable(t *testing.T) {
	w := newReadsWorld(t)
	w.bind(t)
	o := w.opened(t, w.notes, w.tagged)
	before := o.runs.len()
	w.store.Reads.Hold(w.tags.Sql.Table)
	if run := o.open.Load(); run != nil || o.runs.len() != before {
		t.Fatalf("a load over a held table ran: %#v", run)
	}
	w.store.Reads.Ready(w.tags.Sql.Table)
	o.settle()
	if o.runs.len() != before+1 {
		t.Fatalf("%d runs once ready, want %d", o.runs.len(), before+1)
	}
	o.query.Close()
}

func TestReadsRefreshChoosesEachReadOnce(t *testing.T) {
	// A read over two of the written tables runs once.
	w := newReadsWorld(t)
	w.bind(t)
	o := w.opened(t, w.notes, w.tagged)
	o.open.Forget()
	before := o.runs.len()
	_, err := Read(w.store, func(executor sqlite.SqliteExecutor) (struct{}, error) {
		refresh := NewReadRefresh(w.store.Reads, []string{w.tags.Sql.Table, w.notes.Sql.Table}, func() []Change { return nil }, background)
		_, err := refresh.Write.Apply(NewStep(executor, 0, nil, nil))
		must(t, err)
		must(t, refresh.Adopt())
		return struct{}{}, err
	}, background)
	must(t, err)
	if got := o.runs.len() - before; got != 1 {
		t.Fatalf("%d runs, want 1", got)
	}
	o.query.Close()
}

func TestReadsOfAClosedQueryOrRead(t *testing.T) {
	// A read of a closed query is closed: never registered, never run (Pi open-query.ts `read`).
	w := newReadsWorld(t)
	w.bind(t)
	o := w.opened(t, w.notes, w.tagged)
	o.query.Close()
	qEqual(t, len(w.store.Reads.On(w.notes.Sql.Table)), 0)
	late := o.query.Read(ReadKind{Kind: "rows", Range: Range{Limit: 5}}, o.runs)
	if len(o.query.reads) != 0 {
		t.Fatalf("a closed query keeps a read of it: %d", len(o.query.reads))
	}
	before := o.runs.len()
	if run := late.Load(); run != nil {
		t.Fatalf("a read of a closed query ran: %#v", run)
	}
	late.Register()
	qEqual(t, len(w.store.Reads.On(w.notes.Sql.Table)), 0)
	if o.runs.len() != before {
		t.Fatal("a read of a closed query was adopted")
	}
	// A closed read does not load.
	w2 := newReadsWorld(t)
	w2.bind(t)
	o2 := w2.opened(t, w2.notes, w2.tagged)
	read := o2.query.Read(ReadKind{Kind: "rows", Range: Range{Limit: 5}}, o2.runs)
	read.Close()
	count := o2.runs.len()
	if run := read.Load(); run != nil || o2.runs.len() != count {
		t.Fatal("a closed read loaded")
	}
	o2.query.Close()
}
