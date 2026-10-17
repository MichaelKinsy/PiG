package jsmath

import "math"

// Exp is Math.exp as V8 computes it: fdlibm's __ieee754_exp (V8 src/base/ieee754.cc exp), including V8's exact result for exp(1). Go's math.Exp uses an assembly routine on amd64 whose last bit differs for some arguments.
// Every product that feeds an addition is converted with float64() so that the compiler cannot fuse it into a multiply-add, which fdlibm does not do.
func Exp(x float64) float64 {
	const (
		oThreshold = 7.09782712893383973096e+02  // 0x40862E42 FEFA39EF
		uThreshold = -7.45133219101941108420e+02 // 0xc0874910 D52D3051
		ln2HiPos   = 6.93147180369123816490e-01  // 0x3fe62e42 fee00000
		ln2LoPos   = 1.90821492927058770002e-10  // 0x3dea39ef 35793c76
		invln2     = 1.44269504088896338700e+00  // 0x3ff71547 652b82fe
		p1         = 1.66666666666666019037e-01  // 0x3FC55555 5555553E
		p2         = -2.77777777770155933842e-03 // 0xBF66C16C 16BEBD93
		p3         = 6.61375632143793436117e-05  // 0x3F11566A AF25DE2C
		p4         = -1.65339022054652515390e-06 // 0xBEBBBD41 C5D26BF1
		p5         = 4.13813679705723846039e-08  // 0x3E663769 72BEA4D0
		huge       = 1.0e+300
		twom1000   = 9.33263618503218878990e-302 // 2**-1000
		two1023    = 8.988465674311579539e307    // 2**1023
	)
	hx := uint32(math.Float64bits(x) >> 32)
	xsb := int32(hx >> 31)
	hx &= 0x7FFFFFFF
	if hx >= 0x40862E42 { // |x| >= 709.78...
		if hx >= 0x7FF00000 {
			if (hx&0xFFFFF)|uint32(math.Float64bits(x)) != 0 {
				return x + x // NaN
			}
			if xsb == 0 {
				return x
			}
			return 0 // exp(+-inf) = {inf, 0}
		}
		if x > oThreshold {
			return math.Inf(1)
		}
		if x < uThreshold {
			return 0
		}
	}
	var hi, lo float64
	k := int32(0)
	if hx > 0x3FD62E42 { // |x| > 0.5 ln2
		if hx < 0x3FF0A2B2 { // and |x| < 1.5 ln2
			if x == 1.0 {
				return math.E // V8 returns the correctly rounded exp(1); the computation below gets its last bit wrong
			}
			if xsb == 0 {
				hi, lo = x-ln2HiPos, ln2LoPos
			} else {
				hi, lo = x+ln2HiPos, -ln2LoPos
			}
			k = 1 - xsb - xsb
		} else {
			half := 0.5
			if xsb != 0 {
				half = -0.5
			}
			k = int32(float64(invln2*x) + half)
			t := float64(k)
			hi = x - float64(t*ln2HiPos)
			lo = float64(t * ln2LoPos)
		}
		x = hi - lo
	} else if hx < 0x3E300000 { // |x| < 2**-28
		if huge+x > 1 {
			return 1 + x
		}
	}
	t := x * x
	var twopk float64
	if k >= -1021 {
		twopk = math.Float64frombits(uint64(uint32(0x3FF00000+(k<<20))) << 32)
	} else {
		twopk = math.Float64frombits(uint64(uint32(0x3FF00000+((k+1000)<<20))) << 32)
	}
	c := x - float64(t*(p1+float64(t*(p2+float64(t*(p3+float64(t*(p4+float64(t*p5)))))))))
	if k == 0 {
		return 1 - (float64(x*c)/(c-2.0) - x)
	}
	y := 1 - ((lo - float64(x*c)/(2.0-c)) - hi)
	if k >= -1021 {
		if k == 1024 {
			return float64(y*2.0) * two1023
		}
		return y * twopk
	}
	return float64(y*twopk) * twom1000
}
