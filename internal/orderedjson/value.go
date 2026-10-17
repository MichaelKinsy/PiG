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
//
// It decodes data once: each named field (an `any` holding nil) is pointed at a placeholder that keeps the decoder from building the member
// as a map, and the member's raw JSON is found by a walk over the top-level object. Anything that walk cannot settle exactly as the three
// decodes did (an error, a field already set or not an `any`, a key that matches a member only case-insensitively) takes those decodes.
func UnmarshalFields(data []byte, v any, members ...string) error {
	target := reflect.ValueOf(v)
	if target.Kind() != reflect.Pointer || target.IsNil() || target.Elem().Kind() != reflect.Struct || len(members) > maxSinglePassMembers {
		return unmarshalFieldsThreePass(data, v, members)
	}
	structure := target.Elem()
	var fieldArray [maxSinglePassMembers]reflect.Value
	fields := fieldArray[:len(members)]
	for i, member := range members {
		field, ok := fieldByJSONName(structure, member)
		if !ok || field.Kind() != reflect.Interface || field.NumMethod() != 0 || !field.IsNil() {
			return unmarshalFieldsThreePass(data, v, members)
		}
		fields[i] = field
	}
	placeholder := reflect.ValueOf(&skipMember{})
	for _, field := range fields {
		field.Set(placeholder)
	}
	err := json.Unmarshal(data, v)
	var rawArray [maxSinglePassMembers][]byte
	raws := rawArray[:len(members)]
	if err != nil || !lastMembers(data, members, raws) {
		for _, field := range fields {
			field.SetZero()
		}
		return unmarshalFieldsThreePass(data, v, members)
	}
	for i, field := range fields {
		// An absent member leaves the field the nil it held; null and invalid JSON are Value's nil.
		if decoded := Value(raws[i]); decoded == nil {
			field.SetZero()
		} else {
			field.Set(reflect.ValueOf(decoded))
		}
	}
	return nil
}

// maxSinglePassMembers is the most named members UnmarshalFields decodes in one pass; callers name one or two.
const maxSinglePassMembers = 4

// skipMember is the placeholder a named field holds while data decodes: the decoder hands it the member's raw JSON, which it ignores.
type skipMember struct{}

func (*skipMember) UnmarshalJSON([]byte) error { return nil }

// lastMembers sets raws[i] to the raw JSON of the last member of the top-level object in data whose key is members[i] (nil when there is
// none), as a map[string]json.RawMessage decode keeps it. data is valid JSON. It reports false when a key matches a member only
// case-insensitively, which encoding/json decodes into that member's field.
func lastMembers(data []byte, members []string, raws [][]byte) bool {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return true
	}
	i = skipSpace(data, i+1)
	for i < len(data) && data[i] != '}' {
		keyEnd := stringEnd(data, i)
		key := data[i+1 : keyEnd-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			var unquoted string
			if json.Unmarshal(data[i:keyEnd], &unquoted) != nil {
				return false
			}
			key = []byte(unquoted)
		}
		start := skipSpace(data, skipSpace(data, keyEnd)+1) // past the colon
		i = valueEnd(data, start)
		for m, member := range members {
			if string(key) == member {
				raws[m] = data[start:i]
			} else if bytes.EqualFold(key, []byte(member)) {
				return false
			}
		}
		if i = skipSpace(data, i); i < len(data) && data[i] == ',' {
			i = skipSpace(data, i+1)
		}
	}
	return true
}

func skipSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r') {
		i++
	}
	return i
}

// stringEnd is the index just past the string token that starts at data[i].
func stringEnd(data []byte, i int) int {
	for j := i + 1; ; {
		quote := bytes.IndexByte(data[j:], '"')
		if quote < 0 {
			return len(data)
		}
		j += quote
		backslashes := 0
		for k := j - 1; k > i && data[k] == '\\'; k-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return j + 1
		}
		j++
	}
}

// valueEnd is the index just past the value token that starts at data[i].
func valueEnd(data []byte, i int) int {
	switch data[i] {
	case '"':
		return stringEnd(data, i)
	case '{', '[':
		depth := 0
		for i < len(data) {
			switch data[i] {
			case '"':
				i = stringEnd(data, i)
				continue
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return i
	}
	for i < len(data) && data[i] != ',' && data[i] != '}' && data[i] != ']' && data[i] != ' ' && data[i] != '\t' && data[i] != '\n' && data[i] != '\r' {
		i++
	}
	return i
}

// unmarshalFieldsThreePass is UnmarshalFields by three decodes: the struct, the members again as raw JSON, then Value of each named member.
func unmarshalFieldsThreePass(data []byte, v any, members []string) error {
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
