package store

// Ports packages/durable/src/storage/store/queries.ts
//
// StoreStorage's reads as the store's queries (slice 6b D2, §2.4), compiled once from the store's query vocabulary
// into SQL over the declared tables: no SQL is rendered per call, and a read selects only the record cell (by id, an
// entry's commit seq too; each column returned costs) and parses one JSON text per record, as `SqliteStorage`'s do.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// RecordRow is a stored row as a query selects it: the record cell, its version (selected only once the column has
// versions past 1), and an entry's commit seq.
type RecordRow struct {
	Record    string
	RecordV   int64
	CommitSeq int64
}

// Condition is one condition of a read: a column, how it compares, its value; a call site keeps one order of them.
type Condition struct {
	Column string
	Op     chordstore.WhereOp
	Value  sqlite.SqliteValue
}

var (
	ascending  = chordstore.Order{Columns: []string{"id"}, Direction: "asc"}
	descending = chordstore.Order{Columns: []string{"id"}, Direction: "desc"}
	idColumns  = []string{"id"}
)

// placeholder is a value that only shapes a query: its shape binds its values at each call.
const placeholder = int64(0)

// TableQueries is one table's compiled reads: by id (with the byIdExtra columns), and by shape (conditions in
// binding order, an order; the record only), each shape compiled at its first use.
type TableQueries struct {
	table *chordstore.OwnerTable
	// versioned is the record column's read of a cell at a version past 1; nil while the column is at version 1.
	versioned func(text string, version int, ctx context.Context) (any, error)
	byID      string
	extra     []string
	columns   []string

	mu     sync.Mutex
	shapes map[string]string
}

// NewTableQueries compiles the by-id read of table, selecting byIdExtra besides the record.
func NewTableQueries(table *chordstore.OwnerTable, byIDExtra ...string) (*TableQueries, error) {
	version, err := table.JsonVersion("record")
	if err != nil {
		return nil, err
	}
	queries := &TableQueries{table: table, columns: []string{"record"}, extra: byIDExtra, shapes: map[string]string{}}
	if version > 1 {
		if queries.versioned, err = table.Reader("record"); err != nil {
			return nil, err
		}
		queries.columns = []string{"record", "record$v"}
	}
	queries.byID = table.PrepareGet(append(append([]string{}, queries.columns...), byIDExtra...))
	return queries, nil
}

// ByID is the row with id; nil when absent.
func (queries *TableQueries) ByID(executor interface {
	Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error)
}, id int64) (*RecordRow, error) {
	row, err := executor.Get(queries.byID, id)
	if err != nil || row == nil {
		return nil, err
	}
	return queries.rowOf(row, true)
}

// Select is up to limit rows whose conditions hold, by id in order.
func (queries *TableQueries) Select(executor interface {
	All(sql string, params ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error)
}, conditions []Condition, order chordstore.Order, limit int64) ([]RecordRow, error) {
	sql := queries.shape(conditions, order, false)
	params := make([]sqlite.SqliteValue, 0, len(conditions)+1)
	for _, condition := range conditions {
		params = append(params, condition.Value)
	}
	rows, err := executor.All(sql, append(params, limit)...)
	if err != nil {
		return nil, err
	}
	found := make([]RecordRow, len(rows))
	for index, row := range rows {
		item, err := queries.rowOf(row, false)
		if err != nil {
			return nil, err
		}
		found[index] = *item
	}
	return found, nil
}

// First is the first row whose conditions hold, by id in order; nil when none does.
func (queries *TableQueries) First(executor interface {
	Get(sql string, params ...sqlite.SqliteValue) (sqlite.SqliteRow, error)
}, conditions []Condition, order chordstore.Order) (*RecordRow, error) {
	sql := queries.shape(conditions, order, true)
	params := make([]sqlite.SqliteValue, len(conditions))
	for index, condition := range conditions {
		params[index] = condition.Value
	}
	row, err := executor.Get(sql, params...)
	if err != nil || row == nil {
		return nil, err
	}
	return queries.rowOf(row, false)
}

// rowOf is a selected row's record cell, version and, of a read by id, extra columns (the commit seq).
func (queries *TableQueries) rowOf(row sqlite.SqliteRow, byID bool) (*RecordRow, error) {
	var found RecordRow
	switch text := row["record"].(type) {
	case string:
		found.Record = text
	case []byte:
		found.Record = string(text)
	default:
		return nil, fmt.Errorf("stored record cell is %T, not text", row["record"])
	}
	var err error
	if queries.versioned != nil {
		if found.RecordV, err = sqlite.RowInt(row, "record$v"); err != nil {
			return nil, err
		}
	}
	if byID && len(queries.extra) > 0 {
		if found.CommitSeq, err = sqlite.RowInt(row, "commit_seq"); err != nil {
			return nil, err
		}
	}
	return &found, nil
}

// recordOf is a row's record, read as its column's (version checked, migrated on access).
func recordOf[T storedRecord](queries *TableQueries, row RecordRow, ctx context.Context) (T, error) {
	if queries.versioned == nil {
		// The cell holds a record of this table the storage wrote; at version 1 it is that JSON (`OwnerTable.reader`).
		return parseJSON[T](row.Record)
	}
	cell, err := queries.versioned(row.Record, int(row.RecordV), ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	text, err := jsstring.MarshalJSON(cell)
	if err != nil {
		var zero T
		return zero, err
	}
	return parseJSON[T](string(text))
}

// appendRecords appends the records of rows to into, in order, each read as recordOf reads it.
func appendRecords[T storedRecord](queries *TableQueries, rows []RecordRow, into []T, ctx context.Context) ([]T, error) {
	for _, row := range rows {
		value, err := recordOf[T](queries, row, ctx)
		if err != nil {
			return into, err
		}
		into = append(into, value)
	}
	return into, nil
}

// shape is the compiled read of a shape: its conditions' columns and comparisons, the order, one row or many.
func (queries *TableQueries) shape(conditions []Condition, order chordstore.Order, first bool) string {
	var key strings.Builder
	if first {
		key.WriteString("first ")
	} else {
		key.WriteString("many ")
	}
	key.WriteString(order.Direction)
	for _, condition := range conditions {
		key.WriteString(" " + condition.Column + " " + string(condition.Op))
	}
	queries.mu.Lock()
	defer queries.mu.Unlock()
	if sql, ok := queries.shapes[key.String()]; ok {
		return sql
	}
	// One comparison per condition: the vocabulary's node of that name, its value a placeholder.
	var where *chordstore.WhereNode
	if len(conditions) > 0 {
		nodes := make([]chordstore.WhereNode, len(conditions))
		for index, condition := range conditions {
			nodes[index] = chordstore.WhereNode{Op: condition.Op, Column: condition.Column, Value: placeholder}
		}
		where = &chordstore.WhereNode{Op: chordstore.WhereAnd, Nodes: nodes}
	}
	var sql string
	if first {
		sql = queries.table.PrepareFirst(queries.columns, where, order)
	} else {
		sql = queries.table.Prepare(queries.columns, where, order)
	}
	queries.shapes[key.String()] = sql
	return sql
}

// StorageQueries are the compiled reads of the storage's tables.
type StorageQueries struct {
	Conversations *TableQueries
	Entries       *TableQueries
	// LatestHead is the id of a conversation's newest entry with a head marker: any, or at or before an id (bound
	// second).
	LatestHeadAny      string
	LatestHeadAtBefore string
	Tasks              *TableQueries
	Submissions        *TableQueries
}

// NewStorageQueries is the reads of tables.
func NewStorageQueries(tables *CommitTables) (*StorageQueries, error) {
	queries := &StorageQueries{}
	var err error
	if queries.Conversations, err = NewTableQueries(tables.Conversations); err != nil {
		return nil, err
	}
	if queries.Entries, err = NewTableQueries(tables.Entries, "commit_seq"); err != nil {
		return nil, err
	}
	if queries.Tasks, err = NewTableQueries(tables.Tasks); err != nil {
		return nil, err
	}
	if queries.Submissions, err = NewTableQueries(tables.Submissions); err != nil {
		return nil, err
	}
	heads := tables.EntryHeads
	queries.LatestHeadAny = heads.PrepareFirst(idColumns, &chordstore.WhereNode{Op: chordstore.WhereEq, Column: "conversation_id", Value: placeholder}, descending)
	queries.LatestHeadAtBefore = heads.PrepareFirst(idColumns, &chordstore.WhereNode{Op: chordstore.WhereAnd, Nodes: []chordstore.WhereNode{
		{Op: chordstore.WhereEq, Column: "conversation_id", Value: placeholder},
		{Op: chordstore.WhereLte, Column: "id", Value: placeholder},
	}}, descending)
	return queries, nil
}
