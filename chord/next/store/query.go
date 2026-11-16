package store

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/store/query.ts
//
// Queries (decision "Query runner"; slice 6a §6, rule 17; slice 14 §5, §6): `NewQuery(table).Where(…).OrderBy(…)` as
// immutable data (the condition compiled when given, the order with the key appended as the tie-break), optionally
// joined with one row of another plugin's column set, and the opaque cursors that continue a page, bound to the
// table, the order columns and the direction.
//
// Go forced differences: Pi's `query` function is NewQuery (a Go type and function cannot share a name), and the
// type-level ColumnRefs<C> is the untyped ColumnRefs of expression.go. Where, WhereNode, Join and OrderBy throw in Pi;
// here the first error is kept in the query (later calls return the query unchanged) so that calls chain, and `Err`
// returns it: everything that runs a query MUST check `Err` first. A Match is an ordered list, as Pi's object is.

// Joined is the set a query joins: one row of it per own row, by the set's whole key.
type Joined struct {
	Set *ColumnSet
	// Pairs are the set's key columns (SetColumn) and the own columns each is matched to (Column), in key order.
	Pairs []MatchPair
}

// Query is an immutable query of one table: an optional condition (several `Where`s are `and`ed), an order
// (`OrderBy` columns, one direction, the key appended unless already there) and at most one joined set, whose columns
// the condition and the order may use too.
type Query struct {
	Table *TableDefinition
	// Condition is the compiled condition; nil for none.
	Condition *WhereNode
	// Joined is the joined set; nil for none.
	Joined *Joined
	// Expected are the set columns its `exists` were written for (`Expected`): checked when a read of it opens.
	Expected  Expected
	orderBy   []string
	direction string
	err       error
}

// NewQuery is a query of table: its rows, in key order.
func NewQuery(table *TableDefinition) *Query { return &Query{Table: table} }

// Err is the first error a Where, WhereNode, Join or OrderBy of the chain met (Pi throws it from that call); nil for a
// sound query.
func (query *Query) Err() error { return query.err }

func (query *Query) failed(err error) *Query {
	next := *query
	next.err = err
	return &next
}

// Where is this query with fn's condition too; partner are the joined set's columns.
func (query *Query) Where(fn func(row, partner ColumnRefs) Condition) *Query {
	if query.err != nil {
		return query
	}
	// A copy of what is expected so far: the compilation adds what this condition's `exists` use.
	expected := query.Expected.Clone()
	row, partner := query.refs()
	compiled, err := compileCondition(query.scope(), fn(row, partner), "where", &expected)
	if err != nil {
		return query.failed(err)
	}
	return query.and(compiled, expected)
}

// WhereNode is this query with a condition handed over as data (`Compile(set, …)`, slice 14 §6): over the own table
// when it exposes set, over the partner when set is joined. A trust boundary: node (a WhereNode, or decoded JSON) is
// whatever arrived as an argument, and the whole of it is checked first.
func (query *Query) WhereNode(set *ColumnSet, node any) *Query {
	if query.err != nil {
		return query
	}
	what := fmt.Sprintf("%s: whereNode(%s)", query.Table.Name, set.Name)
	raw := nodeData(node)
	if err := CheckNode(raw, &Side{Name: set.Name, ByName: set.ByName}, what); err != nil {
		return query.failed(err)
	}
	decoded, err := DecodeWhereNode(raw)
	if err != nil {
		return query.failed(err)
	}
	if query.Joined != nil && query.Joined.Set.Name == set.Name {
		return query.and(renamed(decoded, func(column string) string { return PartnerName(set.Name, column) }), query.Expected)
	}
	if query.Table.Exposes == nil || query.Table.Exposes.Name != set.Name {
		return query.failed(invalidArgs("%s: the query has no side of that set", what))
	}
	return query.and(decoded, query.Expected)
}

// Join is this query joined with the one row of set whose key equals match's own columns: an inner join driven from
// the own table. The match covers the set's whole key; one set per query, and not the one the table exposes.
func (query *Query) Join(set *ColumnSet, match func(row ColumnRefs) Match) *Query {
	if query.err != nil {
		return query
	}
	table := query.Table
	what := fmt.Sprintf("%s: join(%s)", table.Name, set.Name)
	if query.Joined != nil {
		return query.failed(invalidArgs("%s: a query joins one set", what))
	}
	if table.Exposes != nil && table.Exposes.Name == set.Name {
		return query.failed(invalidArgs("%s: the set is the table's own", what))
	}
	// The callback is a plugin's: what it returns is read as data and checked.
	given := match(table.Refs)
	own := TableScope(table, false)
	pairs := make([]MatchPair, len(given))
	names := make([]string, len(given))
	for i, entry := range given {
		column, found := set.ByName[entry.Column]
		// A reference the scope does not know (another table's, no reference at all) resolves to nothing.
		var matched Resolved
		var resolved bool
		if _, isRef := entry.Ref.isRef(); isRef {
			matched, resolved = own.Resolve(entry.Ref)
		}
		if !found || !resolved {
			return query.failed(invalidArgs("%s: %s is matched to no column", what, quote(entry.Column)))
		}
		if err := CheckPair(set.Name, column, matched.Column, what); err != nil {
			return query.failed(err)
		}
		pairs[i] = MatchPair{SetColumn: entry.Column, Column: matched.Name}
		names[i] = entry.Column
	}
	if len(names) != len(set.Key) {
		return query.failed(invalidArgs("%s: the match covers the whole key (%s)", what, strings.Join(set.Key, ", ")))
	}
	if err := CheckMatch(MatchedSet{Side: set.Side(), Key: set.Key}, names, what); err != nil {
		return query.failed(err)
	}
	keyed := make([]MatchPair, len(set.Key))
	for i, column := range set.Key {
		keyed[i] = pairs[slices.Index(names, column)]
	}
	next := *query
	next.Joined = &Joined{Set: set, Pairs: keyed}
	return &next
}

// OrderBy is this query ordered by fn's column next (a non-`opt` `str` or `int` one, of the table or of the joined
// set), in direction ("asc", the default for "", or "desc"): one direction per query, no column twice
// (`invalid/args`).
func (query *Query) OrderBy(fn func(row, partner ColumnRefs) *ColumnRef, direction string) *Query {
	if query.err != nil {
		return query
	}
	if direction == "" {
		direction = "asc"
	}
	table := query.Table
	// What the plugin's callback returns is checked here.
	row, partner := query.refs()
	var resolved Resolved
	var found bool
	if ref := fn(row, partner); ref != nil {
		if _, isRef := ref.isRef(); isRef {
			resolved, found = query.scope().Resolve(ref)
		}
	}
	column := resolved.Column
	if !found || column.Optional || (column.Type != ColStr && column.Type != ColInt) {
		return query.failed(invalidArgs("%s: orderBy: expected a non-opt str() or int() column", table.Name))
	}
	if slices.Contains(query.orderBy, resolved.Name) {
		return query.failed(invalidArgs("%s: orderBy: %s twice", table.Name, quote(resolved.Name)))
	}
	if query.direction != "" && query.direction != direction {
		return query.failed(invalidArgs("%s: orderBy: one direction per query", table.Name))
	}
	next := *query
	next.orderBy = append(slices.Clone(query.orderBy), resolved.Name)
	next.direction = direction
	return &next
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

// Column is the column name of its order or condition: the table's, or the joined set's (`<set>:<column>`).
func (query *Query) Column(name string) (Column, bool) {
	if joined := query.Joined; joined != nil {
		if rest, partner := strings.CutPrefix(name, PartnerName(joined.Set.Name, "")); partner {
			column, found := joined.Set.ByName[rest]
			return column, found
		}
	}
	column, found := query.Table.ByName[name]
	return column, found
}

func (query *Query) and(compiled WhereNode, expected Expected) *Query {
	next := *query
	next.Condition = &compiled
	if query.Condition != nil {
		next.Condition = &WhereNode{Op: WhereAnd, Nodes: []WhereNode{*query.Condition, compiled}}
	}
	next.Expected = expected
	return &next
}

func (query *Query) scope() Scope {
	own := TableScope(query.Table, true)
	if query.Joined == nil {
		return own
	}
	return JoinedScope(own, query.Joined.Set.Side())
}

// refs are the references its callbacks get: the table's columns, and the joined set's (none without a join).
func (query *Query) refs() (row, partner ColumnRefs) {
	if query.Joined == nil {
		return query.Table.Refs, ColumnRefs{}
	}
	return query.Table.Refs, query.Joined.Set.Refs
}

// renamed is node with every column of its own side renamed (an `exists`' inner condition is another side's).
func renamed(node WhereNode, rename func(column string) string) WhereNode {
	switch node.Op {
	case WhereAnd, WhereOr, WhereNot:
		nodes := make([]WhereNode, len(node.Nodes))
		for i, each := range node.Nodes {
			nodes[i] = renamed(each, rename)
		}
		return WhereNode{Op: node.Op, Nodes: nodes}
	case WhereExists:
		pairs := make([]MatchPair, len(node.Exists.Pairs))
		for i, pair := range node.Exists.Pairs {
			pairs[i] = MatchPair{SetColumn: pair.SetColumn, Column: rename(pair.Column)}
		}
		return WhereNode{Op: WhereExists, Exists: &ExistsNode{Set: node.Exists.Set, Pairs: pairs, Inner: node.Exists.Inner}}
	}
	// isNull and the remaining nodes are `{ [op]: [column, operand] }`.
	renamedNode := node
	renamedNode.Column = rename(node.Column)
	return renamedNode
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
// value of its column's type (`column` finds a joined set's order columns: a query's Column; Pi's default is the table's
// own columns); `invalid/args` otherwise.
func DecodeCursor(table *TableDefinition, sqlName string, order Order, cursor any, column func(name string) (Column, bool)) ([]sqlite.SqliteValue, error) {
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
		found, _ := column(name)
		// Order columns are non-opt `str` or `int` (checked when the query was built).
		value, err := CheckScalar(found.Type, values[i], table.Name+": cursor")
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
