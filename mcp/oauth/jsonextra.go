package oauth

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// The metadata objects of OAuth keep members this package does not model, as
// upstream's `[key: string]: unknown` index signatures do. Structs embed
// extras and route their JSON through these helpers.

func knownKeys(t reflect.Type, keys map[string]bool) {
	for field := range t.Fields() {
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			knownKeys(field.Type, keys)
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
}

// marshalWithExtra writes known, then the extra members that no known field
// claims, in sorted order.
func marshalWithExtra(known any, extra map[string]json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(known)
	if err != nil || len(extra) == 0 {
		return data, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	knownKeys(reflect.TypeOf(known), keys)
	names := make([]string, 0, len(extra))
	for name := range extra {
		if !keys[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	out := data[:len(data)-1]
	for _, name := range names {
		key, _ := json.Marshal(name)
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(out, key...)
		out = append(out, ':')
		out = append(out, extra[name]...)
	}
	return append(out, '}'), nil
}

// unmarshalWithExtra fills known from the members whose names its fields claim and returns the rest. JavaScript reads
// a member by its exact name, so known decodes only exact matches: encoding/json would also fill a field from a member
// that differs in case, and fail on one of another type.
func unmarshalWithExtra(data []byte, known any) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	knownKeys(reflect.TypeOf(known).Elem(), keys)
	claimed := map[string]json.RawMessage{}
	var extra map[string]json.RawMessage
	for name, value := range fields {
		if keys[name] {
			claimed[name] = value
			continue
		}
		if extra == nil {
			extra = map[string]json.RawMessage{}
		}
		extra[name] = value
	}
	exact, err := json.Marshal(claimed)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(exact, known); err != nil {
		return nil, err
	}
	return extra, nil
}
