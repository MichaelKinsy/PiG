package codemode

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// object is a decoded JSON object that keeps JavaScript's property order: canonical array-index keys ascending,
// then the other keys in insertion order. Decoded JSON values are nil, bool, json.Number, string, []any and *object.
type object struct {
	keys []string
	vals map[string]any
}

func (o *object) get(key string) (any, bool) { v, ok := o.vals[key]; return v, ok }

// canonicalIndex reports whether key is a JavaScript array index ("0", "17", not "01" or "-1").
func canonicalIndex(key string) (uint32, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(key, 10, 32)
	if err != nil || n == math.MaxUint32 {
		return 0, false
	}
	return uint32(n), true
}

func (o *object) order() []string {
	var indexes, others []string
	for _, k := range o.keys {
		if _, ok := canonicalIndex(k); ok {
			indexes = append(indexes, k)
		} else {
			others = append(others, k)
		}
	}
	sort.SliceStable(indexes, func(i, j int) bool {
		a, _ := canonicalIndex(indexes[i])
		b, _ := canonicalIndex(indexes[j])
		return a < b
	})
	return append(indexes, others...)
}

// decodeJSON decodes one JSON value, rejecting trailing data like JSON.parse. A \uXXXX escape naming an unpaired
// surrogate stays a WTF-8 sequence, as it stays a lone unit in a JavaScript string; encoding/json would replace it.
func decodeJSON(data []byte) (any, error) {
	if !json.Valid(data) {
		var discard any
		return nil, json.Unmarshal(data, &discard)
	}
	d := jsonDecoder{text: data}
	d.space()
	v := d.value()
	return v, nil
}

// jsonDecoder reads text that json.Valid accepted.
type jsonDecoder struct {
	text []byte
	pos  int
}

func (d *jsonDecoder) space() {
	for d.pos < len(d.text) && strings.IndexByte(" \t\r\n", d.text[d.pos]) >= 0 {
		d.pos++
	}
}

func (d *jsonDecoder) value() any {
	switch c := d.text[d.pos]; c {
	case '{':
		d.pos++
		obj := &object{vals: map[string]any{}}
		d.space()
		if d.text[d.pos] == '}' {
			d.pos++
			return obj
		}
		for {
			d.space()
			key := d.str()
			d.space()
			d.pos++ // ':'
			d.space()
			v := d.value()
			if _, seen := obj.vals[key]; !seen {
				obj.keys = append(obj.keys, key)
			}
			obj.vals[key] = v
			d.space()
			d.pos++ // ',' or '}'
			if d.text[d.pos-1] == '}' {
				return obj
			}
		}
	case '[':
		d.pos++
		arr := []any{}
		d.space()
		if d.text[d.pos] == ']' {
			d.pos++
			return arr
		}
		for {
			d.space()
			arr = append(arr, d.value())
			d.space()
			d.pos++ // ',' or ']'
			if d.text[d.pos-1] == ']' {
				return arr
			}
		}
	case '"':
		return d.str()
	case 't':
		d.pos += 4
		return true
	case 'f':
		d.pos += 5
		return false
	case 'n':
		d.pos += 4
		return nil
	default:
		start := d.pos
		for d.pos < len(d.text) && strings.IndexByte("+-0123456789.eE", d.text[d.pos]) >= 0 {
			d.pos++
		}
		return json.Number(d.text[start:d.pos])
	}
}

// str reads a string literal into WTF-8.
func (d *jsonDecoder) str() string {
	d.pos++ // opening quote
	start := d.pos
	for d.text[d.pos] != '"' && d.text[d.pos] != '\\' {
		d.pos++
	}
	if d.text[d.pos] == '"' {
		s := string(d.text[start:d.pos])
		d.pos++
		return s
	}
	units := utf16.Encode([]rune(string(d.text[start:d.pos])))
	for d.text[d.pos] != '"' {
		if d.text[d.pos] != '\\' {
			r, size := utf8.DecodeRune(d.text[d.pos:])
			units = utf16.AppendRune(units, r)
			d.pos += size
			continue
		}
		d.pos++
		escape := d.text[d.pos]
		d.pos++
		switch escape {
		case 'b':
			units = append(units, '\b')
		case 'f':
			units = append(units, '\f')
		case 'n':
			units = append(units, '\n')
		case 'r':
			units = append(units, '\r')
		case 't':
			units = append(units, '\t')
		case 'u':
			unit, _ := strconv.ParseUint(string(d.text[d.pos:d.pos+4]), 16, 16)
			units = append(units, uint16(unit))
			d.pos += 4
		default:
			units = append(units, uint16(escape))
		}
	}
	d.pos++
	return jsstring.FromUTF16(units)
}

// jsNumberString formats f like Number.prototype.toString.
func jsNumberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	abs := math.Abs(f)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	s := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[:1]
	digits := strings.TrimLeft(exp[1:], "0")
	return mant + "e" + sign + digits
}

// jsQuote formats s like JSON.stringify: control characters escape, U+2028, U+FFFD and HTML characters do not.
func jsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	units := jsstring.ToUTF16(s)
	for i := 0; i < len(units); i++ {
		r := rune(units[i])
		if r >= 0xd800 && r <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			b.WriteRune(utf16.DecodeRune(r, rune(units[i+1])))
			i++
			continue
		}
		if r >= 0xd800 && r <= 0xdfff {
			b.WriteString(`\u`)
			b.WriteString(strconv.FormatUint(uint64(r), 16))
			continue
		}
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte("0123456789abcdef"[r>>4])
			b.WriteByte("0123456789abcdef"[r&15])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jsStringify formats a decoded value like JSON.stringify.
func jsStringify(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return "null"
		}
		return jsNumberString(f)
	case string:
		return jsQuote(v)
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = jsStringify(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *object:
		var parts []string
		for _, k := range v.order() {
			parts = append(parts, jsQuote(k)+":"+jsStringify(v.vals[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return "null"
}

// compareUTF16 orders strings by UTF-16 code units, like Array.prototype.sort's default.
func compareUTF16(a, b string) int {
	ua, ub := jsstring.ToUTF16(a), jsstring.ToUTF16(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			if ua[i] < ub[i] {
				return -1
			}
			return 1
		}
	}
	return len(ua) - len(ub)
}
