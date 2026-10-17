// Every production file is scanned, not only one with a `// Ports packages/` header.
package fusedmuladd

import "math"

// cache-warmer.ts:390: the product rounds before the subtraction in JavaScript.
func expectedSavings(p, missCost, warmCost float64) float64 {
	gross := p * missCost
	return gross - warmCost // want `float product feeds an add or subtract through a variable assigned at line 8: Go fuses \(FMA\) across statements`
}

// radius.ts:129: an integer-valued factor is not exempt; the expiry itself is not an integer.
func expiry(now, expiresIn, skew float64) float64 {
	return now + expiresIn*1000 - skew // want `float product feeds an add or subtract: arm64 fuses it`
}

func accumulate(t, step float64) float64 {
	t *= step
	t += 1 // want `float product feeds \+= or -= through a variable assigned at line 18`
	return t
}

func smooth(t float64) float64 { return t * t * (3 - 2*t) }

func scaled(x float64) float64 {
	r := x * 0.8
	return r
}

func viaReturn(u, x float64) float64 {
	a := 1 - smooth(u) // want `float product feeds an add or subtract through the return value of smooth: an inlined return fuses`
	b := x + scaled(u) // want `float product feeds an add or subtract through the return value of scaled`
	return a + b
}

func bad32(a, b, c float32) float32 {
	k1 := 0.8 * a
	return k1 + b*c // want `float product feeds an add or subtract`
}

func smoothRounded(t float64) float64 { return float64(t * t * (3 - 2*t)) }

func sinOf(x float64) float64 { return math.Sin(x) }

// Near misses: rounded at the assignment or at the use, a power-of-two factor, a merge of several definitions, an
// accumulator whose reaching definition is a sum, a product from another package, a named float conversion.
func crossGood(a, b, c, u float64, xs []float64, cond bool) float64 {
	rounded := float64(a * b)
	r1 := rounded + c
	t := a * b
	r2 := float64(t) - c
	half := a * 0.5
	r3 := half + c + a*2 - b*0.25 + a*b*2
	m := a * b
	if cond {
		m = c
	}
	r4 := m + c
	sum := 0.0
	for _, x := range xs {
		sum += float64(x * c)
	}
	r5 := 1 - smoothRounded(u) + sinOf(u) + math.Abs(a*b)
	type ms float64
	r6 := ms(a*b) + ms(c)
	f32 := float32(a*b) + float32(c)
	return r1 + r2 + r3 + r4 + sum + r5 + float64(r6) + float64(f32)
}

func allowed(days, hour float64) float64 {
	//portlint:allow fusedmuladd integer-valued products below 2^53 are exact, so fusion cannot change the sum
	return days*86400000 + hour*3600000
}
