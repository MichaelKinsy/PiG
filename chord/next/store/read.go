package store

import (
	"context"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/store/read.ts: reads (slice 14 §3.2, §3.3), one statement the store keeps current. A read is
// registered with the store under every table it uses; a commit that writes one of them runs the skip test and,
// unless it skips, runs the read again in its step. What a read does with its rows is its sink's business (a live
// query's rows, a list's page, a count).
//
// Go forms: Pi's class `Read` is `RegisteredRead` (the store's guarded read function already has the name `Read`);
// Pi's `Set<unknown>` of identities is a `map[any]bool`, its `Map<Use, …>` a map by `*Use` (a pointer into the
// plan's Uses); a ReadRun is a struct with a Kind; Pi's `unknown` failure is an `error`.

// ReadResult is the rows of one run, at most the range's Limit; More says a further row followed inside the range. The
// keys are of every row fetched, the one past Limit included (slice 14 §3.3).
type ReadResult struct {
	Rows []StoredRead
	More bool
	// Keys are the own keys of the fetched rows, each as `IdentityOf` its key values.
	Keys map[any]bool
	// Paired is, per use of another table, the fetched rows' values of the own columns it pairs by, each as `IdentityOf` them.
	Paired map[*Use]map[any]bool
	// Past is the position of the row past Limit, when there is one (nil otherwise): the read's range ends there.
	Past Position
	// IDs is the key identity of each fetched row, in order, for multi-column keys; nil for single-column keys.
	IDs []any
}

// ReadRun is one run of a read: its result (Kind "rows"), the number of a count (Kind "count"), or why it could not
// run (Kind "failed").
type ReadRun struct {
	Kind   string
	Result *ReadResult
	Count  int64
	Err    error
}

// ReadExecutor is what a read runs its statements on: a commit step, or the backend under the store's guarded read.
type ReadExecutor interface {
	All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error)
	Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error)
}

// ReadSink is who takes a read's runs: called inside the batch of the commit that ran it.
type ReadSink interface{ Adopt(run ReadRun) }

// KeptKeysWatcher is told when the keys a read keeps change: the commit index it is filed in (slice 15 §3).
type KeptKeysWatcher interface{ Kept(read *RegisteredRead) }

// ReadKind is what a read is: the rows of a range (Kind "rows"), or the number of rows of its condition ("count").
type ReadKind struct {
	Kind  string
	Range Range
}

// RegisteredRead is a read registered with the store. It keeps what the skip test needs of its last adopted run, and
// of a run a commit group has not adopted yet (a tentative one: a later step of the group tests against it).
type RegisteredRead struct {
	Plan *ReadPlan
	What ReadKind
	Sink ReadSink
	mu   sync.Mutex
	// adopted is the last adopted run; nil while loading or failed: such a read is never skipped.
	adopted *ReadRun
	// tentative is a step's run not adopted yet, with the backend transaction it ran in (a rolled-back group's is stale).
	tentative *struct {
		transaction sqlite.SqliteExecutor
		run         *ReadRun
	}
	// watcher is the commit index that files this read by its kept keys, if one does.
	watcher KeptKeysWatcher
}

// NewRegisteredRead is a read of plan, of what, whose runs go to sink.
func NewRegisteredRead(plan *ReadPlan, what ReadKind, sink ReadSink) *RegisteredRead {
	return &RegisteredRead{Plan: plan, What: what, Sink: sink}
}

// Know takes rows as its adopted result without running: every row of its range, known to its owner already (a list's
// page cut from another one's rows). The sink is not told.
func (read *RegisteredRead) Know(rows []StoredRead) {
	// A count has no rows to know.
	rng := read.What.Range
	rng.Limit = max(rng.Limit, len(rows))
	run := &ReadRun{Kind: "rows", Result: read.result(rows, rng)}
	read.mu.Lock()
	read.adopted = run
	watcher := read.watcher
	read.mu.Unlock()
	if watcher != nil {
		watcher.Kept(read)
	}
}

// Forget drops its adopted result: without one it is never skipped, so the next commit over its tables runs it.
func (read *RegisteredRead) Forget() {
	read.mu.Lock()
	read.adopted = nil
	watcher := read.watcher
	read.mu.Unlock()
	if watcher != nil {
		watcher.Kept(read)
	}
}

// WatchKept sets the watcher told whenever kept keys change.
func (read *RegisteredRead) WatchKept(watcher KeptKeysWatcher) {
	read.mu.Lock()
	read.watcher = watcher
	read.mu.Unlock()
}

// KeptSets returns the key sets a skip test of this read may compare against: its adopted run's, and a group's run not
// adopted yet; nil while some such run has no rows (loading, failed, a count).
func (read *RegisteredRead) KeptSets() []map[any]bool {
	read.mu.Lock()
	defer read.mu.Unlock()
	if read.adopted == nil || read.adopted.Kind != "rows" {
		return nil
	}
	if read.tentative != nil && read.tentative.run != nil {
		if read.tentative.run.Kind != "rows" {
			return nil
		}
		return []map[any]bool{read.adopted.Result.Keys, read.tentative.run.Result.Keys}
	}
	return []map[any]bool{read.adopted.Result.Keys}
}

// Latest is the run the skip test of a step in transaction compares against: the group's latest, else the adopted one.
func (read *RegisteredRead) Latest(transaction sqlite.SqliteExecutor) *ReadRun {
	read.mu.Lock()
	defer read.mu.Unlock()
	if read.tentative != nil && read.tentative.transaction == transaction {
		return read.tentative.run
	}
	return read.adopted
}

// Run runs the read inside a commit step, after its writes: the run, or the failure; it never fails.
func (read *RegisteredRead) Run(step ReadExecutor, ctx context.Context) ReadRun {
	run, err := read.Fetch(step, ctx)
	if err != nil {
		return ReadRun{Kind: "failed", Err: err}
	}
	return run
}

// Fetch is one run on reader, counted where it happens; a backend error is returned (the store's guarded read takes it).
func (read *RegisteredRead) Fetch(reader ReadExecutor, ctx context.Context) (ReadRun, error) {
	metrics := chordctx.Telemetry(ctx).Metrics()
	if metrics != nil {
		metrics.Counter("chord.query.runs", telemetry.InstrumentOptions{}).Add(1, nil)
	}
	if read.What.Kind == "count" {
		statement := read.Plan.Count()
		row, err := reader.Get(statement.SQL, statement.Params...)
		if err != nil {
			return ReadRun{}, err
		}
		count := int64(0)
		if row != nil {
			count, err = sqlite.RowInt(row, "count")
			if err != nil {
				return ReadRun{}, err
			}
		}
		return ReadRun{Kind: "count", Count: count}, nil
	}
	// One row more than asked for: whether a further row exists, and where the range ends.
	rng := read.What.Range
	rng.Limit++
	statement := read.Plan.Select(rng)
	stored, err := reader.All(statement.SQL, statement.Params...)
	if err != nil {
		return ReadRun{}, err
	}
	if metrics != nil {
		metrics.Counter("chord.query.rows", telemetry.InstrumentOptions{}).Add(float64(len(stored)), nil)
	}
	return ReadRun{Kind: "rows", Result: read.result(stored, read.What.Range)}, nil
}

// Ran keeps a step's run for the later steps of its group until it is adopted.
func (read *RegisteredRead) Ran(transaction sqlite.SqliteExecutor, run ReadRun) {
	read.mu.Lock()
	read.tentative = &struct {
		transaction sqlite.SqliteExecutor
		run         *ReadRun
	}{transaction, &run}
	watcher := read.watcher
	read.mu.Unlock()
	if watcher != nil {
		watcher.Kept(read)
	}
}

// Adopt takes run as its state and hands it to the sink.
func (read *RegisteredRead) Adopt(run ReadRun) {
	read.mu.Lock()
	if read.tentative != nil && sameRun(*read.tentative.run, run) {
		read.tentative = nil
	}
	if run.Kind == "failed" {
		read.adopted = nil
	} else {
		read.adopted = &run
	}
	watcher := read.watcher
	read.mu.Unlock()
	if watcher != nil {
		watcher.Kept(read)
	}
	read.Sink.Adopt(run)
}

// sameRun is whether two runs are one run (Pi compares the objects): the same result, or the same failure.
func sameRun(a, b ReadRun) bool {
	return a.Kind == b.Kind && a.Result == b.Result && a.Count == b.Count && a.Err == b.Err //nolint:errorlint // Pi compares a run's failure by identity (store/read.ts:115 `this.#tentative?.run === run`): a failure that wraps the other is another run
}

// Probed is the range its probe asks about: the read's own, ending at the row past its limit when there is one.
func (read *RegisteredRead) Probed(result *ReadResult) Range {
	// A count is never probed (slice 14 §3.3).
	rng := read.What.Range
	rng.Limit = 1
	if result.Past != nil {
		rng.Through = result.Past
	}
	return rng
}

func (read *RegisteredRead) result(stored []StoredRead, rng Range) *ReadResult {
	plan := read.Plan
	keyColumns := plan.Query.Table.Key
	keys := map[any]bool{}
	paired := map[*Use]map[any]bool{}
	for index := range plan.Uses {
		if use := &plan.Uses[index]; use.Role != UseOwn && use.Pairs != nil {
			paired[use] = map[any]bool{}
		}
	}
	var ids []any
	if len(keyColumns) > 1 {
		ids = make([]any, len(stored))
	}
	for at, row := range stored {
		id := Identity(keyColumns, row)
		keys[id] = true
		if ids != nil {
			ids[at] = id
		}
		for use, values := range paired {
			values[Identity(use.Pairs, row)] = true
		}
	}
	more := len(stored) > rng.Limit
	result := &ReadResult{Rows: stored, More: more, Keys: keys, Paired: paired, IDs: ids}
	if more {
		result.Rows = stored[:rng.Limit]
		result.Past = make(Position, len(plan.Order.Columns))
		for index, name := range plan.Order.Columns {
			result.Past[index] = stored[rng.Limit][name]
		}
	}
	return result
}

// Identity is the identity of row in columns, for a set or a map of rows of one read: the value itself for one column
// (most keys: no text is built per row), `IdentityOf` the values for several.
func Identity(columns []string, row sqlite.SqliteRow) any {
	if len(columns) == 1 {
		return row[columns[0]]
	}
	values := make([]sqlite.SqliteValue, len(columns))
	for index, name := range columns {
		values[index] = row[name]
	}
	return IdentityOf(values)
}

// IdentityOf is the identity of values, as `Identity` gives it for a row holding them.
func IdentityOf(values []sqlite.SqliteValue) any {
	if len(values) == 1 {
		return values[0]
	}
	text, _ := jsstring.MarshalJSON(values)
	return string(text)
}
