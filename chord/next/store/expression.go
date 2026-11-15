package store

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/store/expression.ts
//
// The table expression language (slice 6a §6, §7; rules 10, 13, 14, 16; decision "Nullable columns and the `set`
// form"): the builders plugins write `where` and `set` with, and their compilation at staging into one data-only
// vocabulary (what the outbox and archives carry; evaluate.go runs it in Go, sql.go renders it).
//
// Go forced differences:
//   - Pi's compiled nodes are plain JSON-shaped objects discriminated by `"and" in node`; here WhereNode, ExprNode and
//     SetNode are structs discriminated by an Op, with MarshalJSON writing Pi's exact JSON (key order, shapes) and
//     DecodeWhereNode / DecodeExprNode / DecodeSetNode reading it back (archives and the outbox carry that JSON).
//   - A `set` is ordered ([]SetEntry; Pi's object keeps insertion order), and so is a plugin's set: SetOf (a map input
//     is taken in sorted key order, which is deterministic where Go has no insertion order).
//   - JavaScript's `undefined` (clears an `opt(...)` column in a set or a rows patch; refused as an operand) is the
//     Undefined value; nil is JavaScript's `null`.
//   - The type-level machinery (Expression<T, K>, SetOf<C, K>, Operand<T, K>, ScalarType) is designed out. An operand
//     is `any`: a Scalar (string, int64 or any Go integer, float64, bool) or an *Expression; every runtime check Pi
//     makes for JavaScript callers (the trust boundary) is kept.
//   - Compile functions return their error as Pi throws it (`invalid/args` ChordErrors).

// WhereOp is the operator of a Condition and of a WhereNode.
type WhereOp string

// The operators of conditions.
const (
	WhereEq      WhereOp = "eq"
	WhereNe      WhereOp = "ne"
	WhereLt      WhereOp = "lt"
	WhereLte     WhereOp = "lte"
	WhereGt      WhereOp = "gt"
	WhereGte     WhereOp = "gte"
	WhereInArray WhereOp = "inArray"
	WhereIsNull  WhereOp = "isNull"
	WherePrefix  WhereOp = "prefix"
	WhereAnd     WhereOp = "and"
	WhereOr      WhereOp = "or"
	WhereNot     WhereOp = "not"
	// WhereExists is the node of an `exists` (a query's condition only); WhereExistsNode is the Condition builder
	// that takes its inner condition as data. Compilation turns both into a WhereExists node.
	WhereExists     WhereOp = "exists"
	WhereExistsNode WhereOp = "existsNode"
)

// isComparison reports the six comparison operators (Pi's `Comparison`).
func (op WhereOp) isComparison() bool {
	switch op {
	case WhereEq, WhereNe, WhereLt, WhereLte, WhereGt, WhereGte:
		return true
	}
	return false
}

// WhereNode is a compiled condition: column comparisons with checked operands, combined by `and`, `or`, `not`. A
// query's condition may also test `exists` (slice 14 §5): `[set, [[setColumn, column], …], inner]`, true for a row
// when the set's table has a row equal in the matched columns that satisfies `inner`. Writes take no `exists`.
//
//	eq ne lt lte gt gte: Column, Value        {"eq":[column,value]}
//	inArray:             Column, Values       {"inArray":[column,[values]]}
//	isNull:              Column               {"isNull":column}
//	prefix:              Column, Value string {"prefix":[column,prefix]}
//	and or:              Nodes                {"and":[nodes]}
//	not:                 Nodes[0]             {"not":node}
//	exists:              Exists               {"exists":[set,[[setColumn,column],…],inner|null]}
type WhereNode struct {
	Op     WhereOp
	Column string
	Value  Scalar
	Values []Scalar
	Nodes  []WhereNode
	Exists *ExistsNode
}

// MatchPair is one pair an `exists` matches: a column of the set and the column of the enclosing side it equals.
type MatchPair struct{ SetColumn, Column string }

// ExistsNode is the data of an `exists`: the set's name, the matched pairs, the inner condition (nil for none).
type ExistsNode struct {
	Set   string
	Pairs []MatchPair
	Inner *WhereNode
}

// ExprOp is the operator of an Expression and of an ExprNode.
type ExprOp string

// The operations of expressions, and the node forms besides them.
const (
	// ExprLiteral is a literal scalar node (Value); with ExprNull it is the one non-operation form.
	ExprLiteral ExprOp = ""
	// ExprNull is the literal `null` (clears an `opt(...)` column).
	ExprNull ExprOp = "null"
	// ExprCol is a column (`{col}`); as an Expression, a ColumnRef.
	ExprCol ExprOp = "col"
	// ExprJSON is a `json(...)` cell's value (`{json}`).
	ExprJSON          ExprOp = "json"
	ExprAdd           ExprOp = "add"
	ExprSub           ExprOp = "sub"
	ExprMax           ExprOp = "max"
	ExprMin           ExprOp = "min"
	ExprConcat        ExprOp = "concat"
	ExprCoalesce      ExprOp = "coalesce"
	ExprReplacePrefix ExprOp = "replacePrefix"
	ExprWhen          ExprOp = "when"
)

// ExprNode is a compiled value expression: a literal (`null` clears), a column, a `json(...)` cell, or an operation.
//
//	ExprLiteral:        Value                        scalar
//	ExprNull:                                        null
//	ExprCol:            Column                       {"col":column}
//	ExprJSON:           Json                         {"json":value}
//	add sub max min concat coalesce: Args[0], Args[1]  {"add":[a,b]}
//	replacePrefix:      Args[0], From, To            {"replacePrefix":[subject,from,to]}
//	when:               Cond, Args[0], Args[1]       {"when":[condition,a,b]}
type ExprNode struct {
	Op     ExprOp
	Value  Scalar
	Column string
	Json   delta.JsonValue
	Args   []ExprNode
	From   string
	To     string
	Cond   *WhereNode
}

// SetEntry is one column of a compiled set and its new value.
type SetEntry struct {
	Column string
	Node   ExprNode
}

// SetNode is a compiled `set`: the new value of each changed column, every right side evaluated against the row
// before, in the order the plugin wrote them.
type SetNode []SetEntry

// Get is the node of column.
func (set SetNode) Get(column string) (ExprNode, bool) {
	for _, entry := range set {
		if entry.Column == column {
			return entry.Node, true
		}
	}
	return ExprNode{}, false
}

// Columns are the assigned columns, in order (Pi's `Object.keys(set)`).
func (set SetNode) Columns() []string {
	columns := make([]string, len(set))
	for index, entry := range set {
		columns[index] = entry.Column
	}
	return columns
}

// With is set with column set to node, after the others (an existing entry is replaced in place): Pi's
// `{ ...set, [column]: node }`.
func (set SetNode) With(column string, node ExprNode) SetNode {
	next := make(SetNode, len(set), len(set)+1)
	copy(next, set)
	for i := range next {
		if next[i].Column == column {
			next[i].Node = node
			return next
		}
	}
	return append(next, SetEntry{Column: column, Node: node})
}

// Undefined is JavaScript's `undefined` as a value of a set or a rows patch: it clears an `opt(...)` column (nil is
// `null`: a `json(...)` cell's JSON null, refused for scalar columns).
var Undefined = undefinedValue{}

type undefinedValue struct{}

// Expression is a value expression as a plugin builds it; compiled when staged. A ColumnRef is the Expression with
// Op ExprCol and its column name as the one argument.
type Expression struct {
	Op   ExprOp
	Args []any
	// Set is a ColumnRef's column set (`Set.refs`); empty for a reference of a table's own columns.
	Set string
}

// ColumnRef is a reference to a column of the row, as `(t) => …` callbacks get them. One of a column set
// (ColumnSet.Refs) carries the set's name in Set: a condition names the side it means by it (a query's own table, a
// joined set, an `exists`).
type ColumnRef = Expression

// ColumnRefs are the column references a table's callbacks get, by column name: `t["path"]`.
type ColumnRefs map[string]*ColumnRef

// NewColumnRef is the reference to column.
func NewColumnRef(column string) *ColumnRef { return &Expression{Op: ExprCol, Args: []any{column}} }

// NewSetColumnRef is the reference to column of the column set named set.
func NewSetColumnRef(column, set string) *ColumnRef {
	return &Expression{Op: ExprCol, Args: []any{column}, Set: set}
}

// isRef reports whether e is a column reference, and its column.
func (e *Expression) isRef() (string, bool) {
	if e == nil || e.Op != ExprCol || len(e.Args) != 1 {
		return "", false
	}
	column, ok := e.Args[0].(string)
	return column, ok
}

// Condition is a condition as a plugin builds it; compiled when staged or run.
type Condition struct {
	Op   WhereOp
	Args []any
}

// SetProp is one column of a plugin's set and its value: a plain value, an *Expression, or Undefined.
type SetProp struct {
	Name  string
	Value any
}

// SetOf is a plugin's `set`, ordered: new values of non-key columns, each a value or an expression of the column's
// type; Undefined clears an `opt(...)` column.
type SetOf []SetProp

// Assign is one column of a SetOf.
func Assign(name string, value any) SetProp { return SetProp{Name: name, Value: value} }

// Eq: the column equals value (`NULL` never matches).
func Eq(column *ColumnRef, value any) Condition { return Condition{WhereEq, []any{column, value}} }

// Ne: the column differs from value (`NULL` never matches).
func Ne(column *ColumnRef, value any) Condition { return Condition{WhereNe, []any{column, value}} }

// Lt: the column is below value.
func Lt(column *ColumnRef, value any) Condition { return Condition{WhereLt, []any{column, value}} }

// Lte: the column is at most value.
func Lte(column *ColumnRef, value any) Condition { return Condition{WhereLte, []any{column, value}} }

// Gt: the column is above value.
func Gt(column *ColumnRef, value any) Condition { return Condition{WhereGt, []any{column, value}} }

// Gte: the column is at least value.
func Gte(column *ColumnRef, value any) Condition { return Condition{WhereGte, []any{column, value}} }

// InArray: the column equals one of values (`[]` matches nothing); values is a slice.
func InArray(column *ColumnRef, values any) Condition {
	return Condition{WhereInArray, []any{column, values}}
}

// IsNull: the column is `NULL` (an `opt(...)` column without a value).
func IsNull(column *ColumnRef) Condition { return Condition{WhereIsNull, []any{column}} }

// Prefix: the text column starts with prefix, compared by code point (`""` matches every value).
func Prefix(column *ColumnRef, prefix string) Condition {
	return Condition{WherePrefix, []any{column, prefix}}
}

// And: every condition holds (`And()` always does).
func And(conditions ...Condition) Condition { return Condition{WhereAnd, conditionArgs(conditions)} }

// Or: some condition holds (`Or()` never does).
func Or(conditions ...Condition) Condition { return Condition{WhereOr, conditionArgs(conditions)} }

// Not: the condition does not hold (`NULL` stays `NULL`: it never matches).
func Not(condition Condition) Condition { return Condition{WhereNot, []any{condition}} }

func conditionArgs(conditions []Condition) []any {
	args := make([]any, len(conditions))
	for i, condition := range conditions {
		args[i] = condition
	}
	return args
}

// Add is `a + b` over integers; a result outside ±(2^53 - 1) fails (rule 10).
func Add(a, b any) *Expression { return &Expression{Op: ExprAdd, Args: []any{a, b}} }

// Sub is `a - b` over integers; a result outside ±(2^53 - 1) fails (rule 10).
func Sub(a, b any) *Expression { return &Expression{Op: ExprSub, Args: []any{a, b}} }

// Max is the larger of a and b (`int` or `num`).
func Max(a, b any) *Expression { return &Expression{Op: ExprMax, Args: []any{a, b}} }

// Min is the smaller of a and b (`int` or `num`).
func Min(a, b any) *Expression { return &Expression{Op: ExprMin, Args: []any{a, b}} }

// Concat is a followed by b.
func Concat(a, b any) *Expression { return &Expression{Op: ExprConcat, Args: []any{a, b}} }

// ReplacePrefix is value with its prefix from replaced by to; a value without the prefix stays as it is.
func ReplacePrefix(value any, from, to string) *Expression {
	return &Expression{Op: ExprReplacePrefix, Args: []any{value, from, to}}
}

// Coalesce is a unless it is `NULL`, else b.
func Coalesce(a, b any) *Expression { return &Expression{Op: ExprCoalesce, Args: []any{a, b}} }

// When is a where condition holds for the row, else b.
func When(condition Condition, a, b any) *Expression {
	return &Expression{Op: ExprWhen, Args: []any{condition, a, b}}
}

// MatchEntry is one column of a set matched to a column of the enclosing row; Match keeps the order Pi's object has.
type MatchEntry struct {
	Column string
	Ref    *ColumnRef
}

// Match are the columns of a set matched to columns of the enclosing row: `{ setColumn: column }`, in order.
type Match []MatchEntry

// Exists is: set's table has a row equal to the enclosing row in the matched columns (a leading prefix of the set's
// key or of one of its declared indexes) that satisfies inner (nil for none). In queries only.
func Exists(set *ColumnSet, match Match, inner func(row ColumnRefs) Condition) Condition {
	var condition any
	if inner != nil {
		condition = inner(set.Refs)
	}
	return Condition{WhereExists, []any{set, match, condition}}
}

// ExistsNodeOf is Exists with its inner condition as data (Compile's result): for a condition handed over as an
// argument. A trust boundary: inner is whatever arrived (a WhereNode, or decoded JSON), and is checked whole before
// anything is rendered.
func ExistsNodeOf(set *ColumnSet, match Match, inner any) Condition {
	return Condition{WhereExistsNode, []any{set, match, inner}}
}

// columnRefs is one ColumnRef per column, for a definition's `(t) => …` callbacks.
func columnRefs(columns []Column) ColumnRefs {
	refs := make(ColumnRefs, len(columns))
	for _, column := range columns {
		refs[column.Name] = NewColumnRef(column.Name)
	}
	return refs
}

// argAt is args[i], nil past the end (a JavaScript read of a missing element is undefined).
func argAt(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

func invalidArgs(format string, args ...any) error {
	return data.Invalid(data.ReasonArgs, fmt.Sprintf(format, args...))
}

// Resolved is a column as a scope resolved it: the column and its name in compiled nodes.
type Resolved struct {
	Column Column
	Name   string
}

// Scope is where a condition's columns are looked up: one side of a query, or two for a joined one.
type Scope struct {
	// Name is named in errors.
	Name string
	// Resolve is the column ref names here and its name in compiled nodes; false when it is of no side of this scope.
	// ref is a column reference (Op ExprCol).
	Resolve func(ref *ColumnRef) (Resolved, bool)
	// Exists is whether a condition here may test `exists` (a query's may, a write's may not).
	Exists bool
}

// TableScope is the scope of one table: its own references, and those of the set it exposes.
func TableScope(table *TableDefinition, exists bool) Scope {
	return Scope{
		Name:   table.Name,
		Exists: exists,
		Resolve: func(ref *ColumnRef) (Resolved, bool) {
			if ref.Set != "" && (table.Exposes == nil || ref.Set != table.Exposes.Name) {
				return Resolved{}, false
			}
			name, _ := ref.isRef()
			column, found := table.ByName[name]
			return Resolved{Column: column, Name: column.Name}, found
		},
	}
}

// SetScope is the scope of a column set alone: its own references (`Compile`, an `exists`' inner condition).
func SetScope(set Side) Scope {
	return Scope{
		Name:   set.Name,
		Exists: true,
		Resolve: func(ref *ColumnRef) (Resolved, bool) {
			if ref.Set != set.Name {
				return Resolved{}, false
			}
			name, _ := ref.isRef()
			column, found := set.ByName[name]
			return Resolved{Column: column, Name: column.Name}, found
		},
	}
}

// PartnerName is the name of a joined set's column in compiled nodes, orders and cursors: `<set>:<column>`.
func PartnerName(set, column string) string { return set + ":" + column }

// JoinedScope is the scope of a query of own joined with set: the partner's columns are named `<set>:<column>`.
func JoinedScope(own Scope, set Side) Scope {
	return Scope{
		Name:   own.Name,
		Exists: true,
		Resolve: func(ref *ColumnRef) (Resolved, bool) {
			if ref.Set != set.Name {
				return own.Resolve(ref)
			}
			name, _ := ref.isRef()
			column, found := set.ByName[name]
			return Resolved{Column: column, Name: PartnerName(set.Name, column.Name)}, found
		},
	}
}

// CompileWhere compiles a plugin's condition over table's columns for a write (a trust boundary: plugin values): known
// scalar columns, operands of their type made storable (`invalid/args` otherwise, `Undefined` included: ask with
// `IsNull`, rule 16). No `exists`. condition is a Condition (or *Condition).
func CompileWhere(table *TableDefinition, condition any, what string) (WhereNode, error) {
	return compileCondition(TableScope(table, false), condition, what, &Expected{})
}

// Compile is a condition over set's columns as plain data (slice 14 §6), for `Query.WhereNode` and `ExistsNodeOf`.
func Compile(set *ColumnSet, condition Condition) (WhereNode, error) {
	return compileCondition(SetScope(set.Side()), condition, "compile", &Expected{})
}

// ExpectedSet is the columns of one set an `exists` was written for, in the order they were first used.
type ExpectedSet struct {
	Set     string
	Columns []Column
}

// Expected are the columns of other plugins' sets a condition was written for, by set name (in the order the sets
// were first used): what `Exists(Set, …)` matched and compared, as the builder's `Set` declares them. A read checks
// them against the table bound to the set when it opens (slice 14 §5: no versions, checked by name and type).
type Expected []ExpectedSet

// Clone is a copy that adding to does not change e.
func (e Expected) Clone() Expected {
	copied := make(Expected, len(e))
	for i, each := range e {
		copied[i] = ExpectedSet{Set: each.Set, Columns: slices.Clone(each.Columns)}
	}
	return copied
}

// add records column of set; a column already recorded keeps its place (a Map's `set`).
func (e *Expected) add(set string, column Column) {
	for i := range *e {
		if (*e)[i].Set != set {
			continue
		}
		for j, each := range (*e)[i].Columns {
			if each.Name == column.Name {
				(*e)[i].Columns[j] = column
				return
			}
		}
		(*e)[i].Columns = append((*e)[i].Columns, column)
		return
	}
	*e = append(*e, ExpectedSet{Set: set, Columns: []Column{column}})
}

// compileCondition compiles a plugin's condition in scope (a trust boundary), as CompileWhere does for one table. The
// set columns its `exists` use are added to expected.
func compileCondition(scope Scope, condition any, what string, expected *Expected) (WhereNode, error) {
	var c Condition
	switch typed := condition.(type) {
	case Condition:
		c = typed
	case *Condition:
		if typed == nil {
			return WhereNode{}, invalidArgs("%s: %s: expected a condition", scope.Name, what)
		}
		c = *typed
	default:
		return WhereNode{}, invalidArgs("%s: %s: expected a condition", scope.Name, what)
	}
	switch c.Op {
	case WhereAnd, WhereOr:
		nodes := make([]WhereNode, len(c.Args))
		for i, each := range c.Args {
			node, err := compileCondition(scope, each, what, expected)
			if err != nil {
				return WhereNode{}, err
			}
			nodes[i] = node
		}
		return WhereNode{Op: c.Op, Nodes: nodes}, nil
	case WhereNot:
		node, err := compileCondition(scope, argAt(c.Args, 0), what, expected)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: WhereNot, Nodes: []WhereNode{node}}, nil
	case WhereExists, WhereExistsNode:
		exists, err := compileExists(scope, c, what, expected)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: WhereExists, Exists: exists}, nil
	case WhereIsNull:
		resolved, err := scalarColumn(scope, argAt(c.Args, 0), what)
		if err == nil {
			resolved, err = ordered(scope, resolved, c.Op, what)
		}
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: WhereIsNull, Column: resolved.Name}, nil
	case WherePrefix:
		resolved, err := scalarColumn(scope, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		if resolved, err = ordered(scope, resolved, c.Op, what); err != nil {
			return WhereNode{}, err
		}
		if resolved.Column.Type != ColStr {
			return WhereNode{}, invalidArgs("%s: %s: prefix needs a str() column", scope.Name, what)
		}
		value, err := operand(resolved.Column, argAt(c.Args, 1), what)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: WherePrefix, Column: resolved.Name, Value: value}, nil
	case WhereInArray:
		resolved, err := scalarColumn(scope, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		list, ok := sliceOf(argAt(c.Args, 1))
		if !ok {
			return WhereNode{}, invalidArgs("%s: %s: inArray needs an array", scope.Name, what)
		}
		values := make([]Scalar, len(list))
		for i, each := range list {
			value, err := operand(resolved.Column, each, what)
			if err != nil {
				return WhereNode{}, err
			}
			values[i] = value
		}
		return WhereNode{Op: WhereInArray, Column: resolved.Name, Values: values}, nil
	default:
		// Pi's default arm: any other op is read as one of the six comparisons (an unknown op becomes that shape too).
		resolved, err := scalarColumn(scope, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		if c.Op != WhereEq && c.Op != WhereNe {
			if resolved, err = ordered(scope, resolved, c.Op, what); err != nil {
				return WhereNode{}, err
			}
		}
		value, err := operand(resolved.Column, argAt(c.Args, 1), what)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: c.Op, Column: resolved.Name, Value: value}, nil
	}
}

// compileExists compiles an `exists` of scope: the set's matched columns are a leading prefix of its key or of one of
// its declared indexes, each of the type (and host kind) of the enclosing column it is matched to; the inner
// condition is the callback's, compiled over the set, or a node, checked as `WhereNode` checks one.
//
// Go forced difference: a condition's set argument is a *ColumnSet by its type; a hand-built Condition with another
// value there is refused (Pi's TypeError), and so is a match that is not a Match.
func compileExists(scope Scope, condition Condition, what string, expected *Expected) (*ExistsNode, error) {
	set, isSet := argAt(condition.Args, 0).(*ColumnSet)
	if !isSet || set == nil {
		return nil, invalidArgs("%s: %s: exists needs a column set", scope.Name, what)
	}
	inner := argAt(condition.Args, 2)
	if !scope.Exists {
		return nil, invalidArgs("%s: %s: exists is for queries, not for writes", scope.Name, what)
	}
	match, isMatch := argAt(condition.Args, 1).(Match)
	if !isMatch {
		return nil, invalidArgs("%s: %s: exists needs a match", scope.Name, what)
	}
	pairs := make([]MatchPair, len(match))
	names := make([]string, len(match))
	for i, entry := range match {
		column, found := set.ByName[entry.Column]
		var outer Resolved
		var resolved bool
		if _, isRef := entry.Ref.isRef(); isRef {
			outer, resolved = scope.Resolve(entry.Ref)
		}
		if !found || !resolved {
			return nil, invalidArgs("%s: %s: exists(%s): %s is matched to no column", scope.Name, what, set.Name, quote(entry.Column))
		}
		if err := CheckPair(set.Name, column, outer.Column, scope.Name+": "+what); err != nil {
			return nil, err
		}
		pairs[i] = MatchPair{SetColumn: entry.Column, Column: outer.Name}
		names[i] = entry.Column
	}
	if err := CheckMatch(set.Matched(), names, scope.Name+": "+what); err != nil {
		return nil, err
	}
	var node *WhereNode
	if condition.Op == WhereExistsNode {
		raw := nodeData(inner)
		if err := CheckNode(raw, &Side{Name: set.Name, ByName: set.ByName}, fmt.Sprintf("%s: %s: existsNode(%s)", scope.Name, what, set.Name)); err != nil {
			return nil, err
		}
		decoded, err := DecodeWhereNode(raw)
		if err != nil {
			return nil, err
		}
		node = &decoded
	} else if inner != nil {
		compiled, err := compileCondition(SetScope(set.Side()), inner, what, expected)
		if err != nil {
			return nil, err
		}
		node = &compiled
	}
	// What this `exists` uses of the set, as its author's `Set` declares it.
	used := names
	if node != nil {
		used = append(slices.Clone(names), ColumnsOf(*node)...)
	}
	for _, name := range used {
		expected.add(set.Name, set.ByName[name])
	}
	return &ExistsNode{Set: set.Name, Pairs: pairs, Inner: node}, nil
}

// nodeData is a condition handed over as data in the form CheckNode reads: a compiled node as its JSON, anything else
// (decoded JSON) as it is.
func nodeData(inner any) any {
	if node, isNode := inner.(WhereNode); isNode {
		return node.JSON()
	}
	return inner
}

// ColumnsOf is the columns a condition uses of its own side: the ones it compares, and the ones its `exists` are
// matched to. An `exists`' inner condition is another side's.
func ColumnsOf(node WhereNode) []string {
	switch node.Op {
	case WhereAnd, WhereOr, WhereNot:
		var columns []string
		for _, each := range node.Nodes {
			columns = append(columns, ColumnsOf(each)...)
		}
		return columns
	case WhereExists:
		columns := make([]string, len(node.Exists.Pairs))
		for i, pair := range node.Exists.Pairs {
			columns[i] = pair.Column
		}
		return columns
	}
	// isNull and the remaining nodes are `{ [op]: [column, operand] }`.
	return []string{node.Column}
}

// ExistsIn is the `exists` a condition tests on its own side (not the ones nested inside them).
func ExistsIn(node WhereNode) []*ExistsNode {
	switch node.Op {
	case WhereAnd, WhereOr, WhereNot:
		var found []*ExistsNode
		for _, each := range node.Nodes {
			found = append(found, ExistsIn(each)...)
		}
		return found
	case WhereExists:
		return []*ExistsNode{node.Exists}
	}
	return nil
}

// MatchedSet is a set as a match is checked against it: its name, columns, key and declared indexes.
type MatchedSet struct {
	Side
	Key     []string
	Indexes IndexRecords
}

// CheckMatch refuses a match that is no leading prefix of set's key or of one of its declared indexes
// (`invalid/args`, listing them): two tables are matched only by such an equality (the bounding rule, slice 14 §5).
func CheckMatch(set MatchedSet, matched []string, what string) error {
	paths := [][]string{set.Key}
	for _, index := range set.Indexes {
		paths = append(paths, index.Columns)
	}
	for _, path := range paths {
		if len(matched) > 0 && len(matched) <= len(path) {
			fits := true
			for _, column := range path[:len(matched)] {
				fits = fits && slices.Contains(matched, column)
			}
			if fits {
				return nil
			}
		}
	}
	listed := []string{fmt.Sprintf("key (%s)", strings.Join(set.Key, ", "))}
	for _, index := range set.Indexes {
		listed = append(listed, fmt.Sprintf("%s (%s)", index.Name, strings.Join(index.Columns, ", ")))
	}
	return invalidArgs("%s: %s is matched by (%s), no leading prefix of its %s", what, set.Name, strings.Join(matched, ", "), strings.Join(listed, " or "))
}

// CheckPair refuses a pair of matched columns of different types or host kinds (`invalid/args`, naming the set's
// column).
func CheckPair(set string, column, outer Column, what string) error {
	a, b := ColumnRecordOf(column), ColumnRecordOf(outer)
	if a.Type == ColJson || a.Type != b.Type || (a.Host == nil) != (b.Host == nil) || (a.Host != nil && *a.Host != *b.Host) {
		return invalidArgs("%s: %s column %s and the column it is matched to differ in type", what, set, quote(column.Name))
	}
	return nil
}

// sliceOf is value as a list when it is a slice of any element type (`Array.isArray`).
func sliceOf(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		return toAny(typed), true
	case []int:
		return toAny(typed), true
	case []int64:
		return toAny(typed), true
	case []float64:
		return toAny(typed), true
	case []bool:
		return toAny(typed), true
	}
	return nil, false
}

func toAny[T any](list []T) []any {
	out := make([]any, len(list))
	for i, each := range list {
		out[i] = each
	}
	return out
}

// CompileSet compiles a plugin's `set` (a SetOf, or a map of column values taken in sorted order; plain values or a
// callback's result) for table (a trust boundary): known non-key columns (rule 13), at least one (rule 14),
// `Undefined` clearing an `opt(...)` column as the explicit `null` (a nil value is a `json(...)` cell's JSON `null`,
// refused for other columns), an expression whose result may be `NULL` only for an `opt(...)` column; integer
// arithmetic only on `int` columns.
func CompileSet(table *TableDefinition, set any, what string) (SetNode, error) {
	var props SetOf
	switch typed := set.(type) {
	case SetOf:
		props = typed
	case []SetProp:
		props = typed
	case map[string]any:
		names := make([]string, 0, len(typed))
		for name := range typed {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			props = append(props, SetProp{Name: name, Value: typed[name]})
		}
	default:
		return nil, invalidArgs("%s: %s: expected an object of column values", table.Name, what)
	}
	if len(props) == 0 {
		return nil, invalidArgs("%s: %s: the set is empty", table.Name, what)
	}
	node := make(SetNode, 0, len(props))
	for _, prop := range props {
		name, value := prop.Name, prop.Value
		column, found := table.ByName[name]
		if !found {
			return nil, invalidArgs("%s: %s: no column %s", table.Name, what, quote(name))
		}
		if slices.Contains(table.Key, name) {
			return nil, invalidArgs("%s: %s: key column %s cannot change (delete and put)", table.Name, what, quote(name))
		}
		where := fmt.Sprintf("%s: column %s", what, quote(name))
		expression, isExpression := value.(*Expression)
		var entry ExprNode
		switch {
		case value == Undefined:
			if !column.Optional {
				return nil, invalidArgs("%s: %s is not opt(...) and cannot be cleared", table.Name, where)
			}
			entry = ExprNode{Op: ExprNull}
		case value == nil && column.Type != ColJson:
			// A row never holds an explicit `null` (decision "Nullable columns and the set form"); a JSON `null` is a value.
			return nil, invalidArgs("%s: %s: null is no %s value (undefined clears)", table.Name, where, column.Type)
		case isExpression:
			if column.Type == ColJson || column.Host != nil {
				kind := "host"
				if column.Type == ColJson {
					kind = "json"
				}
				return nil, invalidArgs("%s: %s: %s(...) columns take plain values", table.Name, where, kind)
			}
			compiled, err := compileExpr(table, expression, column.Type, where)
			if err != nil {
				return nil, err
			}
			if compiled.nullable && !column.Optional {
				return nil, invalidArgs("%s: %s: the expression may be NULL; the column is not opt(...)", table.Name, where)
			}
			entry = compiled.node
		case column.Type == ColJson:
			cell, err := EncodeCell(column, value, fmt.Sprintf("%s: %s", table.Name, where))
			if err != nil {
				return nil, err
			}
			entry = ExprNode{Op: ExprJSON, Json: cell.Stored}
		default:
			scalar, err := encodeScalar(column, value, fmt.Sprintf("%s: %s", table.Name, where))
			if err != nil {
				return nil, err
			}
			entry = ExprNode{Op: ExprLiteral, Value: scalar}
		}
		// A JavaScript object holds a column once: a repeated name keeps the first position, the last value.
		replaced := false
		for i := range node {
			if node[i].Column == name {
				node[i].Node, replaced = entry, true
			}
		}
		if !replaced {
			node = append(node, SetEntry{Column: name, Node: entry})
		}
	}
	return node, nil
}

// quote is a name between quotes, raw: a JavaScript template's `"${name}"`.
func quote(name string) string { return `"` + name + `"` }

// compiled is a compiled expression and whether its result may be `NULL`.
type compiled struct {
	node     ExprNode
	nullable bool
}

func compileExpr(table *TableDefinition, value any, typ ColumnType, what string) (compiled, error) {
	expression, isExpression := value.(*Expression)
	if !isExpression || expression == nil {
		if value == nil || value == Undefined {
			return compiled{node: ExprNode{Op: ExprNull}, nullable: true}, nil
		}
		scalar, err := CheckScalar(typ, value, fmt.Sprintf("%s: %s", table.Name, what))
		if err != nil {
			return compiled{}, err
		}
		return compiled{node: ExprNode{Op: ExprLiteral, Value: scalar}}, nil
	}
	refuse := func(message string) (compiled, error) {
		return compiled{}, invalidArgs("%s: %s: %s", table.Name, what, message)
	}
	args := expression.Args
	switch expression.Op {
	case ExprCol:
		name := argText(argAt(args, 0))
		column, found := table.ByName[name]
		if !found {
			return refuse(fmt.Sprintf("no column %s", quote(name)))
		}
		if column.Type != typ {
			return refuse(fmt.Sprintf("column %s is %s, not %s", quote(column.Name), column.Type, typ))
		}
		if column.Host != nil {
			return refuse(fmt.Sprintf("host(...) column %s holds addresses", quote(column.Name)))
		}
		return compiled{node: ExprNode{Op: ExprCol, Column: column.Name}, nullable: column.Optional}, nil
	case ExprAdd, ExprSub, ExprMax, ExprMin, ExprConcat:
		var types []ColumnType
		var names string
		switch expression.Op {
		case ExprConcat:
			types, names = []ColumnType{ColStr}, "str"
		case ExprAdd, ExprSub:
			types, names = []ColumnType{ColInt}, "int"
		default:
			types, names = []ColumnType{ColInt, ColNum}, "int or num"
		}
		if !slices.Contains(types, typ) {
			return refuse(fmt.Sprintf("%s gives %s, the column is %s", expression.Op, names, typ))
		}
		a, err := compileExpr(table, argAt(args, 0), typ, what)
		if err != nil {
			return compiled{}, err
		}
		b, err := compileExpr(table, argAt(args, 1), typ, what)
		if err != nil {
			return compiled{}, err
		}
		return compiled{node: ExprNode{Op: expression.Op, Args: []ExprNode{a.node, b.node}}, nullable: a.nullable || b.nullable}, nil
	case ExprCoalesce:
		a, err := compileExpr(table, argAt(args, 0), typ, what)
		if err != nil {
			return compiled{}, err
		}
		b, err := compileExpr(table, argAt(args, 1), typ, what)
		if err != nil {
			return compiled{}, err
		}
		return compiled{node: ExprNode{Op: ExprCoalesce, Args: []ExprNode{a.node, b.node}}, nullable: a.nullable && b.nullable}, nil
	case ExprReplacePrefix:
		if typ != ColStr {
			return refuse(fmt.Sprintf("replacePrefix gives str, the column is %s", typ))
		}
		subject, err := compileExpr(table, argAt(args, 0), typ, what)
		if err != nil {
			return compiled{}, err
		}
		from, err := CheckScalar(ColStr, argAt(args, 1), fmt.Sprintf("%s: %s", table.Name, what))
		if err != nil {
			return compiled{}, err
		}
		to, err := CheckScalar(ColStr, argAt(args, 2), fmt.Sprintf("%s: %s", table.Name, what))
		if err != nil {
			return compiled{}, err
		}
		return compiled{
			node:     ExprNode{Op: ExprReplacePrefix, Args: []ExprNode{subject.node}, From: from.(string), To: to.(string)},
			nullable: subject.nullable,
		}, nil
	case ExprWhen:
		condition, err := CompileWhere(table, argAt(args, 0), what)
		if err != nil {
			return compiled{}, err
		}
		a, err := compileExpr(table, argAt(args, 1), typ, what)
		if err != nil {
			return compiled{}, err
		}
		b, err := compileExpr(table, argAt(args, 2), typ, what)
		if err != nil {
			return compiled{}, err
		}
		return compiled{node: ExprNode{Op: ExprWhen, Cond: &condition, Args: []ExprNode{a.node, b.node}}, nullable: a.nullable || b.nullable}, nil
	}
	// Pi's switch has no arm for another op: its function returns undefined and the caller reads it as an empty result.
	return refuse(fmt.Sprintf("unknown operation %s", quote(string(expression.Op))))
}

// argText is `String(value)` for a column name argument.
func argText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return "null"
	}
	return fmt.Sprint(value)
}

// scalarColumn is the column a condition compares: a reference to a known scalar column of scope.
func scalarColumn(scope Scope, ref any, what string) (Resolved, error) {
	expression, _ := ref.(*Expression)
	name, isRef := expression.isRef()
	if !isRef {
		return Resolved{}, invalidArgs("%s: %s: expected a column reference", scope.Name, what)
	}
	resolved, found := scope.Resolve(expression)
	if !found {
		return Resolved{}, invalidArgs("%s: %s: no column %s", scope.Name, what, quote(name))
	}
	if resolved.Column.Type == ColJson {
		return Resolved{}, invalidArgs("%s: %s: json(...) column %s is not queryable", scope.Name, what, quote(name))
	}
	return resolved, nil
}

// ordered is resolved for a comparison by order, `IsNull` or `Prefix`: a `host(...)` column compares only with Eq, Ne
// and InArray.
func ordered(scope Scope, resolved Resolved, op WhereOp, what string) (Resolved, error) {
	if column := resolved.Column; column.Host != nil {
		return Resolved{}, invalidArgs("%s: %s: %s on host(...) column %s (eq, ne, inArray only)", scope.Name, what, op, quote(column.Name))
	}
	return resolved, nil
}

// operand is an operand compared with column, made storable (an address as its key for a `host(...)` column);
// `Undefined` and nil are refused (rule 16).
func operand(column Column, value any, what string) (Scalar, error) {
	if value == nil || value == Undefined {
		text := "null"
		if value == Undefined {
			text = "undefined"
		}
		return nil, invalidArgs("%s: column %s compared with %s; ask with isNull", what, quote(column.Name), text)
	}
	// scalarColumn refused json columns.
	return encodeScalar(column, value, fmt.Sprintf("%s: column %s", what, quote(column.Name)))
}

// ---------------------------------------------------------------------------------------------------------------
// The data form: JSON as Pi's nodes serialize, and back.

// JSON is the node as a JSON value tree (ordered objects, scalars as they are); MarshalJSON writes it.
func (node WhereNode) JSON() any {
	switch node.Op {
	case WhereAnd, WhereOr:
		nodes := make([]any, len(node.Nodes))
		for i, each := range node.Nodes {
			nodes[i] = each.JSON()
		}
		return delta.JsonObjectOf(string(node.Op), nodes)
	case WhereNot:
		return delta.JsonObjectOf("not", node.Nodes[0].JSON())
	case WhereExists:
		pairs := make([]any, len(node.Exists.Pairs))
		for i, each := range node.Exists.Pairs {
			pairs[i] = []any{each.SetColumn, each.Column}
		}
		var inner any
		if node.Exists.Inner != nil {
			inner = node.Exists.Inner.JSON()
		}
		return delta.JsonObjectOf("exists", []any{node.Exists.Set, pairs, inner})
	case WhereIsNull:
		return delta.JsonObjectOf("isNull", node.Column)
	case WhereInArray:
		return delta.JsonObjectOf("inArray", []any{node.Column, toAny(node.Values)})
	default:
		return delta.JsonObjectOf(string(node.Op), []any{node.Column, node.Value})
	}
}

// MarshalJSON is the node as JSON.stringify writes Pi's.
func (node WhereNode) MarshalJSON() ([]byte, error) { return node.AppendStringify(nil) }

// AppendStringify appends the node's JSON.stringify text to dst.
func (node WhereNode) AppendStringify(dst []byte) ([]byte, error) {
	return jsstring.AppendMarshalJSON(dst, node.JSON())
}

// JSON is the node as a JSON value tree.
func (node ExprNode) JSON() any {
	switch node.Op {
	case ExprLiteral:
		return node.Value
	case ExprNull:
		return nil
	case ExprCol:
		return delta.JsonObjectOf("col", node.Column)
	case ExprJSON:
		return delta.JsonObjectOf("json", node.Json)
	case ExprReplacePrefix:
		return delta.JsonObjectOf("replacePrefix", []any{node.Args[0].JSON(), node.From, node.To})
	case ExprWhen:
		return delta.JsonObjectOf("when", []any{node.Cond.JSON(), node.Args[0].JSON(), node.Args[1].JSON()})
	default:
		return delta.JsonObjectOf(string(node.Op), []any{node.Args[0].JSON(), node.Args[1].JSON()})
	}
}

// MarshalJSON is the node as JSON.stringify writes Pi's.
func (node ExprNode) MarshalJSON() ([]byte, error) { return node.AppendStringify(nil) }

// AppendStringify appends the node's JSON.stringify text to dst.
func (node ExprNode) AppendStringify(dst []byte) ([]byte, error) {
	return jsstring.AppendMarshalJSON(dst, node.JSON())
}

// JSON is the set as one ordered JSON object.
func (set SetNode) JSON() any {
	object := delta.NewJsonObject(len(set))
	for _, entry := range set {
		object.Set(entry.Column, entry.Node.JSON())
	}
	return object
}

// MarshalJSON is the set as JSON.stringify writes Pi's.
func (set SetNode) MarshalJSON() ([]byte, error) { return set.AppendStringify(nil) }

// AppendStringify appends the set's JSON.stringify text to dst.
func (set SetNode) AppendStringify(dst []byte) ([]byte, error) {
	return jsstring.AppendMarshalJSON(dst, set.JSON())
}

func decodeError(what, problem string) error {
	return errors.New("store: " + what + ": " + problem)
}

// decodeScalar reads a JSON scalar: a string, a bool, or a number (an integral safe one as int64, else float64).
func decodeScalar(raw any, what string) (Scalar, error) {
	switch typed := raw.(type) {
	case string, bool:
		return typed, nil
	case float64:
		if integer, safe := safeInteger(typed); safe {
			return integer, nil
		}
		return typed, nil
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	}
	return nil, decodeError(what, "expected a string, a number or a boolean")
}

// MaxSafeInteger64 is 2^53 - 1 as a float64, the bound of decoded integral numbers.
const MaxSafeInteger64 = float64(1<<53 - 1)

// single is the one entry of a node object.
func single(raw any, what string) (string, any, error) {
	object, ok := raw.(*delta.JsonObject)
	if !ok || object.Len() != 1 {
		return "", nil, decodeError(what, "expected an object with one operator")
	}
	for key, value := range object.All() {
		return key, value, nil
	}
	return "", nil, decodeError(what, "expected an object with one operator")
}

func pair(raw any, size int, what string) ([]any, error) {
	list, ok := raw.([]any)
	if !ok || len(list) != size {
		return nil, decodeError(what, fmt.Sprintf("expected an array of %d", size))
	}
	return list, nil
}

// DecodeWhereNode reads a WhereNode from its JSON (a decoded *delta.JsonObject).
func DecodeWhereNode(raw any) (WhereNode, error) {
	const what = "where node"
	key, value, err := single(raw, what)
	if err != nil {
		return WhereNode{}, err
	}
	op := WhereOp(key)
	switch {
	case op == WhereAnd || op == WhereOr:
		list, ok := value.([]any)
		if !ok {
			return WhereNode{}, decodeError(what, "expected an array of nodes")
		}
		nodes := make([]WhereNode, len(list))
		for i, each := range list {
			if nodes[i], err = DecodeWhereNode(each); err != nil {
				return WhereNode{}, err
			}
		}
		return WhereNode{Op: op, Nodes: nodes}, nil
	case op == WhereNot:
		inner, err := DecodeWhereNode(value)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: op, Nodes: []WhereNode{inner}}, nil
	case op == WhereExists:
		parts, err := pair(value, 3, what)
		if err != nil {
			return WhereNode{}, err
		}
		set, isText := parts[0].(string)
		list, isList := parts[1].([]any)
		if !isText || !isList {
			return WhereNode{}, decodeError(what, "exists expects [set, pairs, inner]")
		}
		exists := &ExistsNode{Set: set, Pairs: make([]MatchPair, len(list))}
		for i, each := range list {
			entry, ok := each.([]any)
			if !ok || len(entry) != 2 {
				return WhereNode{}, decodeError(what, "an exists pair is [setColumn, column]")
			}
			setColumn, isSetColumn := entry[0].(string)
			column, isColumn := entry[1].(string)
			if !isSetColumn || !isColumn {
				return WhereNode{}, decodeError(what, "an exists pair is [setColumn, column]")
			}
			exists.Pairs[i] = MatchPair{SetColumn: setColumn, Column: column}
		}
		if parts[2] != nil {
			inner, err := DecodeWhereNode(parts[2])
			if err != nil {
				return WhereNode{}, err
			}
			exists.Inner = &inner
		}
		return WhereNode{Op: op, Exists: exists}, nil
	case op == WhereIsNull:
		column, ok := value.(string)
		if !ok {
			return WhereNode{}, decodeError(what, "isNull expects a column name")
		}
		return WhereNode{Op: op, Column: column}, nil
	case op == WhereInArray:
		parts, err := pair(value, 2, what)
		if err != nil {
			return WhereNode{}, err
		}
		column, ok := parts[0].(string)
		list, isList := parts[1].([]any)
		if !ok || !isList {
			return WhereNode{}, decodeError(what, "inArray expects [column, values]")
		}
		values := make([]Scalar, len(list))
		for i, each := range list {
			if values[i], err = decodeScalar(each, what); err != nil {
				return WhereNode{}, err
			}
		}
		return WhereNode{Op: op, Column: column, Values: values}, nil
	case op == WherePrefix || op.isComparison():
		parts, err := pair(value, 2, what)
		if err != nil {
			return WhereNode{}, err
		}
		column, ok := parts[0].(string)
		if !ok {
			return WhereNode{}, decodeError(what, string(op)+" expects [column, value]")
		}
		scalar, err := decodeScalar(parts[1], what)
		if err != nil {
			return WhereNode{}, err
		}
		if _, isText := scalar.(string); op == WherePrefix && !isText {
			return WhereNode{}, decodeError(what, "prefix expects a string")
		}
		return WhereNode{Op: op, Column: column, Value: scalar}, nil
	}
	return WhereNode{}, decodeError(what, fmt.Sprintf("unknown operator %s", quote(key)))
}

// DecodeExprNode reads an ExprNode from its JSON.
func DecodeExprNode(raw any) (ExprNode, error) {
	const what = "expression node"
	switch typed := raw.(type) {
	case nil:
		return ExprNode{Op: ExprNull}, nil
	case string, bool, float64, int64, int:
		scalar, err := decodeScalar(typed, what)
		if err != nil {
			return ExprNode{}, err
		}
		return ExprNode{Op: ExprLiteral, Value: scalar}, nil
	}
	key, value, err := single(raw, what)
	if err != nil {
		return ExprNode{}, err
	}
	op := ExprOp(key)
	switch op {
	case ExprCol:
		column, ok := value.(string)
		if !ok {
			return ExprNode{}, decodeError(what, "col expects a column name")
		}
		return ExprNode{Op: ExprCol, Column: column}, nil
	case ExprJSON:
		return ExprNode{Op: ExprJSON, Json: value}, nil
	case ExprAdd, ExprSub, ExprMax, ExprMin, ExprConcat, ExprCoalesce:
		parts, err := pair(value, 2, what)
		if err != nil {
			return ExprNode{}, err
		}
		a, err := DecodeExprNode(parts[0])
		if err != nil {
			return ExprNode{}, err
		}
		b, err := DecodeExprNode(parts[1])
		if err != nil {
			return ExprNode{}, err
		}
		return ExprNode{Op: op, Args: []ExprNode{a, b}}, nil
	case ExprReplacePrefix:
		parts, err := pair(value, 3, what)
		if err != nil {
			return ExprNode{}, err
		}
		subject, err := DecodeExprNode(parts[0])
		if err != nil {
			return ExprNode{}, err
		}
		from, okFrom := parts[1].(string)
		to, okTo := parts[2].(string)
		if !okFrom || !okTo {
			return ExprNode{}, decodeError(what, "replacePrefix expects [subject, from, to]")
		}
		return ExprNode{Op: op, Args: []ExprNode{subject}, From: from, To: to}, nil
	case ExprWhen:
		parts, err := pair(value, 3, what)
		if err != nil {
			return ExprNode{}, err
		}
		condition, err := DecodeWhereNode(parts[0])
		if err != nil {
			return ExprNode{}, err
		}
		a, err := DecodeExprNode(parts[1])
		if err != nil {
			return ExprNode{}, err
		}
		b, err := DecodeExprNode(parts[2])
		if err != nil {
			return ExprNode{}, err
		}
		return ExprNode{Op: op, Cond: &condition, Args: []ExprNode{a, b}}, nil
	}
	return ExprNode{}, decodeError(what, fmt.Sprintf("unknown operation %s", quote(key)))
}

// DecodeSetNode reads a SetNode from its JSON (an ordered object of expression nodes).
func DecodeSetNode(raw any) (SetNode, error) {
	object, ok := raw.(*delta.JsonObject)
	if !ok {
		return nil, decodeError("set node", "expected an object")
	}
	set := make(SetNode, 0, object.Len())
	for column, value := range object.All() {
		node, err := DecodeExprNode(value)
		if err != nil {
			return nil, err
		}
		set = append(set, SetEntry{Column: column, Node: node})
	}
	return set, nil
}

// ---------------------------------------------------------------------------------------------------------------
// Conditions handed over as data (slice 14 §6).

// NodeDepth is how deep a condition handed over as data may nest (`and`, `or`, `not`, `exists`).
const NodeDepth = 16

// Side is the parts of a table or a column set a condition is checked against: its name and columns.
type Side struct {
	Name   string
	ByName map[string]Column
}

// isHostKey reports whether key is the canonical stored key of an address of host's kind.
func isHostKey(host *HostColumn, key string) bool {
	address, err := host.Address(key)
	if err != nil {
		return false
	}
	canonical, err := host.Key(address, "")
	return err == nil && canonical == key
}

// CheckNode checks a condition handed over as data (a decoded *delta.JsonObject, as WhereNode.JSON writes it) against
// side's columns (a trust boundary): one known kind per node, operand shapes and types (a `host(...)` column's operands
// are stored keys), every column in side, a depth bound. A nil side checks the structure only: the inner condition of
// a nested `exists` is over another set, its columns checked when the read opens and the set's table is known.
// `invalid/args` otherwise.
func CheckNode(node any, side *Side, what string) error { return checkNode(node, side, what, 0) }

func checkNode(node any, side *Side, what string, depth int) error {
	refuse := func(message string) error { return invalidArgs("%s: %s", what, message) }
	if depth > NodeDepth {
		return refuse(fmt.Sprintf("nested deeper than %d", NodeDepth))
	}
	object, isObject := node.(*delta.JsonObject)
	if !isObject || object == nil {
		return refuse("a condition node is an object")
	}
	if object.Len() != 1 {
		return refuse("a condition node has one kind")
	}
	var kind string
	var value any
	for key, each := range object.All() {
		kind, value = key, each
	}
	column := func(name any) (*Column, error) {
		text, isText := name.(string)
		if !isText {
			return nil, refuse("a column is named by a string")
		}
		if side == nil {
			return nil, nil
		}
		found, ok := side.ByName[text]
		if !ok {
			return nil, refuse(fmt.Sprintf("no column %s in %s", quote(text), side.Name))
		}
		if found.Type == ColJson {
			return nil, refuse(fmt.Sprintf("json(...) column %s is not queryable", quote(text)))
		}
		return &found, nil
	}
	operand := func(of *Column, each any) error {
		_, isText := each.(string)
		_, isFlag := each.(bool)
		number, isNumber := numberOf(each)
		if !isText && !isFlag && !(isNumber && isFinite(number)) {
			return refuse("an operand is a string, a number or a boolean")
		}
		if of == nil {
			return nil
		}
		if _, err := CheckScalar(of.Type, each, what); err != nil {
			return err
		}
		// A `host(...)` column holds canonical address keys: anything else would silently match nothing.
		if of.Type == ColStr && of.Host != nil && !isHostKey(of.Host, each.(string)) {
			return refuse(fmt.Sprintf("%s is no address key of %s (column %s)", quote(each.(string)), of.Host.Kind.Kind, quote(of.Name)))
		}
		return nil
	}
	// A `host(...)` column compares only with eq, ne and inArray, as `compile` has it.
	ordered := func(of *Column) error {
		if of != nil && of.Type == ColStr && of.Host != nil {
			return refuse(fmt.Sprintf("%s on host(...) column %s", kind, quote(of.Name)))
		}
		return nil
	}
	pair := func() ([]any, error) {
		list, ok := value.([]any)
		if !ok || len(list) != 2 {
			return nil, refuse(kind + " takes [column, operand]")
		}
		return list, nil
	}
	switch kind {
	case "and", "or":
		list, ok := value.([]any)
		if !ok {
			return refuse(kind + " takes an array")
		}
		for _, each := range list {
			if err := checkNode(each, side, what, depth+1); err != nil {
				return err
			}
		}
		return nil
	case "not":
		return checkNode(value, side, what, depth+1)
	case "isNull":
		of, err := column(value)
		if err != nil {
			return err
		}
		return ordered(of)
	case "inArray":
		parts, err := pair()
		if err != nil {
			return err
		}
		of, err := column(parts[0])
		if err != nil {
			return err
		}
		values, ok := parts[1].([]any)
		if !ok {
			return refuse("inArray takes an array of operands")
		}
		for _, each := range values {
			if err := operand(of, each); err != nil {
				return err
			}
		}
		return nil
	case "prefix":
		parts, err := pair()
		if err != nil {
			return err
		}
		of, err := column(parts[0])
		if err != nil {
			return err
		}
		if err := ordered(of); err != nil {
			return err
		}
		if _, isText := parts[1].(string); !isText || (of != nil && of.Type != ColStr) {
			return refuse("prefix takes a str() column and a string")
		}
		return nil
	case "eq", "ne", "lt", "lte", "gt", "gte":
		parts, err := pair()
		if err != nil {
			return err
		}
		of, err := column(parts[0])
		if err != nil {
			return err
		}
		if kind != "eq" && kind != "ne" {
			if err := ordered(of); err != nil {
				return err
			}
		}
		return operand(of, parts[1])
	case "exists":
		parts, ok := value.([]any)
		if !ok || len(parts) != 3 {
			return refuse("exists takes [set, pairs, inner]")
		}
		_, isText := parts[0].(string)
		pairs, isList := parts[1].([]any)
		if !isText || !isList || len(pairs) == 0 {
			return refuse("exists takes a set name and its matched pairs")
		}
		for _, each := range pairs {
			entry, ok := each.([]any)
			if !ok || len(entry) != 2 {
				return refuse("an exists pair is [setColumn, column]")
			}
			if _, isText := entry[0].(string); !isText {
				return refuse("an exists pair is [setColumn, column]")
			}
			if _, err := column(entry[1]); err != nil {
				return err
			}
		}
		if parts[2] != nil {
			return checkNode(parts[2], nil, what, depth+1)
		}
		return nil
	default:
		return refuse(fmt.Sprintf("unknown kind %s", quote(kind)))
	}
}
