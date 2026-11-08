package codemode

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
)

func (v jval) keysV1() []string {
	decoder := json.NewDecoder(bytes.NewReader(v))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	var keysV1 []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return keysV1
		}
		keysV1 = append(keysV1, key.(string))
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return keysV1
		}
	}
	return keysV1
}
