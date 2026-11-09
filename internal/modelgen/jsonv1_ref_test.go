package modelgen

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

func ParseJSONV1(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValueV1(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after JSON value")
	}
	return value, nil
}

func decodeValueV1(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch v := token.(type) {
	case json.Delim:
		switch v {
		case '{':
			object := NewObj()
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				value, err := decodeValueV1(decoder)
				if err != nil {
					return nil, err
				}
				object.Set(keyToken.(string), value)
			}
			_, err := decoder.Token()
			return object, err
		case '[':
			list := []any{}
			for decoder.More() {
				value, err := decodeValueV1(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			_, err := decoder.Token()
			return list, err
		}
	case json.Number:
		return jsnumber.FromJSON([]byte(v.String())), nil
	}
	return token, nil
}
