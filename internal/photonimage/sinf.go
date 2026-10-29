// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-FileCopyrightText: Copyright (C) 1993 by Sun Microsystems, Inc.
// SPDX-FileCopyrightText: Copyright (c) Rust Project Developers
// SPDX-License-Identifier: MIT AND SunPro

package photonimage

import "math"

// Photon's wasm calls `f32::sin` through compiler_builtins' libm (the symbol `compiler_builtins::math::libm::sinf::sinf` is in
// photon_rs_bg.wasm): the FreeBSD s_sinf.c that musl and the libm crate carry. It is not the correctly rounded sine of the double
// precision `math.Sin`: for some arguments it is one float32 step away, which moves a Lanczos weight by one step and, for a rare pixel,
// the rounded channel by one. sinf ports it so the weights are Photon's.

// Ports libm 0.2.16 src/math/sinf.rs, k_sinf.rs, k_cosf.rs and rem_pio2f.rs (the algorithm is unchanged since libm 0.2.1), for the
// arguments the kernel gives it (|x| < 3*pi, below the 2^28*pi/2 where rem_pio2f needs rem_pio2_large).

// Small multiples of pi/2 in double precision (sinf.rs:15-21): FRAC_PI_2 rounded to float64, times 1, 2, 3 and 4 and rounded again.
// They are variables so that Go multiplies float64 values and does not fold an exact product.
var (
	fracPio2 float64 = math.Pi / 2
	s1Pio2           = 1 * fracPio2 // 0x3FF921FB54442D18
	s2Pio2           = 2 * fracPio2 // 0x400921FB54442D18
	s3Pio2           = 3 * fracPio2 // 0x4012D97C7F3321D2
	s4Pio2           = 4 * fracPio2 // 0x401921FB54442D18
)

const (
	// k_sinf.rs:17-20 and k_cosf.rs:17-20.
	kS1 = -0.166666666416265235595
	kS2 = 0.0083333293858894631756
	kS3 = -0.000198393348360966317347
	kS4 = 0.0000027183114939898219064
	kC0 = -0.499999997251031003120
	kC1 = 0.0416666233237390631894
	kC2 = -0.00138867637746099294692
	kC3 = 0.0000243904487962774090654

	// rem_pio2f.rs:19-24.
	toInt   = 1.5 / 2.220446049250313e-16 // 1.5 / f64::EPSILON
	invPio2 = 6.36619772367581382433e-01
	pio21   = 1.57079631090164184570e+00
	pio21t  = 1.58932547735281966916e-08
)

// kSinf is k_sinf (k_sinf.rs:22-29). Every product is converted to float64 so that no platform fuses it with the addition after it.
func kSinf(x float64) float32 {
	z := x * x
	w := z * z
	r := kS3 + float64(z*kS4)
	s := z * x
	return float32((x + float64(s*(kS1+float64(z*kS2)))) + float64(float64(s*w)*r))
}

// kCosf is k_cosf (k_cosf.rs:22-28).
func kCosf(x float64) float32 {
	z := x * x
	w := z * z
	r := kC2 + float64(z*kC3)
	return float32((((1.0 + float64(z*kC0)) + float64(w*kC1)) + float64(float64(w*z)*r)))
}

// sinf is `f32::sin` of Photon's wasm (sinf.rs:25-82) for |x| < 2^28*pi/2.
func sinf(x float32) float32 {
	x64 := float64(x)
	ix := math.Float32bits(x)
	sign := ix>>31 != 0
	ix &= 0x7fffffff

	if ix <= 0x3f490fda { // |x| ~<= pi/4
		if ix < 0x39800000 { // |x| < 2**-12
			return x
		}
		return kSinf(x64)
	}
	if ix <= 0x407b53d1 { // |x| ~<= 5*pi/4
		if ix <= 0x4016cbe3 { // |x| ~<= 3pi/4
			if sign {
				return -kCosf(x64 + s1Pio2)
			}
			return kCosf(x64 - s1Pio2)
		}
		if sign {
			return kSinf(-(x64 + s2Pio2))
		}
		return kSinf(-(x64 - s2Pio2))
	}
	if ix <= 0x40e231d5 { // |x| ~<= 9*pi/4
		if ix <= 0x40afeddf { // |x| ~<= 7*pi/4
			if sign {
				return kCosf(x64 + s3Pio2)
			}
			return -kCosf(x64 - s3Pio2)
		}
		if sign {
			return kSinf(x64 + s4Pio2)
		}
		return kSinf(x64 - s4Pio2)
	}

	// General argument reduction (rem_pio2f.rs:36-47): the medium size case, which is every argument below 2^28*pi/2.
	tmp := float64(x64*invPio2) + toInt
	fn := tmp - toInt
	n := int32(fn)
	y := (x64 - float64(fn*pio21)) - float64(fn*pio21t)
	switch n & 3 {
	case 0:
		return kSinf(y)
	case 1:
		return kCosf(y)
	case 2:
		return kSinf(-y)
	}
	return -kCosf(y)
}
