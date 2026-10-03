package delta

import (
	"iter"
	"maps"
	"reflect"
	"slices"
	"strconv"
)

// Ports packages/chord/src/delta/index.ts (apply, applyImmutable, applyImmutableBatches).

func isContainer(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

// identity is the identity of a container: the same map, or the same slice header over the same backing array.
type identity struct {
	pointer uintptr
	length  int
}

func identityOf(value any) (identity, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return identity{pointer: reflect.ValueOf(typed).Pointer(), length: -1}, typed != nil
	case []any:
		return identity{pointer: reflect.ValueOf(typed).Pointer(), length: len(typed)}, len(typed) > 0
	default:
		return identity{}, false
	}
}

// scope tracks the containers one immutable application call has copied, so each touched container is copied once. It keeps every owned container reachable, so an address is never reused for a different container during the call.
type scope struct{ owned map[identity]any }

func newScope() *scope { return &scope{owned: map[identity]any{}} }

func (s *scope) adopt(value any) {
	if key, tracked := identityOf(value); tracked {
		s.owned[key] = value
	}
}

func (s *scope) isOwned(value any) bool {
	key, tracked := identityOf(value)
	if !tracked {
		return false
	}
	_, owned := s.owned[key]
	return owned
}

func shallowCopy(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		maps.Copy(out, typed)
		return out
	case []any:
		return append(make([]any, 0, len(typed)), typed...)
	default:
		return value
	}
}

func (s *scope) own(value any) any {
	if s.isOwned(value) {
		return value
	}
	copied := shallowCopy(value)
	s.adopt(copied)
	return copied
}

// mapKey is the property name a segment addresses on an object.
func mapKey(segment Seg) (string, bool) {
	if key, ok := segment.(string); ok {
		return key, true
	}
	if at, ok := index(segment); ok {
		return strconv.Itoa(at), true
	}
	return "", false
}

// canonicalIndex parses a string spelled exactly as an array index.
func canonicalIndex(text string) (int, bool) {
	at, err := strconv.Atoi(text)
	if err != nil || at < 0 || strconv.Itoa(at) != text {
		return 0, false
	}
	return at, true
}

// childOf reads an own element. An array addressed by a non-index segment is UnsafePathError, a missing element is PathError.
func childOf(container any, segment Seg, path Path) (any, error) {
	switch typed := container.(type) {
	case map[string]any:
		key, ok := mapKey(segment)
		if !ok {
			return nil, &PathError{Path: path}
		}
		child, exists := typed[key]
		if !exists {
			return nil, &PathError{Path: path}
		}
		return child, nil
	case []any:
		if key, isText := segment.(string); isText {
			// Array.prototype keeps an index spelled as a string, and "length", as own properties, so Object.hasOwn finds them and the walk then rejects the non-numeric segment; any other name is simply absent.
			if at, spelled := canonicalIndex(key); (spelled && at < len(typed)) || key == "length" {
				return nil, &UnsafePathError{Segment: segment}
			}
			return nil, &PathError{Path: path}
		}
		at, ok := index(segment)
		if !ok {
			return nil, &UnsafePathError{Segment: segment}
		}
		if at >= len(typed) {
			return nil, &PathError{Path: path}
		}
		return typed[at], nil
	default:
		return nil, &PathError{Path: path}
	}
}

// setChild writes an element of an existing key or index; it never grows an array.
func setChild(container any, segment Seg, value any) error {
	switch typed := container.(type) {
	case map[string]any:
		key, ok := mapKey(segment)
		if !ok {
			return &UnsafePathError{Segment: segment}
		}
		typed[key] = value
		return nil
	case []any:
		at, ok := index(segment)
		if !ok || at >= len(typed) {
			return &UnsafePathError{Segment: segment}
		}
		typed[at] = value
		return nil
	default:
		return &PathError{Path: Path{segment}}
	}
}

// resolve walks path for the applier. Unlike copyContainers, which checks for an own element first, it rejects a string segment on an array before looking the element up, as upstream resolveValue does.
func resolve(root any, path Path) (any, error) {
	node := root
	for _, segment := range path {
		if _, isArray := node.([]any); isArray {
			if _, isText := segment.(string); isText {
				return nil, &UnsafePathError{Segment: segment}
			}
		}
		child, err := childOf(node, segment, path)
		if err != nil {
			return nil, err
		}
		node = child
	}
	return node, nil
}

func resolveContainer(root any, path Path) (any, error) {
	node, err := resolve(root, path)
	if err != nil {
		return nil, err
	}
	if !isContainer(node) {
		return nil, &PathError{Path: path}
	}
	return node, nil
}

// replaceAt installs next at path and returns the root; an array edit that changes length rewrites its parent's reference.
func replaceAt(root any, path Path, next any) (any, error) {
	if len(path) == 0 {
		return next, nil
	}
	parent, err := resolveContainer(root, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	return root, setChild(parent, path[len(path)-1], next)
}

// copyContainers copies, once per call, every container along path, root first, so a later in-place edit never reaches a container the caller owns.
func (s *scope) copyContainers(root any, path Path) (any, error) {
	if !isContainer(root) {
		return nil, &PathError{Path: path}
	}
	copiedRoot := s.own(root)
	destination := copiedRoot
	for _, segment := range path {
		child, err := childOf(destination, segment, path)
		if err != nil {
			return nil, err
		}
		if !isContainer(child) {
			return nil, &PathError{Path: path}
		}
		if s.isOwned(child) {
			destination = child
			continue
		}
		copied := shallowCopy(child)
		s.adopt(copied)
		if err := setChild(destination, segment, copied); err != nil {
			return nil, err
		}
		destination = copied
	}
	return copiedRoot, nil
}

// Apply applies decoded operations to a plain mutable value and returns it, because "r" replaces the value outright and an array splice rewrites its parent's reference. A batch must be detached for exactly one replica: "r" adopts its payload rather than copying it.
func Apply(target JsonValue, ops []Op) (JsonValue, error) {
	root := target
	for _, op := range ops {
		if err := AssertValidOp(op); err != nil {
			return target, err
		}
		next, err := applyOp(root, op, nil)
		if err != nil {
			return target, err
		}
		root = next
	}
	return root, nil
}

// ApplyImmutable applies one decoded operation batch without mutating the previous immutable value. The result shares containers with both the target and the operation payloads.
func ApplyImmutable(target JsonValue, ops []Op) (JsonValue, error) {
	return ApplyImmutableBatches(target, slices.Values([][]Op{ops}))
}

// ApplyImmutableBatches applies decoded batches as one final-result-only replay. Containers copied for an earlier batch may be mutated while applying a later one, so no intermediate revision is exposed or safe to retain. A failure, from validation or from a batch source that panics, leaves the target untouched.
func ApplyImmutableBatches(target JsonValue, batches iter.Seq[[]Op]) (JsonValue, error) {
	root := target
	sc := newScope()
	var failure error
	for ops := range batches {
		for _, op := range ops {
			if failure = AssertValidOp(op); failure != nil {
				break
			}
			if op.Verb() == "r" {
				root = op[1]
				continue
			}
			path := op.Path()
			if verb := op.Verb(); verb != "p" && verb != "m" {
				path = path[:len(path)-1]
			}
			if root, failure = sc.copyContainers(root, path); failure != nil {
				break
			}
			if root, failure = applyOp(root, op, sc); failure != nil {
				break
			}
		}
		if failure != nil {
			break
		}
	}
	if failure != nil {
		return target, failure
	}
	return root, nil
}

// applyOp applies one validated operation. With a scope the caller has already copied every container the operation edits in place, and arrays this call creates are adopted.
func applyOp(root any, op Op, sc *scope) (any, error) {
	if op.Verb() == "r" {
		return op[1], nil
	}
	path := op.Path()
	switch op.Verb() {
	case "p":
		return applySplice(root, path, op, sc)
	case "m":
		return root, applyPermutation(root, path, op)
	}
	return applyLeaf(root, path, op, sc)
}

func applySplice(root any, path Path, op Op, sc *scope) (any, error) {
	target, err := resolveContainer(root, path)
	if err != nil {
		return root, err
	}
	items, isArray := target.([]any)
	if !isArray {
		return root, &PathError{Path: path}
	}
	start, _ := index(op[2])
	remove, _ := index(op[3])
	inserted := op[4].([]any)
	start = min(start, len(items))
	end := start + min(remove, len(items)-start)
	next := slices.Replace(items, start, end, inserted...)
	if sc != nil {
		sc.adopt(next)
	}
	return replaceAt(root, path, next)
}

func applyPermutation(root any, path Path, op Op) error {
	target, err := resolveContainer(root, path)
	if err != nil {
		return err
	}
	items, isArray := target.([]any)
	permutation := op[2].([]any)
	if !isArray || len(items) != len(permutation) {
		return &PathError{Path: path}
	}
	previous := slices.Clone(items)
	for at, source := range permutation {
		from, _ := index(source)
		items[at] = previous[from]
	}
	return nil
}

func applyLeaf(root any, path Path, op Op, sc *scope) (any, error) {
	parentPath := path[:len(path)-1]
	parent, err := resolveContainer(root, parentPath)
	if err != nil {
		return root, err
	}
	key := path[len(path)-1]
	if items, isArray := parent.([]any); isArray {
		at, ok := index(key)
		if _, isNumber := number(key); !isNumber || !ok {
			return root, &UnsafePathError{Segment: key}
		}
		// An index may address an existing element or append exactly one past the end, which keeps the value strict JSON: a sparse array does not survive a round trip.
		if at > len(items) {
			return root, &UnsafePathError{Segment: key}
		}
		return applyArrayLeaf(root, parentPath, items, at, path, op, sc)
	}
	object := parent.(map[string]any)
	name, ok := mapKey(key)
	if !ok {
		return root, &UnsafePathError{Segment: key}
	}
	switch op.Verb() {
	case "s":
		object[name] = op[2]
	case "d":
		delete(object, name)
	default:
		text, err := rewriteText(object[name], op, path)
		if err != nil {
			return root, err
		}
		object[name] = text
	}
	return root, nil
}

func applyArrayLeaf(root any, parentPath Path, items []any, at int, path Path, op Op, sc *scope) (any, error) {
	switch op.Verb() {
	case "s":
		if at < len(items) {
			items[at] = op[2]
			return root, nil
		}
		return grow(root, parentPath, append(items, op[2]), sc)
	case "d":
		if at >= len(items) {
			return root, &PathError{Path: path}
		}
		return grow(root, parentPath, slices.Delete(items, at, at+1), sc)
	default:
		var current any
		if at < len(items) {
			current = items[at]
		}
		text, err := rewriteText(current, op, path)
		if err != nil {
			return root, err
		}
		items[at] = text
		return root, nil
	}
}

func grow(root any, parentPath Path, next []any, sc *scope) (any, error) {
	if sc != nil {
		sc.adopt(next)
	}
	return replaceAt(root, parentPath, next)
}

// rewriteText applies "a" or "t" to the string at path.
func rewriteText(current any, op Op, path Path) (any, error) {
	text, ok := current.(string)
	if !ok {
		return nil, &PathError{Path: path}
	}
	if op.Verb() == "a" {
		return text + op[2].(string), nil
	}
	count, _ := index(op[2])
	return utf16Suffix(text, count), nil
}
