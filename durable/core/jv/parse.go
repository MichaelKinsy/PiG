package jv

import (
	"errors"
	"math"
	"strconv"
)

// ErrSyntax reports JSON text that JSON.parse rejects. Read-only.
var ErrSyntax = errors.New("invalid JSON")

// maxDepth bounds nesting; JSON.parse itself is limited only by the stack.
const maxDepth = 10000

// Parse returns JSON.parse(text). Duplicate object keys keep the position of
// the first occurrence and the value of the last. A number outside the float64
// range parses to +Inf or -Inf, as in JavaScript.
func Parse(text []byte) (any, error) {
	p := parser{s: text}
	p.ws()
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.i != len(p.s) {
		return nil, ErrSyntax
	}
	return v, nil
}

// ParseString is Parse for a string.
func ParseString(text string) (any, error) { return Parse([]byte(text)) }

type parser struct {
	s []byte
	i int
}

func (p *parser) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) (any, error) {
	if depth > maxDepth || p.i >= len(p.s) {
		return nil, ErrSyntax
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		return p.str()
	case c == 't':
		return p.lit("true", true)
	case c == 'f':
		return p.lit("false", false)
	case c == 'n':
		return p.lit("null", nil)
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, ErrSyntax
}

func (p *parser) lit(word string, v any) (any, error) {
	if len(p.s)-p.i < len(word) || string(p.s[p.i:p.i+len(word)]) != word {
		return nil, ErrSyntax
	}
	p.i += len(word)
	return v, nil
}

func (p *parser) number() (any, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.s) {
		return nil, ErrSyntax
	}
	switch {
	case p.s[p.i] == '0':
		p.i++
	case p.s[p.i] >= '1' && p.s[p.i] <= '9':
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
	default:
		return nil, ErrSyntax
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		d := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		if p.i == d {
			return nil, ErrSyntax
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		d := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		if p.i == d {
			return nil, ErrSyntax
		}
	}
	f, err := strconv.ParseFloat(string(p.s[start:p.i]), 64)
	if err != nil && !math.IsInf(f, 0) {
		return nil, ErrSyntax
	}
	return f, nil
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func (p *parser) hex4() (int, bool) {
	if p.i+4 > len(p.s) {
		return 0, false
	}
	v := 0
	for j := range 4 {
		h := hexVal(p.s[p.i+j])
		if h < 0 {
			return 0, false
		}
		v = v<<4 | h
	}
	p.i += 4
	return v, true
}

func appendCodePoint(dst []byte, r int) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3f))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6&0x3f), 0x80|byte(r&0x3f))
	}
	return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3f), 0x80|byte(r>>6&0x3f), 0x80|byte(r&0x3f))
}

func (p *parser) str() (string, error) {
	p.i++ // opening quote
	start := p.i
	// fast path: no escapes
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '"' {
			s := string(p.s[start:p.i])
			p.i++
			return s, nil
		}
		if c == '\\' || c < 0x20 {
			break
		}
		p.i++
	}
	buf := append([]byte(nil), p.s[start:p.i]...)
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return string(buf), nil
		case c < 0x20:
			return "", ErrSyntax
		case c != '\\':
			buf = append(buf, c)
			p.i++
			continue
		}
		p.i++
		if p.i >= len(p.s) {
			return "", ErrSyntax
		}
		e := p.s[p.i]
		p.i++
		switch e {
		case '"', '\\', '/':
			buf = append(buf, e)
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'u':
			u, ok := p.hex4()
			if !ok {
				return "", ErrSyntax
			}
			if u >= 0xD800 && u <= 0xDBFF && p.i+6 <= len(p.s) && p.s[p.i] == '\\' && p.s[p.i+1] == 'u' {
				save := p.i
				p.i += 2
				lo, ok := p.hex4()
				if ok && lo >= 0xDC00 && lo <= 0xDFFF {
					buf = appendCodePoint(buf, 0x10000+(u-0xD800)<<10+(lo-0xDC00))
					continue
				}
				p.i = save
			}
			buf = appendCodePoint(buf, u) // a lone surrogate stays in its three-byte form
		default:
			return "", ErrSyntax
		}
	}
	return "", ErrSyntax
}

func (p *parser) array(depth int) (any, error) {
	p.i++
	out := []any{}
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return out, nil
	}
	for {
		p.ws()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.ws()
		if p.i >= len(p.s) {
			return nil, ErrSyntax
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return out, nil
		default:
			return nil, ErrSyntax
		}
	}
}

func (p *parser) object(depth int) (any, error) {
	p.i++
	o := NewObject()
	p.ws()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return o, nil
	}
	for {
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return nil, ErrSyntax
		}
		k, err := p.str()
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, ErrSyntax
		}
		p.i++
		p.ws()
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		o.Set(k, v)
		p.ws()
		if p.i >= len(p.s) {
			return nil, ErrSyntax
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return o, nil
		default:
			return nil, ErrSyntax
		}
	}
}
