package pico3

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"unicode/utf16"
)

// The delta primitives below are the subset of packages/chord's
// operation-log change tracking that Pico3 uses. packages/chord is outside
// PiG's package scope, so only these primitives are ported: the Op tuples,
// IsBase, Apply, ApplyImmutable, and a Tracker that publishes the operations
// transforming the previously published value into the current one.

// Op is one change tuple:
//
//	["r", value]                    replace the complete value
//	["s", path, value]              set a property or array element
//	["d", path]                     delete an object property or array element
//	["a", path, text]               append to a string
//	["t", path, count]              drop UTF-16 code units from a string's front
//	["p", path, index, remove, items] splice an array
//	["m", path, permutation]        reorder an array: new[i] = old[permutation[i]]
//
// A path is a []any of string keys and integer indices. Operation shape is
// not canonical: consumers depend only on the resulting value.
type Op []any

// Verb returns the operation verb.
func (op Op) Verb() string {
	if len(op) == 0 {
		return ""
	}
	verb, _ := op[0].(string)
	return verb
}

// Path returns the operation path; nil for "r".
func (op Op) Path() []any {
	if len(op) < 2 || op.Verb() == "r" {
		return nil
	}
	path, _ := op[1].([]any)
	return path
}

// IsBase reports whether a batch begins with a complete replacement.
func IsBase(ops []Op) bool {
	return len(ops) > 0 && ops[0].Verb() == "r"
}

// PathError reports an operation path that does not resolve.
type PathError struct{ Path any }

func (err *PathError) Error() string { return fmt.Sprintf("unresolvable path: %v", err.Path) }

// UnsafePathError reports a reserved or malformed path segment.
type UnsafePathError struct{ Segment any }

func (err *UnsafePathError) Error() string {
	return fmt.Sprintf("unsafe path segment: %v", err.Segment)
}

var reservedSegments = map[string]bool{"__proto__": true, "constructor": true, "prototype": true}

func segmentIndex(segment any) (int, bool) {
	number, ok := asFloat(segment)
	if !ok || number < 0 || number != math.Trunc(number) {
		return 0, false
	}
	return int(number), true
}

func assertSafePath(path []any) error {
	for _, segment := range path {
		if key, ok := segment.(string); ok {
			if reservedSegments[key] {
				return &UnsafePathError{Segment: segment}
			}
			continue
		}
		if _, ok := segmentIndex(segment); !ok {
			return &UnsafePathError{Segment: segment}
		}
	}
	return nil
}

func assertValidOp(op Op) error {
	arity := map[string]int{"r": 2, "s": 3, "d": 2, "a": 3, "t": 3, "p": 5, "m": 3}
	want, known := arity[op.Verb()]
	if !known {
		return fmt.Errorf("unknown op verb: %v", firstOr(op))
	}
	if len(op) != want {
		return fmt.Errorf("%s arity", op.Verb())
	}
	if op.Verb() == "r" {
		return nil
	}
	path, ok := op[1].([]any)
	if !ok {
		return errors.New("path is not an array")
	}
	if len(path) == 0 && op.Verb() != "p" && op.Verb() != "m" {
		return errors.New("path is empty")
	}
	return assertSafePath(path)
}

func firstOr(op Op) any {
	if len(op) == 0 {
		return nil
	}
	return op[0]
}

// Apply applies decoded operations to a mutable value and returns the result.
func Apply(target JsonValue, ops []Op) (JsonValue, error) {
	root := target
	for _, op := range ops {
		next, err := applyOne(root, op)
		if err != nil {
			return root, err
		}
		root = next
	}
	return root, nil
}

// ApplyImmutable applies operations without mutating target: containers along
// each changed path are copied and unchanged subtrees are shared.
func ApplyImmutable(target JsonValue, ops []Op) (JsonValue, error) {
	root := target
	for _, op := range ops {
		if err := assertValidOp(op); err != nil {
			return root, err
		}
		if op.Verb() == "r" {
			root = op[1]
			continue
		}
		path := op.Path()
		if op.Verb() != "p" && op.Verb() != "m" {
			path = path[:len(path)-1]
		}
		copied, err := copyContainers(root, path)
		if err != nil {
			return root, err
		}
		if root, err = applyOne(copied, op); err != nil {
			return root, err
		}
	}
	return root, nil
}

func shallowCopy(value JsonValue) (JsonValue, bool) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		maps.Copy(out, typed)
		return out, true
	case []any:
		return append([]any(nil), typed...), true
	default:
		return nil, false
	}
}

func copyContainers(root JsonValue, path []any) (JsonValue, error) {
	copiedRoot, ok := shallowCopy(root)
	if !ok {
		return nil, &PathError{Path: path}
	}
	destination := copiedRoot
	for _, segment := range path {
		child, err := childOf(destination, segment, path)
		if err != nil {
			return nil, err
		}
		copiedChild, ok := shallowCopy(child)
		if !ok {
			return nil, &PathError{Path: path}
		}
		if err := setChild(destination, segment, copiedChild); err != nil {
			return nil, err
		}
		destination = copiedChild
	}
	return copiedRoot, nil
}

func childOf(container JsonValue, segment any, path []any) (JsonValue, error) {
	switch typed := container.(type) {
	case map[string]any:
		key, ok := segment.(string)
		if !ok {
			return nil, &PathError{Path: path}
		}
		child, exists := typed[key]
		if !exists {
			return nil, &PathError{Path: path}
		}
		return child, nil
	case []any:
		index, ok := segmentIndex(segment)
		if !ok {
			return nil, &UnsafePathError{Segment: segment}
		}
		if index >= len(typed) {
			return nil, &PathError{Path: path}
		}
		return typed[index], nil
	default:
		return nil, &PathError{Path: path}
	}
}

func setChild(container JsonValue, segment any, value JsonValue) error {
	switch typed := container.(type) {
	case map[string]any:
		key, ok := segment.(string)
		if !ok {
			return &UnsafePathError{Segment: segment}
		}
		typed[key] = value
		return nil
	case []any:
		index, ok := segmentIndex(segment)
		if !ok || index >= len(typed) {
			return &UnsafePathError{Segment: segment}
		}
		typed[index] = value
		return nil
	default:
		return &PathError{Path: segment}
	}
}

func resolvePath(root JsonValue, path []any) (JsonValue, error) {
	node := root
	for _, segment := range path {
		child, err := childOf(node, segment, path)
		if err != nil {
			return nil, err
		}
		node = child
	}
	return node, nil
}

// applyOne applies one op. Arrays are Go slices, so a splice rewrites the
// parent's reference; the returned root carries a root-array splice.
func applyOne(root JsonValue, op Op) (JsonValue, error) {
	if err := assertValidOp(op); err != nil {
		return root, err
	}
	switch op.Verb() {
	case "r":
		return op[1], nil
	case "p", "m":
		return applyArrayOp(root, op)
	}
	path := op.Path()
	parent, err := resolvePath(root, path[:len(path)-1])
	if err != nil {
		return root, err
	}
	return root, applyLeaf(parent, path, op)
}

func applyArrayOp(root JsonValue, op Op) (JsonValue, error) {
	path := op.Path()
	target, err := resolvePath(root, path)
	if err != nil {
		return root, err
	}
	items, ok := target.([]any)
	if !ok {
		return root, &PathError{Path: path}
	}
	var next []any
	if op.Verb() == "p" {
		next, err = spliceOp(items, op)
	} else {
		next, err = permuteOp(items, op)
	}
	if err != nil {
		return root, err
	}
	if len(path) == 0 {
		return next, nil
	}
	parent, err := resolvePath(root, path[:len(path)-1])
	if err != nil {
		return root, err
	}
	return root, setChild(parent, path[len(path)-1], next)
}

func spliceOp(items []any, op Op) ([]any, error) {
	index, okIndex := segmentIndex(op[2])
	remove, okRemove := segmentIndex(op[3])
	inserted, okItems := op[4].([]any)
	if !okIndex || !okRemove || !okItems {
		return nil, errors.New("p shape")
	}
	index = min(index, len(items))
	end := min(index+remove, len(items))
	next := make([]any, 0, len(items)-(end-index)+len(inserted))
	next = append(next, items[:index]...)
	next = append(next, inserted...)
	return append(next, items[end:]...), nil
}

func permuteOp(items []any, op Op) ([]any, error) {
	permutation, ok := op[2].([]any)
	if !ok || len(permutation) != len(items) {
		return nil, &PathError{Path: op.Path()}
	}
	next := make([]any, len(items))
	seen := make([]bool, len(items))
	for index, source := range permutation {
		from, ok := segmentIndex(source)
		if !ok || from >= len(items) || seen[from] {
			return nil, errors.New("m permutation is not a bijection")
		}
		seen[from] = true
		next[index] = items[from]
	}
	return next, nil
}

func applyLeaf(parent JsonValue, path []any, op Op) error {
	key := path[len(path)-1]
	if items, isArray := parent.([]any); isArray {
		index, ok := segmentIndex(key)
		if !ok || index > len(items) {
			return &UnsafePathError{Segment: key}
		}
	}
	switch op.Verb() {
	case "s":
		return writeLeaf(parent, path, key, op[2])
	case "d":
		return deleteLeaf(parent, path, key)
	case "a":
		current, ok := readLeaf(parent, key).(string)
		if !ok {
			return &PathError{Path: path}
		}
		text, _ := op[2].(string)
		return writeLeaf(parent, path, key, current+text)
	default:
		current, ok := readLeaf(parent, key).(string)
		count, okCount := segmentIndex(op[2])
		if !ok || !okCount {
			return &PathError{Path: path}
		}
		return writeLeaf(parent, path, key, utf16Suffix(current, count))
	}
}

func readLeaf(parent JsonValue, key any) JsonValue {
	child, err := childOf(parent, key, nil)
	if err != nil {
		return nil
	}
	return child
}

func writeLeaf(parent JsonValue, path []any, key any, value JsonValue) error {
	switch typed := parent.(type) {
	case map[string]any:
		name, ok := key.(string)
		if !ok {
			return &UnsafePathError{Segment: key}
		}
		typed[name] = value
		return nil
	case []any:
		index, _ := segmentIndex(key)
		if index == len(typed) {
			return &PathError{Path: path}
		}
		typed[index] = value
		return nil
	default:
		return &PathError{Path: path}
	}
}

func deleteLeaf(parent JsonValue, path []any, key any) error {
	object, ok := parent.(map[string]any)
	if !ok {
		return &PathError{Path: path}
	}
	name, ok := key.(string)
	if !ok {
		return &UnsafePathError{Segment: key}
	}
	delete(object, name)
	return nil
}

// utf16Len counts UTF-16 code units, as JavaScript string lengths do.
func utf16Len(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}
	return length
}

// utf16Suffix drops the first count UTF-16 code units.
func utf16Suffix(text string, count int) string {
	units := 0
	for index, r := range text {
		if units >= count {
			return text[index:]
		}
		units += utf16.RuneLen(r)
	}
	return ""
}

// Tracker publishes the operations that turn the previously published value
// into the current state. The first Flush publishes a complete replacement.
type Tracker struct {
	state     JsonObject
	published JsonValue
	base      bool
}

// Track begins tracking root, which becomes tracker-owned.
func Track(root JsonObject) *Tracker {
	return &Tracker{state: root, base: true}
}

// State returns the live tracked value; mutate it in place.
func (tracker *Tracker) State() JsonObject { return tracker.state }

// Rebase makes the next Flush a complete replacement.
func (tracker *Tracker) Rebase() { tracker.base = true }

// Flush returns the pending operations and marks the current state published.
func (tracker *Tracker) Flush() []Op {
	current := cloneJSON(tracker.state)
	if tracker.base {
		tracker.base = false
		tracker.published = current
		return []Op{{"r", cloneJSON(current)}}
	}
	var ops []Op
	diffValue(nil, tracker.published, current, &ops)
	tracker.published = current
	return ops
}

// diffValue appends ops that transform prev into next at path.
func diffValue(path []any, prev, next JsonValue, ops *[]Op) {
	if jsonEqual(prev, next) {
		return
	}
	prevObject, prevIsObject := prev.(map[string]any)
	nextObject, nextIsObject := next.(map[string]any)
	if prevIsObject && nextIsObject {
		diffObject(path, prevObject, nextObject, ops)
		return
	}
	prevArray, prevIsArray := prev.([]any)
	nextArray, nextIsArray := next.([]any)
	if prevIsArray && nextIsArray {
		diffArray(path, prevArray, nextArray, ops)
		return
	}
	prevText, prevIsText := prev.(string)
	nextText, nextIsText := next.(string)
	if prevIsText && nextIsText && len(path) > 0 {
		diffString(path, prevText, nextText, ops)
		return
	}
	if len(path) == 0 {
		*ops = append(*ops, Op{"r", cloneJSON(next)})
		return
	}
	*ops = append(*ops, Op{"s", clonePath(path), cloneJSON(next)})
}

func clonePath(path []any) []any { return append([]any(nil), path...) }

func childPath(path []any, segment any) []any {
	return append(clonePath(path), segment)
}

func diffObject(path []any, prev, next JsonObject, ops *[]Op) {
	for _, key := range sortedKeys(prev) {
		if _, kept := next[key]; !kept {
			*ops = append(*ops, Op{"d", childPath(path, key)})
		}
	}
	for _, key := range sortedKeys(next) {
		before, existed := prev[key]
		if !existed {
			*ops = append(*ops, Op{"s", childPath(path, key), cloneJSON(next[key])})
			continue
		}
		diffValue(childPath(path, key), before, next[key], ops)
	}
}

func diffArray(path []any, prev, next []any, ops *[]Op) {
	if len(prev) == len(next) {
		for index := range prev {
			diffValue(childPath(path, index), prev[index], next[index], ops)
		}
		return
	}
	if drop, ok := frontDrop(prev, next); ok {
		if drop > 0 {
			*ops = append(*ops, Op{"p", clonePath(path), 0, drop, []any{}})
		}
		kept := len(prev) - drop
		*ops = append(*ops, Op{"p", clonePath(path), kept, 0, cloneJSON(next[kept:])})
		return
	}
	prefix := 0
	for prefix < len(prev) && prefix < len(next) && jsonEqual(prev[prefix], next[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(prev)-prefix && suffix < len(next)-prefix &&
		jsonEqual(prev[len(prev)-1-suffix], next[len(next)-1-suffix]) {
		suffix++
	}
	inserted := cloneJSON(next[prefix : len(next)-suffix])
	*ops = append(*ops, Op{"p", clonePath(path), prefix, len(prev) - prefix - suffix, inserted})
}

// frontDrop finds the smallest k such that prev[k:] is a prefix of next and
// next is longer than prev[k:]: a front truncation followed by an append.
func frontDrop(prev, next []any) (int, bool) {
	for drop := 0; drop <= len(prev); drop++ {
		kept := prev[drop:]
		if len(kept) >= len(next) {
			continue
		}
		if prefixEqual(kept, next) {
			return drop, true
		}
	}
	return 0, false
}

func prefixEqual(prefix, items []any) bool {
	for index, item := range prefix {
		if !jsonEqual(item, items[index]) {
			return false
		}
	}
	return true
}

func diffString(path []any, prev, next string, ops *[]Op) {
	if strings.HasPrefix(next, prev) {
		*ops = append(*ops, Op{"a", clonePath(path), next[len(prev):]})
		return
	}
	if shared := overlap(prev, next, min(len(prev), len(next))); shared > 0 {
		drop := utf16Len(prev) - utf16Len(next[:shared])
		*ops = append(*ops, Op{"t", clonePath(path), drop}, Op{"a", clonePath(path), next[shared:]})
		return
	}
	*ops = append(*ops, Op{"s", clonePath(path), next})
}

// overlap returns the byte length of the longest suffix of a that is a prefix
// of b, probing with a long head first and then one byte.
func overlap(a, b string, scan int) int {
	if a == "" || b == "" || scan == 0 {
		return 0
	}
	tail := a
	if len(a) > scan {
		tail = a[len(a)-scan:]
	}
	for _, head := range []int{min(64, len(b)), 1} {
		probe := b[:head]
		tried := 0
		for start := 0; start <= len(tail); {
			found := strings.Index(tail[start:], probe)
			if found < 0 {
				break
			}
			position := start + found
			tried++
			if tried > 8 {
				break
			}
			shared := len(tail) - position
			if shared <= len(b) && tail[position:] == b[:shared] {
				return shared
			}
			start = position + 1
		}
		if head == 1 {
			break
		}
	}
	return 0
}
