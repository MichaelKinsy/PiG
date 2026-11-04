// SPDX-License-Identifier: MIT

package json

import (
	"math"
	"math/bits"
)

// addSize returns a+b, or math.MaxInt when the sum does not fit an int, so a make that takes it panics ("len out of range") instead of
// allocating a wrapped, too small size. Operands are lengths and capacities; a negative operand is treated like an overflow. The SDK is a
// separate module and cannot use PiG's internal/allocsize, which this repeats.
func addSize(a, b int) int {
	sum, carry := bits.Add64(uint64(a), uint64(b), 0)
	if carry != 0 || sum > math.MaxInt {
		return math.MaxInt
	}
	return int(sum)
}

// mulSize returns a*b, or math.MaxInt when the product does not fit an int; see addSize.
func mulSize(a, b int) int {
	high, low := bits.Mul64(uint64(a), uint64(b))
	if high != 0 || low > math.MaxInt {
		return math.MaxInt
	}
	return int(low)
}
