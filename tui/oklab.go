package tui

// Ports packages/tui/src/oklab.ts
//
// Oklab and OKHSL <-> sRGB conversion. colors.go builds its OKLCH, OKHSL, and color mixing on it.
//
// Oklab and OKHSL are Björn Ottosson's color spaces; OKHSL's saturation is relative to the sRGB gamut at
// each hue and lightness. This is a port of his reference implementation (https://bottosson.github.io/posts/colorpicker/),
// Copyright (c) 2021 Björn Ottosson, used under the MIT license:
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of this software and
// associated documentation files (the "Software"), to deal in the Software without restriction, including
// without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the
// following conditions: The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software. THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY
// KIND, EXPRESS OR IMPLIED.

import "math"

type vector3 [3]float64
type matrix3 [3]vector3

func (m matrix3) multiply(v vector3) vector3 {
	var out vector3
	for i, row := range m {
		out[i] = row[0]*v[0] + row[1]*v[1] + row[2]*v[2]
	}
	return out
}

var (
	linearSrgbToLMS = matrix3{
		{0.4122214694707629, 0.5363325372617349, 0.0514459932675022},
		{0.2119034958178251, 0.6806995506452344, 0.1073969535369405},
		{0.0883024591900564, 0.2817188391361215, 0.6299787016738222},
	}
	lmsToLab = matrix3{
		{0.210454268309314, 0.793617774702305, -0.0040720430116193},
		{1.9779985324311684, -2.42859224204858, 0.450593709617411},
		{0.0259040424655478, 0.7827717124575296, -0.8086757549230774},
	}
	labToLMS = matrix3{
		{1, 0.3963377773761749, 0.2158037573099136},
		{1, -0.1055613458156586, -0.0638541728258133},
		{1, -0.0894841775298119, -1.2914855480194092},
	}
	lmsToLinearSrgb = matrix3{
		{4.0767416360759583, -3.3077115392580629, 0.2309699031821043},
		{-1.2684379732850315, 2.6097573492876882, -0.341319376002657},
		{-0.0041960761386756, -0.7034186179359362, 1.7076146940746117},
	}
)

// saturationFit holds, per sRGB channel (red, green, blue), the (a, b) half-plane where that channel clips
// first, and the polynomial approximating the maximum saturation there.
var saturationFit = [3]struct {
	plane [2]float64
	poly  [5]float64
}{
	{[2]float64{-1.8817031, -0.80936501}, [5]float64{1.19086277, 1.76576728, 0.59662641, 0.75515197, 0.56771245}},
	{[2]float64{1.8144408, -1.19445267}, [5]float64{0.73956515, -0.45954404, 0.08285427, 0.12541073, -0.14503204}},
	{[2]float64{0.13110758, 1.81333971}, [5]float64{1.35733652, -0.00915799, -1.1513021, -0.50559606, 0.00692167}},
}

const (
	okhslK1 = 0.206
	okhslK2 = 0.03
	okhslK3 = (1 + okhslK1) / (1 + okhslK2)
)

// OklabToOkhslLightness converts Oklab lightness to OKHSL lightness.
func OklabToOkhslLightness(x float64) float64 {
	t := okhslK3*x - okhslK1
	return 0.5 * (t + math.Sqrt(t*t+4*okhslK2*okhslK3*x))
}

// okhslToOklabLightness converts OKHSL lightness to Oklab lightness.
func okhslToOklabLightness(x float64) float64 {
	return (x*x + okhslK1*x) / (okhslK3 * (x + okhslK2))
}

// linearToSrgb is the sRGB transfer function: linear to encoded channel, both 0-1.
func linearToSrgb(value float64) float64 {
	if value > 0.0031308 {
		return 1.055*math.Pow(value, 1/2.4) - 0.055
	}
	return 12.92 * value
}

// srgbToLinear is the inverse sRGB transfer function: encoded to linear channel, both 0-1.
func srgbToLinear(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func cubeVector(v vector3) vector3 {
	return vector3{v[0] * v[0] * v[0], v[1] * v[1] * v[1], v[2] * v[2] * v[2]}
}

// oklabToLinearSrgb converts Oklab [L, a, b] to linear sRGB [r, g, b] (0-1, may leave the gamut).
func oklabToLinearSrgb(lab vector3) vector3 {
	return lmsToLinearSrgb.multiply(cubeVector(labToLMS.multiply(lab)))
}

// linearSrgbToOklab converts linear sRGB [r, g, b] (0-1) to Oklab [L, a, b].
func linearSrgbToOklab(rgb vector3) vector3 {
	lms := linearSrgbToLMS.multiply(rgb)
	return lmsToLab.multiply(vector3{math.Cbrt(lms[0]), math.Cbrt(lms[1]), math.Cbrt(lms[2])})
}

// rgbToOklab converts sRGB channels (0-255) to Oklab [L, a, b].
func rgbToOklab(color RgbColor) vector3 {
	return linearSrgbToOklab(vector3{srgbToLinear(color.R / 255), srgbToLinear(color.G / 255), srgbToLinear(color.B / 255)})
}

// jsRound is JavaScript's Math.round: halves round toward positive infinity.
func jsRound(value float64) float64 { return math.Floor(value + 0.5) }

// linearSrgbToRgb converts linear sRGB [r, g, b] to sRGB channels (0-255, rounded), clipping out-of-gamut channels.
func linearSrgbToRgb(linear vector3) RgbColor {
	channel := func(value float64) float64 {
		return jsRound(math.Min(1, math.Max(0, linearToSrgb(value))) * 255)
	}
	return RgbColor{R: channel(linear[0]), G: channel(linear[1]), B: channel(linear[2])}
}

// lmsSlopes is the rate of change of each cube-root LMS component along a chroma direction (a, b).
func lmsSlopes(a, b float64) vector3 {
	return vector3{labToLMS[0][1]*a + labToLMS[0][2]*b, labToLMS[1][1]*a + labToLMS[1][2]*b, labToLMS[2][1]*a + labToLMS[2][2]*b}
}

func dotWeights(weights vector3, values vector3) float64 {
	return weights[0]*values[0] + weights[1]*values[1] + weights[2]*values[2]
}

// maxSaturation is the largest saturation (C/L) inside sRGB for hue (a, b): a polynomial fit plus one Halley step.
func maxSaturation(a, b float64) float64 {
	channel := 2
	for index, fit := range saturationFit {
		if index == 2 || fit.plane[0]*a+fit.plane[1]*b > 1 {
			channel = index
			break
		}
	}
	k := saturationFit[channel].poly
	weights := lmsToLinearSrgb[channel]
	saturation := k[0] + k[1]*a + k[2]*b + k[3]*a*a + k[4]*a*b

	slopes := lmsSlopes(a, b)
	var base, cubed, first, second vector3
	for i := range base {
		base[i] = 1 + saturation*slopes[i]
		cubed[i] = base[i] * base[i] * base[i]
		first[i] = 3 * slopes[i] * base[i] * base[i]
		second[i] = 6 * slopes[i] * slopes[i] * base[i]
	}
	f := dotWeights(weights, cubed)
	f1 := dotWeights(weights, first)
	f2 := dotWeights(weights, second)
	return saturation - (f*f1)/(f1*f1-0.5*f*f2)
}

// cusp returns the Oklab lightness and chroma of the most saturated sRGB color of hue (a, b).
func cusp(a, b float64) [2]float64 {
	saturation := maxSaturation(a, b)
	rgb := oklabToLinearSrgb(vector3{1, saturation * a, saturation * b})
	lightness := math.Cbrt(1 / math.Max(rgb[0], math.Max(rgb[1], rgb[2])))
	return [2]float64{lightness, lightness * saturation}
}

// maxChroma is the chroma where the constant-lightness line at lightness leaves the sRGB gamut.
func maxChroma(a, b, lightness float64, peak [2]float64) float64 {
	cuspL, cuspC := peak[0], peak[1]
	if lightness <= cuspL {
		return (cuspC * lightness) / cuspL
	}
	// Upper half: triangle edge, then one Halley step against each channel reaching 1.
	t := (cuspC * (lightness - 1)) / (cuspL - 1)
	slopes := lmsSlopes(a, b)
	var lms, cubes, first, second vector3
	for i := range lms {
		lms[i] = lightness + t*slopes[i]
		cubes[i] = lms[i] * lms[i] * lms[i]
		first[i] = 3 * slopes[i] * lms[i] * lms[i]
		second[i] = 6 * slopes[i] * slopes[i] * lms[i]
	}
	step := math.MaxFloat64
	for _, row := range lmsToLinearSrgb {
		f := dotWeights(row, cubes) - 1
		f1 := dotWeights(row, first)
		f2 := dotWeights(row, second)
		u := f1 / (f1*f1 - 0.5*f*f2)
		candidate := math.MaxFloat64
		if u >= 0 {
			candidate = -f * u
		}
		step = math.Min(step, candidate)
	}
	return t + step
}

// chromaStops returns OKHSL's chroma reference points at Oklab lightness and hue (a, b): c0, cMid, cMax.
func chromaStops(lightness, a, b float64) (c0, cMid, cMax float64) {
	peak := cusp(a, b)
	cMax = maxChroma(a, b, lightness, peak)
	k := cMax / math.Min(lightness*(peak[1]/peak[0]), (1-lightness)*(peak[1]/(1-peak[0])))
	midS := 0.11516993 +
		1/(7.4477897+
			4.1590124*b+
			a*(-2.19557347+
				1.75198401*b+
				a*(-2.13704948-10.02301043*b+a*(-4.24894561+5.38770819*b+4.69891013*a))))
	midT := 0.11239642 +
		1/(1.6132032-
			0.68124379*b+
			a*(0.40370612+
				0.90148123*b+
				a*(-0.27087943+0.6122399*b+a*(0.00299215-0.45399568*b-0.14661872*a))))
	cMid = 0.9 * k * math.Sqrt(math.Sqrt(1/(1/math.Pow(lightness*midS, 4)+1/math.Pow((1-lightness)*midT, 4))))
	c0Low, c0High := lightness*0.4, (1-lightness)*0.8
	c0 = math.Sqrt(1 / (1/(c0Low*c0Low) + 1/(c0High*c0High)))
	return c0, cMid, cMax
}

// okhslToRgb converts OKHSL (hue in degrees, saturation and lightness 0-1) to sRGB channels (0-255, rounded), clipping out-of-gamut channels.
func okhslToRgb(hue, saturation, lightness float64) RgbColor {
	L := okhslToOklabLightness(lightness)
	lab := vector3{L, 0, 0}
	if L > 0 && L < 1 && saturation > 0 {
		angle := (2 * math.Pi * jsMod(jsMod(hue, 360)+360, 360)) / 360
		a := math.Cos(angle)
		b := math.Sin(angle)
		c0, cMid, cMax := chromaStops(L, a, b)
		// Chroma rises from 0 through cMid at s = 0.8 to cMax at s = 1.
		var chroma float64
		if saturation < 0.8 {
			t := 1.25 * saturation
			k1 := 0.8 * c0
			chroma = (t * k1) / (1 - (1-k1/cMid)*t)
		} else {
			t := 5 * (saturation - 0.8)
			k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
			chroma = cMid + (t*k1)/(1-(1-k1/(cMax-cMid))*t)
		}
		lab = vector3{L, chroma * a, chroma * b}
	}
	return linearSrgbToRgb(oklabToLinearSrgb(lab))
}

// rgbToOkhsl converts sRGB channels (0-255) to OKHSL: hue in degrees (0 for grays), saturation and lightness 0-1.
func rgbToOkhsl(color RgbColor) OkhslChannels {
	lab := rgbToOklab(color)
	L, labA, labB := lab[0], lab[1], lab[2]
	chroma := math.Hypot(labA, labB)
	lightness := OklabToOkhslLightness(L)
	if chroma < 1e-9 || lightness <= 0 || lightness >= 1 {
		return OkhslChannels{H: 0, S: 0, L: lightness}
	}

	hue := jsMod(math.Atan2(labB, labA)*180/math.Pi+360, 360)
	c0, cMid, cMax := chromaStops(L, labA/chroma, labB/chroma)
	var saturation float64
	if chroma < cMid {
		k1 := 0.8 * c0
		saturation = 0.8 * (chroma / (k1 + (1-k1/cMid)*chroma))
	} else {
		k1 := (0.2 * cMid * cMid * 1.25 * 1.25) / c0
		offset := chroma - cMid
		saturation = 0.8 + 0.2*(offset/(k1+(1-k1/(cMax-cMid))*offset))
	}
	return OkhslChannels{H: hue, S: math.Min(1, math.Max(0, saturation)), L: lightness}
}

// jsMod is JavaScript's `%`: the result takes the dividend's sign, as math.Mod does.
func jsMod(value, modulus float64) float64 { return math.Mod(value, modulus) }
