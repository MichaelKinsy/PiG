package imageprocessing

// ConvertImageBytesToPng applies EXIF orientation before encoding PNG, as
// upstream image-convert.ts does through Photon. Failure returns nil.
func ConvertImageBytesToPng(data []byte) []byte {
	img, err := decodeAutoOriented(data)
	if err != nil {
		return nil
	}
	png, err := encodePNG(img)
	if err != nil {
		return nil
	}
	return png
}

// DecodeNodeBase64 mirrors Node's Buffer.from(data, "base64"): it accepts the
// standard and URL-safe alphabets, skips every other character (whitespace
// included), stops at the first '=', and decodes a trailing partial quantum of
// two or three characters to one or two bytes.
func DecodeNodeBase64(data string) []byte {
	out := make([]byte, 0, len(data)-len(data)/4)
	var acc uint32
	n := 0
decode:
	for i := range len(data) {
		c := data[i]
		var v byte
		switch {
		case c >= 'A' && c <= 'Z':
			v = c - 'A'
		case c >= 'a' && c <= 'z':
			v = c - 'a' + 26
		case c >= '0' && c <= '9':
			v = c - '0' + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			break decode
		default:
			continue
		}
		acc = acc<<6 | uint32(v)
		n++
		if n == 4 {
			out = append(out, byte(acc>>16), byte(acc>>8), byte(acc))
			acc, n = 0, 0
		}
	}
	switch n {
	case 2:
		out = append(out, byte(acc>>4))
	case 3:
		out = append(out, byte(acc>>10), byte(acc>>2))
	}
	return out
}
