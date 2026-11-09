package orderedjson

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

func (o *Object) UnmarshalJSONV1(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return errors.New("orderedjson: value is not an object")
	}
	o.keys, o.indexKeys, o.values = nil, 0, map[string]json.RawMessage{}
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
