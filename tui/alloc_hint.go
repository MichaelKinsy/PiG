package tui

import "math"

// capHint returns a+b as a slice capacity hint, or 0 when either operand is
// negative or the sum would overflow int. It only sizes preallocation: append
// still grows the slice, so the result is the same either way.
func capHint(a, b int) int {
	if a < 0 || b < 0 || a > math.MaxInt-b {
		return 0
	}
	return a + b
}
