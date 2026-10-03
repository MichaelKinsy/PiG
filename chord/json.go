package chord

import "github.com/MichaelKinsy/PiG/chord/delta"

// Ports packages/chord/src/json.ts copyJson.

// CopyJSON returns a detached copy of a strict JSON value: nil, bool, float64
// (Go integer and float32 kinds are normalized), string, string-keyed maps,
// and slices. Non-finite numbers, cycles, and other Go kinds return a
// *delta.StrictJSONError. Go has no undefined, so upstream's
// omitUndefinedProperties option has no counterpart.
func CopyJSON(value any) (any, error) { return delta.CloneJSON(value) }

// CopyJSONObject is CopyJSON for an object root.
func CopyJSONObject(value map[string]any) (map[string]any, error) {
	copied, err := delta.CloneJSON(value)
	if err != nil {
		return nil, err
	}
	object, _ := copied.(map[string]any)
	return object, nil
}
