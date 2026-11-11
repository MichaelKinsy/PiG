package http

import (
	"strings"
	"unicode/utf8"
)

// URLSearchParams is the query of a request as `URLSearchParams` reads it (the WHATWG application/x-www-form-urlencoded
// parser): its pairs in order, `+` a space, percent escapes decoded leniently (a bad one stays as written, bytes that are no
// UTF-8 become U+FFFD), a `;` just a character. Go's url.ParseQuery drops a pair with a `;` or a bad escape and loses the
// order, so it is not used. Go-only file.
type URLSearchParams struct {
	pairs []QueryPair
}

// QueryPair is one name and value of a query.
type QueryPair struct{ Name, Value string }

// ParseURLSearchParams is `new URLSearchParams(query)` (the query without its `?`).
func ParseURLSearchParams(query string) *URLSearchParams {
	params := &URLSearchParams{}
	for sequence := range strings.SplitSeq(query, "&") {
		if sequence == "" {
			continue
		}
		name, value, _ := strings.Cut(sequence, "=")
		params.pairs = append(params.pairs, QueryPair{Name: formDecode(name), Value: formDecode(value)})
	}
	return params
}

// Get is the first value of name; false without one.
func (p *URLSearchParams) Get(name string) (string, bool) {
	for _, pair := range p.pairs {
		if pair.Name == name {
			return pair.Value, true
		}
	}
	return "", false
}

// GetAll is every value of name, in order.
func (p *URLSearchParams) GetAll(name string) []string {
	var values []string
	for _, pair := range p.pairs {
		if pair.Name == name {
			values = append(values, pair.Value)
		}
	}
	return values
}

// Has is whether name has a value.
func (p *URLSearchParams) Has(name string) bool {
	_, ok := p.Get(name)
	return ok
}

// Pairs is every pair, in order.
func (p *URLSearchParams) Pairs() []QueryPair { return append([]QueryPair(nil), p.pairs...) }

// formDecode is a name or value of a query: `+` as a space, then percent-decoded as UTF-8 (lossy).
func formDecode(text string) string {
	if !strings.ContainsAny(text, "+%") {
		return text
	}
	raw := []byte(strings.ReplaceAll(text, "+", " "))
	decoded := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '%' && i+2 < len(raw) && isHex(raw[i+1]) && isHex(raw[i+2]) {
			decoded = append(decoded, unhex(raw[i+1])<<4|unhex(raw[i+2]))
			i += 2
			continue
		}
		decoded = append(decoded, raw[i])
	}
	return lossyUTF8(decoded)
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func unhex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	}
	return b - 'A' + 10
}

// lossyUTF8 is bytes decoded as UTF-8 by the Encoding Standard's decoder: a byte that cannot continue a character, or the
// longest start of one, becomes one U+FFFD.
func lossyUTF8(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	var out strings.Builder
	var point rune
	needed, seen := 0, 0
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(raw); {
		b := raw[i]
		if needed == 0 {
			switch {
			case b <= 0x7F:
				out.WriteByte(b)
			case b >= 0xC2 && b <= 0xDF:
				needed, point = 1, rune(b&0x1F)
			case b >= 0xE0 && b <= 0xEF:
				if b == 0xE0 {
					lower = 0xA0
				}
				if b == 0xED {
					upper = 0x9F
				}
				needed, point = 2, rune(b&0xF)
			case b >= 0xF0 && b <= 0xF4:
				if b == 0xF0 {
					lower = 0x90
				}
				if b == 0xF4 {
					upper = 0x8F
				}
				needed, point = 3, rune(b&0x7)
			default:
				out.WriteRune(utf8.RuneError)
			}
			i++
			continue
		}
		if b < lower || b > upper {
			// The byte is not consumed: it starts again.
			needed, seen, point = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(utf8.RuneError)
			continue
		}
		lower, upper = 0x80, 0xBF
		point = point<<6 | rune(b&0x3F)
		seen++
		i++
		if seen == needed {
			out.WriteRune(point)
			needed, seen, point = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(utf8.RuneError)
	}
	return out.String()
}
