package jsvalue

import "github.com/MichaelKinsy/PiG/chord/delta"

// FromDelta converts a decoded Chord JSON value (objects as *delta.JsonObject, in key order) to a value of this package: *Object for objects,
// []any for arrays, other values as they are (integers as float64).
func FromDelta(value any) any {
	switch typed := value.(type) {
	case *delta.JsonObject:
		if typed == nil {
			return nil
		}
		object := NewObject()
		for key, member := range typed.All() {
			object.Set(key, FromDelta(member))
		}
		return object
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = FromDelta(item)
		}
		return out
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return value
}

// ObjectFromDelta is FromDelta for a value that must be an object: an empty object for anything else.
func ObjectFromDelta(value any) *Object {
	if object, ok := FromDelta(value).(*Object); ok {
		return object
	}
	return NewObject()
}
