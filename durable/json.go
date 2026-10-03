package durable

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// ToJsonValue returns a detached strict JSON copy of value: nil, bool, float64, string, []any, or map[string]any. It
// is Chord's copyJson: JSON kinds are copied and checked by chord.CopyJSON, and typed Go values (structs, values with
// a JSON encoding) are copied through their JSON encoding. A non-finite number fails with a strict JSON error, as
// copyJson throws.
func ToJsonValue(value any) (JsonValue, error) {
	copied, copyErr := chord.CopyJSON(value)
	if copyErr == nil {
		return copied, nil
	}
	encoded, err := marshalStrict(value)
	if err != nil {
		if errors.Is(err, delta.ErrNotStrictJSON) {
			return nil, err
		}
		return nil, copyErr
	}
	var decoded JsonValue
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func isNonFinite(text string) bool {
	return text == "NaN" || text == "+Inf" || text == "-Inf"
}

// nonFiniteError is chord's non-finite strict JSON error for a typed value that JSON encoding rejected.
type nonFiniteError struct{}

func (nonFiniteError) Error() string {
	return "Value contains a non-finite number and is not strict JSON"
}

func (nonFiniteError) Is(target error) bool { return target == delta.ErrNotStrictJSON }

// ToJsonObject returns the JSON object representation of value; it fails when value does not encode as an object.
func ToJsonObject(value any) (JsonObject, error) {
	decoded, err := ToJsonValue(value)
	if err != nil {
		return nil, err
	}
	object, ok := decoded.(JsonObject)
	if !ok {
		return nil, fmt.Errorf("value encodes as %T, not a JSON object", decoded)
	}
	return object, nil
}

// FromJsonValue returns a detached copy of a JSON value as T. When T holds JSON kinds (JsonValue, JsonObject, []any)
// the copy is chord.CopyJSON, so a non-JSON or non-finite value fails as strict JSON; otherwise the value is decoded
// through its JSON encoding.
func FromJsonValue[T any](value JsonValue) (T, error) {
	var decoded T
	if _, ok := value.(T); ok && isJSONKind(value) {
		copied, err := chord.CopyJSON(value)
		if err != nil {
			return decoded, err
		}
		typed, _ := copied.(T)
		return typed, nil
	}
	encoded, err := marshalStrict(value)
	if err != nil {
		return decoded, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

func isJSONKind(value any) bool {
	switch value.(type) {
	case nil, bool, float64, string, map[string]any, []any:
		return true
	}
	return false
}

// marshalStrict is json.Marshal with a non-finite number reported as chord's strict JSON error.
func marshalStrict(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if unsupported, ok := errors.AsType[*json.UnsupportedValueError](err); ok && isNonFinite(unsupported.Str) {
		return nil, nonFiniteError{}
	}
	return encoded, err
}

// CopyJson returns a detached copy of a JSON value.
func CopyJson(value JsonValue) (JsonValue, error) {
	return ToJsonValue(value)
}
