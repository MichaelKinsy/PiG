package store

import (
	"bytes"
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/store/live-query.ts
//
// Live queries (decision "Live queries over an object's own tables"; slice 14 §3): the rows of a query as a JSON watchable,
// kept current by the store's reads. `self.query(table, q, limit)` is the owner's own unnamed window (its value the rows);
// `self.liveQuery(namedQuery, args, { first })` reads a named query (its value `{ rows, more }`). Both are this source over
// one open read; a commit's run is published in that commit's batch as the diff from the previous rows (W1).
//
// Go forms:
//   - A limit is an int64: Pi's check that it is a safe integer is the positive check alone.
//   - The rows are decoded rows as JSON objects in column order (Pi's rows are the table's row objects); numbers are float64.
//   - Pi's `decode` throws a row the code cannot read; here it returns the error. `present` cannot fail.
//   - Pi's `read` is async; here it blocks until the read's first run and returns it (`nil` where Pi resolves `undefined`).
//     `load` blocks and returns the failure that fails the activation.
//   - Pi's one thread is the host's sections: the fields of a live query are touched only inside one (its adoptions and
//     detach run in the commit's, the reading and the failure check of `Load` take one with the batch's Section). A binding
//     that changed reads again on a goroutine of its own (Pi's `void reading.read.load()`).

// CheckLiveQuery refuses what the types cannot (plugin code is bundled without type checks; declaration policy): a query of
// another table than table, and a limit that is no positive integer (a live query must be bounded: one run reads at most
// limit rows).
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

// LiveTarget is what a live source reads: a query to keep open, and how a stored row of it becomes the value's row (decoded,
// projected, mapped); a row the code cannot read is an error.
type LiveTarget struct {
	Spec   QuerySpec
	Decode func(stored StoredRead, ctx context.Context) (delta.JsonValue, error)
}

// LiveShape is what a live query reads and shows: the rows of Range (Kind "rows"; Present makes the value of them and whether
// a further row exists: the rows as they are, or `{ rows, more }`), or a count (Kind "count").
type LiveShape struct {
	Kind    string
	Range   Range
	Present func(rows []delta.JsonValue, more bool) delta.JsonValue
}

// reading is one of a live query's reads, open, with the query it is of and what it reads.
type reading struct {
	query  *OpenQuery
	read   *OpenRead
	target LiveTarget
}

// adoptRun is a read's sink: a function.
type adoptRun func(run ReadRun)

func (adopt adoptRun) Adopt(run ReadRun) { adopt(run) }

// LiveQuery is a live query as a JSON source (a state member, a derive source, an `on` source): the rows of one range of a
// query, or its count. `Read` gives it the query; a later `Read` (arguments that follow a value, a provider's successor) shows
// the previous value until the new read's first run is adopted, then switches in one frame.
type LiveQuery struct {
	state.FramedSource
	site  ReadSite
	shape LiveShape
	rows  DecodedRows
	// current is the read its value is of, and next the one that replaces it once its first run is adopted.
	current *reading
	next    *reading
}

var _ state.SourceImpl = (*LiveQuery)(nil)

// NewLiveQuery is a live query of shape, owned by owner, in site.
func NewLiveQuery(site ReadSite, owner state.FramedOwner, shape LiveShape) *LiveQuery {
	query := &LiveQuery{site: site, shape: shape}
	query.Init(site.Batch, owner)
	return query
}

// Load is the first run of the owner's own window, before its object goes live (activation, after its tables are prepared): a
// failure is returned and fails the activation.
func (query *LiveQuery) Load(target LiveTarget, ctx context.Context) error {
	query.site.Batch.Section(query.Quiet)
	query.Read(target, ctx)
	var failure error
	query.site.Batch.Section(func() { failure = query.Failure() })
	return failure
}

// Read reads target from now on (its shape's range of it, or its count). It returns its first run once adopted (nil: a
// commit's run came first, a table it reads is held, or the query was detached or given another read meanwhile).
func (query *LiveQuery) Read(target LiveTarget, ctx context.Context) *ReadRun {
	return query.Open(target, ctx)()
}

// Open is the part of Read that Pi's `read` runs when it is called, before its first await: the read's query is open and
// registered (it follows its sets' bindings, and replaces the previous pending read), nothing is loaded. It returns the
// load, which blocks on the store and returns what Read returns; nothing of the read happens before it runs, and a read
// the query closed meanwhile loads nothing. A reader opens in the section of the move that gave it the target and runs
// the load on a task later: the load must not decide whether the read exists.
func (query *LiveQuery) Open(target LiveTarget, ctx context.Context) (load func() *ReadRun) {
	var opened *reading
	query.site.Batch.Section(func() {
		if query.Detached() {
			return
		}
		if query.next != nil {
			query.next.query.Close()
		}
		shape := query.shape
		// A binding changed: its one read runs again (a failure is a failed run handed to it; nothing waits for the run).
		var each *reading
		rebound := func() { each.read.Reload() }
		each = &reading{target: target}
		each.query = NewOpenQuery(query.site, target.Spec, ReadShape(shape.Kind), rebound, ctx)
		what := ReadKind{Kind: "rows", Range: shape.Range}
		if shape.Kind == "count" {
			what = ReadKind{Kind: "count"}
		}
		each.read = each.query.Read(what, adoptRun(func(run ReadRun) { query.adopt(each, run) }))
		query.next = each
		opened = each
	})
	if opened == nil {
		return func() *ReadRun { return nil }
	}
	return opened.read.Load
}

// Fail is: what it should read cannot be read (its arguments threw, its provider refused them): a failed member, as a failed
// run makes it. Its reads close: neither the read of the previous arguments nor a pending one may publish or heal the failure;
// the next `Read` does.
func (query *LiveQuery) Fail(err error, ctx context.Context) {
	query.site.Batch.Section(func() {
		if query.Detached() {
			return
		}
		query.close()
		// The failure has no error to return.
		_ = query.site.Batch.Within(func() error { query.FailWith(err); return nil }, ctx)
	})
}

// Detach is: its attempt ended: no commit runs it again, reads return err (the object's), its watches end `ended`.
func (query *LiveQuery) Detach(err error) {
	query.close()
	query.End(err)
}

func (query *LiveQuery) close() {
	if query.current != nil {
		query.current.query.Close()
	}
	if query.next != nil {
		query.next.query.Close()
	}
	query.current, query.next = nil, nil
}

// adopt takes a run of one of its reads, inside a batch. The first run of a new read makes it the current one. A failed run, and
// a row the code cannot read, make the query a failed member.
func (query *LiveQuery) adopt(each *reading, run ReadRun) {
	if query.Detached() {
		return
	}
	if each == query.next {
		if query.current != nil {
			query.current.query.Close()
		}
		query.current = each
		query.next = nil
		query.rows.Reset()
	} else if each != query.current {
		return
	}
	switch run.Kind {
	case "failed":
		query.FailWith(run.Err)
	case "count":
		query.Set(float64(run.Count))
	default:
		query.present(each, run.Result)
	}
}

// present is the value of a run's rows; a row the code cannot read fails the member.
func (query *LiveQuery) present(each *reading, result *ReadResult) {
	shape := query.shape
	// A count's read returns counts.
	if shape.Kind != "rows" {
		return
	}
	rows, err := query.rows.Decode(result.Rows, each.target, each.query.Ctx)
	if err != nil {
		query.FailWith(err)
		return
	}
	query.Set(shape.Present(rows, result.More))
}

// decoded is a row as a run decoded it, with the stored row it came from.
type decoded struct {
	stored StoredRead
	row    delta.JsonValue
}

// DecodedRows are the decoded rows of a live source's last value, by key: a row whose stored columns are all the same in the next
// run is that value's object (one comparison per selected column), any other is decoded. So a frame, the diff from the value
// the watches had, compares unchanged rows by identity (a window shifted by one row: one insert and one removal, not a
// comparison of every row with every other).
type DecodedRows struct{ byKey map[any]decoded }

// Decode is the rows of stored as target decodes them, unchanged ones reused; a row the code cannot read is the error.
func (rows *DecodedRows) Decode(stored []StoredRead, target LiveTarget, ctx context.Context) ([]delta.JsonValue, error) {
	key := target.Spec.Query.Table.Key
	values := make([]delta.JsonValue, 0, len(stored))
	byKey := make(map[any]decoded, len(stored))
	for _, each := range stored {
		id := Identity(key, each)
		var row delta.JsonValue
		if before, ok := rows.byKey[id]; ok && sameStored(before.stored, each) {
			row = before.row
		} else {
			var err error
			if row, err = target.Decode(each, ctx); err != nil {
				return nil, err
			}
		}
		values = append(values, row)
		byKey[id] = decoded{stored: each, row: row}
	}
	rows.byKey = byKey
	return values, nil
}

// Reset forgets the last value's rows: another target decodes its own way, nothing of the previous one is reused.
func (rows *DecodedRows) Reset() { rows.byKey = nil }

// sameStored reports whether two stored rows of one statement have the same value in every column (SQLite values compare by
// `===`: a blob by its bytes).
func sameStored(a, b StoredRead) bool {
	for column, left := range a {
		right := b[column]
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

// DecodedRowJSON is a stored row of definition's table as the JSON object a live query shows (Pi's `rows.decode(stored,
// context) as JsonValue`): its columns' values, scalars and JSON, a row the code cannot read is the error.
func DecodedRowJSON(definition *TableDefinition, stored StoredRead, ctx context.Context) (delta.JsonValue, error) {
	decoded, err := definition.Rows.Decode(ctx, stored)
	if err != nil {
		return nil, err
	}
	return rowJSON(definition, decoded), nil
}

// rowJSON is a decoded row as a JSON object in the table's column order (absent optional columns left out; integers as JSON
// numbers; a `host(...)` cell as the address's object).
func rowJSON(definition *TableDefinition, row Row) *delta.JsonObject {
	object := delta.NewJsonObject(len(definition.Columns))
	for _, column := range definition.Columns {
		value, present := row[column.Name]
		if !present || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int64:
			value = float64(typed)
		case contracts.Address:
			// A `host(...)` cell is the address's plain object (a JSON value cannot hold the struct).
			value = delta.JsonObjectOf("kind", typed.Kind, "params", typed.Params, "key", typed.Key)
		}
		object.Set(column.Name, value)
	}
	return object
}
