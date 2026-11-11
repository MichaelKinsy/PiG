// Package strictjson is JSON encoding that reports a value strict JSON cannot carry as Chord's copyJson does
// (packages/chord/src/json.ts), instead of with encoding/json's own text.
package strictjson

import (
	stdjson "encoding/json"
	"errors"
	"reflect"

	"github.com/MichaelKinsy/PiG/chord/delta"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func isNonFinite(text string) bool {
	return text == "NaN" || text == "+Inf" || text == "-Inf"
}

// nonFiniteError is chord's non-finite strict JSON error for a typed value that JSON encoding rejected.
type nonFiniteError struct{}

func (nonFiniteError) Error() string {
	return "Value contains a non-finite number and is not strict JSON"
}

func (nonFiniteError) Is(target error) bool { return target == delta.ErrNotStrictJSON }

// nonFunctionError is chord's strict JSON error for a function a typed value holds (json.ts copy: `Value contains a
// non-JSON ${typeof value}; expected strict JSON`, typeof "function"); encoding/json's own text is "json: unsupported
// type: func()".
type nonFunctionError struct{}

func (nonFunctionError) Error() string {
	return "Value contains a non-JSON function; expected strict JSON"
}

func (nonFunctionError) Is(target error) bool { return target == delta.ErrNotStrictJSON }

// Marshal is json.Marshal with a non-finite number or a function reported as chord's strict JSON error.
func Marshal(value any) ([]byte, error) {
	encoded, err := jsstring.MarshalKeepingSurrogates(value)
	return encoded, Error(err)
}

// Error maps an encoding/json failure to chord's strict JSON error where one applies, and returns any other err
// unchanged. A value's own MarshalJSON may use either JSON package, so the failure can arrive as either package's error.
func Error(err error) error {
	if err == nil {
		return nil
	}
	if unsupported, ok := errors.AsType[*sdkjson.UnsupportedValueError](err); ok && isNonFinite(unsupported.Str) {
		return nonFiniteError{}
	}
	if unsupported, ok := errors.AsType[*stdjson.UnsupportedValueError](err); ok && isNonFinite(unsupported.Str) {
		return nonFiniteError{}
	}
	if unsupported, ok := errors.AsType[*stdjson.UnsupportedTypeError](err); ok && unsupported.Type.Kind() == reflect.Func {
		return nonFunctionError{}
	}
	if unsupported, ok := errors.AsType[*sdkjson.UnsupportedTypeError](err); ok && unsupported.Type.Kind() == reflect.Func {
		return nonFunctionError{}
	}
	return err
}
