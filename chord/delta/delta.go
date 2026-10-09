// Package delta ports @earendil-works/chord/delta (packages/chord/src/delta): immutable JSON revisions, exact operation batches for ordered replicas, the operation applier, the path-interning wire codec, revision diffing, and the revision tracker.
//
// A JSON value is nil, bool, float64, string, []any, or *chordjson.Object, an object that keeps JavaScript's property order; chordjson.Decode produces that representation. Immutability is an ownership contract: nothing is frozen or copied defensively, so an illegal mutation silently corrupts state.
//
// Go mapping decisions (language mechanics, not behavior changes):
//   - Every emission and traversal that visits object keys visits them in JavaScript's own-key order (chordjson.Object.All): array-index keys first in numeric order, then the other keys in insertion order. A Go map[string]any placed in a working copy has no order; normalize converts it with its other keys ascending (chordjson.MapOwnKeys).
//   - Path segments are strings or non-negative integers. Operations built in Go use int; decoded JSON carries float64. Both address the same element.
//   - A string is UTF-16 in JavaScript. Operations that count string units ("t", the overlap scan) count UTF-16 code units.
//   - JavaScript accepts an array index as a string key and rejects it at run time; Go segments are typed, so a string segment on an array is rejected with UnsafePathError exactly as upstream does.
package delta

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// JsonValue is a strict JSON value.
type JsonValue = any

// Seg is one path segment: an object key (string) or an array index (a non-negative integer).
type Seg = any

// Path is a sequence of segments.
type Path = []any

// NonEmptyPath is the path of an "s", "d", "a" or "t" operation, which cannot target the root. It is a Path; AssertValidOp and
// AssertValidWireOp reject an empty one, as upstream's [Seg, ...Seg[]] tuple type does.
type NonEmptyPath = Path

// PathRef is a wire path reference: the path inline, or the numeric id the encoder assigned on the path's second use.
type PathRef = any

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
// "r" is the only operation that replaces a whole value. "s", "d", "a" and "t" cannot target the root, "p" and "m" may because a tracked value can itself be an array. Operation shape is not canonical: an array may be emptied by either a replacement or a root splice. An object an operation carries is a *JsonObject; decode operation text as Ops, since encoding/json decodes an Op's objects into Go maps, which draft handles cannot open.
type Op = []any

// Verb returns the verb of op, or the empty string for a malformed tuple.
func Verb(op Op) string {
	if len(op) == 0 {
		return ""
	}
	verb, _ := op[0].(string)
	return verb
}

// PathOf returns the path of op; nil for "r" and for a tuple without one.
func PathOf(op Op) Path {
	if len(op) < 2 || Verb(op) == "r" {
		return nil
	}
	path, _ := op[1].([]any)
	return path
}

// WireOp is what crosses a boundary. It adds two compressions to Op and nothing else: ["#", id, path] defines an id on a path's second use, a numeric path reference points at a defined id, and a shortened tuple reuses the previous operation's path (arity disambiguates). ["r", value] carries no path, so it encodes to itself.
type WireOp []any

// Ops is a batch of operations that decodes JSON with chordjson.Decode, so an object an operation carries keeps its key order. A []Op decoded by encoding/json holds map[string]any objects instead; use Ops for a decoded field.
type Ops []Op

// UnmarshalJSON decodes a JSON array of operations, keeping object key order.
func (ops *Ops) UnmarshalJSON(data []byte) error {
	value, err := chordjson.Decode(data)
	if err != nil {
		return err
	}
	if value == nil {
		*ops = nil
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return &json.UnmarshalTypeError{Value: "non-array", Type: reflect.TypeFor[Ops]()}
	}
	decoded := make(Ops, len(items))
	for index, item := range items {
		op, ok := item.([]any)
		if !ok {
			return &json.UnmarshalTypeError{Value: "non-array", Type: reflect.TypeFor[Op]()}
		}
		decoded[index] = op
	}
	*ops = decoded
	return nil
}

// UnmarshalJSON decodes a wire operation with chordjson.Decode, so an object it carries keeps its key order.
func (op *WireOp) UnmarshalJSON(data []byte) error {
	items, err := decodeTuple(data)
	*op = items
	return err
}

func decodeTuple(data []byte) ([]any, error) {
	value, err := chordjson.Decode(data)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, &json.UnmarshalTypeError{Value: "non-array", Type: reflect.TypeFor[[]any]()}
	}
	return items, nil
}

// IsReplace reports whether op replaces the complete value.
func IsReplace(op Op) bool { return Verb(op) == "r" }

// IsBase reports whether a batch begins with a replacement. A tracker batch carries "r" at index 0 or not at all, so this is exact.
func IsBase(ops []Op) bool { return len(ops) > 0 && Verb(ops[0]) == "r" }

// TypeError reports a malformed operation, path or value, the failures upstream raises as TypeError.
type TypeError struct{ Message string }

func (err *TypeError) Error() string { return err.Message }

// Is reports that a TypeError is an [ErrInvalidOp], so errors.Is(err, ErrInvalidOp) holds for every malformed operation, path or value.
func (err *TypeError) Is(target error) bool { return target == ErrInvalidOp }

// ErrInvalidOp matches every [TypeError] under errors.Is. The error itself carries Pi's message.
var ErrInvalidOp = errors.New("invalid delta operation")

func typeError(format string, args ...any) error {
	return &TypeError{Message: fmt.Sprintf(format, args...)}
}

// PathError reports an operation path that does not resolve, or a path dictionary id that is not defined.
type PathError struct {
	Path any
}

// NewPathError is `new PathError(path)`.
func NewPathError(path any) *PathError {
	err := &PathError{Path: path}
	return err
}

func (err *PathError) Error() string { return "unresolvable path: " + jsonText(err.Path) }

// Name is the `name` property, "PathError" (delta/index.ts:315).
func (*PathError) Name() string { return "PathError" }

// UnsafePathError reports a segment that reaches the prototype chain upstream, or an array index that is not a position in the array.
type UnsafePathError struct {
	Segment any
}

// NewUnsafePathError is `new UnsafePathError(segment)`.
func NewUnsafePathError(segment any) *UnsafePathError {
	err := &UnsafePathError{Segment: segment}
	return err
}

// Error is `unsafe path segment: ${String(segment)}` (delta/index.ts:138).
func (err *UnsafePathError) Error() string { return "unsafe path segment: " + segmentText(err.Segment) }

// Name is the `name` property, "UnsafePathError" (delta/index.ts:140).
func (*UnsafePathError) Name() string { return "UnsafePathError" }

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

// segmentText is JavaScript's String(value) for a decoded JSON value, as upstream's error messages and path costs print a segment or verb.
func segmentText(segment any) string {
	switch typed := segment.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case []any:
		parts := make([]string, len(typed))
		for index, item := range typed {
			if item != nil {
				parts[index] = segmentText(item)
			}
		}
		return strings.Join(parts, ",")
	case *chordjson.Object:
		return "[object Object]"
	}
	if n, ok := number(segment); ok {
		return jsnumber.String(n)
	}
	return fmt.Sprint(segment)
}

// AssertSafePath rejects reserved segments and segments that are not non-negative integers.
func AssertSafePath(path Path) error {
	for _, segment := range path {
		if key, ok := segment.(string); ok {
			if ReservedSegments[key] {
				return NewUnsafePathError(segment)
			}
			continue
		}
		if !integer(segment, 0) {
			return NewUnsafePathError(segment)
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

// permutationItems reads an "m" permutation: the []any a decoded operation holds, or the []int a Go caller builds for Pi's number[], which the 0.4.1 API accepted.
func permutationItems(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []int:
		items := make([]any, len(typed))
		for position, source := range typed {
			items[position] = source
		}
		return items, true
	default:
		return nil, false
	}
}

func assertPermutation(value any) error {
	permutation, ok := permutationItems(value)
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
	case WireOp:
		return typed, true
	default:
		return nil, false
	}
}

// AssertValidOp checks op as AssertValidOpValue does.
//
// Deprecated: Use [AssertValidOpValue], which accepts any value, as Pi's assertValidOp takes unknown.
func AssertValidOp(op Op) error { return AssertValidOpValue(op) }

// AssertValidOpValue is Pi's assertValidOp: it checks the verb, arity and payload shape of a decoded operation: paths inline, no "#", no short forms. It does not inspect payload values. Validating against the wire grammar would be laxer than the type: a two-element ["s", value] would pass and an applier would read the value as a path, so each vocabulary has its own validator.
func AssertValidOpValue(value any) error {
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

// AssertValidWireOp is AssertValidOpValue for the wire grammar, where path ids and short forms are legal.
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

// jsonText is JSON.stringify for a path or a dictionary id: strings keep <, >, & and U+2028/U+2029 unescaped, and numbers print as JavaScript prints them (-0 as 0, a non-finite number as null).
func jsonText(value any) string {
	switch typed := value.(type) {
	case []any:
		parts := make([]string, len(typed))
		for i, item := range typed {
			parts[i] = jsonText(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case float64:
		return string(jsnumber.JSON(typed))
	}
	var buffer strings.Builder
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprint(value)
	}
	return unescapeLineSeparators(strings.TrimSuffix(buffer.String(), "\n"))
}

// unescapeLineSeparators restores the U+2028 and U+2029 that encoding/json escapes and JSON.stringify writes as they are. An escape is \u2028 or \u2029 after an odd run of backslashes.
func unescapeLineSeparators(quoted string) string {
	if !strings.Contains(quoted, `\u202`) {
		return quoted
	}
	var out strings.Builder
	backslashes := 0
	for i := 0; i < len(quoted); i++ {
		if backslashes%2 == 1 && (strings.HasPrefix(quoted[i:], "u2028") || strings.HasPrefix(quoted[i:], "u2029")) {
			kept := strings.TrimSuffix(out.String(), `\`)
			out.Reset()
			out.WriteString(kept)
			if quoted[i+4] == '8' {
				out.WriteRune('\u2028')
			} else {
				out.WriteRune('\u2029')
			}
			i += len("u2028") - 1
			backslashes = 0
			continue
		}
		if quoted[i] == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
		out.WriteByte(quoted[i])
	}
	return out.String()
}
