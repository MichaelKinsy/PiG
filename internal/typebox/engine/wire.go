package engine

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsparity"
)

// The wire form of a JavaScript value is JSON, with one escape for what JSON has no form for. An object whose first
// key is "\u0000" is an envelope:
//
//	{"\u0000":"u"}                          undefined
//	{"\u0000":"n","v":"123"}                a bigint
//	{"\u0000":"f", ...}                     an opaque value (a function of the extension), kept as it came
//	{"\u0000":"o","h":{...},"e":{...}}      an object with hidden members (h) and enumerable ones (e, in order), or
//	                                        one whose own keys start with "\u0000"
//
// Every other object is its enumerable members, in order.
const envelopeKey = "\x00"

func numberString(f float64) string { return jsnumber.String(f) }

type wireReader struct {
	text string
	pos  int
}

// Decode reads one wire value.
func Decode(text string) (value any, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*Error); ok {
				err = e
				return
			}
			panic(r)
		}
	}()
	r := &wireReader{text: text}
	value = r.value()
	r.space()
	if r.pos != len(r.text) {
		r.fail("unexpected data after the value")
	}
	return value, nil
}

func (r *wireReader) fail(format string, args ...any) {
	throw("SyntaxError", "wire: "+format+" at %d", append(args, r.pos)...)
}

func (r *wireReader) space() {
	for r.pos < len(r.text) {
		switch r.text[r.pos] {
		case ' ', '\t', '\n', '\r':
			r.pos++
		default:
			return
		}
	}
}

func (r *wireReader) expect(c byte) {
	r.space()
	if r.pos >= len(r.text) || r.text[r.pos] != c {
		r.fail("expected %q", c)
	}
	r.pos++
}

func (r *wireReader) value() any {
	r.space()
	if r.pos >= len(r.text) {
		r.fail("unexpected end")
	}
	switch c := r.text[r.pos]; {
	case c == '{':
		return r.object()
	case c == '[':
		r.pos++
		out := []any{}
		r.space()
		if r.pos < len(r.text) && r.text[r.pos] == ']' {
			r.pos++
			return out
		}
		for {
			out = append(out, r.value())
			r.space()
			if r.pos < len(r.text) && r.text[r.pos] == ',' {
				r.pos++
				continue
			}
			r.expect(']')
			return out
		}
	case c == '"':
		return r.str()
	case strings.HasPrefix(r.text[r.pos:], "true"):
		r.pos += 4
		return true
	case strings.HasPrefix(r.text[r.pos:], "false"):
		r.pos += 5
		return false
	case strings.HasPrefix(r.text[r.pos:], "null"):
		r.pos += 4
		return nil
	default:
		start := r.pos
		for r.pos < len(r.text) && strings.IndexByte("+-0123456789.eE", r.text[r.pos]) >= 0 {
			r.pos++
		}
		f, err := strconv.ParseFloat(r.text[start:r.pos], 64)
		if start == r.pos || (err != nil && f == 0) {
			r.fail("bad number")
		}
		return f
	}
}

func (r *wireReader) str() string {
	start := r.pos
	r.pos++
	for r.pos < len(r.text) && r.text[r.pos] != '"' {
		if r.text[r.pos] == '\\' {
			r.pos++
		}
		r.pos++
	}
	if r.pos >= len(r.text) {
		r.fail("unterminated string")
	}
	r.pos++
	s, err := strconv.Unquote(strings.ReplaceAll(strings.ReplaceAll(r.text[start:r.pos], `\/`, "/"), `\u0000`, `\x00`))
	if err != nil {
		s, err = unquoteJSON(r.text[start:r.pos])
		if err != nil {
			r.fail("bad string")
		}
	}
	return s
}

// unquoteJSON decodes a JSON string literal, surrogate pairs included, where strconv.Unquote cannot (\uXXXX of a
// surrogate half, \/).
func unquoteJSON(lit string) (string, error) {
	var b strings.Builder
	body := lit[1 : len(lit)-1]
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("bad escape")
		}
		switch body[i] {
		case '"', '\\', '/':
			b.WriteByte(body[i])
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			if i+4 >= len(body)+0 && i+4 > len(body)-1+0 {
				if i+4 > len(body)-1 {
					return "", fmt.Errorf("bad escape")
				}
			}
			n, err := strconv.ParseUint(body[i+1:i+5], 16, 16)
			if err != nil {
				return "", err
			}
			i += 4
			u := rune(n)
			if u >= 0xd800 && u < 0xdc00 && strings.HasPrefix(body[i+1:], `\u`) && i+6 < len(body)+1 {
				if lo, err := strconv.ParseUint(body[i+3:i+7], 16, 16); err == nil && lo >= 0xdc00 && lo < 0xe000 {
					b.WriteRune(0x10000 + (u-0xd800)<<10 + rune(lo) - 0xdc00)
					i += 6
					continue
				}
			}
			b.WriteString(wtf8(u))
		default:
			return "", fmt.Errorf("bad escape")
		}
	}
	return b.String(), nil
}

// wtf8 encodes a UTF-16 code unit, a lone surrogate included, the way the host keeps JavaScript strings.
func wtf8(u rune) string {
	if u >= 0xd800 && u < 0xe000 {
		return string([]byte{0xe0 | byte(u>>12), 0x80 | byte(u>>6)&0x3f, 0x80 | byte(u)&0x3f})
	}
	return string(u)
}

func (r *wireReader) object() any {
	r.pos++
	type member struct {
		key   string
		value any
	}
	var members []member
	r.space()
	if r.pos < len(r.text) && r.text[r.pos] == '}' {
		r.pos++
		return NewObj()
	}
	for {
		r.space()
		if r.pos >= len(r.text) || r.text[r.pos] != '"' {
			r.fail("expected a key")
		}
		key := r.str()
		r.expect(':')
		members = append(members, member{key, r.value()})
		r.space()
		if r.pos < len(r.text) && r.text[r.pos] == ',' {
			r.pos++
			continue
		}
		r.expect('}')
		break
	}
	if members[0].key == envelopeKey {
		return r.envelope(members[0].value, func(name string) (any, bool) {
			for _, m := range members[1:] {
				if m.key == name {
					return m.value, true
				}
			}
			return nil, false
		})
	}
	o := NewObj()
	for _, m := range members {
		o.Set(m.key, m.value)
	}
	return o
}

func (r *wireReader) envelope(kind any, field func(string) (any, bool)) any {
	switch kind {
	case "u":
		return Undef
	case "n":
		v, _ := field("v")
		n, ok := new(big.Int).SetString(fmt.Sprint(v), 10)
		if !ok {
			r.fail("bad bigint")
		}
		return n
	case "f":
		i, _ := field("i")
		return &Opaque{Raw: fmt.Sprint(i)}
	case "o":
		o := NewObj()
		if h, ok := field("h"); ok {
			if ho, ok := h.(*Obj); ok {
				for _, k := range ho.names {
					o.SetHidden(k, ho.props[k].value)
				}
			}
		}
		if en, ok := field("e"); ok {
			if eo, ok := en.(*Obj); ok {
				for _, k := range eo.names {
					o.Set(k, eo.props[k].value)
				}
			}
		}
		return o
	}
	r.fail("unknown envelope %v", kind)
	return nil
}

// Encode writes a value in the wire form.
func Encode(value any) string {
	var b strings.Builder
	encode(&b, value)
	return b.String()
}

func writeString(b *strings.Builder, s string) { b.Write(jsparity.AppendString(nil, s)) }

func encode(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		b.Write(jsparity.AppendNumber(nil, v))
	case string:
		writeString(b, v)
	case *big.Int:
		b.WriteString(`{"\u0000":"n","v":"` + v.String() + `"}`)
	case []any:
		b.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			encode(b, e)
		}
		b.WriteByte(']')
	case *Opaque:
		if v.Raw == "" {
			b.WriteString(`{"\u0000":"u"}`)
		} else {
			b.WriteString(`{"\u0000":"f","i":` + v.Raw + `}`)
		}
	case *undefined:
		b.WriteString(`{"\u0000":"u"}`)
	case *Obj:
		hidden := []string{}
		for _, k := range v.Keys() {
			if !v.props[k].enumerable {
				hidden = append(hidden, k)
			}
		}
		needEnvelope := len(hidden) > 0
		enum := v.EnumKeys()
		for _, k := range enum {
			if strings.HasPrefix(k, envelopeKey) {
				needEnvelope = true
			}
		}
		if needEnvelope {
			b.WriteString(`{"\u0000":"o"`)
			if len(hidden) > 0 {
				b.WriteString(`,"h":{`)
				for i, k := range hidden {
					if i > 0 {
						b.WriteByte(',')
					}
					writeString(b, k)
					b.WriteByte(':')
					encode(b, v.props[k].value)
				}
				b.WriteByte('}')
			}
			b.WriteString(`,"e":`)
		}
		b.WriteByte('{')
		for i, k := range enum {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, k)
			b.WriteByte(':')
			encode(b, v.props[k].value)
		}
		b.WriteByte('}')
		if needEnvelope {
			b.WriteByte('}')
		}
	default:
		panic(fmt.Sprintf("engine: cannot encode %T", value))
	}
}
