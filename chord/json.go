package chord

import "github.com/MichaelKinsy/PiG/chord/delta"

// Ports packages/chord/src/json.ts copyJson.

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
