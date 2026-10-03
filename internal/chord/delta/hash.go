package delta

import (
	"hash/fnv"
	"math"
	"sort"
)

// hashValue is a structural hash of a JSON value: equal values hash equal whatever their object key order or number type.
func hashValue(value JsonValue) uint64 {
	hasher := fnv.New64a()
	var write func(value any)
	var scratch [9]byte
	write = func(value any) {
		switch typed := value.(type) {
		case nil:
			hasher.Write([]byte{0})
		case bool:
			if typed {
				hasher.Write([]byte{1, 1})
			} else {
				hasher.Write([]byte{1, 0})
			}
		case string:
			hasher.Write([]byte{2})
			hasher.Write([]byte(typed))
			hasher.Write([]byte{0})
		case []any:
			hasher.Write([]byte{3})
			for _, item := range typed {
				write(item)
			}
			hasher.Write([]byte{4})
		case map[string]any:
			hasher.Write([]byte{5})
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				hasher.Write([]byte(key))
				hasher.Write([]byte{0})
				write(typed[key])
			}
			hasher.Write([]byte{6})
		default:
			n, _ := number(value)
			if n == 0 {
				n = 0 // -0 and +0 are equal
			}
			scratch[0] = 7
			bits := math.Float64bits(n)
			for at := range 8 {
				scratch[1+at] = byte(bits >> (8 * at))
			}
			hasher.Write(scratch[:])
		}
	}
	write(value)
	return hasher.Sum64()
}
