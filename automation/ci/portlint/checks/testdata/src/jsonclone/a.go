package jsonclone

import (
	"bytes"
	"encoding/json"
)

type T struct{ A []int }

func bad(in T) (T, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return T{}, err
	}
	var out T
	err = json.Unmarshal(data, &out) // want `Unmarshal decodes JSON this function just encoded`
	return out, err
}

func badDecoder(in map[string]any) (any, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out any
	err = json.NewDecoder(bytes.NewReader(raw)).Decode(&out) // want `NewDecoder decodes JSON this function just encoded`
	return out, err
}

// Decoding input the function did not encode is not a round trip.
func good(data []byte) (T, error) {
	var out T
	err := json.Unmarshal(data, &out)
	return out, err
}

// Encoding for output is not a round trip.
func encodeOnly(in T) ([]byte, error) { return json.Marshal(in) }
