// Package chordjson ports packages/chord/src/json.ts: strict-JSON checking and alias-free copying.
//
// A JSON value is nil, bool, float64, string, []any, or *Object, an object that keeps JavaScript's property order. Decode and Stored produce that representation. Copy also accepts Go values of other numeric kinds, normalized to float64, and a Go map[string]any, which has no property order: its keys are copied in OwnKeys order with the other keys ascending. JavaScript's undefined, sparse arrays, accessors, symbols, prototypes and class instances have no Go counterpart: a Go value either is one of those shapes or is not strict JSON.
package chordjson

import (
	"cmp"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Value is a strict JSON value.
type Value = any

// Error reports a value that is not strict JSON.
type Error struct{ Message string }

func (err *Error) Error() string { return err.Message }

func fail(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

// activeSet tracks the containers on the current path, to reject cycles.
type activeSet map[uintptr]struct{}

func containerPointer(value any) (uintptr, bool) {
	switch typed := value.(type) {
	case *Object:
		return reflect.ValueOf(typed).Pointer(), typed != nil
	case map[string]any:
		return reflect.ValueOf(typed).Pointer(), typed != nil
	case []any:
		return reflect.ValueOf(typed).Pointer(), len(typed) > 0
	default:
		return 0, false
	}
}

func finiteNumber(value any) (float64, bool, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		n := float64(typed)
		return n, true, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(typed), true, true
	case int8:
		return float64(typed), true, true
	case int16:
		return float64(typed), true, true
	case int32:
		return float64(typed), true, true
	case int64:
		return float64(typed), true, true
	case uint:
		return float64(typed), true, true
	case uint8:
		return float64(typed), true, true
	case uint16:
		return float64(typed), true, true
	case uint32:
		return float64(typed), true, true
	case uint64:
		return float64(typed), true, true
	default:
		return 0, false, false
	}
}

// Copy copies a value into an alias-free strict-JSON tree owned by the caller. Every container is new, so a value placed at two paths becomes two independent containers.
func Copy(value any) (Value, error) { return copyValue(value, activeSet{}) }

func copyValue(value any, active activeSet) (Value, error) {
	switch typed := value.(type) {
	case nil, bool, string:
		return typed, nil
	case []any:
		pointer, tracked := containerPointer(typed)
		if tracked {
			if _, cyclic := active[pointer]; cyclic {
				return nil, fail("Value contains cycles and is not strict JSON")
			}
			active[pointer] = struct{}{}
			defer delete(active, pointer)
		}
		out := make([]any, len(typed))
		for at, item := range typed {
			copied, err := copyValue(item, active)
			if err != nil {
				return nil, err
			}
			out[at] = copied
		}
		return out, nil
	case *Object:
		if typed == nil {
			return nil, fail("Value contains a nil object and is not strict JSON")
		}
		return copyObject(typed, typed.Len(), typed.All(), active)
	case map[string]any:
		if typed == nil {
			return nil, fail("Value contains a nil object and is not strict JSON")
		}
		return copyObject(typed, len(typed), func(yield func(string, any) bool) {
			for _, key := range MapOwnKeys(typed) {
				if !yield(key, typed[key]) {
					return
				}
			}
		}, active)
	}
	if n, isNumber, finite := finiteNumber(value); isNumber {
		if !finite {
			return nil, fail("Value contains a non-finite number and is not strict JSON")
		}
		return n, nil
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Func:
		return nil, fail("Value contains a non-JSON function; expected strict JSON")
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array, reflect.Pointer, reflect.Interface:
		// A Go composite stands where upstream sees an object that is not a plain object or array.
		return nil, fail("Value must contain strict JSON plain objects or arrays")
	}
	return nil, fail("Value contains a non-JSON %T; expected strict JSON", value)
}

// copyObject copies properties in own-key order, as upstream does, so the first failing property is the one upstream reports and the copy's insertion order is the source's own-key order.
func copyObject(source any, size int, properties iter.Seq2[string, any], active activeSet) (Value, error) {
	pointer, _ := containerPointer(source)
	if _, cyclic := active[pointer]; cyclic {
		return nil, fail("Value contains cycles and is not strict JSON")
	}
	active[pointer] = struct{}{}
	defer delete(active, pointer)
	out := NewObject(size)
	for key, item := range properties {
		copied, err := copyValue(item, active)
		if err != nil {
			return nil, err
		}
		out.Set(key, copied)
	}
	return out, nil
}

// OwnKeys returns an object's keys in JavaScript's own-key order (Reflect.ownKeys, Object.keys): array-index keys first in ascending numeric order, then the other keys in insertion order.
func OwnKeys(object *Object) []string { return object.Keys() }

// MapOwnKeys orders a Go map's keys for copying into an Object: array-index keys first in ascending numeric order, then the other keys ascending, since a Go map has no insertion order.
func MapOwnKeys(object map[string]any) []string {
	keys := slices.Collect(maps.Keys(object))
	SortOwnKeys(keys)
	return keys
}

// SortOwnKeys sorts unordered keys as MapOwnKeys does: array-index keys first in ascending numeric order, then the other keys ascending.
func SortOwnKeys(keys []string) { slices.SortFunc(keys, compareOwnKeys) }

func compareOwnKeys(left, right string) int {
	leftIndex, rightIndex := isArrayIndexKey(left), isArrayIndexKey(right)
	switch {
	case leftIndex && rightIndex:
		if len(left) != len(right) {
			return cmp.Compare(len(left), len(right))
		}
	case leftIndex:
		return -1
	case rightIndex:
		return 1
	}
	return strings.Compare(left, right)
}

// isArrayIndexKey reports whether key is the canonical decimal form of an integer in [0, 2^32-2], a key JavaScript orders first.
func isArrayIndexKey(key string) bool {
	if key == "0" {
		return true
	}
	if key == "" || key[0] == '0' || len(key) > 10 {
		return false
	}
	for _, digit := range key {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	number, err := strconv.ParseUint(key, 10, 64)
	return err == nil && number <= 1<<32-2
}

// IsValue reports whether a value is finite strict JSON with no cycles. It does not normalize the value.
func IsValue(value any) bool { return check(value, activeSet{}) }

func check(value any, active activeSet) bool {
	switch typed := value.(type) {
	case nil, bool, string:
		return true
	case []any:
		if pointer, tracked := containerPointer(typed); tracked {
			if _, cyclic := active[pointer]; cyclic {
				return false
			}
			active[pointer] = struct{}{}
			defer delete(active, pointer)
		}
		for _, item := range typed {
			if !check(item, active) {
				return false
			}
		}
		return true
	case *Object:
		return typed != nil && checkObject(typed, typed.All(), active)
	case map[string]any:
		return typed != nil && checkObject(typed, maps.All(typed), active)
	}
	_, isNumber, finite := finiteNumber(value)
	return isNumber && finite
}

func checkObject(source any, properties iter.Seq2[string, any], active activeSet) bool {
	pointer, _ := containerPointer(source)
	if _, cyclic := active[pointer]; cyclic {
		return false
	}
	active[pointer] = struct{}{}
	defer delete(active, pointer)
	for _, item := range properties {
		if !check(item, active) {
			return false
		}
	}
	return true
}

// Stored returns the strict-JSON representation of any Go value: what survives a JSON round trip. Fields with `omitempty` and nil pointers vanish as undefined does upstream, numbers become float64, and objects keep their encoded key order (struct field order; a Go map encodes its keys ascending).
func Stored(value any) (Value, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fail("Value is not strict JSON: %v", err)
	}
	stored, err := Decode(encoded)
	if err != nil {
		return nil, fail("Value is not strict JSON: %v", err)
	}
	return stored, nil
}

// Validate returns the first reason a value is not finite strict JSON without cycles, or nil. It does not normalize the value.
func Validate(value any) error {
	if IsValue(value) {
		return nil
	}
	if _, err := Copy(value); err != nil {
		return err
	}
	return fail("Value is not strict JSON")
}
