package nodeurl

import "strings"

const (
	punycodeBase        = 36
	punycodeTMin        = 1
	punycodeTMax        = 26
	punycodeSkew        = 38
	punycodeDamp        = 700
	punycodeInitialBias = 72
	punycodeInitialN    = 128
	punycodeMaxInt32    = 1<<31 - 1
)

func punycodeAdapt(delta, numPoints int32, first bool) int32 {
	if first {
		delta /= punycodeDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := int32(0)
	for delta > (punycodeBase-punycodeTMin)*punycodeTMax/2 {
		delta /= punycodeBase - punycodeTMin
		k += punycodeBase
	}
	return k + (punycodeBase-punycodeTMin+1)*delta/(delta+punycodeSkew)
}

// decodePunycode is ada's punycode_to_utf32 (the decoder of Node 24.22 and 26): RFC 3492 over lowercase letters and digits only, which unlike x/net/idna's decoder accepts a delimiter with nothing before it ("-wit2j"), and which rejects a label that decodes to one starting with "xn--" (whatwg/url#803). The basic part must be ASCII; a decoded code point below U+0080 is rejected. It does not check that the result is a valid scalar value or a valid label.
func decodePunycode(input string) ([]rune, bool) {
	var out []rune
	if end := strings.LastIndexByte(input, '-'); end >= 0 {
		for i := range end {
			if input[i] >= 0x80 {
				return nil, false
			}
			out = append(out, rune(input[i]))
		}
		input = input[end+1:]
	}
	n, i, bias := int32(punycodeInitialN), int32(0), int32(punycodeInitialBias)
	for len(input) > 0 {
		oldI, w := i, int32(1)
		for k := int32(punycodeBase); ; k += punycodeBase {
			if len(input) == 0 {
				return nil, false
			}
			c := input[0]
			input = input[1:]
			var digit int32
			switch {
			case c >= 'a' && c <= 'z':
				digit = int32(c - 'a')
			case c >= '0' && c <= '9':
				digit = int32(c-'0') + 26
			default:
				return nil, false
			}
			if digit > (punycodeMaxInt32-i)/w {
				return nil, false
			}
			i += digit * w
			t := min(max(k-bias, punycodeTMin), punycodeTMax)
			if digit < t {
				break
			}
			if w > punycodeMaxInt32/(punycodeBase-t) {
				return nil, false
			}
			w *= punycodeBase - t
		}
		count := int32(len(out)) + 1
		bias = punycodeAdapt(i-oldI, count, oldI == 0)
		if i/count > punycodeMaxInt32-n {
			return nil, false
		}
		n += i / count
		i %= count
		if n < 0x80 {
			return nil, false
		}
		out = append(out, 0)
		copy(out[i+1:], out[i:])
		out[i] = rune(n)
		i++
	}
	if len(out) >= 4 && out[0] == 'x' && out[1] == 'n' && out[2] == '-' && out[3] == '-' {
		return nil, false
	}
	return out, true
}

// decodeACELabel is ada's to_unicode for one ASCII label that starts with "xn--": the Punycode payload must decode to a non-empty non-ASCII label that UTS #46 maps to itself and finds valid, which encoding it again and decoding that proves: the label's own Punycode need not be the canonical one. Any other label is not decoded.
func decodeACELabel(label string) (string, bool) {
	runes, ok := decodePunycode(label[len("xn--"):])
	if !ok {
		return "", false
	}
	decoded := string(runes)
	if decoded == "" || isASCII(decoded) {
		return "", false
	}
	ascii, err := whatwgDomainToASCII.ToASCII(decoded)
	if err != nil {
		return "", false
	}
	if stable, err := whatwgDomainToASCII.ToUnicode(ascii); err != nil || stable != decoded {
		return "", false
	}
	return decoded, true
}
