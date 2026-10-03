// Package delta produces immutable JSON revisions and exact operation batches
// for ordered replicas.
//
// Ports packages/chord/src/delta/index.ts (operations, path safety, and the
// immutable applier) and packages/chord/src/delta/tracker.ts (the overlay
// draft tracker).
//
// JSON values use Go's dynamic representation: nil, bool, float64, string,
// map[string]any, and []any. Immutability is an ownership contract, exactly
// as upstream: nothing is frozen or defensively copied, so mutating a revision,
// an operation payload, or a value returned by ApplyImmutable silently
// corrupts state.
package delta

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Op is one operation tuple:
//
//	["r", value]                      replace the complete value
//	["s", path, value]                set an object property or array element
//	["d", path]                       delete an object property or remove an array element
//	["a", path, text]                 append to a string
//	["t", path, count]                remove UTF-16 code units from a string's front
//	["p", path, index, remove, items] splice an array
//	["m", path, permutation]          reorder an array: new[i] = old[permutation[i]]
//
// A path is a []any of string keys and non-negative integer indices.
// Operation shape is not canonical: only the resulting value is contractual.
type Op = []any

// reservedSegments reach the JavaScript prototype chain upstream; the tracker
// never emits them and the appliers reject them so a batch replays
// identically in every runtime.
var reservedSegments = map[string]bool{"__proto__": true, "constructor": true, "prototype": true}

// IsReplace reports whether op replaces the complete value.
func IsReplace(op Op) bool { return len(op) > 0 && op[0] == "r" }

// IsBase reports whether a batch begins with a replacement.
func IsBase(ops []Op) bool { return len(ops) > 0 && IsReplace(ops[0]) }

// UnsafePathError reports a reserved or malformed path segment.
type UnsafePathError struct{ Segment any }

func (err *UnsafePathError) Error() string {
	return fmt.Sprintf("unsafe path segment: %v", err.Segment)
}

// PathError reports a path that does not resolve in the target value.
type PathError struct{ Path any }

func (err *PathError) Error() string { return fmt.Sprintf("unresolvable path: %v", err.Path) }

// ErrInvalidOp reports an operation whose verb, arity, or payload shape is invalid.
var ErrInvalidOp = errors.New("invalid delta operation")

func invalidOp(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidOp, reason) }

// segmentIndex returns an integral path segment as an index. Decoded JSON
// carries indices as float64.
func segmentIndex(segment any) (int, bool) {
	switch value := segment.(type) {
	case int:
		return value, true
	case float64:
		if value == math.Trunc(value) && !math.IsInf(value, 0) && value >= 0 && value <= math.MaxInt32*2 {
			return int(value), true
		}
	case int64:
		return int(value), true
	}
	return 0, false
}

func pathOf(value any) ([]any, bool) {
	path, ok := value.([]any)
	return path, ok
}

func assertSafePath(path []any) error {
	for _, segment := range path {
		if key, ok := segment.(string); ok {
			if reservedSegments[key] {
				return &UnsafePathError{Segment: key}
			}
			continue
		}
		index, ok := segmentIndex(segment)
		if !ok || index < 0 {
			return &UnsafePathError{Segment: segment}
		}
	}
	return nil
}

// AssertValidOp checks verb, arity, and payload shape of a decoded operation.
func AssertValidOp(op Op) error {
	if len(op) == 0 {
		return invalidOp("op is not a tuple")
	}
	verb, _ := op[0].(string)
	switch verb {
	case "r":
		if len(op) != 2 {
			return invalidOp("r arity")
		}
		return nil
	case "s":
		if len(op) != 3 {
			return invalidOp("s arity")
		}
		return assertPathArg(op[1], true)
	case "d":
		if len(op) != 2 {
			return invalidOp("d arity")
		}
		return assertPathArg(op[1], true)
	case "a":
		if len(op) != 3 {
			return invalidOp("a arity")
		}
		if _, ok := op[2].(string); !ok {
			return invalidOp("a payload")
		}
		return assertPathArg(op[1], true)
	case "t":
		if len(op) != 3 {
			return invalidOp("t arity")
		}
		if count, ok := segmentIndex(op[2]); !ok || count < 0 {
			return invalidOp("t payload")
		}
		return assertPathArg(op[1], true)
	case "p":
		if len(op) != 5 {
			return invalidOp("p arity")
		}
		if err := assertPathArg(op[1], false); err != nil {
			return err
		}
		if index, ok := segmentIndex(op[2]); !ok || index < 0 {
			return invalidOp("p index")
		}
		if remove, ok := segmentIndex(op[3]); !ok || remove < 0 {
			return invalidOp("p remove")
		}
		if _, ok := op[4].([]any); !ok {
			return invalidOp("p items")
		}
		return nil
	case "m":
		if len(op) != 3 {
			return invalidOp("m arity")
		}
		if err := assertPathArg(op[1], false); err != nil {
			return err
		}
		return assertPermutation(op[2])
	}
	// Silently skipping an unknown verb is how a newer producer's op vanishes.
	return invalidOp("unknown op verb: " + jsString(op[0]))
}

// jsString is JavaScript's String(value) for a decoded JSON value.
func jsString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return jsnumber.String(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case []any:
		parts := make([]string, len(typed))
		for index, item := range typed {
			if item != nil {
				parts[index] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return fmt.Sprint(typed)
	}
}

func assertPathArg(value any, nonEmpty bool) error {
	path, ok := pathOf(value)
	if !ok || (nonEmpty && len(path) == 0) {
		return invalidOp("path")
	}
	return nil
}

func assertPermutation(value any) error {
	permutation, err := permutationOf(value)
	if err != nil {
		return err
	}
	seen := make([]bool, len(permutation))
	for _, index := range permutation {
		if index < 0 || index >= len(permutation) || seen[index] {
			return invalidOp("m permutation")
		}
		seen[index] = true
	}
	return nil
}

func permutationOf(value any) ([]int, error) {
	switch items := value.(type) {
	case []int:
		return items, nil
	case []any:
		result := make([]int, len(items))
		for position, item := range items {
			index, ok := segmentIndex(item)
			if !ok {
				return nil, invalidOp("m permutation")
			}
			result[position] = index
		}
		return result, nil
	}
	return nil, invalidOp("m permutation")
}

// ApplyImmutable applies one batch without mutating target. The result shares
// unchanged containers with target and adopts operation payloads without
// copying them.
func ApplyImmutable(target any, ops []Op) (any, error) {
	return ApplyImmutableBatches(target, [][]Op{ops})
}

// ApplyImmutableBatches applies ordered batches as one final-result-only
// replay. Containers copied for an earlier batch may be mutated by a later
// batch, so no intermediate revision is exposed.
func ApplyImmutableBatches(target any, batches [][]Op) (any, error) {
	root := target
	owned := ownedSet{}
	for _, ops := range batches {
		for _, op := range ops {
			if err := AssertValidOp(op); err != nil {
				return nil, err
			}
			if op[0] == "r" {
				root = op[1]
				continue
			}
			next, err := applyOne(root, op, owned)
			if err != nil {
				return nil, err
			}
			root = next
		}
	}
	return root, nil
}

// ownedSet records containers this replay already copied, so repeated
// operations under one parent copy it once.
type ownedSet map[uintptr]bool

func containerID(value any) (uintptr, bool) {
	switch container := value.(type) {
	case map[string]any:
		return uintptr(reflect.ValueOf(container).UnsafePointer()), true
	case []any:
		if cap(container) == 0 {
			return 0, false
		}
		return uintptr(reflect.ValueOf(container).UnsafePointer()), true
	}
	return 0, false
}

func (owned ownedSet) copy(value any, path []any) (any, error) {
	if id, ok := containerID(value); ok && owned[id] {
		return value, nil
	}
	var result any
	switch container := value.(type) {
	case map[string]any:
		copied := make(map[string]any, len(container)+1)
		maps.Copy(copied, container)
		result = copied
	case []any:
		copied := make([]any, len(container), len(container)+1)
		copy(copied, container)
		result = copied
	default:
		return nil, &PathError{Path: path}
	}
	if id, ok := containerID(result); ok {
		owned[id] = true
	}
	return result, nil
}

// applyOne applies one non-replacement op, copying every container from the
// root to the op's target container.
func applyOne(root any, op Op, owned ownedSet) (any, error) {
	path, _ := pathOf(op[1])
	if err := assertSafePath(path); err != nil {
		return nil, err
	}
	verb := op[0].(string)
	if verb == "p" || verb == "m" {
		return updateAt(root, path, path, owned, func(target any) (any, error) {
			items, ok := target.([]any)
			if !ok {
				return nil, &PathError{Path: path}
			}
			if verb == "p" {
				return spliceItems(items, op, path)
			}
			return permuteItems(items, op, path)
		})
	}
	parentPath, key := path[:len(path)-1], path[len(path)-1]
	return updateAt(root, parentPath, path, owned, func(parent any) (any, error) {
		return applyLeaf(parent, key, op, path)
	})
}

// updateAt copies the containers along path and replaces the container at
// path with update's result.
func updateAt(root any, path, full []any, owned ownedSet, update func(any) (any, error)) (any, error) {
	copied, err := owned.copy(root, full)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 {
		return update(copied)
	}
	segment := path[0]
	switch container := copied.(type) {
	case map[string]any:
		key, ok := segment.(string)
		if !ok {
			return nil, &PathError{Path: full}
		}
		child, present := container[key]
		if !present {
			return nil, &PathError{Path: full}
		}
		next, err := updateAt(child, path[1:], full, owned, update)
		if err != nil {
			return nil, err
		}
		container[key] = next
		return container, nil
	case []any:
		index, ok := segmentIndex(segment)
		if !ok || index >= len(container) {
			return nil, &PathError{Path: full}
		}
		next, err := updateAt(container[index], path[1:], full, owned, update)
		if err != nil {
			return nil, err
		}
		container[index] = next
		return container, nil
	}
	return nil, &PathError{Path: full}
}

func spliceItems(items []any, op Op, path []any) (any, error) {
	start, _ := segmentIndex(op[2])
	remove, _ := segmentIndex(op[3])
	inserted, _ := op[4].([]any)
	if start > len(items) {
		start = len(items)
	}
	if remove > len(items)-start {
		remove = len(items) - start
	}
	result := make([]any, 0, len(items)-remove+len(inserted))
	result = append(result, items[:start]...)
	result = append(result, inserted...)
	result = append(result, items[start+remove:]...)
	return result, nil
}

func permuteItems(items []any, op Op, path []any) (any, error) {
	permutation, err := permutationOf(op[2])
	if err != nil {
		return nil, err
	}
	if len(permutation) != len(items) {
		return nil, &PathError{Path: path}
	}
	result := make([]any, len(items))
	for index, from := range permutation {
		result[index] = items[from]
	}
	return result, nil
}

func applyLeaf(parent any, key any, op Op, path []any) (any, error) {
	verb := op[0].(string)
	switch container := parent.(type) {
	case map[string]any:
		name, ok := key.(string)
		if !ok {
			return nil, &PathError{Path: path}
		}
		switch verb {
		case "s":
			container[name] = op[2]
		case "d":
			delete(container, name)
		case "a", "t":
			current, ok := container[name].(string)
			if !ok {
				return nil, &PathError{Path: path}
			}
			container[name] = stringEdit(current, op)
		}
		return container, nil
	case []any:
		index, ok := segmentIndex(key)
		if !ok {
			return nil, &UnsafePathError{Segment: key}
		}
		if index > len(container) {
			return nil, &UnsafePathError{Segment: key}
		}
		switch verb {
		case "s":
			if index == len(container) {
				return append(container, op[2]), nil
			}
			container[index] = op[2]
			return container, nil
		case "d":
			if index >= len(container) {
				return nil, &PathError{Path: path}
			}
			return append(container[:index:index], container[index+1:]...), nil
		case "a", "t":
			if index >= len(container) {
				return nil, &PathError{Path: path}
			}
			current, ok := container[index].(string)
			if !ok {
				return nil, &PathError{Path: path}
			}
			container[index] = stringEdit(current, op)
			return container, nil
		}
	}
	return nil, &PathError{Path: path}
}

func stringEdit(current string, op Op) string {
	if op[0] == "a" {
		return current + op[2].(string)
	}
	count, _ := segmentIndex(op[2])
	return utf16Suffix(current, count)
}

// Overlap returns the longest suffix of a that is a prefix of b, in UTF-16
// code units, considering at most the last scan units of a. It probes a long
// head of b first, then one unit, and gives up after eight candidates per
// probe; giving up returns 0, which emits a set: larger, never wrong.
func Overlap(a, b string, scan int) int {
	if a == "" || b == "" || scan == 0 {
		return 0
	}
	left := utf16.Encode([]rune(a))
	right := utf16.Encode([]rune(b))
	tail := left
	if len(tail) > scan {
		tail = tail[len(tail)-scan:]
	}
	const probe, maxCandidates = 64, 8
	for _, h := range []int{min(probe, len(right)), 1} {
		head := right[:h]
		tried := 0
		for k := indexUnits(tail, head, 0); k != -1; k = indexUnits(tail, head, k+1) {
			tried++
			if tried > maxCandidates {
				break
			}
			n := len(tail) - k
			if n <= len(right) && slices.Equal(tail[k:], right[:n]) {
				return n
			}
		}
		if h == 1 {
			break
		}
	}
	return 0
}

func indexUnits(haystack, needle []uint16, from int) int {
	for index := from; index+len(needle) <= len(haystack); index++ {
		if slices.Equal(haystack[index:index+len(needle)], needle) {
			return index
		}
	}
	return -1
}

// utf16Len returns the JavaScript length of text.
func utf16Len(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}
	return length
}

// utf16Suffix drops count UTF-16 code units from the front of text, as
// JavaScript's String.prototype.slice(count) does.
func utf16Suffix(text string, count int) string {
	if count <= 0 {
		return text
	}
	units := utf16.Encode([]rune(text))
	if count >= len(units) {
		return ""
	}
	return string(utf16.Decode(units[count:]))
}
