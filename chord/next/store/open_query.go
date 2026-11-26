package store

import (
	"context"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/store/open-query.ts
//
// Open queries (slice 14 §3.2, §5): a query kept compiled against the store's bindings for as long as someone reads it,
// and the reads of it, one per range (a live query has one, a list one per page). The query compiles once into a plan;
// when a column set it names is bound, moved, dropped or its table becomes ready after a migration, it compiles again, its
// reads are registered for the new plan and its owner is told, which reads again (one read alone; a list every page as
// one new tiling: rows and order may be others everywhere). A query that cannot be compiled (a set nobody serves, a column
// that is not what it was written for) is a failed run of each read, not a throw.
//
// Go forms: Pi's `Set<OpenRead>` is an insertion-ordered slice; `compiled` is the plan, or the error that stopped it
// (Pi's `{ kind: "compiled", plan } | { kind: "failed", error }`); `load` blocks until its run and returns nil where Pi resolves `undefined`; Pi's one thread
// is a mutex over the query's and the read's own fields (never held across a call out).

// QuerySpec is what an open query reads: a query of the table named Table, and the columns its mapping gets.
type QuerySpec struct {
	Query *Query
	// Table is the own table's SQL name.
	Table      string
	Projection Projection
	// Owner is who offers the query (the attempt's table handle): when it retires, the query's reads leave the index.
	Owner any
}

// ReadSite is where an open query lives: the store whose index its reads are in, and the batch adoptions outside a commit
// run in.
type ReadSite struct {
	Store *ChordStore
	Batch *state.Batch
}

// CompiledKind tags a Compiled open-query result.
type CompiledKind string

const (
	// CompiledPlan means the query compiled and Plan is non-nil.
	CompiledPlan CompiledKind = "compiled"
	// CompiledFailed means the query failed to compile and Err is the cause.
	CompiledFailed CompiledKind = "failed"
)

// Compiled is a query's plan while it compiles, else why it does not.
type Compiled struct {
	Kind CompiledKind
	Plan *ReadPlan
	Err  error
}

// OpenQuery is a query compiled for the bindings as they are, with its open reads.
type OpenQuery struct {
	Spec QuerySpec
	Site ReadSite
	// Ctx is the context its reads run and recompile under.
	Ctx     context.Context
	shape   ReadShape
	rebound func()

	mu       sync.Mutex
	reads    []*OpenRead
	releases []func()
	closed   bool
	compiled Compiled
	// compiling serializes the compiles: the one that ends last has compiled the latest bindings, so its plan is the one that
	// stays. `NewOpenQuery` and `recompile` (called by a binding change on the goroutine that changed it) compile under it.
	compiling sync.Mutex
}

// NewOpenQuery is the open query of spec in site; shape is what it reads. rebound is called whenever a binding changed and
// the query was compiled again: its reads are registered for the new plan and none has a result (a commit over their
// tables runs them); its owner reads again.
func NewOpenQuery(site ReadSite, spec QuerySpec, shape ReadShape, rebound func(), ctx context.Context) *OpenQuery {
	query := &OpenQuery{Spec: spec, Site: site, Ctx: ctx, shape: shape, rebound: rebound}
	reads := site.Store.Reads
	// The sets are watched first, so no change is missed; a change that arrives meanwhile compiles after this compile.
	query.compiling.Lock()
	defer query.compiling.Unlock()
	for _, set := range SetsOf(spec.Query) {
		release := reads.Watch(set, query.recompile)
		query.releases = append(query.releases, release)
	}
	reads.Opened(query)
	query.compileInto()
	return query
}

// compileInto compiles the query for the bindings as they are and keeps that as its plan; the caller holds `compiling`.
func (query *OpenQuery) compileInto() {
	compiled := query.compile()
	query.mu.Lock()
	query.compiled = compiled
	query.mu.Unlock()
}

// Owner is who offers the query.
func (query *OpenQuery) Owner() any { return query.Spec.Owner }

// Compiled is the plan while the query compiles, else why it does not.
func (query *OpenQuery) Compiled() Compiled {
	query.mu.Lock()
	defer query.mu.Unlock()
	return query.compiled
}

// Closed is whether it was closed (by its reader, or because its owner retired).
func (query *OpenQuery) Closed() bool {
	query.mu.Lock()
	defer query.mu.Unlock()
	return query.closed
}

// Read is a read of what, registered now (a commit in between runs it); `Load` runs it the first time. A read of a closed
// query is closed: it is never registered and never runs (a list whose provider retired cuts and loads pages of runs still
// in flight; no statement of the old plan may enter the index again).
func (query *OpenQuery) Read(what ReadKind, sink ReadSink) *OpenRead {
	read := newOpenRead(query, what, sink)
	query.mu.Lock()
	if !query.closed {
		query.reads = append(query.reads, read)
	}
	query.mu.Unlock()
	return read
}

// ReadKnown is a read of rng whose rows are known already: every row of the range, as another read of this query returned
// them (a list's page cut from, or merged of, other pages). Nothing is read and its sink is not told.
func (query *OpenQuery) ReadKnown(rng Range, rows []StoredRead, sink ReadSink) *OpenRead {
	read := query.Read(ReadKind{Kind: "rows", Range: rng}, sink)
	read.Know(rows)
	return read
}

// Forget says read was closed: the query keeps nothing of it.
func (query *OpenQuery) Forget(read *OpenRead) {
	query.mu.Lock()
	query.reads = slices.DeleteFunc(slices.Clone(query.reads), func(each *OpenRead) bool { return each == read })
	query.mu.Unlock()
}

// Close takes its reads out of the index and stops it following bindings: no commit runs them again.
func (query *OpenQuery) Close() {
	query.mu.Lock()
	if query.closed {
		query.mu.Unlock()
		return
	}
	releases, reads := query.releases, slices.Clone(query.reads)
	query.closed, query.releases = true, nil
	query.mu.Unlock()
	for _, release := range releases {
		release()
	}
	query.Site.Store.Reads.Closed(query)
	for _, read := range reads {
		read.Close()
	}
}

func (query *OpenQuery) compile() Compiled {
	spec := query.Spec
	plan, err := NewReadPlan(spec.Query, spec.Table, spec.Projection, query.shape, query.Site.Store.Reads)
	if err != nil {
		return Compiled{Kind: CompiledFailed, Err: err}
	}
	return Compiled{Kind: CompiledPlan, Plan: plan}
}

// recompile is a binding changed: the query is compiled for the tables as they are now, and its owner reads again.
func (query *OpenQuery) recompile() {
	query.mu.Lock()
	if query.closed {
		query.mu.Unlock()
		return
	}
	query.mu.Unlock()
	query.compiling.Lock()
	query.compileInto()
	query.compiling.Unlock()
	query.mu.Lock()
	reads := slices.Clone(query.reads)
	query.mu.Unlock()
	for _, read := range reads {
		read.Register()
	}
	query.rebound()
}

// OpenRead is one read of an open query: registered with the store while the query compiles.
type OpenRead struct {
	What  ReadKind
	query *OpenQuery
	sink  ReadSink

	mu     sync.Mutex
	read   *RegisteredRead
	closed bool
	// generation counts its adoptions and registrations: a first run that either overtook is dropped.
	generation int
	// registration counts its registrations: a run belongs to the registration that computed it. A commit's step may have
	// run the read before a binding changed; that run is of a plan the query no longer has and is dropped when the commit
	// adopts it. Closing registers nothing, so a run a commit made before the close still reaches the sink.
	registration int
}

func newOpenRead(query *OpenQuery, what ReadKind, sink ReadSink) *OpenRead {
	read := &OpenRead{What: what, query: query, sink: sink, closed: query.Closed()}
	read.Register()
	return read
}

// Read is the registered read, while the query compiles and this read is open; nil otherwise.
func (read *OpenRead) Read() *RegisteredRead {
	read.mu.Lock()
	defer read.mu.Unlock()
	return read.read
}

// adopting is the sink of one registration: a run of another registration is dropped.
type adopting struct {
	read         *OpenRead
	registration int
}

func (sink adopting) Adopt(run ReadRun) {
	read := sink.read
	read.mu.Lock()
	if sink.registration != read.registration {
		read.mu.Unlock()
		return
	}
	read.generation++
	read.mu.Unlock()
	read.sink.Adopt(run)
}

// Register enters a read of the query's current plan into the index (the previous one leaves it). Entered before its first
// run: a commit in between runs it (a read without a result is never skipped).
func (read *OpenRead) Register() {
	reads := read.query.Site.Store.Reads
	compiled := read.query.Compiled()
	read.mu.Lock()
	previous := read.read
	read.read = nil
	read.generation++
	if read.closed {
		read.mu.Unlock()
		if previous != nil {
			reads.Delete(previous)
		}
		return
	}
	read.registration++
	registration := read.registration
	if compiled.Kind == CompiledFailed {
		read.mu.Unlock()
		if previous != nil {
			reads.Delete(previous)
		}
		return
	}
	registered := NewRegisteredRead(compiled.Plan, read.What, adopting{read, registration})
	read.read = registered
	read.mu.Unlock()
	if previous != nil {
		reads.Delete(previous)
	}
	reads.Add(registered)
}

// Know takes rows as its adopted result without running: every row of its range, known to its owner (a page whose rows a
// run of another range of the query just changed). Its sink is not told.
func (read *OpenRead) Know(rows []StoredRead) {
	if registered := read.Read(); registered != nil {
		registered.Know(rows)
	}
}

// Forget makes its result unknown from now on: the next commit over its tables runs it, whatever it writes.
func (read *OpenRead) Forget() {
	if registered := read.Read(); registered != nil {
		registered.Forget()
	}
}

// Load runs it once, outside the commit queue, and adopts the run in a batch; returns that run, nil when a commit's run
// overtook it, a table it reads is held (it runs when the table is ready), it was closed, or the index closed (nothing is
// adopted after `Reads.Close`). Nothing fails: a query that does not compile and a run that fails are failed runs.
func (read *OpenRead) Load() *ReadRun { return read.load(read.query.Ctx) }

// load is Load under ctx: the context of the task a rebinding's reload runs on, or the query's.
func (read *OpenRead) load(ctx context.Context) *ReadRun {
	if run, settled := read.failCompile(); settled {
		return run
	}
	site := read.query.Site
	read.mu.Lock()
	registered := read.read
	generation := read.generation
	read.mu.Unlock()
	if registered == nil {
		return nil
	}
	if slices.ContainsFunc(registered.Plan.Uses, func(use Use) bool { return site.Store.Reads.IsHeld(use.Table) }) {
		return nil
	}
	run, err := Read(site.Store, func(executor sqlite.SqliteExecutor) (ReadRun, error) { return registered.Fetch(executor, ctx) }, ctx)
	if err != nil {
		run = ReadRun{Kind: "failed", Err: err}
	}
	// Pi's `if (this.#generation !== generation) return undefined; site.batch.within(...)` (open-query.ts:206-207) is one turn.
	// Here the adoption waits for a section of the host, in which a commit's adoption of this read may have run first: the
	// check is made in the section, as one with the adoption (CheckAct), or the older run of this load would land after the
	// commit's.
	adopted := site.Batch.CheckAct(func() func() {
		if site.Store.Reads.isClosed() {
			return nil
		}
		read.mu.Lock()
		overtaken := read.generation != generation
		read.mu.Unlock()
		if overtaken {
			return nil
		}
		return func() { _ = site.Batch.Within(func() error { registered.Adopt(run); return nil }, ctx) }
	})
	if !adopted {
		return nil
	}
	return &run
}

// failCompile is what Pi's `load` does before its first await (open-query.ts:194-198): a closed read has nothing to load
// (settled, no run); a query that does not compile hands its sink a failed run (settled, that run). Otherwise the read has to
// run: not settled. Pi decides and adopts in one turn, so the decision to adopt is made in the section that adopts (CheckAct): a
// read closed, or a query that compiles, by the time the section is entered adopts nothing. Closed never reverts, and a plan that
// compiles needs no section (the load's own check, made in its adopting section, covers what changes after), so those two
// are answered without one. The adoption has no error to return; it runs in a section of the host (a first run is read
// outside them).
func (read *OpenRead) failCompile() (run *ReadRun, settled bool) {
	query := read.query
	if read.isClosed() {
		return nil, true
	}
	if query.Compiled().Kind != CompiledFailed {
		return nil, false
	}
	settled = true
	query.Site.Batch.CheckAct(func() func() {
		if read.isClosed() {
			return nil
		}
		compiled := query.Compiled()
		if compiled.Kind != CompiledFailed {
			settled = false
			return nil
		}
		failed := ReadRun{Kind: "failed", Err: compiled.Err}
		return func() {
			run = &failed
			_ = query.Site.Batch.Within(func() error { read.sink.Adopt(failed); return nil }, query.Ctx)
		}
	})
	return run, settled
}

func (read *OpenRead) isClosed() bool {
	read.mu.Lock()
	defer read.mu.Unlock()
	return read.closed
}

// Reload is Pi's `void open.load()` of a binding that changed: a read of a query that no longer compiles fails at once, as
// `load` does before its first await (its owner may end before any task runs); a read that runs, runs on a task the index
// owns (nothing waits for it; the index's close cancels and joins it, `Reads.Go`), and its error is its failed run.
func (read *OpenRead) Reload() {
	if _, settled := read.failCompile(); !settled {
		read.query.Site.Store.Reads.Go(read.query.Ctx, func(ctx context.Context) { read.load(ctx) })
	}
}

// Close takes it out of the index: no commit runs it again. A run a commit made already still reaches its sink.
func (read *OpenRead) Close() {
	read.mu.Lock()
	if read.closed {
		read.mu.Unlock()
		return
	}
	read.closed = true
	read.generation++
	registered := read.read
	read.read = nil
	read.mu.Unlock()
	if registered != nil {
		read.query.Site.Store.Reads.Delete(registered)
	}
	read.query.Forget(read)
}
