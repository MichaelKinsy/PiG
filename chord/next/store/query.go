package store

import (
	"encoding/base64"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/query.ts
//
// Queries (decision "Query runner"; slice 6a §6, rule 17): `NewQuery(table).Where(…).OrderBy(…)` as immutable data
// (the condition compiled when given, the order with the key appended as the tie-break), and the opaque cursors that
// continue a page, bound to the table, the order columns and the direction.
//
// Go forced differences: Pi's `query` function is NewQuery (a Go type and function cannot share a name), and the
// type-level ColumnRefs<C> is the untyped ColumnRefs of expression.go. Where and OrderBy throw in Pi; here the first
// error is kept in the query (later calls return the query unchanged) so that calls chain, and `Err` returns it:
// everything that runs a query MUST check `Err` first.

// Query is an immutable query of one table: an optional condition (several `Where`s are `and`ed) and an order
// (`OrderBy` columns, one direction, the key appended unless already there).
type Query struct {
	Table *TableDefinition
	// Condition is the compiled condition; nil for none.
	Condition *WhereNode
	orderBy   []string
	direction string
	err       error
}

// NewQuery is a query of table: its rows, in key order.
func NewQuery(table *TableDefinition) *Query { return &Query{Table: table} }

// Err is the first error a Where or OrderBy of the chain met (Pi throws it from that call); nil for a sound query.
func (query *Query) Err() error { return query.err }

func (query *Query) failed(err error) *Query {
	return &Query{Table: query.Table, Condition: query.Condition, orderBy: query.orderBy, direction: query.direction, err: err}
}

// Where is this query with fn's condition too.
func (query *Query) Where(fn func(row ColumnRefs) Condition) *Query {
	if query.err != nil {
		return query
	}
	compiled, err := CompileWhere(query.Table, fn(query.Table.Refs), "where")
	if err != nil {
		return query.failed(err)
	}
	condition := compiled
	if query.Condition != nil {
		condition = WhereNode{Op: WhereAnd, Nodes: []WhereNode{*query.Condition, compiled}}
	}
	return &Query{Table: query.Table, Condition: &condition, orderBy: query.orderBy, direction: query.direction}
}

// OrderBy is this query ordered by fn's column next (a non-`opt` `str` or `int` one), in direction ("asc", the
// default for "", or "desc"): one direction per query, no column twice (`invalid/args`).
func (query *Query) OrderBy(fn func(row ColumnRefs) *ColumnRef, direction string) *Query {
	if query.err != nil {
		return query
	}
	if direction == "" {
		direction = "asc"
	}
	table := query.Table
	// What the plugin's callback returns is checked here.
	name, _ := fn(table.Refs).isRef()
	column, found := table.ByName[name]
	if !found || column.Optional || (column.Type != ColStr && column.Type != ColInt) {
		return query.failed(invalidArgs("%s: orderBy: expected a non-opt str() or int() column", table.Name))
	}
	if slices.Contains(query.orderBy, column.Name) {
		return query.failed(invalidArgs("%s: orderBy: %s twice", table.Name, quote(column.Name)))
	}
	if query.direction != "" && query.direction != direction {
		return query.failed(invalidArgs("%s: orderBy: one direction per query", table.Name))
	}
	orderBy := append(append([]string{}, query.orderBy...), column.Name)
	return &Query{Table: table, Condition: query.Condition, orderBy: orderBy, direction: direction}
}

// Order is the order of its rows: the `OrderBy` columns, then the key columns not among them.
func (query *Query) Order() Order {
	columns := append([]string{}, query.orderBy...)
	for _, column := range query.Table.Key {
		if !slices.Contains(query.orderBy, column) {
			columns = append(columns, column)
		}
	}
	direction := query.direction
	if direction == "" {
		direction = "asc"
	}
	return Order{Columns: columns, Direction: direction}
}

// EncodeCursor is the cursor of the position after a row with the order values values in the table named table (its
// SQL name): the base64url (unpadded) of `JSON.stringify({ t, o, d, v })`.
func EncodeCursor(table string, order Order, values []sqlite.SqliteValue) string {
	columns := make([]any, len(order.Columns))
	for i, column := range order.Columns {
		columns[i] = column
	}
	text, err := jsstring.MarshalJSON(delta.JsonObjectOf("t", table, "o", columns, "d", order.Direction, "v", values))
	if err != nil {
		// Order values are checked scalars (strings and integers): they always serialize.
		panic("store: cursor values do not serialize: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(text)
}

// DecodeCursor is the order values of cursor (a trust boundary: an app hands it back): one made for table's order
// (any condition) in the table named sqlName (another object's table of the same definition is another table), each
// value of its column's type; `invalid/args` otherwise.
func DecodeCursor(table *TableDefinition, sqlName string, order Order, cursor any) ([]sqlite.SqliteValue, error) {
	refuse := func() ([]sqlite.SqliteValue, error) {
		return nil, invalidArgs("%s: the cursor is not one of this table's order", table.Name)
	}
	text, ok := cursor.(string)
	if !ok {
		return refuse()
	}
	bytes, ok := atob(strings.NewReplacer("-", "+", "_", "/").Replace(text))
	if !ok || !utf8.Valid(bytes) {
		return refuse()
	}
	// TextDecoder drops a leading byte order mark.
	bytes = []byte(strings.TrimPrefix(string(bytes), "\uFEFF"))
	decoded, err := delta.DecodeJson(bytes)
	if err != nil {
		return refuse()
	}
	// Wire input: every field is checked below.
	object, ok := decoded.(*delta.JsonObject)
	if !ok {
		return refuse()
	}
	if t, isText := object.Value("t").(string); !isText || t != sqlName {
		return refuse()
	}
	if d, isText := object.Value("d").(string); !isText || d != order.Direction {
		return refuse()
	}
	columns, ok := object.Value("o").([]any)
	if !ok || len(columns) != len(order.Columns) {
		return refuse()
	}
	for i, column := range columns {
		if name, isText := column.(string); !isText || name != order.Columns[i] {
			return refuse()
		}
	}
	values, ok := object.Value("v").([]any)
	if !ok || len(values) != len(order.Columns) {
		return refuse()
	}
	out := make([]sqlite.SqliteValue, len(order.Columns))
	for i, name := range order.Columns {
		column := table.ByName[name]
		// Order columns are non-opt `str` or `int` (checked when the query was built).
		value, err := CheckScalar(column.Type, values[i], table.Name+": cursor")
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

// atob is the forgiving base64 decode of the standard alphabet: padding optional (but valid when present).
func atob(text string) ([]byte, bool) {
	if strings.HasSuffix(text, "=") {
		if len(text)%4 != 0 {
			return nil, false
		}
		text = strings.TrimRight(text, "=")
		if len(text)%4 == 1 {
			return nil, false
		}
	}
	bytes, err := base64.RawStdEncoding.DecodeString(text)
	return bytes, err == nil
}
