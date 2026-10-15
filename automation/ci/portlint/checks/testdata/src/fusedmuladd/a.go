// Ports packages/tui/src/fuzzy.ts
package fusedmuladd

func bad(score, a, b, c float64, i int) float64 {
	score += float64(i) * 0.1 // want `float product feeds \+= or -=: arm64 fuses it`
	score -= a * b            // want `float product feeds \+= or -=: arm64 fuses it`
	x := a*b + c              // want `float product feeds an add or subtract: arm64 fuses it`
	y := c - (a * b)          // want `float product feeds an add or subtract: arm64 fuses it`
	z := -(a * b) + c         // want `float product feeds an add or subtract: arm64 fuses it`
	return score + x + y + z
}

func good(score, a, b, c float64, i int) float64 {
	score += float64(float64(i) * 0.1)
	x := float64(a*b) + c
	n := i*2 + 1
	const k = 2.0*3.0 + 1.0
	return score + x + float64(n) + k
}
