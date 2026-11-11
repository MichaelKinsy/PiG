// Ports packages/schema/src/formats/typebox/idna/format/puny.ts

package idna

import (
	"math"
	"strings"
)

const (
	punycodeBase        = 36
	punycodeTMin        = 1
	punycodeTMax        = 26
	punycodeSkew        = 38
	punycodeDamp        = 700
	punycodeInitialBias = 72
	punycodeInitialN    = 128
)

// IsAcePrefixed reports whether value starts with "xn--" in any letter case.
func IsAcePrefixed(value string) bool {
	//portlint:allow utf16units puny.ts IsAcePrefixed `value.toLowerCase().startsWith("xn--")`: the four prefix units are ASCII, so byte and UTF-16 offsets agree, and no non-ASCII character lowercases to x, n or -
	return len(value) >= 4 && value[2] == '-' && value[3] == '-' && (value[0] == 'x' || value[0] == 'X') && (value[1] == 'n' || value[1] == 'N')
}

// LowerAceBody returns value without its four-byte ACE prefix, lowercased as JavaScript's toLowerCase does. Only U+212A (KELVIN SIGN) lowercases to an ASCII letter among the non-ASCII code points; any other non-ASCII code point stays non-ASCII, and Decode rejects such a body, so the result is false.
func LowerAceBody(value string) (string, bool) {
	var body strings.Builder
	body.Grow(len(value) - 4)
	//portlint:allow utf16units callers pass a value IsAcePrefixed accepted (label_puny.go, format_bidi.go): its first four units are ASCII, so byte 4 is code unit 4 of puny.ts `value.toLowerCase().slice(4)`
	for _, r := range value[4:] {
		switch {
		case r >= 'A' && r <= 'Z':
			body.WriteByte(byte(r) + 'a' - 'A')
		case r == 0x212a:
			body.WriteByte('k')
		case r > 0x7f:
			return "", false
		default:
			body.WriteByte(byte(r))
		}
	}
	return body.String(), true
}

// toInt32 is JavaScript's ToInt32.
func toInt32(x float64) int32 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	m := math.Mod(math.Trunc(x), 4294967296)
	if m < 0 {
		m += 4294967296
	}
	//portlint:allow numbers m is Mod(Trunc(x), 2^32) shifted into [0, 2^32): an exact integer, so uint32(m) then int32 wraps as ToInt32 does
	return int32(uint32(m))
}

// adapt keeps JavaScript's double arithmetic: the products and sums are plain float64 operations, with the int32 conversion of `>>`.
func adapt(delta float64, numPoints float64, firstTime bool) float64 {
	if firstTime {
		delta = math.Floor(delta / punycodeDamp)
	} else {
		delta = float64(toInt32(delta) >> 1)
	}
	delta += math.Floor(delta / numPoints)
	k := 0.0
	for delta > ((punycodeBase-punycodeTMin)*punycodeTMax)>>1 {
		delta = math.Floor(delta / (punycodeBase - punycodeTMin))
		k += punycodeBase
	}
	return k + math.Floor(float64((punycodeBase-punycodeTMin+1)*delta)/(delta+punycodeSkew))
}

// Decode decodes a lowercase punycode payload to its code points, or reports false where Pi's Decode throws. The result is the code point sequence of the JavaScript string String.fromCodePoint builds: a decoded high surrogate followed by a decoded low surrogate is one supplementary code point, and an unpaired surrogate stays a code point in the 0xD800 to 0xDFFF range.
func Decode(value string) ([]rune, bool) {
	// Every UTF-16 unit of the payload, before or after the delimiter, has to be ASCII: a unit of 128 or more before the delimiter throws, and one after it is not a digit.
	for i := range len(value) {
		if value[i] >= 0x80 {
			return nil, false
		}
	}
	var output []float64
	n := float64(punycodeInitialN)
	i := 0.0
	bias := float64(punycodeInitialBias)
	delimIdx := strings.LastIndexByte(value, '-')
	if delimIdx > 0 {
		for j := range delimIdx {
			output = append(output, float64(value[j]))
		}
	}
	inIdx := 0
	if delimIdx >= 0 {
		inIdx = delimIdx + 1
	}
	for inIdx < len(value) {
		oldi := i
		w := 1.0
		k := float64(punycodeBase)
		for {
			if inIdx >= len(value) {
				return nil, false
			}
			ch := value[inIdx]
			inIdx++
			var digit float64
			switch {
			case ch >= 0x61 && ch <= 0x7a:
				digit = float64(ch - 0x61)
			case ch >= 0x30 && ch <= 0x39:
				digit = float64(ch-0x30) + 26
			default:
				return nil, false
			}
			i += float64(digit * w)
			var t float64
			switch {
			case k <= bias:
				t = punycodeTMin
			case k >= bias+punycodeTMax:
				t = punycodeTMax
			default:
				t = k - bias
			}
			if digit < t {
				break
			}
			w *= punycodeBase - t
			k += punycodeBase
		}
		outLen := float64(len(output) + 1)
		bias = adapt(i-oldi, outLen, oldi == 0)
		n += math.Floor(i / outLen)
		i = math.Mod(i, outLen)
		at := 0
		if !math.IsNaN(i) {
			//portlint:allow numbers i is Mod(i, outLen) of an integral float, so 0 <= i < outLen is an exact index; NaN takes the branch above, as splice(NaN, 0, n) inserts at 0 (puny.ts Decode)
			at = int(i)
		}
		output = append(output, 0)
		copy(output[at+1:], output[at:])
		output[at] = n
		i++
	}
	units := make([]rune, 0, len(output))
	for _, cp := range output {
		if math.IsNaN(cp) || cp < 0 || cp > 0x10ffff || cp != math.Floor(cp) {
			return nil, false
		}
		//portlint:allow numbers the guard above rejects NaN, non-integers and values outside 0..0x10ffff, where String.fromCodePoint(...output) throws RangeError (puny.ts Decode)
		units = append(units, rune(cp))
	}
	return mergeSurrogates(units), true
}

// mergeSurrogates joins each high surrogate that is directly followed by a low surrogate into the supplementary code point UTF-16 makes of the pair, as iterating a JavaScript string does.
func mergeSurrogates(cps []rune) []rune {
	out := cps[:0]
	for i := 0; i < len(cps); i++ {
		cp := cps[i]
		if cp >= 0xd800 && cp <= 0xdbff && i+1 < len(cps) && cps[i+1] >= 0xdc00 && cps[i+1] <= 0xdfff {
			cp = 0x10000 + (cp-0xd800)<<10 + (cps[i+1] - 0xdc00)
			i++
		}
		out = append(out, cp)
	}
	return out
}

func digitToChar(digit int) byte {
	if digit < 26 {
		return byte(digit + 0x61)
	}
	return byte(digit - 26 + 0x30)
}

// encode returns the punycode payload (the part after "xn--") of codePoints.
func encode(codePoints []rune) string {
	var result []byte
	basicLength := 0
	for _, cp := range codePoints {
		if cp <= 0x7f {
			result = append(result, byte(cp))
			basicLength++
		}
	}
	if basicLength > 0 {
		result = append(result, '-')
	}
	n := punycodeInitialN
	delta := 0
	bias := punycodeInitialBias
	handledCPCount := basicLength
	for handledCPCount < len(codePoints) {
		m := math.MaxInt
		for _, cp := range codePoints {
			if int(cp) >= n && int(cp) < m {
				m = int(cp)
			}
		}
		delta += (m - n) * (handledCPCount + 1)
		n = m
		for _, cp := range codePoints {
			if int(cp) < n {
				delta++
			}
			if int(cp) == n {
				q := delta
				for k := punycodeBase; ; k += punycodeBase {
					var t int
					switch {
					case k <= bias:
						t = punycodeTMin
					case k >= bias+punycodeTMax:
						t = punycodeTMax
					default:
						t = k - bias
					}
					if q < t {
						break
					}
					result = append(result, digitToChar(t+(q-t)%(punycodeBase-t)))
					//portlint:allow numbers q >= t here, so (q-t) is non-negative and integer division equals puny.ts Encode's Math.floor((q - t) / (PUNYCODE_BASE - t))
					q = (q - t) / (punycodeBase - t)
				}
				result = append(result, digitToChar(q))
				//portlint:allow numbers adapt returns an integral finite float (a sum of Math.floor results and multiples of 36), so int() is exact for puny.ts Encode's `bias = Adapt(...)`
				bias = int(adapt(float64(delta), float64(handledCPCount+1), handledCPCount == basicLength))
				delta = 0
				handledCPCount++
			}
		}
		delta++
		n++
	}
	return string(result)
}
