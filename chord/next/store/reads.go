package store

import (
	"context"
	"fmt"
	"slices"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/store/reads.ts
//
// The store's reads (slice 14 §3.2, §5): which reads read which table, whoever owns them, and which table each column set
// is bound to. A transaction that wrote tables stages `ReadRefresh`: inside its commit step, after its writes, it takes
// the reads over those tables from the index as it is then, runs the skip test, runs the others again and adopts their
// results in the commit's batch.
//
// Go forms: Pi's `Set` and `Map` keep insertion order, which decides the order reads run and watchers are told in: they
// are slices here (a handful of entries each). An owner is any comparable value (Pi's `object`, by identity: a pointer).
// Pi's `throw` is the returned error. Pi's one thread is a mutex: `Reads` is called from the commit step, from open
// queries and from a plugin's activation; a watcher is called with no lock held.

// Retirable is an open query as the index retires it: who offers it, and its close.
type Retirable interface {
	// Owner is the one that offers the query (`QuerySpec.owner`).
	Owner() any
	// Close closes the query: its reads leave the index.
	Close()
}

// binding is a binding as the store keeps it: the table, and the object that owns it (which alone moves or drops it).
type binding struct {
	Bound
	owner any
}

// watcher is one `changed` callback of a set, identified by its pointer (a func is not comparable).
type watcher struct{ changed func() }

// Reads is the index from a table's SQL name to the reads over it, the bindings of column sets, and the tables that are
// not ready (reads over them are held: not run, their rows kept). One per store.
type Reads struct {
	mu      sync.Mutex
	byTable map[string][]*RegisteredRead
	tables  []string
	sets    map[string]*binding
	setList []string
	// bound are the names each owner has bound: an activation asks for its own without walking every binding.
	bound map[any][]string
	// held are the tables being migrated or not created yet; reads over them wait for `Ready`.
	held map[string]bool
	// watchers are who is told when a set's binding or its table's readiness changes, by set name.
	watchers map[string][]*watcher
	// offered are the open queries by who offers them (`QuerySpec.owner`): closed when that one retires.
	offered map[any][]Retirable
	// lifetime is the context every task of the index runs under (with its parent's): `Close` cancels it. tasks counts the
	// tasks running, idle is closed when the last of them ended (nil while none runs), closed is that `Close` was called.
	lifetime context.Context
	stop     context.CancelFunc
	tasks    int
	idle     chan struct{}
	closed   bool
}

// NewReads is an empty index.
func NewReads() *Reads {
	lifetime, stop := context.WithCancel(chordctx.Background())
	return &Reads{
		byTable:  map[string][]*RegisteredRead{},
		sets:     map[string]*binding{},
		bound:    map[any][]string{},
		held:     map[string]bool{},
		watchers: map[string][]*watcher{},
		offered:  map[any][]Retirable{},
		lifetime: lifetime,
		stop:     stop,
	}
}

// closedChan is the barrier of an index with no task running.
var closedChan = func() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

// Go runs task as a task the index owns (Pi's unawaited `void open.load()`: nothing waits for its result, but it must not
// outlive its owner): under a context that ends with parent's and with `Close`, joined by `Close`; a task offered after
// `Close` does not run. The task's error is its own to report (a read's is its failed run).
func (reads *Reads) Go(parent context.Context, task func(ctx context.Context)) {
	reads.mu.Lock()
	if reads.closed {
		reads.mu.Unlock()
		return
	}
	reads.tasks++
	if reads.idle == nil {
		reads.idle = make(chan struct{})
	}
	reads.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	unwatch := context.AfterFunc(reads.lifetime, cancel)
	go func() {
		defer func() {
			unwatch()
			cancel()
			reads.ended()
		}()
		task(ctx)
	}()
}

// ended is one task of the index ended: the barrier closes with the last.
func (reads *Reads) ended() {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	reads.tasks--
	if reads.tasks == 0 {
		close(reads.idle)
		reads.idle = nil
	}
}

// idleChan is a barrier: closed once the tasks running now ended (and any that started meanwhile), already closed when none
// runs. What a test waits on where Pi's `await flushed()` lets the unawaited loads of a changed binding settle.
func (reads *Reads) idleChan() <-chan struct{} {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	if reads.idle == nil {
		return closedChan
	}
	return reads.idle
}

// Close ends the index's tasks: none starts from now on, the running ones are cancelled and joined. Closing again is the same
// close.
func (reads *Reads) Close() {
	reads.mu.Lock()
	reads.closed = true
	reads.mu.Unlock()
	reads.stop()
	<-reads.idleChan()
}

// isClosed is whether `Close` was called: nothing a task read is adopted after it.
func (reads *Reads) isClosed() bool {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return reads.closed
}

// Add enters read under every table it uses: every commit writing one of them tests it from now on.
func (reads *Reads) Add(read *RegisteredRead) {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	for _, use := range read.Plan.Uses {
		on, found := reads.byTable[use.Table]
		if !found {
			reads.tables = append(reads.tables, use.Table)
		}
		if !slices.Contains(on, read) {
			reads.byTable[use.Table] = append(on, read)
		}
	}
}

// Delete removes read: no commit runs it again.
func (reads *Reads) Delete(read *RegisteredRead) {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	for _, use := range read.Plan.Uses {
		on, found := reads.byTable[use.Table]
		if !found {
			continue
		}
		if on = slices.DeleteFunc(slices.Clone(on), func(each *RegisteredRead) bool { return each == read }); len(on) == 0 {
			delete(reads.byTable, use.Table)
			reads.tables = without(reads.tables, use.Table)
		} else {
			reads.byTable[use.Table] = on
		}
	}
}

// Opened says query is open: it closes when its owner retires.
func (reads *Reads) Opened(query Retirable) {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	owner := query.Owner()
	if !slices.Contains(reads.offered[owner], query) {
		reads.offered[owner] = append(reads.offered[owner], query)
	}
}

// Closed says query was closed.
func (reads *Reads) Closed(query Retirable) {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	owner := query.Owner()
	queries, found := reads.offered[owner]
	if !found {
		return
	}
	if queries = slices.DeleteFunc(slices.Clone(queries), func(each Retirable) bool { return each == query }); len(queries) == 0 {
		delete(reads.offered, owner)
	} else {
		reads.offered[owner] = queries
	}
}

// Retire says owner offers its queries no more (a provider's attempt ended; slice 14 §3.2): their reads leave the index, so
// no commit runs a statement compiled for its tables as they were. Their readers keep their last values and read the
// successor's queries when it is live.
func (reads *Reads) Retire(owner any) {
	reads.mu.Lock()
	queries := slices.Clone(reads.offered[owner])
	reads.mu.Unlock()
	for _, query := range queries {
		query.Close()
	}
}

// On is the reads over table, if any (a snapshot, in the order they entered; nil when there is none).
func (reads *Reads) On(table string) []*RegisteredRead {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return slices.Clone(reads.byTable[table])
}

// OnHostKind is the reads over a table with a `host(...)` column of host kind kind: what a host data change runs again.
func (reads *Reads) OnHostKind(kind string) []*RegisteredRead {
	reads.mu.Lock()
	var found []*RegisteredRead
	for _, table := range reads.tables {
		for _, read := range reads.byTable[table] {
			if !slices.Contains(found, read) && reads.usesHostKind(read, kind) {
				found = append(found, read)
			}
		}
	}
	reads.mu.Unlock()
	return found
}

// Get is the binding of set, if any.
func (reads *Reads) Get(set string) (Bound, bool) {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return reads.get(set)
}

func (reads *Reads) get(set string) (Bound, bool) {
	current, found := reads.sets[set]
	if !found {
		return Bound{}, false
	}
	return current.Bound, true
}

// Bind binds set to bound for owner (its table declares `exposes`). A set bound by another owner to another table is
// refused (declaration policy: two tables exposing one name), naming both.
func (reads *Reads) Bind(set string, bound Bound, owner any) error {
	reads.mu.Lock()
	current := reads.sets[set]
	if current != nil && current.owner != owner && current.Table != bound.Table {
		reads.mu.Unlock()
		return fmt.Errorf("column set \"%s\" is exposed by tables \"%s\" and \"%s\"", set, current.Table, bound.Table)
	}
	if current != nil && current.Table == bound.Table && current.Definition == bound.Definition && current.owner == owner {
		reads.mu.Unlock()
		return nil
	}
	if current != nil {
		reads.bound[current.owner] = without(reads.bound[current.owner], set)
		if len(reads.bound[current.owner]) == 0 {
			delete(reads.bound, current.owner)
		}
	} else {
		reads.setList = append(reads.setList, set)
	}
	reads.sets[set] = &binding{Bound: bound, owner: owner}
	if !slices.Contains(reads.bound[owner], set) {
		reads.bound[owner] = append(reads.bound[owner], set)
	}
	reads.mu.Unlock()
	reads.changed(set)
	return nil
}

// Unbind drops owner's binding of set (its new code exposes it no more, or it ended).
func (reads *Reads) Unbind(set string, owner any) {
	reads.mu.Lock()
	current := reads.sets[set]
	if current == nil || current.owner != owner {
		reads.mu.Unlock()
		return
	}
	delete(reads.sets, set)
	reads.setList = without(reads.setList, set)
	if sets := without(reads.bound[owner], set); len(sets) == 0 {
		delete(reads.bound, owner)
	} else {
		reads.bound[owner] = sets
	}
	reads.mu.Unlock()
	reads.changed(set)
}

// BoundBy is the sets owner has bound.
func (reads *Reads) BoundBy(owner any) []string {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return slices.Clone(reads.bound[owner])
}

// Hold marks table not ready (its owner's successor creates or migrates it): reads over it are held.
func (reads *Reads) Hold(table string) {
	reads.mu.Lock()
	reads.held[table] = true
	reads.mu.Unlock()
}

// Ready says table is ready again: the reads over the sets bound to it compile and run once.
func (reads *Reads) Ready(table string) {
	reads.mu.Lock()
	if !reads.held[table] {
		reads.mu.Unlock()
		return
	}
	delete(reads.held, table)
	var changed []string
	for _, set := range reads.setList {
		if reads.sets[set].Table == table {
			changed = append(changed, set)
		}
	}
	reads.mu.Unlock()
	for _, set := range changed {
		reads.changed(set)
	}
}

// IsHeld is whether a read over table must wait.
func (reads *Reads) IsHeld(table string) bool {
	reads.mu.Lock()
	defer reads.mu.Unlock()
	return reads.held[table]
}

// Watch calls changed whenever set's binding or its table's readiness changes, until the returned release.
func (reads *Reads) Watch(set string, changed func()) (release func()) {
	each := &watcher{changed}
	reads.mu.Lock()
	reads.watchers[set] = append(reads.watchers[set], each)
	reads.mu.Unlock()
	return func() {
		reads.mu.Lock()
		defer reads.mu.Unlock()
		current, found := reads.watchers[set]
		if !found {
			return
		}
		if current = slices.DeleteFunc(slices.Clone(current), func(other *watcher) bool { return other == each }); len(current) == 0 {
			delete(reads.watchers, set)
		} else {
			reads.watchers[set] = current
		}
	}
}

func (reads *Reads) changed(set string) {
	reads.mu.Lock()
	watchers := slices.Clone(reads.watchers[set])
	reads.mu.Unlock()
	for _, each := range watchers {
		each.changed()
	}
}

// without is names without name, as a new slice.
func without(names []string, name string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(each string) bool { return each == name })
}

// usesHostKind is whether read reads a table with a host column of kind kind: its own, or, through the bindings of the
// sets its query names, a joined or `exists` one. The caller holds the lock.
func (reads *Reads) usesHostKind(read *RegisteredRead, kind string) bool {
	query := read.Plan.Query
	if hostKindOf(query.Table) == kind {
		return true
	}
	for index := range read.Plan.Uses {
		use := &read.Plan.Uses[index]
		if use.Role == UseOwn {
			continue
		}
		for _, set := range SetsOf(query) {
			if bound, found := reads.get(set); found && bound.Table == use.Table && hostKindOf(bound.Definition) == kind {
				return true
			}
		}
	}
	return false
}

// hostKindOf is the host kind of table's `host(...)` column, "" when it has none.
func hostKindOf(table *TableDefinition) string {
	if table.HostColumn == nil {
		return ""
	}
	return table.HostColumn.Host.Kind.Kind
}

// SetsOf is the names of the column sets query reads besides its own table: the joined one and every `exists`'.
func SetsOf(query *Query) []string {
	var sets []string
	add := func(set string) {
		if !slices.Contains(sets, set) {
			sets = append(sets, set)
		}
	}
	if query.Joined != nil {
		add(query.Joined.Set.Name)
	}
	var collect func(condition WhereNode)
	collect = func(condition WhereNode) {
		for _, each := range ExistsIn(condition) {
			add(each.Set)
			if each.Inner != nil {
				collect(*each.Inner)
			}
		}
	}
	if query.Condition != nil {
		collect(*query.Condition)
	}
	return sets
}

// ReadRefresh is the refresh of a transaction's reads: `Write` runs in the commit step after the commit's own writes (the
// last write of the last phase) and chooses the reads there, from the index at that moment; `Adopt` hands each run to its
// read, inside the commit's batch. A refused step adopts nothing. A transaction stages it as its own change.
type ReadRefresh struct {
	reads   *Reads
	tables  []string
	changes func() []Change
	ctx     context.Context
	mu      sync.Mutex
	runs    []refreshed
	// Write is the staged write that reads; it writes nothing and records nothing in the outbox.
	Write StagedWrite
}

type refreshed struct {
	read *RegisteredRead
	run  ReadRun
}

// NewReadRefresh is the refresh of the reads over tables (the SQL names of the tables the transaction wrote); changes
// gives its writes, and is asked only when a read over one of them exists in the step.
func NewReadRefresh(reads *Reads, tables []string, changes func() []Change, ctx context.Context) *ReadRefresh {
	refresh := &ReadRefresh{reads: reads, tables: tables, changes: changes, ctx: ctx}
	refresh.Write = readRefreshWrite{refresh}
	return refresh
}

// readRefreshWrite is the staged write of a ReadRefresh: the last of the last phase.
type readRefreshWrite struct{ refresh *ReadRefresh }

func (readRefreshWrite) StageOrder() WriteOrder { return OrderWrite }

func (write readRefreshWrite) Apply(step *Step) ([]StoreWrite, error) {
	return nil, write.refresh.run(step)
}

func (refresh *ReadRefresh) run(step *Step) error {
	refresh.mu.Lock()
	defer refresh.mu.Unlock()
	// A grouped commit that rolled back applies again alone (store.go): only the last application's runs count.
	refresh.runs = refresh.runs[:0]
	var chosen []*RegisteredRead
	for _, table := range refresh.tables {
		for _, read := range refresh.reads.On(table) {
			if !slices.Contains(chosen, read) {
				chosen = append(chosen, read)
			}
		}
	}
	if len(chosen) == 0 {
		return nil
	}
	changes := refresh.changes()
	metrics := chordctx.Telemetry(refresh.ctx).Metrics()
	for _, read := range chosen {
		// Held: a table it reads is being created or migrated; it runs once when the table is ready.
		if slices.ContainsFunc(read.Plan.Uses, func(use Use) bool { return refresh.reads.IsHeld(use.Table) }) {
			continue
		}
		skipped, err := Skips(read, changes, step, refresh.ctx)
		if err != nil {
			return err
		}
		if skipped {
			if metrics != nil {
				metrics.Counter("chord.query.skips", telemetry.InstrumentOptions{}).Add(1, nil)
			}
			continue
		}
		run := read.Run(step, refresh.ctx)
		read.Ran(step.Executor, run)
		refresh.runs = append(refresh.runs, refreshed{read, run})
	}
	return nil
}

// Adopt hands each run to its read, inside the commit's batch.
func (refresh *ReadRefresh) Adopt() error {
	refresh.mu.Lock()
	runs := slices.Clone(refresh.runs)
	refresh.mu.Unlock()
	for _, each := range runs {
		each.read.Adopt(each.run)
	}
	return nil
}

// Committed is nothing to do after the commit.
func (*ReadRefresh) Committed() {}

// Discarded is nothing to undo.
func (*ReadRefresh) Discarded() {}
