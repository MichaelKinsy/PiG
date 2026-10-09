package delta

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// JsonObject is a JSON object that keeps JavaScript's property order: array-index keys first in ascending numeric order, then the other keys in insertion order. It is the object representation of every value this package stores and returns.
type JsonObject = chordjson.Object

// NewJsonObject returns an empty JsonObject with room for capacity keys.
func NewJsonObject(capacity int) *JsonObject { return chordjson.NewObject(capacity) }

// JsonObjectOf returns a JsonObject holding pairs in order; pairs alternates keys and values.
func JsonObjectOf(pairs ...any) *JsonObject { return chordjson.ObjectOf(pairs...) }

// DecodeJson parses JSON text into a strict JSON value whose objects are *JsonObject with their keys in document order, as JSON.parse does.
func DecodeJson(data []byte) (any, error) { return chordjson.Decode(data) }

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
// in the canonical representation (float64 numbers, *JsonObject objects,
// []any arrays). A Go map has no key order; its keys are copied in
// chordjson.SortOwnKeys order. Draft handles are cloned from their current content. It
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
	case *JsonObject:
		if typed == nil {
			return nil, strictJSONError("Value contains a nil map and is not strict JSON")
		}
		id := uintptr(reflect.ValueOf(typed).UnsafePointer())
		if active[id] {
			return nil, strictJSONError("Value contains cycles and is not strict JSON")
		}
		active[id] = true
		defer delete(active, id)
		result := chordjson.NewObject(typed.Len())
		for key, item := range typed.All() {
			cloned, err := cloneStrict(item, active, held)
			if err != nil {
				return nil, err
			}
			result.Set(key, cloned)
		}
		return result, nil
	case map[string]any:
		if typed == nil {
			return nil, strictJSONError("Value contains a nil map and is not strict JSON")
		}
		id := uintptr(reflect.ValueOf(typed).UnsafePointer())
		if active[id] {
			return nil, strictJSONError("Value contains cycles and is not strict JSON")
		}
		active[id] = true
		defer delete(active, id)
		result := chordjson.NewObject(len(typed))
		for _, key := range chordjson.MapOwnKeys(typed) {
			cloned, err := cloneStrict(typed[key], active, held)
			if err != nil {
				return nil, err
			}
			result.Set(key, cloned)
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
		keys := make([]string, 0, value.Len())
		for _, key := range value.MapKeys() {
			keys = append(keys, key.String())
		}
		chordjson.SortOwnKeys(keys)
		result := chordjson.NewObject(len(keys))
		for _, key := range keys {
			cloned, err := cloneStrict(value.MapIndex(reflect.ValueOf(key).Convert(value.Type().Key())).Interface(), active, held)
			if err != nil {
				return nil, err
			}
			result.Set(key, cloned)
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
	case *JsonObject:
		result := chordjson.NewObject(typed.Len())
		for key, item := range typed.All() {
			result.Set(key, copyTrusted(item))
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
	case *JsonObject:
		b, ok := right.(*JsonObject)
		if !ok || a.Len() != b.Len() {
			return false
		}
		if sameContainer(a, b) {
			return true
		}
		for key, item := range a.All() {
			other, present := b.Get(key)
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

// containerID is the address of a container allocation; an empty slice without capacity has none.
func containerID(value any) (uintptr, bool) {
	switch container := value.(type) {
	case *JsonObject:
		return uintptr(reflect.ValueOf(container).UnsafePointer()), container != nil
	case []any:
		if cap(container) == 0 {
			return 0, false
		}
		return uintptr(reflect.ValueOf(container).UnsafePointer()), true
	}
	return 0, false
}
