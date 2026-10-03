// Package chordjson ports packages/chord/src/json.ts: strict-JSON checking and alias-free copying.
//
// A JSON value is nil, bool, float64, string, []any, or map[string]any, exactly what encoding/json produces when decoding into any. Go values of other numeric kinds are accepted as numbers and normalized to float64 by Copy. JavaScript's undefined, sparse arrays, accessors, symbols, prototypes and class instances have no Go counterpart: a Go value either is one of those shapes or is not strict JSON.
package chordjson

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
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
	case map[string]any:
		if typed == nil {
			return nil, fail("Value contains a nil object and is not strict JSON")
		}
		pointer, _ := containerPointer(typed)
		if _, cyclic := active[pointer]; cyclic {
			return nil, fail("Value contains cycles and is not strict JSON")
		}
		active[pointer] = struct{}{}
		defer delete(active, pointer)
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			copied, err := copyValue(item, active)
			if err != nil {
				return nil, err
			}
			out[key] = copied
		}
		return out, nil
	}
	if n, isNumber, finite := finiteNumber(value); isNumber {
		if !finite {
			return nil, fail("Value contains a non-finite number and is not strict JSON")
		}
		return n, nil
	}
	return nil, fail("Value contains a non-JSON %T; expected strict JSON", value)
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
	case map[string]any:
		if typed == nil {
			return false
		}
		pointer, _ := containerPointer(typed)
		if _, cyclic := active[pointer]; cyclic {
			return false
		}
		active[pointer] = struct{}{}
		defer delete(active, pointer)
		for _, item := range typed {
			if !check(item, active) {
				return false
			}
		}
		return true
	}
	_, isNumber, finite := finiteNumber(value)
	return isNumber && finite
}

// Stored returns the strict-JSON representation of any Go value: what survives a JSON round trip. Fields with `omitempty` and nil pointers vanish as undefined does upstream, and numbers become float64.
func Stored(value any) (Value, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fail("Value is not strict JSON: %v", err)
	}
	var stored any
	if err := json.Unmarshal(encoded, &stored); err != nil {
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
