package text

import "testing"

func TestQuoteUTF16(t *testing.T) {
	for _, tc := range []struct {
		name string
		text []uint16
		want string
	}{
		{"empty", nil, `""`},
		{"ascii-and-HTML", []uint16{'a', '<', '>', '&', '/'}, `"a<>&/"`},
		{"controls", []uint16{0, '\b', '\f', '\n', '\r', '\t', '"', '\\'}, `"\u0000\b\f\n\r\t\"\\"`},
		{"pair", []uint16{0xd83d, 0xde00}, `"😀"`},
		{"high", []uint16{0xd800}, `"\ud800"`},
		{"low", []uint16{0xdc00}, `"\udc00"`},
		{"reversed", []uint16{0xdc00, 0xd800}, `"\udc00\ud800"`},
		{"separated", []uint16{0xd83d, 'x', 0xde00}, `"\ud83dx\ude00"`},
		{"replacement", []uint16{0xfffd}, `"�"`},
		{"literal-escape", []uint16{'\\', 'u', 'd', '8', '0', '0'}, `"\\ud800"`},
		{"line-separators", []uint16{0x2028, 0x2029}, "\"\u2028\u2029\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(QuoteUTF16(tc.text)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
