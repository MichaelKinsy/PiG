// Package delta ports @earendil-works/chord/delta (packages/chord/src/delta): immutable JSON revisions, exact operation batches for ordered replicas, the operation applier, the path-interning wire codec, revision diffing, and the revision tracker.
//
// A JSON value is nil, bool, float64, string, []any, or map[string]any, exactly what encoding/json produces when decoding into any. Immutability is an ownership contract: nothing is frozen or copied defensively, so an illegal mutation silently corrupts state.
//
// Go mapping decisions (language mechanics, not behavior changes):
//   - A JavaScript object keeps insertion order; a Go map has none. Every emission and traversal that visits object keys visits them in ascending order, which makes operation batches deterministic.
//   - Path segments are strings or non-negative integers. Operations built in Go use int; decoded JSON carries float64. Both address the same element.
//   - A string is UTF-16 in JavaScript. Operations that count string units ("t", the overlap scan) count UTF-16 code units.
//   - JavaScript accepts an array index as a string key and rejects it at run time; Go segments are typed, so a string segment on an array is rejected with UnsafePathError exactly as upstream does.
package delta

import (
	"encoding/json"
	"fmt"
	"math"
	"unicode/utf16"
	"unicode/utf8"
)

// JsonValue is a strict JSON value.
type JsonValue = any

// Seg is one path segment: an object key (string) or an array index (a non-negative integer).
type Seg = any

// Path is a sequence of segments.
type Path = []any

// Op is one change tuple, the form in memory, on the wire, and on disk:
//
//	["r", value]                      replace the complete value
//	["s", path, value]                set an object property or array element
//	["d", path]                       delete an object property or array element
//	["a", path, text]                 append to a string
//	["t", path, count]                remove count UTF-16 code units from a string's front
//	["p", path, index, remove, items] splice an array
//	["m", path, permutation]          reorder an array: new[i] = old[permutation[i]]
//
// "r" is the only operation that replaces a whole value. "s", "d", "a" and "t" cannot target the root, "p" and "m" may because a tracked value can itself be an array. Operation shape is not canonical: an array may be emptied by either a replacement or a root splice.
type Op []any

// Verb returns the operation verb, or the empty string for a malformed tuple.
func (op Op) Verb() string {
	if len(op) == 0 {
		return ""
	}
	verb, _ := op[0].(string)
	return verb
}

// Path returns the operation path; nil for "r" and for a tuple without one.
func (op Op) Path() Path {
	if len(op) < 2 || op.Verb() == "r" {
		return nil
	}
	path, _ := op[1].([]any)
	return path
}

// WireOp is what crosses a boundary. It adds two compressions to Op and nothing else: ["#", id, path] defines an id on a path's second use, a numeric path reference points at a defined id, and a shortened tuple reuses the previous operation's path (arity disambiguates). ["r", value] carries no path, so it encodes to itself.
type WireOp []any

// IsReplace reports whether op replaces the complete value.
func IsReplace(op Op) bool { return op.Verb() == "r" }

// IsBase reports whether a batch begins with a replacement. A tracker batch carries "r" at index 0 or not at all, so this is exact.
func IsBase(ops []Op) bool { return len(ops) > 0 && ops[0].Verb() == "r" }

// TypeError reports a malformed operation, path or value, the failures upstream raises as TypeError.
type TypeError struct{ Message string }

func (err *TypeError) Error() string { return err.Message }

func typeError(format string, args ...any) error {
	return &TypeError{Message: fmt.Sprintf(format, args...)}
}

// PathError reports an operation path that does not resolve, or a path dictionary id that is not defined.
type PathError struct{ Path any }

func (err *PathError) Error() string { return "unresolvable path: " + jsonText(err.Path) }

// UnsafePathError reports a segment that reaches the prototype chain upstream, or an array index that is not a position in the array.
type UnsafePathError struct{ Segment any }

func (err *UnsafePathError) Error() string { return "unsafe path segment: " + segmentText(err.Segment) }

// ReservedSegments are the segments that reach the prototype chain in JavaScript. Paths are data from facets, plugins and tools, so none of them is trusted; the tracker never emits these segments.
var ReservedSegments = map[string]bool{"__proto__": true, "constructor": true, "prototype": true}

// number converts any Go number to float64.
func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case float32:
		return float64(typed), true
	default:
		return 0, false
	}
}

// integer reports whether value is a finite integer number at least minimum.
func integer(value any, minimum float64) bool {
	n, ok := number(value)
	return ok && !math.IsInf(n, 0) && !math.IsNaN(n) && n >= minimum && math.Trunc(n) == n
}

// index converts a validated non-negative integer; a value beyond int saturates, as Array.prototype.splice clamps and Object.hasOwn misses an index beyond the array.
func index(value any) (int, bool) {
	n, ok := number(value)
	if !ok || n < 0 || n != math.Trunc(n) || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, false
	}
	if n >= float64(math.MaxInt) {
		return math.MaxInt, true
	}
	return int(n), true
}

func segmentText(segment any) string {
	switch typed := segment.(type) {
	case nil:
		return "null"
	case string:
		return typed
	}
	if n, ok := number(segment); ok {
		return jsonText(n)
	}
	return fmt.Sprint(segment)
}

// AssertSafePath rejects reserved segments and segments that are not non-negative integers.
func AssertSafePath(path Path) error {
	for _, segment := range path {
		if key, ok := segment.(string); ok {
			if ReservedSegments[key] {
				return &UnsafePathError{Segment: segment}
			}
			continue
		}
		if !integer(segment, 0) {
			return &UnsafePathError{Segment: segment}
		}
	}
	return nil
}

func assertPathArg(value any, nonEmpty bool) error {
	path, ok := value.([]any)
	if !ok {
		return typeError("path is not an array")
	}
	if nonEmpty && len(path) == 0 {
		return typeError("path is empty")
	}
	return AssertSafePath(path)
}

func assertPermutation(value any) error {
	permutation, ok := value.([]any)
	if !ok {
		return typeError("m permutation is not an array")
	}
	seen := make([]bool, len(permutation))
	for _, item := range permutation {
		at, ok := index(item)
		if !ok || at >= len(permutation) || seen[at] {
			return typeError("m permutation is not a bijection")
		}
		seen[at] = true
	}
	return nil
}

func tuple(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case Op:
		return typed, true
	case WireOp:
		return typed, true
	default:
		return nil, false
	}
}

// AssertValidOp checks the verb, arity and payload shape of a decoded operation: paths inline, no "#", no short forms. It does not inspect payload values. Validating against the wire grammar would be laxer than the type: a two-element ["s", value] would pass and an applier would read the value as a path, so each vocabulary has its own validator.
func AssertValidOp(value any) error {
	op, ok := tuple(value)
	if !ok || len(op) == 0 {
		return typeError("op is not a tuple")
	}
	verb, _ := op[0].(string)
	switch verb {
	case "r":
		if len(op) != 2 {
			return typeError("r arity")
		}
		return nil
	case "s":
		if len(op) != 3 {
			return typeError("s arity")
		}
		return assertPathArg(op[1], true)
	case "d":
		if len(op) != 2 {
			return typeError("d arity")
		}
		return assertPathArg(op[1], true)
	case "a":
		if _, isText := op[len(op)-1].(string); len(op) != 3 || !isText {
			return typeError("a shape")
		}
		return assertPathArg(op[1], true)
	case "t":
		if len(op) != 3 || !integer(op[2], 0) {
			return typeError("t shape")
		}
		return assertPathArg(op[1], true)
	case "p":
		if len(op) != 5 {
			return typeError("p arity")
		}
		if err := assertPathArg(op[1], false); err != nil {
			return err
		}
		if !integer(op[2], 0) {
			return typeError("p index")
		}
		if !integer(op[3], 0) {
			return typeError("p remove")
		}
		if _, isArray := op[4].([]any); !isArray {
			return typeError("p items")
		}
		return nil
	case "m":
		if len(op) != 3 {
			return typeError("m arity")
		}
		if err := assertPathArg(op[1], false); err != nil {
			return err
		}
		return assertPermutation(op[2])
	default:
		return typeError("unknown op verb: %s", segmentText(op[0]))
	}
}

func assertRef(value any) error {
	if _, isNumber := number(value); isNumber {
		if !integer(value, 0) {
			return typeError("bad path id")
		}
		return nil
	}
	path, ok := value.([]any)
	if !ok {
		// A string is not a path. Unchecked, "a".slice(0, -1) is "" upstream, which resolves to the root and writes there.
		return typeError("path is not an array")
	}
	return AssertSafePath(path)
}

// AssertValidWireOp is AssertValidOp for the wire grammar, where path ids and short forms are legal.
func AssertValidWireOp(value any) error {
	op, ok := tuple(value)
	if !ok || len(op) == 0 {
		return typeError("op is not a tuple")
	}
	verb, _ := op[0].(string)
	switch verb {
	case "r":
		if len(op) != 2 {
			return typeError("r arity")
		}
		return nil
	case "s", "d", "a", "t", "m":
		long := 3
		if verb == "d" {
			long = 2
		}
		if len(op) == long {
			if err := assertRef(op[1]); err != nil {
				return err
			}
		} else if len(op) != long-1 {
			return typeError("%s arity", verb)
		}
		last := op[len(op)-1]
		switch verb {
		case "a":
			if _, isText := last.(string); !isText {
				return typeError("a value")
			}
		case "t":
			if !integer(last, 0) {
				return typeError("t count")
			}
		case "m":
			return assertPermutation(last)
		}
		return nil
	case "p":
		first := 1
		switch len(op) {
		case 5:
			first = 2
			if err := assertRef(op[1]); err != nil {
				return err
			}
		case 4:
		default:
			return typeError("p arity")
		}
		if !integer(op[first], 0) {
			return typeError("p index")
		}
		if !integer(op[first+1], 0) {
			return typeError("p remove")
		}
		if _, isArray := op[first+2].([]any); !isArray {
			return typeError("p items")
		}
		return nil
	case "#":
		if len(op) != 3 || !integer(op[1], 0) {
			return typeError("# shape")
		}
		path, isArray := op[2].([]any)
		if !isArray {
			return typeError("# shape")
		}
		return AssertSafePath(path)
	default:
		return typeError("unknown op verb: %s", segmentText(op[0]))
	}
}

// utf16Len counts UTF-16 code units, as a JavaScript string length does.
func utf16Len(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}
	return length
}

// utf16Suffix drops the first count UTF-16 code units, like String.prototype.slice(count). A count that ends inside a surrogate pair leaves JavaScript a lone low surrogate, which a Go string cannot hold; it becomes U+FFFD, as encoding/json decodes it, so the result keeps the same UTF-16 length.
func utf16Suffix(text string, count int) string {
	units := 0
	for at, r := range text {
		if units >= count {
			return text[at:]
		}
		width := utf16.RuneLen(r)
		if units+width > count {
			_, size := utf8.DecodeRuneInString(text[at:])
			return string(utf8.RuneError) + text[at+size:]
		}
		units += width
	}
	return ""
}

// jsonText renders a value like JSON.stringify, for error messages.
func jsonText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
