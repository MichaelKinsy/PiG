package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// marshalOrdered emits only the listed JSON members of a struct value, in list
// order, honoring each field's omitempty tag. Members that are not listed are
// never encoded, so union variants emit exactly their upstream members.
func marshalOrdered(value any, keys []string) ([]byte, error) {
	fields := map[string]taggedField{}
	collectTaggedFields(reflect.ValueOf(value), fields)
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for _, key := range keys {
		field, ok := fields[key]
		if !ok || (field.omitEmpty && isEmptyJSONValue(field.value)) {
			continue
		}
		raw, err := json.Marshal(field.value.Interface())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.WriteString(jsonQuote(key))
		out.WriteByte(':')
		out.Write(raw)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

type taggedField struct {
	value     reflect.Value
	omitEmpty bool
}

// collectTaggedFields indexes the exported fields of a struct by JSON name,
// descending into embedded structs like encoding/json.
func collectTaggedFields(value reflect.Value, fields map[string]taggedField) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	for index := range value.NumField() {
		field := value.Type().Field(index)
		tag := field.Tag.Get("json")
		name, options, _ := strings.Cut(tag, ",")
		if field.Anonymous && tag == "" {
			collectTaggedFields(value.Field(index), fields)
			continue
		}
		if !field.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = taggedField{value: value.Field(index), omitEmpty: strings.Contains(options, "omitempty")}
	}
}

// isEmptyJSONValue mirrors encoding/json's omitempty test.
func isEmptyJSONValue(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool:
		return !value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return value.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return value.IsNil()
	default:
		return false
	}
}

// optionalJSON returns a pointer to a present JSON member, keeping an explicit
// null distinct from an absent key.
func optionalJSON(fields map[string]json.RawMessage, key string) (*JsonValue, error) {
	raw, ok := fields[key]
	if !ok {
		return nil, nil
	}
	var value JsonValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return &value, nil
}

func rawFields(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func stringsOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// jsonQuote quotes text as JSON.stringify does.
func jsonQuote(text string) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(text); err != nil {
		return `""`
	}
	return strings.TrimSuffix(out.String(), "\n")
}
