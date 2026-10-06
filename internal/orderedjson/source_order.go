package orderedjson

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
)

// MarshalInSourceOrder encodes value with the object member order of the JSON text source, as a JavaScript object holds the members its handlers edited: a member the source lists keeps its place, a member it does not list follows in sorted order, a removed member is gone, and integer-like keys come first in ascending order, as in every JavaScript object. A map has no insertion order, so two members added after the source was taken are sorted. An array matches its source element by element. A source that is not the same shape as value orders nothing, and the members are sorted.
func MarshalInSourceOrder(value any, source []byte) ([]byte, error) {
	switch typed := value.(type) {
	case map[string]any:
		var members *Object
		if parsed, err := Parse(source); err == nil {
			members = parsed
		}
		var keys []string
		if members != nil {
			for _, key := range members.Keys() {
				if _, ok := typed[key]; ok {
					keys = append(keys, key)
				}
			}
		}
		var added []string
		for key := range typed {
			if !slices.Contains(keys, key) {
				added = append(added, key)
			}
		}
		slices.Sort(added)
		keys = append(keys, added...)
		slices.SortStableFunc(keys, func(a, b string) int { return cmp.Compare(arrayIndex(a), arrayIndex(b)) })
		var out bytes.Buffer
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			name, err := Marshal(key)
			if err != nil {
				return nil, err
			}
			var memberSource []byte
			if members != nil {
				memberSource, _ = members.Get(key)
			}
			member, err := MarshalInSourceOrder(typed[key], memberSource)
			if err != nil {
				return nil, err
			}
			out.Write(name)
			out.WriteByte(':')
			out.Write(member)
		}
		out.WriteByte('}')
		return out.Bytes(), nil
	case []any:
		var elements []json.RawMessage
		if err := json.Unmarshal(source, &elements); err != nil {
			elements = nil
		}
		var out bytes.Buffer
		out.WriteByte('[')
		for i, element := range typed {
			if i > 0 {
				out.WriteByte(',')
			}
			var elementSource []byte
			if i < len(elements) {
				elementSource = elements[i]
			}
			encoded, err := MarshalInSourceOrder(element, elementSource)
			if err != nil {
				return nil, err
			}
			out.Write(encoded)
		}
		out.WriteByte(']')
		return out.Bytes(), nil
	}
	return Marshal(value)
}

// notArrayIndex orders after every array index; a JavaScript object enumerates its other keys in insertion order after its array indices.
const notArrayIndex = 1<<32 - 1

// arrayIndex returns key's value when key is an array index (a canonical decimal below 2^32-1), which a JavaScript object enumerates first in ascending order, and notArrayIndex otherwise.
func arrayIndex(key string) uint64 {
	if n, err := strconv.ParseUint(key, 10, 32); err == nil && n < notArrayIndex && strconv.FormatUint(n, 10) == key {
		return n
	}
	return notArrayIndex
}
