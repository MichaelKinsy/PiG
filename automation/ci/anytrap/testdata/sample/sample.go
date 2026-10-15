package sample

import (
	"bytes"
	"encoding/json"
)

type typed struct{ A int }

type Holder struct{}

func (h *Holder) Loose(data []byte) (map[string]any, error) {
	var out map[string]any
	err := json.Unmarshal(data, &out)
	return out, err
}

func Boxed(data []byte) (any, error) {
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	var again any
	err := json.NewDecoder(bytes.NewReader(data)).Decode(&again)
	return again, err
}

func Strict(data []byte) (typed, error) {
	var out typed
	err := json.Unmarshal(data, &out)
	return out, err
}

func Raw(data []byte) (json.RawMessage, error) {
	var out json.RawMessage
	err := json.Unmarshal(data, &out)
	return out, err
}

func List(data []byte) ([]any, error) {
	var out []any
	err := json.Unmarshal(data, &out)
	return out, err
}

func Decode[T any](data []byte) (T, error) {
	var out T
	err := json.Unmarshal(data, &out)
	return out, err
}
