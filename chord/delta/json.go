package delta

import (
	"errors"
	"fmt"
	"math"
	"reflect"
)

// ErrNotStrictJSON matches every StrictJSONError.
var ErrNotStrictJSON = errors.New("not strict JSON")

// StrictJSONError reports a placement or copy that is not strict JSON. It is
// upstream's TypeError from tracker.ts clonePlacement and json.ts copyJson.
type StrictJSONError struct{ message string }

func (err *StrictJSONError) Error() string { return err.message }

// Is reports ErrNotStrictJSON.
func (err *StrictJSONError) Is(target error) bool { return target == ErrNotStrictJSON }

func strictJSONError(format string, args ...any) error {
	return &StrictJSONError{message: fmt.Sprintf(format, args...)}
}

// clonePlacement validates value as strict JSON and returns a detached copy
// in the canonical representation (float64 numbers, map[string]any objects,
// []any arrays). Draft handles are cloned from their current content. It
// mirrors tracker.ts clonePlacement: rejected values leave the draft unchanged.
func clonePlacement(value any) (any, error) {
	return cloneStrict(value, map[uintptr]bool{}, nil)
}

// clonePlacementHeld is clonePlacement while held's lock is owned by the caller.
func clonePlacementHeld(value any, held *overlay) (any, error) {
	return cloneStrict(value, map[uintptr]bool{}, held)
}

func handleContent(n *node, held *overlay) any {
	if n.ctx == held {
		held.assertOpen()
		return n.current()
	}
	n.ctx.mu.Lock()
	defer n.ctx.mu.Unlock()
	n.ctx.assertOpen()
	return n.current()
}

func cloneStrict(value any, active map[uintptr]bool, held *overlay) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case bool, string:
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, strictJSONError("Value contains a non-finite number and is not strict JSON")
		}
		return typed, nil
	case *Object:
		return cloneStrict(handleContent(typed.n, held), active, held)
	case *Array:
		return cloneStrict(handleContent(typed.n, held), active, held)
	case map[string]any:
		id := uintptr(reflect.ValueOf(typed).UnsafePointer())
		if active[id] {
			return nil, strictJSONError("Value contains cycles and is not strict JSON")
		}
		active[id] = true
		defer delete(active, id)
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned, err := cloneStrict(item, active, held)
			if err != nil {
				return nil, err
			}
			result[key] = cloned
		}
		return result, nil
	case []any:
		var id uintptr
		if cap(typed) > 0 {
			id = uintptr(reflect.ValueOf(typed).UnsafePointer())
			if active[id] {
				return nil, strictJSONError("Value contains cycles and is not strict JSON")
			}
			active[id] = true
			defer delete(active, id)
		}
		result := make([]any, len(typed))
		for index, item := range typed {
			cloned, err := cloneStrict(item, active, held)
			if err != nil {
				return nil, err
			}
			result[index] = cloned
		}
		return result, nil
	}
	return cloneReflected(reflect.ValueOf(value), active, held)
}

// cloneReflected accepts Go numeric kinds and string-keyed maps or slices of
// other element types, normalizing them to the canonical representation.
func cloneReflected(value reflect.Value, active map[uintptr]bool, held *overlay) (any, error) {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(value.Uint()), nil
	case reflect.Float32:
		return cloneStrict(value.Float(), active, held)
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return nil, strictJSONError("Value must contain strict JSON plain objects or arrays")
		}
		if value.IsNil() {
			return nil, strictJSONError("Value contains a nil map and is not strict JSON")
		}
		result := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			cloned, err := cloneStrict(iterator.Value().Interface(), active, held)
			if err != nil {
				return nil, err
			}
			result[iterator.Key().String()] = cloned
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		result := make([]any, value.Len())
		for index := range value.Len() {
			cloned, err := cloneStrict(value.Index(index).Interface(), active, held)
			if err != nil {
				return nil, err
			}
			result[index] = cloned
		}
		return result, nil
	}
	return nil, strictJSONError("Value contains a non-JSON %s; expected strict JSON", value.Kind())
}

// copyTrusted deep-copies trusted JSON, which tracked content is by construction, so the copy shares no container with
// the value.
func copyTrusted(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = copyTrusted(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = copyTrusted(item)
		}
		return result
	}
	return value
}

// equalTrustedJSON compares two trusted JSON values deeply, ignoring object
// key order.
func equalTrustedJSON(left, right any) bool {
	switch a := left.(type) {
	case map[string]any:
		b, ok := right.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		if sameContainer(a, b) {
			return true
		}
		for key, item := range a {
			other, present := b[key]
			if !present || !equalTrustedJSON(item, other) {
				return false
			}
		}
		return true
	case []any:
		b, ok := right.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for index := range a {
			if !equalTrustedJSON(a[index], b[index]) {
				return false
			}
		}
		return true
	case nil:
		return right == nil
	case bool:
		b, ok := right.(bool)
		return ok && a == b
	case string:
		b, ok := right.(string)
		return ok && a == b
	case float64:
		b, ok := right.(float64)
		return ok && a == b
	}
	return false
}

func isContainer(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

// sameContainer reports whether two containers are the same allocation.
func sameContainer(left, right any) bool {
	leftID, leftOK := containerID(left)
	rightID, rightOK := containerID(right)
	if !leftOK || !rightOK || leftID != rightID {
		return false
	}
	if l, ok := left.([]any); ok {
		r, ok := right.([]any)
		return ok && len(l) == len(r)
	}
	return true
}

// CloneJSON validates value as strict JSON and returns a detached copy in the
// canonical representation. It is the walk behind chord.CopyJSON and draft
// placements.
func CloneJSON(value any) (any, error) { return clonePlacement(value) }
