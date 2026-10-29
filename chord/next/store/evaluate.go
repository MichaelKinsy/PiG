package store

import (
	"maps"
	"strconv"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/store/evaluate.ts
//
// The evaluator of the compiled table vocabulary (slice 6a §6; rule 10): the `tx.get` overlay matches `where` nodes
// and applies `set` nodes with SQL's three-valued `NULL` rules and code point text order, as the SQL that sql.go
// renders from the same nodes does in SQLite.
//
// Go forced differences: Pi's `undefined` (an absent column, a cleared value) is an absent map key; an explicit nil
// value is a `json(...)` cell's JSON `null` (never a scalar). Numbers are int64 (`int`), float64 (`num`), compared
// numerically across the two as `Number(a) - Number(b)` does. Evaluation is synchronous: errors are returned.

// EvaluatedRow is a row as the evaluator reads it: stored cells (str string, int int64, num float64, bool bool, json
// any JSON value), an absent column being `NULL`.
type EvaluatedRow = map[string]any

// truth is SQL's three-valued truth.
type truth int

const (
	truthFalse truth = iota
	truthTrue
	truthUnknown
)

// Matches reports whether row matches node under SQL's rules: unknown (`NULL`) does not match.
func Matches(node WhereNode, row EvaluatedRow) bool { return truthOf(node, row) == truthTrue }

func truthOf(node WhereNode, row EvaluatedRow) truth {
	switch node.Op {
	case WhereAnd:
		all := truthTrue
		for _, each := range node.Nodes {
			if all == truthFalse {
				break
			}
			all = both(all, truthOf(each, row))
		}
		return all
	case WhereOr:
		some := truthFalse
		for _, each := range node.Nodes {
			if some == truthTrue {
				break
			}
			some = either(some, truthOf(each, row))
		}
		return some
	case WhereNot:
		inner := truthOf(node.Nodes[0], row)
		switch inner {
		case truthUnknown:
			return truthUnknown
		case truthTrue:
			return truthFalse
		}
		return truthTrue
	case WhereIsNull:
		_, present := row[node.Column]
		return fromBool(!present)
	case WhereInArray:
		// SQL: `x IN ()` is false, for a `NULL` x too.
		if len(node.Values) == 0 {
			return truthFalse
		}
		value, present := row[node.Column]
		if !present {
			return truthUnknown
		}
		for _, each := range node.Values {
			if CompareScalar(value, each) == 0 {
				return truthTrue
			}
		}
		return truthFalse
	case WherePrefix:
		value, present := row[node.Column]
		if !present {
			return truthUnknown
		}
		text, _ := value.(string)
		prefix, _ := node.Value.(string)
		return fromBool(len(text) >= len(prefix) && text[:len(prefix)] == prefix)
	}
	// The remaining nodes are the six comparisons.
	value, present := row[node.Column]
	if !present {
		return truthUnknown
	}
	order := CompareScalar(value, node.Value)
	switch node.Op {
	case WhereEq:
		return fromBool(order == 0)
	case WhereNe:
		return fromBool(order != 0)
	case WhereLt:
		return fromBool(order < 0)
	case WhereLte:
		return fromBool(order <= 0)
	case WhereGt:
		return fromBool(order > 0)
	case WhereGte:
		return fromBool(order >= 0)
	}
	return truthUnknown
}

func fromBool(value bool) truth {
	if value {
		return truthTrue
	}
	return truthFalse
}

func both(a, b truth) truth {
	if a == truthFalse || b == truthFalse {
		return truthFalse
	}
	if a == truthUnknown || b == truthUnknown {
		return truthUnknown
	}
	return truthTrue
}

func either(a, b truth) truth {
	if a == truthTrue || b == truthTrue {
		return truthTrue
	}
	if a == truthUnknown || b == truthUnknown {
		return truthUnknown
	}
	return truthFalse
}

// CompareScalar orders two cells of one column as SQLite does: text by code point, booleans as 0 and 1, numbers
// numerically. The sign is the result (not Pi's difference).
func CompareScalar(a, b Scalar) int {
	if text, ok := a.(string); ok {
		other, _ := b.(string)
		return CompareText(text, other)
	}
	x, y := numeric(a), numeric(b)
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// numeric is `Number(scalar)`.
func numeric(scalar Scalar) float64 {
	switch typed := scalar.(type) {
	case int64:
		return float64(typed)
	case float64:
		return typed
	case bool:
		if typed {
			return 1
		}
	}
	return 0
}

// ApplySet is row after set, every right side evaluated against row (simultaneous: `{ a: b, b: a }` swaps); an
// integer result outside ±(2^53 - 1) is `invalid/args` (rule 10), as the column's `CHECK` fails the step.
func ApplySet(set SetNode, row EvaluatedRow, table string) (EvaluatedRow, error) {
	next := make(EvaluatedRow, len(row)+len(set))
	maps.Copy(next, row)
	for _, entry := range set {
		value, present, err := evaluate(entry.Node, row, table)
		if err != nil {
			return nil, err
		}
		if !present {
			delete(next, entry.Column)
		} else {
			next[entry.Column] = value
		}
	}
	return next, nil
}

// evaluate is the value of node over row; present is false for `undefined` (`NULL`).
func evaluate(node ExprNode, row EvaluatedRow, table string) (value any, present bool, err error) {
	switch node.Op {
	case ExprNull:
		return nil, false, nil
	case ExprLiteral:
		return node.Value, true, nil
	case ExprCol:
		value, present = row[node.Column]
		return value, present, nil
	case ExprJSON:
		return node.Json, true, nil
	case ExprWhen:
		chosen := node.Args[1]
		if Matches(*node.Cond, row) {
			chosen = node.Args[0]
		}
		return evaluate(chosen, row, table)
	case ExprCoalesce:
		first, present, err := evaluate(node.Args[0], row, table)
		if err != nil {
			return nil, false, err
		}
		// `??`: null and undefined both fall through (operands are scalars, so only absence occurs).
		if present && first != nil {
			return first, true, nil
		}
		return evaluate(node.Args[1], row, table)
	case ExprReplacePrefix:
		subject, present, err := evaluate(node.Args[0], row, table)
		if err != nil || !present {
			return nil, false, err
		}
		text, _ := subject.(string)
		if len(text) < len(node.From) || text[:len(node.From)] != node.From {
			return subject, true, nil
		}
		return node.To + text[len(node.From):], true, nil
	}
	// The remaining nodes are the arithmetic and text operations, over scalar operands (compiled so).
	a, okA, err := evaluate(node.Args[0], row, table)
	if err != nil {
		return nil, false, err
	}
	b, okB, err := evaluate(node.Args[1], row, table)
	if err != nil {
		return nil, false, err
	}
	if !okA || !okB {
		return nil, false, nil
	}
	switch node.Op {
	case ExprConcat:
		x, _ := a.(string)
		y, _ := b.(string)
		return x + y, true, nil
	case ExprMax:
		if CompareScalar(a, b) >= 0 {
			return a, true, nil
		}
		return b, true, nil
	case ExprMin:
		if CompareScalar(a, b) <= 0 {
			return a, true, nil
		}
		return b, true, nil
	}
	// add and sub: JavaScript's double arithmetic over safe integers.
	x, y := numeric(a), numeric(b)
	result := x + y
	if node.Op == ExprSub {
		result = x - y
	}
	if result > float64(MaxSafeInteger) || result < -float64(MaxSafeInteger) {
		return nil, false, data.Invalid(data.ReasonArgs,
			table+": "+string(node.Op)+" gives "+strconv.FormatFloat(result, 'f', -1, 64)+", outside ±(2^53 - 1)")
	}
	return int64(result), true, nil
}
