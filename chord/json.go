package chord

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/json.ts (JsonValue, copyJson, isJsonValue). JsonRepresentation<T> is a compile-time mapped
// type with no Go form: a Go value's strict JSON representation is what CopyJSON returns for it.

// JsonValue is a strict JSON value: nil, bool, float64, string, []any or *delta.JsonObject.
type JsonValue = delta.JsonValue

// IsJsonValue reports whether value is finite strict JSON with no cycles, without normalizing it.
func IsJsonValue(value any) bool { return chordjson.IsValue(value) }

// CopyJSON returns a detached copy of a strict JSON value: nil, bool, float64
// (Go integer and float32 kinds are normalized), string, *delta.JsonObject,
// string-keyed maps, and slices. Objects are copied as *delta.JsonObject in
// own-key order; a Go map's keys, which have no order, follow
// chordjson.SortOwnKeys. Non-finite numbers, cycles, and other Go kinds return a
// *delta.StrictJSONError. Go has no undefined, so upstream's
// omitUndefinedProperties option has no counterpart.
func CopyJSON(value any) (any, error) { return delta.CloneJSON(value) }

// CopyJSONObject is CopyJSON for an object root: a *delta.JsonObject or a string-keyed map. Any other value returns nil.
func CopyJSONObject(value any) (*delta.JsonObject, error) {
	copied, err := delta.CloneJSON(value)
	if err != nil {
		return nil, err
	}
	object, _ := copied.(*delta.JsonObject)
	return object, nil
}
