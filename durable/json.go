package durable

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable/internal/ordered"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// ToJsonValue returns a detached strict JSON copy of value: nil, bool, float64, string, []any, or *delta.JsonObject. It
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
	// Objects decode in encoding order, so a struct's fields keep their declared order.
	return decodeJson(encoded)
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
// through its JSON encoding. A T that is a Go map decodes from the JSON encoding, since the copy of an object is a
// *delta.JsonObject.
func FromJsonValue[T any](value JsonValue) (T, error) {
	var decoded T
	if _, ok := value.(T); ok && isJSONKind(value) {
		copied, err := chord.CopyJSON(value)
		if err != nil {
			return decoded, err
		}
		if typed, ok := copied.(T); ok {
			return typed, nil
		}
	}
	encoded, err := marshalStrict(value)
	if err != nil {
		return decoded, err
	}
	if holdsDynamicJSON[T]() {
		dynamic, err := decodeJson(encoded)
		if err != nil {
			return decoded, err
		}
		typed, ok := dynamic.(T)
		if !ok && dynamic != nil {
			return decoded, fmt.Errorf("value encodes as %T, not %T", dynamic, decoded)
		}
		return typed, nil
	}
	if err := sdkjson.Unmarshal(encoded, &decoded); err != nil {
		return decoded, err
	}
	if ordered.HasDynamic(reflect.TypeFor[T]()) {
		// A JsonValue member holds the text's objects in order, as JSON.parse gives Pi.
		tree, err := decodeJson(encoded)
		if err != nil {
			return decoded, err
		}
		ordered.Restore(&decoded, tree)
	}
	return decoded, nil
}

func isJSONKind(value any) bool {
	switch value.(type) {
	case nil, bool, float64, string, *delta.JsonObject, map[string]any, []any:
		return true
	}
	return false
}

// holdsDynamicJSON reports whether T is a dynamic JSON kind, which encoding/json would fill with unordered maps.
func holdsDynamicJSON[T any]() bool {
	switch any((*T)(nil)).(type) {
	case *any, *[]any, **delta.JsonObject:
		return true
	}
	return false
}

// marshalStrict is json.Marshal with a non-finite number reported as chord's strict JSON error.
func marshalStrict(value any) ([]byte, error) {
	encoded, err := sdkjson.Marshal(value)
	if unsupported, ok := errors.AsType[*sdkjson.UnsupportedValueError](err); ok && isNonFinite(unsupported.Str) {
		return nil, nonFiniteError{}
	}
	return encoded, err
}

// CopyJson returns a detached copy of a JSON value.
func CopyJson(value JsonValue) (JsonValue, error) {
	return ToJsonValue(value)
}

// unmarshalOrdered decodes data into target, then gives target's JsonValue members the text's key order, as JSON.parse gives Pi.
func unmarshalOrdered(data []byte, target any) error {
	if dynamic, ok := target.(*JsonValue); ok {
		value, err := decodeJson(data)
		if err == nil {
			*dynamic = value
		}
		return err
	}
	if err := sdkjson.Unmarshal(data, target); err != nil {
		return err
	}
	if ordered.HasDynamic(reflect.TypeOf(target).Elem()) {
		tree, err := decodeJson(data)
		if err != nil {
			return err
		}
		ordered.Restore(target, tree)
	}
	return nil
}

// decodeJson is delta.DecodeJson with JavaScript string semantics: a lone surrogate escape (`"\ud83d"`) is kept, as the
// WTF-8 form of its UTF-16 unit, where encoding/json would replace it with U+FFFD. Pi's strings hold such a unit and
// JSON.stringify writes it back as the escape, so a value must survive a copy unchanged.
func decodeJson(data []byte) (any, error) {
	return chordjson.DecodeUnquoting(data, func(raw []byte, text *string) error { return sdkjson.Unmarshal(raw, text) })
}
