package modeldata

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
)

func parseModelDataObjectV1(data []byte) *modelDataObject {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil
	}
	object := &modelDataObject{values: map[string]json.RawMessage{}}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil
		}
		key, ok := token.(string)
		if !ok {
			return nil
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil
		}
		if _, exists := object.values[key]; !exists {
			object.keys = append(object.keys, key)
		}
		object.values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil
	}
	orderModelIntegerKeys(object.keys)
	return object
}
