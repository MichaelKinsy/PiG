package main

import (
	"bytes"
	"fmt"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// rpcObject carries source insertion order and UTF-16 string content for JSON/RPC objects. Nested wire values remain raw JSON when read so removing a field does not reorder its siblings or descendants.
type rpcObject []rpcField

type rpcField struct {
	name  string
	value any
}

func (object rpcObject) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range object {
		if i > 0 {
			out.WriteByte(',')
		}
		name, err := json.Marshal(field.name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func (object *rpcObject) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("RPC event is not an object")
	}
	var fields rpcObject
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := name.(string)
		if !ok {
			return fmt.Errorf("RPC event key is not a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		fields = append(fields, rpcField{key, value})
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*object = fields
	return nil
}
