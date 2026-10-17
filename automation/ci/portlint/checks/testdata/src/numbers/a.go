// Ports packages/ai/src/utils/x.ts
package numbers

import "strconv"

func bad(a, b int, f float64) (int, int, error) {
	half := a / b                  // want `integer division truncates`
	n := int(f)                    // want `float-to-integer conversion`
	_, err := strconv.Atoi("12px") // want `strconv.Atoi is stricter`
	_ = half
	return n, a, err
}

func good(a, b float64) float64 { return a / b }

const k = 10 / 3
