package codemode

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"

	"github.com/MichaelKinsy/PiG/internal/jsonopts"
)

// jval is a script value as JSON text: empty is JavaScript's undefined, and an object keeps its keys in the order the script
// wrote them, which a Go map would not.
type jval json.RawMessage

func (v jval) kind() string {
	trimmed := bytes.TrimSpace(v)
	if len(trimmed) == 0 {
		return "undefined"
	}
	switch trimmed[0] {
	case 'n':
		return "null"
	case '[':
		return "array"
	case '{':
		return "object"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	}
	return "number"
}

func (v jval) isRecord() bool { return v.kind() == "object" }

func (v jval) str() (string, bool) {
	if v.kind() != "string" {
		return "", false
	}
	var text string
	return text, json.Unmarshal(v, &text) == nil
}

// keys are the object's keys in the order written (JavaScript would list integer-like keys first, ascending, which a
// model call's contexts do not use as keys).
func (v jval) keys() []string {
	decoder := jsontext.NewDecoder(bytes.NewReader(v), jsonopts.Parse)
	if token, err := decoder.ReadToken(); err != nil || token.Kind() != '{' {
		return nil
	}
	var keys []string
	for decoder.PeekKind() != '}' {
		key, err := decoder.ReadToken()
		if err != nil {
			return keys
		}
		keys = append(keys, key.String()) // a Token is void after the next call on the decoder
		if decoder.SkipValue() != nil {
			return keys
		}
	}
	return keys
}

// field is the property of an object; undefined when absent or when v is not an object. A duplicate key keeps the last
// value, as JSON.parse does.
func (v jval) field(name string) jval {
	if !v.isRecord() {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(v, &fields) != nil {
		return nil
	}
	return jval(fields[name])
}

func (v jval) items() []jval {
	var raw []json.RawMessage
	if v.kind() != "array" || json.Unmarshal(v, &raw) != nil {
		return nil
	}
	items := make([]jval, len(raw))
	for i, item := range raw {
		items[i] = jval(item)
	}
	return items
}

// values are the object's property values in key order.
func (v jval) values() []jval {
	values := make([]jval, 0)
	for _, key := range v.keys() {
		values = append(values, v.field(key))
	}
	return values
}

// json is JSON.stringify of the value: undefined stays empty.
func (v jval) json() string {
	if v.kind() == "undefined" {
		return ""
	}
	var out bytes.Buffer
	if json.Compact(&out, v) != nil {
		return string(v)
	}
	return out.String()
}
