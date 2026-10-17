package orderedjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// JSONNames returns the member names encoding/json reads and writes for the exported fields of the struct type t, which has no embedded
// fields: each field's json tag name, or its Go name when the tag gives none. A field tagged "-" has no member.
func JSONNames(t reflect.Type) map[string]struct{} {
	names := make(map[string]struct{}, t.NumField())
	for field := range t.Fields() {
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = field.Name
		}
		names[name] = struct{}{}
	}
	return names
}

// AppendMembers returns the encoded JSON object with members added after its own, in sorted key order. No key of members may already be in
// object. With no members it returns object unchanged; otherwise the result does not alias object.
func AppendMembers(object []byte, members map[string]json.RawMessage) ([]byte, error) {
	if len(members) == 0 {
		return object, nil
	}
	trimmed := bytes.TrimSpace(object)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, errors.New("orderedjson: value is not an object")
	}
	body := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
	out := make([]byte, 0, len(trimmed)+len(members)*16)
	out = append(out, '{')
	out = append(out, body...)
	for _, key := range slices.Sorted(maps.Keys(members)) {
		if len(out) > 1 {
			out = append(out, ',')
		}
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		out = append(out, name...)
		out = append(out, ':')
		out = append(out, members[key]...)
	}
	return append(out, '}'), nil
}
