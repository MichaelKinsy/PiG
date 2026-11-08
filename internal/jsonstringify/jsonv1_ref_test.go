package jsonstringify

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
)

func objectV1(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var members []member
	positions := map[string]int{}
	for decoder.More() {
		start := decoder.InputOffset()
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		key := string(stringToken(bytes.TrimLeft(raw[start:decoder.InputOffset()], ", \t\r\n")))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		encoded, err := canonicalize(value)
		if err != nil {
			return nil, err
		}
		if position, exists := positions[key]; exists {
			members[position].value = encoded
			continue
		}
		index := uint64(math.MaxUint32)
		name := key[1 : len(key)-1]
		if n, err := strconv.ParseUint(name, 10, 32); err == nil && strconv.FormatUint(n, 10) == name {
			index = n
		}
		positions[key] = len(members)
		members = append(members, member{key, encoded, index})
	}
	slices.SortStableFunc(members, func(a, b member) int {
		if a.index < b.index {
			return -1
		}
		if a.index > b.index {
			return 1
		}
		return 0
	})
	out := []byte{'{'}
	for i, entry := range members {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, entry.key...)
		out = append(out, ':')
		out = append(out, entry.value...)
	}
	return append(out, '}'), nil
}
