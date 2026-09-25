package pico3

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// applyTracked applies live envelopes without replacing the published root.
// Keep this separate from durable delta replay: live array deletion is a
// splice, and permutation ranks equal values as Chord's tracked sort does.
func applyTracked(root PublishedConversationView, ops []Op) error {
	for _, op := range ops {
		if op.Verb() == "r" {
			return errors.New("live Pico envelope unexpectedly replaced the view root")
		}
		if op.Verb() == "p" || op.Verb() == "m" {
			if err := applyTrackedArray(root, op); err != nil {
				return err
			}
			continue
		}
		if err := applyTrackedLeaf(root, op); err != nil {
			return err
		}
	}
	return nil
}

func trackedPath(path []any) string {
	parts := make([]string, len(path))
	for index, segment := range path {
		parts[index] = fmt.Sprint(segment)
	}
	return strings.Join(parts, ".")
}

func applyTrackedArray(root JsonObject, op Op) error {
	path := op.Path()
	target, _ := resolvePath(root, path)
	items, ok := target.([]any)
	var next []any
	var err error
	if op.Verb() == "p" {
		if !ok {
			return fmt.Errorf("Pico splice path is not an array: %s", trackedPath(path))
		}
		next, err = spliceOp(items, op)
	} else {
		permutation, valid := op[2].([]any)
		if !ok || !valid || len(items) != len(permutation) {
			return fmt.Errorf("Pico permutation path is not a matching array: %s", trackedPath(path))
		}
		next = trackedPermutation(items, permutation)
	}
	if err != nil {
		return err
	}
	parent, err := resolvePath(root, path[:len(path)-1])
	if err != nil {
		return err
	}
	return setChild(parent, path[len(path)-1], next)
}

type trackedValueKey struct {
	kind  reflect.Kind
	value any
}

func trackedKey(value any) trackedValueKey {
	switch value.(type) {
	case map[string]any, []any:
		ref := reflect.ValueOf(value)
		return trackedValueKey{kind: ref.Kind(), value: ref.Pointer()}
	default:
		if number, ok := asFloat(value); ok {
			return trackedValueKey{value: number}
		}
		return trackedValueKey{value: value}
	}
}

func trackedPermutation(items, permutation []any) []any {
	rank := make(map[trackedValueKey]int, len(items))
	for index, position := range permutation {
		from, ok := segmentIndex(position)
		if ok && from < len(items) {
			key := trackedKey(items[from])
			if _, exists := rank[key]; !exists {
				rank[key] = index
			}
		}
	}
	next := slices.Clone(items)
	slices.SortStableFunc(next, func(left, right any) int {
		leftRank, leftOK := rank[trackedKey(left)]
		rightRank, rightOK := rank[trackedKey(right)]
		if !leftOK || !rightOK {
			return 0
		}
		return leftRank - rightRank
	})
	return next
}

func applyTrackedLeaf(root JsonObject, op Op) error {
	path := op.Path()
	parent, err := resolvePath(root, path[:len(path)-1])
	if err != nil {
		return fmt.Errorf("Pico operation parent is not an object: %s", trackedPath(path))
	}
	switch parent.(type) {
	case map[string]any, []any:
	default:
		return fmt.Errorf("Pico operation parent is not an object: %s", trackedPath(path))
	}
	key := path[len(path)-1]
	if op.Verb() == "d" {
		if _, ok := parent.([]any); ok {
			return applyTrackedArray(root, Op{"p", path[:len(path)-1], key, 1, []any{}})
		}
		return deleteLeaf(parent, path, key)
	}
	value := op[2]
	if op.Verb() == "a" {
		value = trackedLeafString(parent, key) + op[2].(string)
	}
	if op.Verb() == "t" {
		count, _ := segmentIndex(op[2])
		value = utf16Suffix(trackedLeafString(parent, key), count)
	}
	return writeLeaf(parent, path, key, value)
}

func trackedLeafString(parent any, key any) string {
	value, err := childOf(parent, key, nil)
	if err != nil {
		return "undefined"
	}
	return trackedString(value)
}

func trackedString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case map[string]any:
		return "[object Object]"
	case []any:
		parts := make([]string, len(typed))
		for index, item := range typed {
			if item != nil {
				parts[index] = trackedString(item)
			}
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(typed)
	}
}
