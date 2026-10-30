// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright (C) 1993 by Sun Microsystems, Inc.
// SPDX-FileCopyrightText: Copyright (c) 2005 David Schultz
// SPDX-FileCopyrightText: Copyright (c) Rust Project Developers
// SPDX-License-Identifier: MIT AND SunPro AND BSD-2-Clause

package photonimage

import "math"

// Photon's wasm calls `f32::powf` and `f32::exp2` through compiler_builtins' libm (the symbols `compiler_builtins::math::libm::powf::powf`
// and `expf` are in photon_rs_bg.wasm): FreeBSD's e_powf.c and s_exp2f.c as the libm crate carries them. image's Radiance HDR decoder
// turns an RGBE pixel into 8-bit colour with `f32::exp2(e - 136) * c` and `f32::powf(v, 2.2) * 255 + 0.5` (hdr/decoder.rs:266-325).
// The decoder (hdrdecode.go) computes that with math.Pow; this literal port of the libm functions proves it right: every product is
// wrapped in a float32 conversion so that Go rounds it as wasm does and never fuses it, and TestHDRConversionTableIsLibms compares the
// 8-bit result for all 65,536 (exponent, mantissa) pairs, which is every input the decoder can have.

// Ports libm 0.2.16 src/math/powf.rs and exp2f.rs (unchanged in the algorithm since libm 0.2.1; Photon's wasm was built with rustc
// 3f43b1a636738f41c48df073c5bcb97a97bf8459).

const (
	powfTwo24  float32 = 16777216.0 // 0x4b800000
	powfHuge   float32 = 1.0e30
	powfTiny   float32 = 1.0e-30
	powfL1     float32 = 6.0000002384e-01
	powfL2     float32 = 4.2857143283e-01
	powfL3     float32 = 3.3333334327e-01
	powfL4     float32 = 2.7272811532e-01
	powfL5     float32 = 2.3066075146e-01
	powfL6     float32 = 2.0697501302e-01
	powfP1     float32 = 1.6666667163e-01
	powfP2     float32 = -2.7777778450e-03
	powfP3     float32 = 6.6137559770e-05
	powfP4     float32 = -1.6533901999e-06
	powfP5     float32 = 4.1381369442e-08
	powfLg2    float32 = 6.9314718246e-01
	powfLg2H   float32 = 6.93145752e-01
	powfLg2L   float32 = 1.42860654e-06
	powfOvt    float32 = 4.2995665694e-08
	powfCp     float32 = 9.6179670095e-01
	powfCpH    float32 = 9.6191406250e-01
	powfCpL    float32 = -1.1736857402e-04
	powfIvln2  float32 = 1.4426950216e+00
	powfIvln2H float32 = 1.4426879883e+00
	powfIvln2L float32 = 7.0526075433e-06
)

var (
	powfBP  = [2]float32{1.0, 1.5}
	powfDPH = [2]float32{0.0, 5.84960938e-01}
	powfDPL = [2]float32{0.0, 1.56322085e-06}
)

func f32bits(x float32) int32       { return int32(math.Float32bits(x)) }
func f32frombits(i int32) float32   { return math.Float32frombits(uint32(i)) }
func f32frombitsU(u uint32) float32 { return math.Float32frombits(u) }

// powf is `f32::powf` (libm powf.rs:50-343).
func powf(x, y float32) float32 {
	hx := f32bits(x)
	hy := f32bits(y)
	ix := hx & 0x7fffffff
	iy := hy & 0x7fffffff

	// x**0 = 1, even if x is NaN.
	if iy == 0 {
		return 1.0
	}
	// 1**y = 1, even if y is NaN.
	if hx == 0x3f800000 {
		return 1.0
	}
	// NaN if either arg is NaN.
	if ix > 0x7f800000 || iy > 0x7f800000 {
		return x + y
	}

	// yisint: 0 y is not an integer, 1 y is an odd int, 2 y is an even int (x < 0 only).
	yisint := int32(0)
	if hx < 0 {
		if iy >= 0x4b800000 {
			yisint = 2
		} else if iy >= 0x3f800000 {
			k := (iy >> 23) - 0x7f
			j := iy >> uint(23-k)
			if (j << uint(23-k)) == iy {
				yisint = 2 - (j & 1)
			}
		}
	}

	// special value of y
	if iy == 0x7f800000 {
		switch {
		case ix == 0x3f800000:
			return 1.0
		case ix > 0x3f800000:
			if hy >= 0 {
				return y
			}
			return 0.0
		default:
			if hy >= 0 {
				return 0.0
			}
			return -y
		}
	}
	if iy == 0x3f800000 {
		if hy >= 0 {
			return x
		}
		return float32(1.0 / x)
	}
	if hy == 0x40000000 {
		return float32(x * x)
	}
	if hy == 0x3f000000 && hx >= 0 {
		return float32(math.Sqrt(float64(x)))
	}

	ax := float32(math.Abs(float64(x)))
	// special value of x
	if ix == 0x7f800000 || ix == 0 || ix == 0x3f800000 {
		z := ax
		if hy < 0 {
			z = float32(1.0 / z)
		}
		if hx < 0 {
			if ((ix - 0x3f800000) | yisint) == 0 {
				z = float32(float32(z-z) / float32(z-z))
			} else if yisint == 1 {
				z = -z
			}
		}
		return z
	}

	sn := float32(1.0)
	if hx < 0 {
		if yisint == 0 {
			return float32(float32(x-x) / float32(x-x))
		}
		if yisint == 1 {
			sn = -1.0
		}
	}

	var t1, t2 float32
	if iy > 0x4d000000 {
		// |y| is HUGE (> 2**27)
		if ix < 0x3f7ffff8 {
			if hy < 0 {
				return float32(float32(sn*powfHuge) * powfHuge)
			}
			return float32(float32(sn*powfTiny) * powfTiny)
		}
		if ix > 0x3f800007 {
			if hy > 0 {
				return float32(float32(sn*powfHuge) * powfHuge)
			}
			return float32(float32(sn*powfTiny) * powfTiny)
		}
		// |1-x| <= 2**-20: log(x) by x-x^2/2+x^3/3-x^4/4.
		t := float32(ax - 1.)
		w := float32(float32(t*t) * float32(0.5-float32(t*float32(float32(0.333333333333)-float32(t*0.25)))))
		u := float32(powfIvln2H * t)
		v := float32(float32(t*powfIvln2L) - float32(w*powfIvln2))
		t1 = float32(u + v)
		t1 = f32frombitsU(uint32(f32bits(t1)) & 0xfffff000)
		t2 = float32(v - float32(t1-u))
	} else {
		n := int32(0)
		// take care of a subnormal number
		if ix < 0x00800000 {
			ax = float32(ax * powfTwo24)
			n -= 24
			ix = f32bits(ax)
		}
		n += (ix >> 23) - 0x7f
		j := ix & 0x007fffff
		// determine the interval
		ix = j | 0x3f800000
		var k int32
		switch {
		case j <= 0x1cc471:
			k = 0
		case j < 0x5db3d7:
			k = 1
		default:
			k = 0
			n++
			ix -= 0x00800000
		}
		ax = f32frombits(ix)

		// s = s_h + s_l = (x-1)/(x+1) or (x-1.5)/(x+1.5)
		u := float32(ax - powfBP[k])
		v := float32(1.0 / float32(ax+powfBP[k]))
		s := float32(u * v)
		sH := f32frombitsU(uint32(f32bits(s)) & 0xfffff000)
		// t_h = ax + bp[k] high
		is := int32(((uint32(ix) >> 1) & 0xfffff000) | 0x20000000)
		tH := f32frombitsU(uint32(is) + 0x00400000 + uint32(k)<<21)
		tL := float32(ax - float32(tH-powfBP[k]))
		sL := float32(v * float32(float32(u-float32(sH*tH))-float32(sH*tL)))
		// log(ax)
		s2 := float32(s * s)
		poly := float32(powfL5 + float32(s2*powfL6))
		poly = float32(powfL4 + float32(s2*poly))
		poly = float32(powfL3 + float32(s2*poly))
		poly = float32(powfL2 + float32(s2*poly))
		poly = float32(powfL1 + float32(s2*poly))
		r := float32(float32(s2*s2) * poly)
		r = float32(r + float32(sL*float32(sH+s)))
		s2 = float32(sH * sH)
		tH = float32(float32(3.0+s2) + r)
		tH = f32frombitsU(uint32(f32bits(tH)) & 0xfffff000)
		tL = float32(r - float32(float32(tH-3.0)-s2))
		// u+v = s*(1+...)
		u = float32(sH * tH)
		v = float32(float32(sL*tH) + float32(tL*s))
		// 2/(3log2)*(s+...)
		pH := float32(u + v)
		pH = f32frombitsU(uint32(f32bits(pH)) & 0xfffff000)
		pL := float32(v - float32(pH-u))
		zH := float32(powfCpH * pH)
		zL := float32(float32(float32(powfCpL*pH)+float32(pL*powfCp)) + powfDPL[k])
		// log2(ax) = (s+..)*2/(3*log2) = n + dp_h + z_h + z_l
		t := float32(n)
		t1 = float32(float32(float32(zH+zL)+powfDPH[k]) + t)
		t1 = f32frombitsU(uint32(f32bits(t1)) & 0xfffff000)
		t2 = float32(zL - float32(float32(float32(t1-t)-powfDPH[k])-zH))
	}

	// split y into y1+y2 and compute (y1+y2)*(t1+t2)
	y1 := f32frombitsU(uint32(f32bits(y)) & 0xfffff000)
	pL := float32(float32(float32(y-y1)*t1) + float32(y*t2))
	pH := float32(y1 * t1)
	z := float32(pL + pH)
	j := f32bits(z)
	if j > 0x43000000 { // z > 128
		return float32(float32(sn*powfHuge) * powfHuge)
	} else if j == 0x43000000 { // z == 128
		if float32(pL+powfOvt) > float32(z-pH) {
			return float32(float32(sn*powfHuge) * powfHuge)
		}
	} else if (j & 0x7fffffff) > 0x43160000 { // z < -150
		return float32(float32(sn*powfTiny) * powfTiny)
	} else if uint32(j) == 0xc3160000 && pL <= float32(z-pH) { // z == -150
		return float32(float32(sn*powfTiny) * powfTiny)
	}

	// compute 2**(p_h+p_l)
	i := j & 0x7fffffff
	k := (i >> 23) - 0x7f
	n := int32(0)
	if i > 0x3f000000 { // |z| > 0.5, set n = [z+0.5]
		n = j + (0x00800000 >> uint(k+1))
		k = ((n & 0x7fffffff) >> 23) - 0x7f
		t := f32frombitsU(uint32(n) & ^(uint32(0x007fffff) >> uint(k)))
		n = ((n & 0x007fffff) | 0x00800000) >> uint(23-k)
		if j < 0 {
			n = -n
		}
		pH = float32(pH - t)
	}
	t := float32(pL + pH)
	t = f32frombitsU(uint32(f32bits(t)) & 0xffff8000)
	u := float32(t * powfLg2H)
	v := float32(float32(float32(pL-float32(t-pH))*powfLg2) + float32(t*powfLg2L))
	z = float32(u + v)
	w := float32(v - float32(z-u))
	t = float32(z * z)
	poly := float32(powfP4 + float32(t*powfP5))
	poly = float32(powfP3 + float32(t*poly))
	poly = float32(powfP2 + float32(t*poly))
	poly = float32(powfP1 + float32(t*poly))
	t1 = float32(z - float32(t*poly))
	r := float32(float32(float32(z*t1)/float32(t1-2.0)) - float32(w+float32(z*w)))
	z = float32(1.0 - float32(r-z))
	j = f32bits(z)
	j += n << 23
	if (j >> 23) <= 0 {
		// subnormal output: libm scalbnf(z, n), the single rounding of z * 2**n
		z = float32(math.Ldexp(float64(z), int(n)))
	} else {
		z = f32frombits(j)
	}
	return float32(sn * z)
}

var exp2fTable = [16]uint64{
	0x3fe6a09e667f3bcd, 0x3fe7a11473eb0187, 0x3fe8ace5422aa0db, 0x3fe9c49182a3f090,
	0x3feae89f995ad3ad, 0x3fec199bdd85529c, 0x3fed5818dcfba487, 0x3feea4afa2a490da,
	0x3ff0000000000000, 0x3ff0b5586cf9890f, 0x3ff172b83c7d517b, 0x3ff2387a6e756238,
	0x3ff306fe0a31b715, 0x3ff3dea64c123422, 0x3ff4bfdad5362a27, 0x3ff5ab07dd485429,
}

// exp2f is `f32::exp2` (libm exp2f.rs:77-141): a table of 16 and a degree 4 polynomial in double precision.
func exp2f(x float32) float32 {
	const tblSize = 16
	redux := float32(math.Float32frombits(0x4b400000) / tblSize)
	p1 := float64(math.Float32frombits(0x3f317218))
	p2 := float64(math.Float32frombits(0x3e75fdf0))
	p3 := float64(math.Float32frombits(0x3d6359a4))
	p4 := float64(math.Float32frombits(0x3c1d964e))
	x1p127 := math.Float32frombits(0x7f000000)

	// Filter out exceptional cases.
	ui := math.Float32bits(x)
	ix := ui & 0x7fffffff
	if ix > 0x42fc0000 { // |x| > 126
		if ix > 0x7f800000 { // NaN
			return x
		}
		if ui >= 0x43000000 && ui < 0x80000000 { // x >= 128
			return float32(x * x1p127)
		}
		if ui >= 0x80000000 { // x < -126
			if ui >= 0xc3160000 { // x <= -150
				return 0.0
			}
		}
	} else if ix <= 0x33000000 { // |x| <= 0x1p-25
		return float32(1.0 + x)
	}

	// Reduce x, computing z, i0, and k.
	ui = math.Float32bits(float32(x + redux))
	i0 := ui
	i0 += tblSize / 2
	k := i0 / tblSize
	ukf := math.Float64frombits(uint64(0x3ff+k) << 52)
	i0 &= tblSize - 1
	uf := math.Float32frombits(ui)
	uf = float32(uf - redux)
	z := float64(float32(x - uf))
	// Compute r = exp2(y) = exp2ft[i0] * p(z).
	r := math.Float64frombits(exp2fTable[i0])
	t := float64(r * z)
	// r + t*(p1 + z*p2) + t*(z*z)*(p3 + z*p4), summed from the left.
	a := float64(t * float64(p1+float64(z*p2)))
	b := float64(float64(t*float64(z*z)) * float64(p3+float64(z*p4)))
	r = float64(float64(r+a) + b)
	// Scale by 2**k.
	return float32(float64(r * ukf))
}
