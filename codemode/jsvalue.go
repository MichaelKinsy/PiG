package codemode

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
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

// decodeJSON decodes one JSON value, rejecting trailing data like JSON.parse.
func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	if delim == '[' {
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	obj := &object{vals: map[string]any{}}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := keyTok.(string)
		v, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}
		if _, seen := obj.vals[key]; !seen {
			obj.keys = append(obj.keys, key)
		}
		obj.vals[key] = v
	}
	_, err = dec.Token()
	return obj, err
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
	for _, r := range s {
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
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
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
