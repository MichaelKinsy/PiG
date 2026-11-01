package store

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports packages/chord/src/next/store/table-handle.ts
//
// Table handles (slice 6a §6; decision "Query runner"): what `self.table(Def)` returns, one per attempt and table:
// reads of committed rows (`Page`, `Rows`, `Get`, `Count`) through the store's guarded reads, and what a transaction's
// writes to the table are staged with; a handle of an older version than the stored table is refused (R15, slice 7b).
//
// Go forced differences:
//   - Pi's `Table<C, K>` type-level interface is designed out; TableHandle's methods are it. `get` of an absent row
//     returns a nil Row; `page`'s `next` is a nil *string when no page follows.
//   - `rows` (an AsyncIterable) is a RowsIterator: Next(ctx) reads the next row (fetching a page of ROWS_PAGE rows at
//     a time, with the context Rows was given), Close stops it; a page is only fetched when the previous one was read.
//   - A limit is an int64: Pi's non-integer limits cannot be written (designed out); the positive check stays.
//   - A TableOwner is an interface (Pi's object type): its liveness check returns the `released` error.
//   - Async contract: each read blocks on one guarded read of the store (ordered with the store's close, cancellable by
//     ctx as Read is) and returns `(value, error)`; handles are safe for concurrent reads.

// Page is one page of rows (`items`, as Durable's pages) and, when more follow, the cursor to continue after it (nil
// otherwise).
type Page struct {
	Items []Row
	Next  *string
}

// PageOptions is what Page takes besides the query, the limit and the context.
type PageOptions struct {
	// After is a cursor from an earlier page of the same order; nil starts at the first row.
	After *string
}

// TableOwner is who owns a table handle: its object (whose transactions alone write it), liveness check and live
// failure.
type TableOwner interface {
	// Label names the owner in messages.
	Label() string
	// Scope is the object's documents: a transaction of another object refuses the handle.
	Scope() any
	// AssertLive returns `released` once the object ended.
	AssertLive() error
	// Fail fails the owner `broken` with a row the code cannot read (a `json(...)` cell, slice 6a §9; §1.4).
	Fail(err *data.ChordError)
}

// RowsPage is the rows `Rows` reads per page (rule 17).
const RowsPage = 100

// TableHandle is a table handle: the definition the attempt declared, the table's SQL name and statements, the store.
type TableHandle struct {
	Definition *TableDefinition
	Sql        *TableSql
	Store      *ChordStore
	Owner      TableOwner
}

// NewTableHandle is the handle of definition at SQL name sqlName of store, owned by owner.
func NewTableHandle(definition *TableDefinition, sqlName string, store *ChordStore, owner TableOwner) *TableHandle {
	named := make([]NamedType, len(definition.Columns))
	for index, column := range definition.Columns {
		named[index] = NamedType{Name: column.Name, Type: column.Type}
	}
	return &TableHandle{
		Definition: definition,
		Sql:        NewTableSql(sqlName, named, definition.Rows.Physical, definition.Key),
		Store:      store,
		Owner:      owner,
	}
}

// Page is up to limit rows of q (after options.After), and the cursor of the next page when there is one.
func (handle *TableHandle) Page(q *Query, limit int64, ctx context.Context, options PageOptions) (Page, error) {
	if err := q.Err(); err != nil {
		return Page{}, err
	}
	if err := handle.Owner.AssertLive(); err != nil {
		return Page{}, err
	}
	if err := handle.AssertVersion(); err != nil {
		return Page{}, err
	}
	if err := handle.assertQuery(q); err != nil {
		return Page{}, err
	}
	if limit < 1 {
		return Page{}, data.Invalid(data.ReasonArgs, handle.Definition.Name+": limit is a positive integer")
	}
	order := q.Order()
	var after []sqlite.SqliteValue
	if options.After != nil {
		decoded, err := DecodeCursor(handle.Definition, handle.Sql.Table, order, *options.After)
		if err != nil {
			return Page{}, err
		}
		after = decoded
	}
	statement := handle.Sql.Select(q.Condition, order, after, limit+1, nil)
	stored, err := Read(handle.Store, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(statement.SQL, statement.Params...)
	}, ctx)
	if err != nil {
		return Page{}, err
	}
	more := int64(len(stored)) > limit
	if more {
		stored = stored[:limit]
	}
	items := make([]Row, len(stored))
	for index, row := range stored {
		if items[index], err = handle.Decode(row, ctx); err != nil {
			return Page{}, err
		}
	}
	if !more {
		return Page{Items: items}, nil
	}
	last := stored[limit-1]
	values := make([]sqlite.SqliteValue, len(order.Columns))
	for index, column := range order.Columns {
		values[index] = last[column]
	}
	next := EncodeCursor(handle.Sql.Table, order, values)
	return Page{Items: items, Next: &next}, nil
}

// RowsIterator is every row of a query, read a page of RowsPage at a time.
type RowsIterator struct {
	handle   *TableHandle
	q        *Query
	ctx      context.Context
	buffer   []Row
	after    *string
	finished bool
}

// Rows is every row of q, read a page of 100 at a time; the first page is read at the first Next.
func (handle *TableHandle) Rows(q *Query, ctx context.Context) *RowsIterator {
	return &RowsIterator{handle: handle, q: q, ctx: ctx}
}

// Next is the next row; ok is false once every row was read. A cancelled call ctx returns its cause before reading.
func (rows *RowsIterator) Next(ctx context.Context) (Row, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, context.Cause(ctx)
	}
	for len(rows.buffer) == 0 {
		if rows.finished {
			return nil, false, nil
		}
		options := PageOptions{After: rows.after}
		page, err := rows.handle.Page(rows.q, RowsPage, rows.ctx, options)
		if err != nil {
			rows.finished = true
			return nil, false, err
		}
		rows.buffer, rows.after = page.Items, page.Next
		rows.finished = page.Next == nil
	}
	row := rows.buffer[0]
	rows.buffer = rows.buffer[1:]
	return row, true, nil
}

// Close ends the iteration: no further page is read.
func (rows *RowsIterator) Close() {
	rows.finished = true
	rows.buffer = nil
}

// Get is the row with key; nil when absent.
func (handle *TableHandle) Get(key any, ctx context.Context) (Row, error) {
	if err := handle.Owner.AssertLive(); err != nil {
		return nil, err
	}
	if err := handle.AssertVersion(); err != nil {
		return nil, err
	}
	encoded, err := handle.Definition.Rows.Key(key, "get")
	if err != nil {
		return nil, err
	}
	stored, err := Read(handle.Store, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
		return executor.Get(handle.Sql.Get, encoded.Params...)
	}, ctx)
	if err != nil || stored == nil {
		return nil, err
	}
	return handle.Decode(stored, ctx)
}

// Count is the number of rows of q.
func (handle *TableHandle) Count(q *Query, ctx context.Context) (int64, error) {
	if err := q.Err(); err != nil {
		return 0, err
	}
	if err := handle.Owner.AssertLive(); err != nil {
		return 0, err
	}
	if err := handle.AssertVersion(); err != nil {
		return 0, err
	}
	if err := handle.assertQuery(q); err != nil {
		return 0, err
	}
	statement := handle.Sql.Count(q.Condition)
	row, err := Read(handle.Store, func(executor sqlite.SqliteExecutor) (sqlite.SqliteRow, error) {
		return executor.Get(statement.SQL, statement.Params...)
	}, ctx)
	if err != nil || row == nil {
		return 0, err
	}
	return sqlite.RowInt(row, "count")
}

// AssertVersion is the version fence of old table handles (R15, slice 7b §5): once the store's table is at a newer
// version than this handle's definition (a reload's new code migrated it), old code reads and writes it no more
// (`released/version`), so it never issues SQL for a dropped column.
func (handle *TableHandle) AssertVersion() error {
	stored, ok := handle.Store.TableVersion(handle.Sql.Table)
	if !ok || stored <= handle.Definition.Version {
		return nil
	}
	name, version := handle.Definition.Name, handle.Definition.Version
	message := fmt.Sprintf("%s: table \"%s\" of version %d; the store has version %d", handle.Owner.Label(), name, version, stored)
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonVersion, Message: message}, nil)
}

// Decode is a stored row as reads return it; one the code cannot read fails the owner and returns the failure.
func (handle *TableHandle) Decode(stored sqlite.SqliteRow, ctx context.Context) (Row, error) {
	row, err := handle.Definition.Rows.Decode(stored, ctx)
	if err == nil {
		return row, nil
	}
	failure, ok := data.IsChordError(err)
	if !ok {
		// JavaScript's `String(error)` of an Error is `Error: <message>`.
		failure = data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "Error: " + err.Error()}, err)
	}
	handle.Owner.Fail(failure)
	return nil, failure
}

// assertQuery refuses a query of another table (`invalid/args`).
func (handle *TableHandle) assertQuery(q *Query) error {
	if q.Table != handle.Definition {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: a query of table %q run on %q", handle.Owner.Label(), q.Table.Name, handle.Definition.Name))
	}
	return nil
}
