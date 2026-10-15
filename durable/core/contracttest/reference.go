package contracttest

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ReferenceEncode is a Go model of JSON.stringify(JSON.parse(in)) for the JSON the core writes (CONTRACT 3.1, 5.6). It exists so a
// core's encoder can be fuzzed against an oracle with unlimited inputs; the vectors in testdata/oracle/encode.jsonl, produced by
// the reference runtime, prove the model (TestReferenceEncodeReproducesEveryEncodeVector). It is test tooling: it uses the
// standard library freely and is not part of the core.
//
// What it models: object properties in JavaScript order (array-index keys ascending first, then the others in first-insertion
// order, a repeated key keeping its first position and its last value), numbers as Number.prototype.toString prints them (-0 is
// 0, a value beyond the double range is null), strings with JSON.stringify's escapes and lone surrogates written as \udxxx.
func ReferenceEncode(in []byte) ([]byte, error) {
	p := parser{s: in}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.s) {
		return nil, errors.New("trailing characters")
	}
	return appendValue(nil, v), nil
}

type node struct {
	kind byte // 'n' null, 't' true, 'f' false, '0' number, 's' string, 'a' array, 'o' object
	num  float64
	str  []uint16
	arr  []*node
	keys [][]uint16
	vals []*node
}

type parser struct {
	s []byte
	i int
}

func (p *parser) space() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\n' || p.s[p.i] == '\r' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) lit(word string, n *node) (*node, error) {
	if !strings.HasPrefix(string(p.s[p.i:]), word) {
		return nil, errors.New("bad literal")
	}
	p.i += len(word)
	return n, nil
}

func (p *parser) value() (*node, error) {
	p.space()
	if p.i >= len(p.s) {
		return nil, errors.New("unexpected end")
	}
	switch p.s[p.i] {
	case 'n':
		return p.lit("null", &node{kind: 'n'})
	case 't':
		return p.lit("true", &node{kind: 't'})
	case 'f':
		return p.lit("false", &node{kind: 'f'})
	case '"':
		s, err := p.str()
		return &node{kind: 's', str: s}, err
	case '[':
		p.i++
		n := &node{kind: 'a'}
		p.space()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return n, nil
		}
		for {
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			n.arr = append(n.arr, v)
			p.space()
			if p.i >= len(p.s) {
				return nil, errors.New("unterminated array")
			}
			if p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.s[p.i] == ']' {
				p.i++
				return n, nil
			}
			return nil, errors.New("bad array")
		}
	case '{':
		p.i++
		n := &node{kind: 'o'}
		p.space()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return n, nil
		}
		for {
			p.space()
			if p.i >= len(p.s) || p.s[p.i] != '"' {
				return nil, errors.New("bad key")
			}
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			p.space()
			if p.i >= len(p.s) || p.s[p.i] != ':' {
				return nil, errors.New("missing colon")
			}
			p.i++
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			n.set(k, v)
			p.space()
			if p.i >= len(p.s) {
				return nil, errors.New("unterminated object")
			}
			if p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.s[p.i] == '}' {
				p.i++
				return n, nil
			}
			return nil, errors.New("bad object")
		}
	default:
		return p.number()
	}
}

func (n *node) set(k []uint16, v *node) {
	for i, have := range n.keys {
		if equalUnits(have, k) {
			n.vals[i] = v
			return
		}
	}
	n.keys = append(n.keys, k)
	n.vals = append(n.vals, v)
}

func equalUnits(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (p *parser) number() (*node, error) {
	start := p.i
	if p.i < len(p.s) && p.s[p.i] == '-' {
		p.i++
	}
	digits := func() int {
		n := 0
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			n++
		}
		return n
	}
	if p.i < len(p.s) && p.s[p.i] == '0' {
		p.i++
	} else if digits() == 0 {
		return nil, errors.New("bad number")
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if digits() == 0 {
			return nil, errors.New("bad fraction")
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if digits() == 0 {
			return nil, errors.New("bad exponent")
		}
	}
	f, err := strconv.ParseFloat(string(p.s[start:p.i]), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return nil, err
	}
	return &node{kind: '0', num: f}, nil
}

// str reads a string into UTF-16 code units: a lone surrogate escape survives, which a Go string cannot hold.
func (p *parser) str() ([]uint16, error) {
	p.i++
	var out []uint16
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return out, nil
		case c < 0x20:
			return nil, errors.New("control character in string")
		case c == '\\':
			p.i++
			if p.i >= len(p.s) {
				return nil, errors.New("bad escape")
			}
			e := p.s[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				out = append(out, uint16(e))
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				if p.i+4 > len(p.s) {
					return nil, errors.New("bad unicode escape")
				}
				u, err := strconv.ParseUint(string(p.s[p.i:p.i+4]), 16, 16)
				if err != nil {
					return nil, errors.New("bad unicode escape")
				}
				p.i += 4
				out = append(out, uint16(u))
			default:
				return nil, errors.New("bad escape")
			}
		default:
			r, size := utf8.DecodeRune(p.s[p.i:])
			if r == utf8.RuneError && size == 1 {
				return nil, errors.New("invalid UTF-8")
			}
			p.i += size
			if r >= 0x10000 {
				a, b := utf16.EncodeRune(r)
				out = append(out, uint16(a), uint16(b))
			} else {
				out = append(out, uint16(r))
			}
		}
	}
	return nil, errors.New("unterminated string")
}

// arrayIndex reports whether a key is a canonical array index (0 to 2^32-2), the keys a JavaScript object orders first.
func arrayIndex(k []uint16) (uint32, bool) {
	if len(k) == 0 || len(k) > 10 || (k[0] == '0' && len(k) > 1) {
		return 0, false
	}
	var n uint64
	for _, u := range k {
		if u < '0' || u > '9' {
			return 0, false
		}
		n = n*10 + uint64(u-'0')
	}
	if n > math.MaxUint32-1 {
		return 0, false
	}
	return uint32(n), true
}

func appendValue(b []byte, n *node) []byte {
	switch n.kind {
	case 'n':
		return append(b, "null"...)
	case 't':
		return append(b, "true"...)
	case 'f':
		return append(b, "false"...)
	case '0':
		return appendNumber(b, n.num)
	case 's':
		return appendString(b, n.str)
	case 'a':
		b = append(b, '[')
		for i, v := range n.arr {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendValue(b, v)
		}
		return append(b, ']')
	}
	order := make([]int, len(n.keys))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool {
		a, aok := arrayIndex(n.keys[order[x]])
		c, cok := arrayIndex(n.keys[order[y]])
		switch {
		case aok && cok:
			return a < c
		case aok != cok:
			return aok
		}
		return false
	})
	b = append(b, '{')
	for i, j := range order {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendString(b, n.keys[j])
		b = append(b, ':')
		b = appendValue(b, n.vals[j])
	}
	return append(b, '}')
}

func appendString(b []byte, s []uint16) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		u := s[i]
		switch {
		case u == '"':
			b = append(b, '\\', '"')
		case u == '\\':
			b = append(b, '\\', '\\')
		case u == '\b':
			b = append(b, '\\', 'b')
		case u == '\f':
			b = append(b, '\\', 'f')
		case u == '\n':
			b = append(b, '\\', 'n')
		case u == '\r':
			b = append(b, '\\', 'r')
		case u == '\t':
			b = append(b, '\\', 't')
		case u < 0x20:
			b = append(b, '\\', 'u', '0', '0', hex[u>>4], hex[u&15])
		case u >= 0xd800 && u < 0xdc00 && i+1 < len(s) && s[i+1] >= 0xdc00 && s[i+1] < 0xe000:
			b = utf8.AppendRune(b, utf16.DecodeRune(rune(u), rune(s[i+1])))
			i++
		case u >= 0xd800 && u < 0xe000:
			b = append(b, '\\', 'u', hex[u>>12], hex[u>>8&15], hex[u>>4&15], hex[u&15])
		default:
			b = utf8.AppendRune(b, rune(u))
		}
	}
	return append(b, '"')
}

// appendNumber writes Number.prototype.toString(10): the shortest digits that round-trip, positional for 1e-7 <= |x| < 1e21,
// exponent form outside it. Infinity (JSON.stringify writes null) and NaN cannot come from JSON text, but 1e999 parses to
// Infinity.
func appendNumber(b []byte, f float64) []byte {
	switch {
	case math.IsInf(f, 0) || math.IsNaN(f):
		return append(b, "null"...)
	case f == 0:
		return append(b, '0')
	}
	if f < 0 {
		b = append(b, '-')
		f = -f
	}
	// digits d1d2...dk and exponent n such that value = 0.d1...dk * 10^n (ECMA-262 Number::toString).
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±xx
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	n, k := x+1, len(digits)
	switch {
	case k <= n && n <= 21:
		b = append(b, digits...)
		for i := 0; i < n-k; i++ {
			b = append(b, '0')
		}
	case 0 < n && n <= 21:
		b = append(b, digits[:n]...)
		b = append(b, '.')
		b = append(b, digits[n:]...)
	case -6 < n && n <= 0:
		b = append(b, "0."...)
		for i := 0; i < -n; i++ {
			b = append(b, '0')
		}
		b = append(b, digits...)
	default:
		b = append(b, digits[0])
		if k > 1 {
			b = append(b, '.')
			b = append(b, digits[1:]...)
		}
		b = append(b, 'e')
		if n-1 < 0 {
			b = append(b, '-')
		} else {
			b = append(b, '+')
		}
		b = strconv.AppendInt(b, int64(math.Abs(float64(n-1))), 10)
	}
	return b
}
