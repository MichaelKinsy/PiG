package store

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/store/live-query.ts
//
// Live queries (decision "Live queries over an object's own tables"): `self.query(table, q, limit)`, a JSON watchable of a
// query's rows over a table of the object itself. Run once before the object goes live, then again inside the commit step of
// every commit of the object that wrote the table (after its writes, so it sees them) and published in that commit's batch
// as the diff from the previous rows (W1). Only the object writes its tables, so its commits are the exact invalidation; a
// commit writing no table with a live query runs none.
//
// Go forced differences:
//   - A limit is an int64: Pi's check that it is a safe integer is the positive check alone.
//   - The runs are decoded rows as JSON objects in column order (Pi's rows are the table's row objects); numbers are float64.
//   - Pi's `QueryRefresh` takes LiveQuery instances; here it takes QueryRunner, the two methods it calls (Rerun, Adopt), so a
//     test stands in for a query as Pi's casts a stub.
//   - A LiveQuery's state is guarded by a mutex: Load runs off the host's sections, Rerun in the store's commit step, and
//     Adopt, Deliver, Detach and the reads in sections.
//   - Async contract: Pi's `load` and `rerun` are async; here they block and return `(value, error)`. `rerun` never throws
//     (its failure is the run), `load` returns the failure that fails the activation.

// QueryOwner is who a live query belongs to: its object, and where its failures after activation are reported
// (`query_failed`).
type QueryOwner interface {
	Label() string
	Report(err error)
}

// queryRows are the rows of one run: in order, and by key with the stored row each was decoded from, which the next run
// compares against to keep an unchanged row the same object.
type queryRows struct {
	value []any
	byKey map[any]decodedRow
}

// decodedRow is a row as a run decoded it, with the stored row it came from.
type decodedRow struct {
	stored sqlite.SqliteRow
	row    *delta.JsonObject
}

// QueryRun is one run of a live query in a commit step: its rows, or why it could not read them (Err).
type QueryRun struct {
	Rows *queryRows
	Err  error
}

type queryStateKind int

const (
	queryLoading queryStateKind = iota
	queryRowsKind
	queryFailed
	queryDetached
)

// queryState is where a live query stands: before its first run (activation), with rows, failed (its last run threw; its
// last good rows kept for a watch opened meanwhile), or detached (its attempt ended).
type queryState struct {
	kind queryStateKind
	// value is the rows (queryRowsKind).
	value []any
	// err is the failure (queryFailed, queryDetached).
	err error
	// last is a failed query's last good rows; nil when it had none.
	last []any
}

// CheckLiveQuery refuses what the types cannot (plugin code is bundled without type checks; declaration policy): a query of
// another table than table, and a limit that is no positive integer (a live query must be bounded: one run per commit that
// writes its table reads at most limit rows).
func CheckLiveQuery(table *TableDefinition, q *Query, limit int64, label string) error {
	if err := q.Err(); err != nil {
		return err
	}
	if q.Table != table {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf(`%s: self.query: a query of table "%s" over "%s"`, label, q.Table.Name, table.Name))
	}
	if limit < 1 {
		return data.Invalid(data.ReasonArgs, label+": self.query: a limit is required, a positive integer")
	}
	return nil
}

// LiveQueries are the live queries of one object by table (its SQL name), across its attempts: what its commits re-run.
type LiveQueries struct {
	mu      sync.Mutex
	byTable map[string][]*LiveQuery
}

// NewLiveQueries is no query yet.
func NewLiveQueries() *LiveQueries { return &LiveQueries{byTable: map[string][]*LiveQuery{}} }

// Add adds query (its attempt declared it): re-run by every commit writing its table from now on.
func (queries *LiveQueries) Add(query *LiveQuery) {
	queries.mu.Lock()
	defer queries.mu.Unlock()
	table := query.handle.Sql.Table
	queries.byTable[table] = append(queries.byTable[table], query)
}

// Delete removes query (its attempt ended).
func (queries *LiveQueries) Delete(query *LiveQuery) {
	queries.mu.Lock()
	defer queries.mu.Unlock()
	table := query.handle.Sql.Table
	list := queries.byTable[table]
	for index, each := range list {
		if each == query {
			list = append(list[:index:index], list[index+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(queries.byTable, table)
		return
	}
	queries.byTable[table] = list
}

// On are the live queries over table, if any; the slice is a copy.
func (queries *LiveQueries) On(table string) []*LiveQuery {
	queries.mu.Lock()
	defer queries.mu.Unlock()
	return append([]*LiveQuery(nil), queries.byTable[table]...)
}

// RefreshOf is the re-run of the live queries over the tables a transaction writes (handles, in the order it first wrote
// them), nil when none has one (the common case: a map lookup per written table, nothing more). ctx is the
// transaction's: the runs count through it.
func RefreshOf(handles []*TableHandle, ctx context.Context) *QueryRefresh {
	var queries []QueryRunner
	seen := map[*LiveQuery]struct{}{}
	for _, handle := range handles {
		for _, query := range handle.Owner.Queries().On(handle.Sql.Table) {
			if _, twice := seen[query]; !twice {
				seen[query] = struct{}{}
				queries = append(queries, query)
			}
		}
	}
	if queries == nil {
		return nil
	}
	return NewQueryRefresh(queries, ctx)
}

// QueryRunner is what a refresh asks of a live query: its run in a commit step, and the adoption of that run.
type QueryRunner interface {
	// Rerun is a run inside the commit step step, after its writes: the rows, or the failure; it never fails otherwise.
	Rerun(step *Step, ctx context.Context) QueryRun
	// Adopt takes a commit's run, inside its batch.
	Adopt(run QueryRun)
}

// QueryRefresh is the re-run of live queries in one commit: Write reads them in the commit step after the commit's own
// writes (the last write of the last phase), and Adopt publishes their rows in the commit's batch; a refused step adopts
// nothing. A transaction stages it as its own change (documents.Staged).
type QueryRefresh struct {
	queries []QueryRunner
	ctx     context.Context
	runs    []QueryRun
	// Write is the staged write that reads the queries; it writes nothing and records nothing in the outbox.
	Write StagedWrite
}

// NewQueryRefresh is the refresh of queries, whose runs count through ctx.
func NewQueryRefresh(queries []QueryRunner, ctx context.Context) *QueryRefresh {
	refresh := &QueryRefresh{queries: queries, ctx: ctx}
	refresh.Write = &refreshWrite{refresh: refresh}
	return refresh
}

type refreshWrite struct{ refresh *QueryRefresh }

func (*refreshWrite) StageOrder() WriteOrder { return OrderWrite }

func (write *refreshWrite) Apply(step *Step) ([]StoreWrite, error) {
	refresh := write.refresh
	// A grouped commit that rolled back applies again alone (store.go): only the last application's runs count.
	refresh.runs = refresh.runs[:0]
	for _, query := range refresh.queries {
		refresh.runs = append(refresh.runs, query.Rerun(step, refresh.ctx))
	}
	return nil, nil
}

// Adopt publishes each query's new rows (or its failure), inside the commit's batch.
func (refresh *QueryRefresh) Adopt() error {
	for index, query := range refresh.queries {
		query.Adopt(refresh.runs[index])
	}
	return nil
}

// Committed is nothing to do after the commit.
func (*QueryRefresh) Committed() {}

// Discarded is nothing to undo.
func (*QueryRefresh) Discarded() {}

// noValue is what the watches of a failed live query had: no rows, so its next rows replace the value whole.
var noValue = &struct{}{}

// LiveQuery is a live query: the rows of its query (at most limit, in its order) over its object's table handle, as a JSON
// source (a state member, a derive source, an `on` source). Its statement is compiled once; each run decodes only the rows
// whose stored columns changed and keeps the others the same objects, so its frame, the diff from the rows its watches had,
// compares unchanged rows by identity (a window shifted by one row: one insert and one removal, not a comparison of every
// row with every other).
//
// pig divergence (D431): Pi main 2439933cf, ahead of the v1.5.0 pin.
type LiveQuery struct {
	state.Source
	handle    *TableHandle
	owner     QueryOwner
	statement Statement

	mu    sync.Mutex
	state queryState
	// byKey is the last adopted rows by key, with the stored rows they were decoded from (the next run reuses unchanged
	// ones).
	byKey map[any]decodedRow
	// framed is the rows this query's watches have, kept from its adoption to its frame in the batch; nil: not listed; noValue
	// stands for Pi's NO_VALUE.
	framed any
	// adopted counts the commits adopted so far: an activation run that a commit overtook keeps the commit's newer rows.
	adopted int
	// failing is set from a failed run until the next good one: a failure episode is reported once.
	failing bool
}

var _ state.SourceImpl = (*LiveQuery)(nil)

// NewLiveQuery is the live query of q's rows (at most limit) over handle, owned by owner, in batch.
func NewLiveQuery(handle *TableHandle, q *Query, limit int64, owner QueryOwner, batch *state.Batch) *LiveQuery {
	query := &LiveQuery{handle: handle, owner: owner, statement: handle.Sql.Select(q.Condition, q.Order(), nil, limit, nil)}
	query.Init(batch, query)
	return query
}

// Handle is the table handle the query reads.
func (query *LiveQuery) Handle() *TableHandle { return query.handle }

// Value is the rows; a query read before it ran, a failed one and a detached one return their error.
func (query *LiveQuery) Value() (delta.JsonValue, error) {
	query.mu.Lock()
	defer query.mu.Unlock()
	return query.valueLocked()
}

func (query *LiveQuery) valueLocked() (delta.JsonValue, error) {
	switch query.state.kind {
	case queryRowsKind:
		return query.state.value, nil
	case queryLoading:
		return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: query.owner.Label() + ": a live query read before it ran"}, nil)
	}
	return nil, query.state.err
}

// Start is its rows, or while failed its failure and last good rows (a detached query returns its error as a failed start
// with no last value).
func (query *LiveQuery) Start() state.WatchStart {
	query.mu.Lock()
	defer query.mu.Unlock()
	if query.state.kind == queryFailed {
		return state.WatchStart{Kind: state.StartFailed, Error: query.state.err, HasLast: query.state.last != nil, Last: query.state.last}
	}
	value, err := query.valueLocked()
	if err != nil {
		return state.WatchStart{Kind: state.StartFailed, Error: err}
	}
	return state.WatchStart{Kind: state.StartValue, Value: value}
}

// Load is the first run, before its object goes live (activation, after its tables are prepared): a failure is returned and
// fails the activation. Rows a commit adopted meanwhile are newer and stay.
func (query *LiveQuery) Load(ctx context.Context) error {
	query.mu.Lock()
	adopted := query.adopted
	query.mu.Unlock()
	countRun(ctx)
	statement := query.statement
	stored, err := Read(query.handle.Store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(statement.SQL, statement.Params...)
	}, ctx)
	if err != nil {
		return err
	}
	// Decoded outside the store's read: a row the code cannot read is this query's failure, not the backend's.
	rows, err := query.decode(stored, ctx)
	if err != nil {
		return err
	}
	query.mu.Lock()
	defer query.mu.Unlock()
	if query.adopted != adopted {
		return nil
	}
	query.byKey = rows.byKey
	query.state = queryState{kind: queryRowsKind, value: rows.value}
	return nil
}

// Rerun is a run inside the commit step step, after its writes: the rows, or the failure; it never fails otherwise.
func (query *LiveQuery) Rerun(step *Step, ctx context.Context) QueryRun {
	countRun(ctx)
	statement := query.statement
	stored, err := step.All(statement.SQL, statement.Params...)
	if err != nil {
		return QueryRun{Err: err}
	}
	rows, err := query.decode(stored, ctx)
	if err != nil {
		return QueryRun{Err: err}
	}
	return QueryRun{Rows: rows}
}

// Adopt takes a commit's run, inside its batch: listed for a frame when watched, its dependents marked. A failed run makes
// the query a failed member (as a throwing derive): its watches get one failure call, the owner a report per failure episode;
// the next good run replaces the value whole.
func (query *LiveQuery) Adopt(run QueryRun) {
	query.mu.Lock()
	if query.state.kind == queryDetached {
		query.mu.Unlock()
		return
	}
	query.adopted++
	if query.Watched() && query.framed == nil {
		if query.state.kind == queryRowsKind {
			query.framed = query.state.value
		} else {
			query.framed = noValue
		}
	}
	report := false
	if run.Err == nil {
		query.failing = false
		query.byKey = run.Rows.byKey
		query.state = queryState{kind: queryRowsKind, value: run.Rows.value}
	} else {
		report = !query.failing
		query.failing = true
		var last []any
		switch query.state.kind {
		case queryRowsKind:
			last = query.state.value
		case queryFailed:
			last = query.state.last
		}
		query.state = queryState{kind: queryFailed, err: run.Err, last: last}
	}
	query.mu.Unlock()
	if report {
		query.owner.Report(state.HandedOut(run.Err))
	}
	query.Changed()
}

// Deliver delivers the frame: the diff from the rows its watches had, or one failure call when they had rows.
func (query *LiveQuery) Deliver(ctx context.Context) error {
	query.mu.Lock()
	before := query.framed
	query.framed = nil
	current := query.state
	query.mu.Unlock()
	if current.kind == queryRowsKind {
		// Listed by Adopt, so before was kept (a detach in the batch leaves no rows).
		var ops []delta.Op
		if before == noValue {
			ops = []delta.Op{{"r", current.value}}
		} else {
			ops = delta.DiffRevisions(before.([]any), current.value)
		}
		if len(ops) > 0 {
			query.Publish(current.value, ops, false, ctx)
		}
		return nil
	}
	// Still failed since the last call: nothing new for the watches.
	if current.kind == queryFailed && before != noValue {
		query.FailWatches(current.err, ctx)
	}
	return nil
}

// Detach is: its attempt ended: no commit re-runs it, reads return err (the object's), its watches end `ended`.
func (query *LiveQuery) Detach(err error) {
	query.handle.Owner.Queries().Delete(query)
	query.mu.Lock()
	query.state = queryState{kind: queryDetached, err: err}
	query.framed = nil
	query.mu.Unlock()
	query.EndWatches()
}

// countRun counts one run, where it happens (the cost guard: a commit not writing the table runs none).
func countRun(ctx context.Context) {
	if metrics := chordctx.Telemetry(ctx).Metrics(); metrics != nil {
		metrics.Counter("chord.query.runs", telemetry.InstrumentOptions{}).Add(1, nil)
	}
}

// decode is the rows of stored: a row whose stored columns are all the same as in the last adopted run is that run's object
// (one comparison per physical column), any other is decoded.
func (query *LiveQuery) decode(stored []sqlite.SqliteRow, ctx context.Context) (*queryRows, error) {
	definition := query.handle.Definition
	key := definition.Key
	query.mu.Lock()
	previous := query.byKey
	query.mu.Unlock()
	rows := &queryRows{value: make([]any, 0, len(stored)), byKey: make(map[any]decodedRow, len(stored))}
	for _, each := range stored {
		id := keyOf(key, each)
		var row *delta.JsonObject
		if before, ok := previous[id]; ok && sameStored(before.stored, each, definition.Rows.Physical) {
			row = before.row
		} else {
			// A table's rows hold its columns' values: scalars and JSON.
			decoded, err := definition.Rows.Decode(ctx, each)
			if err != nil {
				return nil, err
			}
			row = rowJSON(definition, decoded)
		}
		rows.value = append(rows.value, row)
		rows.byKey[id] = decodedRow{stored: each, row: row}
	}
	return rows, nil
}

// keyOf is a stored row's identity: its key column's value, or the JSON of its key columns' values.
func keyOf(key []string, row sqlite.SqliteRow) any {
	if len(key) == 1 {
		return row[key[0]]
	}
	values := make([]any, len(key))
	for index, column := range key {
		values[index] = row[column]
	}
	encoded, _ := jsstring.MarshalJSON(values)
	return string(encoded)
}

// sameStored reports whether two stored rows have the same value in every physical column.
func sameStored(a, b sqlite.SqliteRow, physical []string) bool {
	for _, column := range physical {
		left, right := a[column], b[column]
		if blob, ok := left.([]byte); ok {
			other, isBlob := right.([]byte)
			if !isBlob || !bytes.Equal(blob, other) {
				return false
			}
			continue
		}
		if left != right {
			return false
		}
	}
	return true
}

// rowJSON is a decoded row as a JSON object in the table's column order (absent optional columns left out; integers as JSON
// numbers).
func rowJSON(definition *TableDefinition, row Row) *delta.JsonObject {
	object := delta.NewJsonObject(len(definition.Columns))
	for _, column := range definition.Columns {
		value, present := row[column.Name]
		if !present || value == nil {
			continue
		}
		if number, isInt := value.(int64); isInt {
			value = float64(number)
		}
		object.Set(column.Name, value)
	}
	return object
}
