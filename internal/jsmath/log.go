// Package jsmath implements ECMAScript Math functions with the results V8 returns, where Go's math package rounds differently.
package jsmath

import (
	"math"
	"runtime"
)

// contracted reports whether V8's fdlibm runs with multiply-adds contracted on this architecture. Node's arm64 builds compile
// src/base/ieee754.cc with clang, whose default -ffp-contract=on fuses a product into the addition or subtraction of the same
// expression (but not across statements); Math.log of Node 24.19 on darwin-arm64 matches that bit for bit. x64 builds target a
// baseline without FMA, so every operation rounds there.
const contracted = runtime.GOARCH == "arm64"

// mulAdd is a*b + c as V8's fdlibm evaluates it on this architecture: one fused rounding when contracted, else the product
// rounded first. float64() forces that rounding; assigning a product to a variable does not, since Go may fuse across statements.
func mulAdd(a, b, c float64) float64 {
	if contracted {
		return math.FMA(a, b, c)
	}
	return float64(a*b) + c
}

// Log is Math.log as V8 computes it: fdlibm's __ieee754_log (V8 src/base/ieee754.cc log). Go's math.Log is within 1 ulp of it but differs in the last bit for about 1 in 120 arguments on amd64.
// Each product is rounded on its own (float64()) or fused into its expression's addition through mulAdd, exactly where the C
// expression in fdlibm has a product feeding an addition, so the compiler cannot fuse anything else.
func Log(x float64) float64 {
	const (
		ln2Hi = 6.93147180369123816490e-01 // 3fe62e42 fee00000
		ln2Lo = 1.90821492927058770002e-10 // 3dea39ef 35793c76
		two54 = 1.80143985094819840000e+16 // 43500000 00000000
		lg1   = 6.666666666666735130e-01   // 3FE55555 55555593
		lg2   = 3.999999999940941908e-01   // 3FD99999 9997FA04
		lg3   = 2.857142874366239149e-01   // 3FD24924 94229359
		lg4   = 2.222219843214978396e-01   // 3FCC71C5 1D8E78AF
		lg5   = 1.818357216161805012e-01   // 3FC74664 96CB03DE
		lg6   = 1.531383769920937332e-01   // 3FC39A09 D078C69F
		lg7   = 1.479819860511658591e-01   // 3FC2F112 DF3E5244
	)
	bits := math.Float64bits(x)
	hx := int32(bits >> 32)
	lx := uint32(bits)
	k := int32(0)
	if hx < 0x00100000 { // x < 2**-1022
		if (hx&0x7fffffff)|int32(lx) == 0 {
			return math.Inf(-1) // log(+-0) = -inf
		}
		if hx < 0 {
			return math.NaN() // log(-#) = NaN
		}
		k -= 54
		x *= two54 // subnormal number, scale up x
		hx = int32(math.Float64bits(x) >> 32)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	x = math.Float64frombits(uint64(uint32(hx|(i^0x3ff00000)))<<32 | uint64(uint32(math.Float64bits(x)))) // normalize x or x/2
	k += i >> 20
	f := x - 1.0
	if (0x000fffff & (2 + hx)) < 3 { // -2**-20 <= f < 2**-20
		if f == 0 {
			if k == 0 {
				return 0
			}
			dk := float64(k)
			// dk*ln2Hi is exact for every reachable k, so contracting either product gives this same sum.
			return float64(dk*ln2Hi) + float64(dk*ln2Lo)
		}
		r := float64(float64(f*f) * mulAdd(-0.33333333333333333, f, 0.5))
		if k == 0 {
			return f - r
		}
		dk := float64(k)
		return mulAdd(dk, ln2Hi, -(mulAdd(-dk, ln2Lo, r) - f))
	}
	s := f / (2.0 + f)
	dk := float64(k)
	z := s * s
	i = hx - 0x6147a
	w := z * z
	j := 0x6b851 - hx
	t1 := float64(w * mulAdd(w, mulAdd(w, lg6, lg4), lg2))
	t2 := float64(z * mulAdd(w, mulAdd(w, mulAdd(w, lg7, lg5), lg3), lg1))
	i |= j
	r := t2 + t1
	if i > 0 {
		hfsq := float64(float64(0.5*f) * f)
		if k == 0 {
			return f - mulAdd(-s, hfsq+r, hfsq)
		}
		return mulAdd(dk, ln2Hi, -((hfsq - mulAdd(s, hfsq+r, float64(dk*ln2Lo))) - f))
	}
	if k == 0 {
		return mulAdd(-s, f-r, f)
	}
	return mulAdd(dk, ln2Hi, -(mulAdd(s, f-r, -float64(dk*ln2Lo)) - f))
}
