package jsstring

import "slices"

// ArrayIndex reports whether key is a canonical array index (0 to 2^32-2, no sign or leading zero) and returns it. ECMAScript enumerates such keys of an object first, in ascending order.
func ArrayIndex(key string) (uint32, bool) {
	// 4294967294 has ten digits; a canonical index has no leading zero, so only "0" starts with one.
	if len(key) == 0 || len(key) > 10 || key[0] < '0' || key[0] > '9' || (key[0] == '0' && len(key) > 1) {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		digit := key[i] - '0'
		if digit > 9 {
			return 0, false
		}
		n = n*10 + uint64(digit)
	}
	if n >= 1<<32-1 {
		return 0, false
	}
	return uint32(n), true
}

// SortOwnKeys orders items as an ECMAScript object enumerates its own string keys: array-index keys first in ascending order, then the others in their existing (insertion) order.
func SortOwnKeys[T any](items []T, key func(T) string) {
	slices.SortStableFunc(items, func(a, b T) int {
		x, xIndex := ArrayIndex(key(a))
		y, yIndex := ArrayIndex(key(b))
		switch {
		case xIndex && yIndex:
			if x < y {
				return -1
			}
			if x > y {
				return 1
			}
			return 0
		case xIndex:
			return -1
		case yIndex:
			return 1
		}
		return 0
	})
}
