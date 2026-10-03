package orderedjson

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// Value and UnmarshalFields use the shared host/Go-SDK codec, which keeps an unmatched UTF-16 unit as the WTF-8 representation (extensions/sdk/json/README.md); a raw container keeps a lone surrogate as its lowercase escape, as JSON.stringify writes it.
//
// Value decodes one JSON value a JavaScript object would hold. A container stays a json.RawMessage holding JSON.stringify(JSON.parse(raw)): the member order it was written in (integer-like keys first, ascending, as a JavaScript object orders them), and numbers and strings in the form JSON.stringify writes, whichever SDK encoded it (Python writes `1.0` and `\u00e9`, the Go SDK `\u003c`). A scalar decodes as encoding/json decodes it into an `any`. Absent, empty, null and invalid input is nil. The result does not alias raw.
func Value(raw []byte) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '{' || raw[0] == '[' {
		canonical, err := jsonstringify.Canonicalize(raw)
		if err != nil {
			return nil
		}
		return json.RawMessage(canonical)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

// UnmarshalFields decodes data into v, a pointer to a struct, like encoding/json, then replaces each named member of an `any` field with [Value] of its raw JSON, so an object an extension supplied is not reordered by a Go map. A named member absent from data leaves its field as it is. v must not have an UnmarshalJSON method that calls UnmarshalFields: decode into a local alias type.
func UnmarshalFields(data []byte, v any, members ...string) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	target := reflect.ValueOf(v)
	if target.Kind() != reflect.Pointer || target.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("orderedjson: UnmarshalFields needs a pointer to a struct, got %T", v)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	structure := target.Elem()
	for _, member := range members {
		value, present := raw[member]
		if !present {
			continue
		}
		field, ok := fieldByJSONName(structure, member)
		if !ok || field.Kind() != reflect.Interface || field.NumMethod() != 0 {
			return fmt.Errorf("orderedjson: %s has no `any` field with JSON name %q", structure.Type(), member)
		}
		if decoded := Value(value); decoded == nil {
			field.SetZero()
		} else {
			field.Set(reflect.ValueOf(decoded))
		}
	}
	return nil
}

func fieldByJSONName(structure reflect.Value, name string) (reflect.Value, bool) {
	for i := range structure.NumField() {
		field := structure.Type().Field(i)
		tag, _, _ := bytes.Cut([]byte(field.Tag.Get("json")), []byte(","))
		if string(tag) == name && field.IsExported() {
			return structure.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// Map returns the members of a JSON object value, whether it is held as a map or as raw JSON.
func Map(value any) (map[string]any, bool) {
	switch value := value.(type) {
	case map[string]any:
		return value, true
	case json.RawMessage:
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return nil, false
		}
		var object map[string]any
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return nil, false
		}
		return object, true
	}
	return nil, false
}

// MarshalMap writes a map's members with the keys of first in that order when present, then the rest sorted: the order a JavaScript object built member by member has when its creator is known.
func MarshalMap(members map[string]any, first ...string) ([]byte, error) {
	keys := make([]string, 0, len(members))
	for _, key := range first {
		if _, present := members[key]; present && !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	rest := make([]string, 0, len(members))
	for key := range members {
		if !slices.Contains(keys, key) {
			rest = append(rest, key)
		}
	}
	slices.Sort(rest)
	out := []byte{'{'}
	for i, key := range append(keys, rest...) {
		if i > 0 {
			out = append(out, ',')
		}
		name, err := Marshal(key)
		if err != nil {
			return nil, err
		}
		value, err := Marshal(members[key])
		if err != nil {
			return nil, err
		}
		out = append(out, name...)
		out = append(out, ':')
		out = append(out, value...)
	}
	return append(out, '}'), nil
}

// MarshalArray writes items as a JSON array in which each map item is written by [MarshalMap] with first; other items are written as encoding/json writes them. A nil slice is `[]`.
func MarshalArray(items []any, first ...string) ([]byte, error) {
	out := []byte{'['}
	for i, item := range items {
		if i > 0 {
			out = append(out, ',')
		}
		var encoded []byte
		var err error
		if object, ok := item.(map[string]any); ok {
			encoded, err = MarshalMap(object, first...)
		} else {
			encoded, err = Marshal(item)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, encoded...)
	}
	return append(out, ']'), nil
}

// Marshal is json.Marshal without HTML escaping. A MarshalJSON method returns text the encoder that called it then writes as it is or HTML-escapes as its own setting says, so text a method produces must not be escaped already: Pi's JSON.stringify never escapes `<`, `>` or `&`.
func Marshal(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
