package store

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/store/expression.ts
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
)

// isComparison reports the six comparison operators (Pi's `Comparison`).
func (op WhereOp) isComparison() bool {
	switch op {
	case WhereEq, WhereNe, WhereLt, WhereLte, WhereGt, WhereGte:
		return true
	}
	return false
}

// WhereNode is a compiled condition: column comparisons with checked operands, combined by `and`, `or`, `not`.
//
//	eq ne lt lte gt gte: Column, Value        {"eq":[column,value]}
//	inArray:             Column, Values       {"inArray":[column,[values]]}
//	isNull:              Column               {"isNull":column}
//	prefix:              Column, Value string {"prefix":[column,prefix]}
//	and or:              Nodes                {"and":[nodes]}
//	not:                 Nodes[0]             {"not":node}
type WhereNode struct {
	Op     WhereOp
	Column string
	Value  Scalar
	Values []Scalar
	Nodes  []WhereNode
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
}

// ColumnRef is a reference to a column of the row, as `(t) => …` callbacks get them.
type ColumnRef = Expression

// ColumnRefs are the column references a table's callbacks get, by column name: `t["path"]`.
type ColumnRefs map[string]*ColumnRef

// NewColumnRef is the reference to column.
func NewColumnRef(column string) *ColumnRef { return &Expression{Op: ExprCol, Args: []any{column}} }

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
func Add(a, b any) *Expression { return &Expression{ExprAdd, []any{a, b}} }

// Sub is `a - b` over integers; a result outside ±(2^53 - 1) fails (rule 10).
func Sub(a, b any) *Expression { return &Expression{ExprSub, []any{a, b}} }

// Max is the larger of a and b (`int` or `num`).
func Max(a, b any) *Expression { return &Expression{ExprMax, []any{a, b}} }

// Min is the smaller of a and b (`int` or `num`).
func Min(a, b any) *Expression { return &Expression{ExprMin, []any{a, b}} }

// Concat is a followed by b.
func Concat(a, b any) *Expression { return &Expression{ExprConcat, []any{a, b}} }

// ReplacePrefix is value with its prefix from replaced by to; a value without the prefix stays as it is.
func ReplacePrefix(value any, from, to string) *Expression {
	return &Expression{ExprReplacePrefix, []any{value, from, to}}
}

// Coalesce is a unless it is `NULL`, else b.
func Coalesce(a, b any) *Expression { return &Expression{ExprCoalesce, []any{a, b}} }

// When is a where condition holds for the row, else b.
func When(condition Condition, a, b any) *Expression {
	return &Expression{ExprWhen, []any{condition, a, b}}
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

// CompileWhere compiles a plugin's condition over table's columns (a trust boundary: plugin values): known scalar
// columns, operands of their type made storable (`invalid/args` otherwise, `Undefined` included: ask with `IsNull`,
// rule 16). condition is a Condition (or *Condition).
func CompileWhere(table *TableDefinition, condition any, what string) (WhereNode, error) {
	var c Condition
	switch typed := condition.(type) {
	case Condition:
		c = typed
	case *Condition:
		if typed == nil {
			return WhereNode{}, invalidArgs("%s: %s: expected a condition", table.Name, what)
		}
		c = *typed
	default:
		return WhereNode{}, invalidArgs("%s: %s: expected a condition", table.Name, what)
	}
	switch c.Op {
	case WhereAnd, WhereOr:
		nodes := make([]WhereNode, len(c.Args))
		for i, each := range c.Args {
			node, err := CompileWhere(table, each, what)
			if err != nil {
				return WhereNode{}, err
			}
			nodes[i] = node
		}
		return WhereNode{Op: c.Op, Nodes: nodes}, nil
	case WhereNot:
		node, err := CompileWhere(table, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: WhereNot, Nodes: []WhereNode{node}}, nil
	case WhereIsNull:
		column, err := scalarColumn(table, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: WhereIsNull, Column: column.Name}, nil
	case WherePrefix:
		column, err := scalarColumn(table, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		if column.Type != ColStr {
			return WhereNode{}, invalidArgs("%s: %s: prefix needs a str() column", table.Name, what)
		}
		value, err := operand(column, argAt(c.Args, 1), what)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: WherePrefix, Column: column.Name, Value: value}, nil
	case WhereInArray:
		column, err := scalarColumn(table, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		list, ok := sliceOf(argAt(c.Args, 1))
		if !ok {
			return WhereNode{}, invalidArgs("%s: %s: inArray needs an array", table.Name, what)
		}
		values := make([]Scalar, len(list))
		for i, each := range list {
			value, err := operand(column, each, what)
			if err != nil {
				return WhereNode{}, err
			}
			values[i] = value
		}
		return WhereNode{Op: WhereInArray, Column: column.Name, Values: values}, nil
	default:
		// Pi's default arm: any other op is read as one of the six comparisons (an unknown op becomes that shape too).
		column, err := scalarColumn(table, argAt(c.Args, 0), what)
		if err != nil {
			return WhereNode{}, err
		}
		value, err := operand(column, argAt(c.Args, 1), what)
		if err != nil {
			return WhereNode{}, err
		}
		return WhereNode{Op: c.Op, Column: column.Name, Value: value}, nil
	}
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
			if column.Type == ColJson {
				return nil, invalidArgs("%s: %s: json(...) columns take plain values", table.Name, where)
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
			scalar, err := CheckScalar(column.Type, value, fmt.Sprintf("%s: %s", table.Name, where))
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

// scalarColumn is the column a condition compares: a reference to a known scalar column.
func scalarColumn(table *TableDefinition, ref any, what string) (Column, error) {
	expression, _ := ref.(*Expression)
	name, isRef := expression.isRef()
	if !isRef {
		return Column{}, invalidArgs("%s: %s: expected a column reference", table.Name, what)
	}
	column, found := table.ByName[name]
	if !found {
		return Column{}, invalidArgs("%s: %s: no column %s", table.Name, what, quote(name))
	}
	if column.Type == ColJson {
		return Column{}, invalidArgs("%s: %s: json(...) column %s is not queryable", table.Name, what, quote(name))
	}
	return column, nil
}

// operand is an operand compared with column, made storable; `Undefined` and nil are refused (rule 16).
func operand(column Column, value any, what string) (Scalar, error) {
	if value == nil || value == Undefined {
		text := "null"
		if value == Undefined {
			text = "undefined"
		}
		return nil, invalidArgs("%s: column %s compared with %s; ask with isNull", what, quote(column.Name), text)
	}
	// scalarColumn refused json columns.
	return CheckScalar(column.Type, value, fmt.Sprintf("%s: column %s", what, quote(column.Name)))
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
	case WhereIsNull:
		return delta.JsonObjectOf("isNull", node.Column)
	case WhereInArray:
		return delta.JsonObjectOf("inArray", []any{node.Column, toAny(node.Values)})
	default:
		return delta.JsonObjectOf(string(node.Op), []any{node.Column, node.Value})
	}
}

// MarshalJSON is the node as JSON.stringify writes Pi's.
func (node WhereNode) MarshalJSON() ([]byte, error) { return jsstring.MarshalJSON(node.JSON()) }

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
func (node ExprNode) MarshalJSON() ([]byte, error) { return jsstring.MarshalJSON(node.JSON()) }

// JSON is the set as one ordered JSON object.
func (set SetNode) JSON() any {
	object := delta.NewJsonObject(len(set))
	for _, entry := range set {
		object.Set(entry.Column, entry.Node.JSON())
	}
	return object
}

// MarshalJSON is the set as JSON.stringify writes Pi's.
func (set SetNode) MarshalJSON() ([]byte, error) { return jsstring.MarshalJSON(set.JSON()) }

func decodeError(what, problem string) error {
	return errors.New("store: " + what + ": " + problem)
}

// decodeScalar reads a JSON scalar: a string, a bool, or a number (an integral safe one as int64, else float64).
func decodeScalar(raw any, what string) (Scalar, error) {
	switch typed := raw.(type) {
	case string, bool:
		return typed, nil
	case float64:
		if typed == math.Trunc(typed) && math.Abs(typed) <= MaxSafeInteger64 {
			return int64(typed), nil
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
