package subprocess

import "reflect"

// equalWireValue reports whether a and b encode to the same JSON, for the value shapes the host builds for a state push: nil, booleans, numbers, strings, string-keyed maps and slices of those. It allocates nothing for them, which matters because a push compares its theme and keybinding table with the last pushed copy on every dispatch; any other shape falls back to reflect.DeepEqual. Map order never matters, as encoding sorts keys.
func equalWireValue(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case int:
		y, ok := b.(int)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		if reflect.ValueOf(x).UnsafePointer() == reflect.ValueOf(y).UnsafePointer() {
			return true // one shared map: the caller must not modify it in place
		}
		for key, value := range x {
			other, present := y[key]
			if !present || !equalWireValue(value, other) {
				return false
			}
		}
		return true
	case map[string]string:
		y, ok := b.(map[string]string)
		if !ok || len(x) != len(y) {
			return false
		}
		for key, value := range x {
			if other, present := y[key]; !present || value != other {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalWireValue(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}
