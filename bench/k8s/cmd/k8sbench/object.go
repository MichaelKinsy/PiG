package main

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/jsonopts"
)

// object is a JSON object that keeps its key order, so a workload's result line passes through with its fields in
// the order the workload wrote them, and the tracker line has the field order of durable-report's track-row.sh.
type object struct {
	keys   []string
	values map[string]json.RawMessage
}

func newObject() *object { return &object{values: map[string]json.RawMessage{}} }

// set replaces a key's value in place or appends the key.
func (o *object) set(key string, value any) {
	raw, err := marshalPlain(value)
	if err != nil {
		panic(fmt.Sprintf("k8sbench: encode %s: %v", key, err))
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = raw
}

// setRaw sets a key to an already encoded value.
func (o *object) setRaw(key string, raw json.RawMessage) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = raw
}

func (o *object) has(key string) bool { _, ok := o.values[key]; return ok }

// get decodes a key's value into dst and reports whether the key exists.
func (o *object) get(key string, dst any) bool {
	raw, ok := o.values[key]
	if !ok {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

func (o *object) str(key string) string {
	var s string
	o.get(key, &s)
	return s
}

func (o *object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, err := marshalPlain(k)
		if err != nil {
			return nil, err
		}
		b.Write(name)
		b.WriteByte(':')
		b.Write(o.values[k])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (o *object) UnmarshalJSON(data []byte) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(data), jsonopts.Parse)
	token, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() != '{' {
		return errors.New("not a JSON object")
	}
	o.keys, o.values = nil, map[string]json.RawMessage{}
	for decoder.PeekKind() != '}' {
		token, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		key := token.String() // a Token and the bytes ReadValue returns are void after the next call on the decoder
		valueBytes, err := decoder.ReadValue()
		if err != nil {
			return err
		}
		value := json.RawMessage(bytes.Clone(valueBytes))
		if _, dup := o.values[key]; !dup {
			o.keys = append(o.keys, key)
		}
		o.values[key] = value
	}
	_, err = decoder.ReadToken()
	return err
}

// marshalPlain encodes like JSON.stringify: no HTML escaping of <, > and &.
func marshalPlain(value any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
