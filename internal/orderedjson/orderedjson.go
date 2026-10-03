// Package orderedjson holds JSON objects that keep their keys in the order
// they were read or set, the way a JavaScript object does. MCP schemas and
// tool arguments cross the client unchanged, so their key order must survive
// a merge such as `{...schema, type: "object"}`.
package orderedjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Object is a JSON object with insertion-ordered keys. Values stay raw, so
// numbers and nested objects keep their exact text.
type Object struct {
	keys   []string
	values map[string]json.RawMessage
}

// New returns an empty object.
func New() *Object { return &Object{values: map[string]json.RawMessage{}} }

// Parse decodes data as a JSON object. It returns an error for any other JSON
// value, including null.
func Parse(data []byte) (*Object, error) {
	o := New()
	if err := o.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	return o, nil
}

// IsObject reports whether data is a JSON object.
func IsObject(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{' && json.Valid(data)
}

// UnmarshalJSON reads an object and keeps its key order. A repeated key keeps
// its first position and its last value, as JSON.parse does.
func (o *Object) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errors.New("orderedjson: value is not an object")
	}
	o.keys, o.values = nil, map[string]json.RawMessage{}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("orderedjson: unexpected key %v", keyToken)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		o.Set(key, raw)
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	return nil
}

// MarshalJSON writes the keys in order.
func (o *Object) MarshalJSON() ([]byte, error) {
	if o == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(o.values[key])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Len is the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// Keys returns the keys in order.
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Has reports whether key is present, even with a null value.
func (o *Object) Has(key string) bool { _, ok := o.values[key]; return ok }

// Get returns the raw value of key.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	value, ok := o.values[key]
	return value, ok
}

// Set replaces the value of an existing key in place or appends a new key.
func (o *Object) Set(key string, value json.RawMessage) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// SetValue marshals value and sets it under key.
func (o *Object) SetValue(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	o.Set(key, raw)
	return nil
}

// Delete removes key.
func (o *Object) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	for i, existing := range o.keys {
		if existing == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// Clone returns a copy that shares no key storage.
func (o *Object) Clone() *Object {
	c := New()
	for _, key := range o.keys {
		c.Set(key, o.values[key])
	}
	return c
}
