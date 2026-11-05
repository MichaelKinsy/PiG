// Package allocsize computes the size of an allocation from lengths without wrapping.
//
// Go's make checks a size it is given, but an `a + b` or `2*n + c` that wraps reaches it as a small or negative number: the first
// allocates too little and the caller then writes past it, the second panics late. These helpers return math.MaxInt for a sum or
// product that does not fit an int, so make panics ("len out of range"), as it does for any size larger than memory. Operands are
// lengths and capacities, never negative; a negative operand is treated like an overflow.
package allocsize

import (
	"math"
	"math/bits"
)

// Add returns a+b, or math.MaxInt when the sum does not fit an int.
func Add(a, b int) int {
	sum, carry := bits.Add64(uint64(a), uint64(b), 0)
	if carry != 0 || sum > math.MaxInt {
		return math.MaxInt
	}
	return int(sum)
}

// Mul returns a*b, or math.MaxInt when the product does not fit an int.
func Mul(a, b int) int {
	high, low := bits.Mul64(uint64(a), uint64(b))
	if high != 0 || low > math.MaxInt {
		return math.MaxInt
	}
	return int(low)
}
