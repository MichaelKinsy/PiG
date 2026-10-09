package ai

import (
	"math"
	"reflect"
	"slices"
	"unicode/utf8"
)

// plainWalk is the containers on the path from the root to the value being visited. A container met again below itself
// makes the tree cyclic, which the marshaling path reports.
type plainWalk struct{ active []uintptr }

// enter records a non-empty container; it reports false when the container is already on the path.
func (walk *plainWalk) enter(container any) bool {
	pointer := reflect.ValueOf(container).Pointer()
	if slices.Contains(walk.active, pointer) {
		return false
	}
	walk.active = append(walk.active, pointer)
	return true
}

func (walk *plainWalk) leave() { walk.active = walk.active[:len(walk.active)-1] }

// clonePlainJSON deep-copies a tree built only of nil, bool, string, finite numbers, []any, map[string]any and
// JsonObject, the shape decoded JSON and tool arguments have. It returns exactly what cloneJSONValue returns for such a
// tree: normalizeJSONValue's marshal-and-decode round trip changes nothing in it except that strings with invalid
// UTF-8 would be rewritten, and preserveJSONValueTypes restores every native number. ok is false for any other value;
// the caller then takes the marshaling path.
func clonePlainJSON(value any, walk *plainWalk) (copied any, ok bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case bool:
		return typed, true
	case string:
		return typed, utf8.ValidString(typed)
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		return typed, !math.IsNaN(float64(typed)) && !math.IsInf(float64(typed), 0)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return typed, true
	case map[string]any:
		if typed == nil {
			return typed, true
		}
		return clonePlainObject(typed, walk)
	case JsonObject:
		if typed == nil {
			return map[string]any(nil), true
		}
		return clonePlainObject(typed, walk)
	case []any:
		if typed == nil {
			return typed, true
		}
		if len(typed) > 0 {
			if !walk.enter(typed) {
				return nil, false
			}
			defer walk.leave()
		}
		out := make([]any, len(typed))
		for index, item := range typed {
			if out[index], ok = clonePlainJSON(item, walk); !ok {
				return nil, false
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func clonePlainObject(object map[string]any, walk *plainWalk) (any, bool) {
	if len(object) > 0 {
		if !walk.enter(object) {
			return nil, false
		}
		defer walk.leave()
	}
	out := make(map[string]any, len(object))
	for key, item := range object {
		if !utf8.ValidString(key) {
			return nil, false
		}
		copied, ok := clonePlainJSON(item, walk)
		if !ok {
			return nil, false
		}
		out[key] = copied
	}
	return out, true
}

// isPlainJSON reports whether value is a plain tree that marshals without error; false means the marshaling path must
// decide.
func isPlainJSON(value any, walk *plainWalk) bool {
	switch typed := value.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return true
	case string:
		return utf8.ValidString(typed)
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case float32:
		return !math.IsNaN(float64(typed)) && !math.IsInf(float64(typed), 0)
	case map[string]any:
		return isPlainObject(typed, walk)
	case JsonObject:
		return isPlainObject(typed, walk)
	case []any:
		if len(typed) > 0 {
			if !walk.enter(typed) {
				return false
			}
			defer walk.leave()
		}
		for _, item := range typed {
			if !isPlainJSON(item, walk) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isPlainObject(object map[string]any, walk *plainWalk) bool {
	if len(object) > 0 {
		if !walk.enter(object) {
			return false
		}
		defer walk.leave()
	}
	for key, item := range object {
		if !utf8.ValidString(key) || !isPlainJSON(item, walk) {
			return false
		}
	}
	return true
}
