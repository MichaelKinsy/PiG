// Package delta ports Chord's immutable JSON deltas (@earendil-works/chord
// 1.0.4 `delta`: operations, the immutable applier and the overlay draft
// tracker) onto the ordered value model of package jv.
//
// A draft is a mutation log over an immutable base revision. Preparing it emits
// the same operation batch Chord's tracker emits for the same mutations, which
// is what Pi stores in a document delta, and applies the batch to the base to
// obtain the next revision. The package uses no reflection and no
// encoding/json, and keeps no package-level mutable state.
package delta

import (
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// Verb names an operation kind: the first element of an operation tuple.
type Verb byte

// The operation verbs.
const (
	Replace Verb = 'r' // ["r", value]
	Set     Verb = 's' // ["s", path, value]
	Delete  Verb = 'd' // ["d", path]
	Append  Verb = 'a' // ["a", path, text]
	Trim    Verb = 't' // ["t", path, count]: drop count UTF-16 units from the front of a string
	Splice  Verb = 'p' // ["p", path, index, remove, items]
	Permute Verb = 'm' // ["m", path, permutation]: new[i] = old[permutation[i]]
)

// Seg is one path segment: an object key or an array index.
type Seg struct {
	Key     string
	Index   int
	IsIndex bool
}

// Key returns the segment for an object key.
func Key(k string) Seg { return Seg{Key: k} }

// Idx returns the segment for an array index.
func Idx(i int) Seg { return Seg{Index: i, IsIndex: true} }

// Path addresses a value below the root.
type Path []Seg

// Op is one operation. Which fields are meaningful depends on Verb.
type Op struct {
	Verb   Verb
	Path   Path
	Value  any    // Replace, Set
	Text   string // Append
	Count  int    // Trim
	Index  int    // Splice
	Remove int    // Splice
	Items  []any  // Splice
	Perm   []int  // Permute
}

// Errors reported by the applier and the operation decoder.
var (
	// Read-only: assigned once at initialization.
	ErrInvalidOp = errors.New("invalid delta operation")
	// Read-only: assigned once at initialization.
	ErrUnsafePath = errors.New("unsafe path segment")
	// Read-only: assigned once at initialization.
	ErrPath = errors.New("unresolvable path")
)

// Read-only: assigned once at initialization.
var reserved = [...]string{"__proto__", "constructor", "prototype"}

func isReserved(k string) bool {
	for _, r := range reserved {
		if k == r {
			return true
		}
	}
	return false
}

// IsBase reports whether a batch begins with a root replacement.
func IsBase(ops []Op) bool { return len(ops) > 0 && ops[0].Verb == Replace }

// Tuple returns the operation as the tuple Chord stores: a []any whose path is
// an array of strings and numbers.
func (op Op) Tuple() any {
	if op.Verb == Replace {
		return []any{"r", op.Value}
	}
	path := make([]any, len(op.Path))
	for i, s := range op.Path {
		if s.IsIndex {
			path[i] = float64(s.Index)
		} else {
			path[i] = s.Key
		}
	}
	switch op.Verb {
	case Set:
		return []any{"s", path, op.Value}
	case Delete:
		return []any{"d", path}
	case Append:
		return []any{"a", path, op.Text}
	case Trim:
		return []any{"t", path, float64(op.Count)}
	case Splice:
		items := op.Items
		if items == nil {
			items = []any{}
		}
		return []any{"p", path, float64(op.Index), float64(op.Remove), items}
	}
	perm := make([]any, len(op.Perm))
	for i, p := range op.Perm {
		perm[i] = float64(p)
	}
	return []any{"m", path, perm}
}

// OpsValue returns the operations as a JSON array value.
func OpsValue(ops []Op) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = op.Tuple()
	}
	return out
}

// DecodeOps decodes a stored operation array.
func DecodeOps(value any) ([]Op, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, invalid("operations are not an array")
	}
	ops := make([]Op, len(list))
	for i, item := range list {
		op, err := DecodeOp(item)
		if err != nil {
			return nil, err
		}
		ops[i] = op
	}
	return ops, nil
}

func invalid(reason string) error { return errors.Join(ErrInvalidOp, errors.New(reason)) }

func intOf(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != float64(int(f)) || f < 0 || f > 4294967294 {
		return 0, false
	}
	return int(f), true
}

func decodePath(v any, nonEmpty bool) (Path, error) {
	list, ok := v.([]any)
	if !ok || (nonEmpty && len(list) == 0) {
		return nil, invalid("path")
	}
	path := make(Path, len(list))
	for i, item := range list {
		switch s := item.(type) {
		case string:
			if isReserved(s) {
				return nil, errors.Join(ErrUnsafePath, errors.New(s))
			}
			path[i] = Key(s)
		default:
			n, ok := intOf(item)
			if !ok {
				return nil, errors.Join(ErrUnsafePath, errors.New("segment"))
			}
			path[i] = Idx(n)
		}
	}
	return path, nil
}

// DecodeOp decodes one stored operation tuple and checks its verb, arity and
// payload shape.
func DecodeOp(value any) (Op, error) {
	t, ok := value.([]any)
	if !ok || len(t) == 0 {
		return Op{}, invalid("op is not a tuple")
	}
	verb, _ := t[0].(string)
	switch verb {
	case "r":
		if len(t) != 2 {
			return Op{}, invalid("r arity")
		}
		return Op{Verb: Replace, Value: t[1]}, nil
	case "s":
		if len(t) != 3 {
			return Op{}, invalid("s arity")
		}
		p, err := decodePath(t[1], true)
		return Op{Verb: Set, Path: p, Value: t[2]}, err
	case "d":
		if len(t) != 2 {
			return Op{}, invalid("d arity")
		}
		p, err := decodePath(t[1], true)
		return Op{Verb: Delete, Path: p}, err
	case "a":
		if len(t) != 3 {
			return Op{}, invalid("a arity")
		}
		text, ok := t[2].(string)
		if !ok {
			return Op{}, invalid("a payload")
		}
		p, err := decodePath(t[1], true)
		return Op{Verb: Append, Path: p, Text: text}, err
	case "t":
		if len(t) != 3 {
			return Op{}, invalid("t arity")
		}
		n, ok := intOf(t[2])
		if !ok {
			return Op{}, invalid("t payload")
		}
		p, err := decodePath(t[1], true)
		return Op{Verb: Trim, Path: p, Count: n}, err
	case "p":
		if len(t) != 5 {
			return Op{}, invalid("p arity")
		}
		p, err := decodePath(t[1], false)
		if err != nil {
			return Op{}, err
		}
		index, ok1 := intOf(t[2])
		remove, ok2 := intOf(t[3])
		items, ok3 := t[4].([]any)
		if !ok1 || !ok2 || !ok3 {
			return Op{}, invalid("p payload")
		}
		return Op{Verb: Splice, Path: p, Index: index, Remove: remove, Items: items}, nil
	case "m":
		if len(t) != 3 {
			return Op{}, invalid("m arity")
		}
		p, err := decodePath(t[1], false)
		if err != nil {
			return Op{}, err
		}
		list, ok := t[2].([]any)
		if !ok {
			return Op{}, invalid("m permutation")
		}
		perm := make([]int, len(list))
		seen := make([]bool, len(list))
		for i, item := range list {
			n, ok := intOf(item)
			if !ok || n >= len(list) || seen[n] {
				return Op{}, invalid("m permutation")
			}
			seen[n] = true
			perm[i] = n
		}
		return Op{Verb: Permute, Path: p, Perm: perm}, nil
	}
	return Op{}, invalid("unknown op verb")
}

func pathString(p Path) string {
	b := []byte{'['}
	for i, s := range p {
		if i > 0 {
			b = append(b, ',')
		}
		if s.IsIndex {
			b = strconv.AppendInt(b, int64(s.Index), 10)
		} else {
			b = jv.AppendString(b, s.Key)
		}
	}
	return string(append(b, ']'))
}

func pathErr(p Path) error { return errors.Join(ErrPath, errors.New(pathString(p))) }
