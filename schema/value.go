package schema

// Go forms of the JavaScript value operations the upstream sources use directly. JSON data is nil, bool, float64 (any Go numeric kind reads as a number), string, []any, *Object, map[string]any, and []byte for Uint8Array.

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// jsonObject views an object value, an *Object or a map[string]any, without allocating.
type jsonObject struct {
	ordered *Object
	plain   map[string]any
}

// nodeKey identifies a schema object by identity, as the upstream Maps keyed by object do.
type nodeKey = unsafe.Pointer

func asObject(v any) (jsonObject, bool) {
	switch x := v.(type) {
	case *Object:
		return jsonObject{ordered: x}, true
	case map[string]any:
		return jsonObject{plain: x}, true
	}
	return jsonObject{}, false
}

func (o jsonObject) get(key string) (any, bool) {
	if o.ordered != nil {
		return o.ordered.Get(key)
	}
	value, ok := o.plain[key]
	return value, ok
}

// value is the member under key, nil when it is absent. Callers that must tell a null member from an absent one use get.
func (o jsonObject) value(key string) any {
	value, _ := o.get(key)
	return value
}

func (o jsonObject) has(key string) bool {
	_, ok := o.get(key)
	return ok
}

func (o jsonObject) len() int {
	if o.ordered != nil {
		return o.ordered.Len()
	}
	return len(o.plain)
}

// keys returns the own keys in JavaScript order: array-index keys ascending, then insertion order; a Go map has no insertion order, so its other keys ascend.
func (o jsonObject) keys() []string {
	if o.ordered != nil {
		return o.ordered.Keys()
	}
	return chordjson.MapOwnKeys(o.plain)
}

// identity is the schema object's identity: equal for the same *Object or the same map.
func (o jsonObject) identity() nodeKey {
	if o.ordered != nil {
		return nodeKey(o.ordered)
	}
	return reflect.ValueOf(o.plain).UnsafePointer()
}

// each calls fn for every member until fn returns false, in no particular order.
func (o jsonObject) each(fn func(key string, value any) bool) {
	if o.ordered != nil {
		for key, value := range o.ordered.All() {
			if !fn(key, value) {
				return
			}
		}
		return
	}
	for key, value := range o.plain {
		if !fn(key, value) {
			return
		}
	}
}

// isObject reports whether v is a JSON object: not null, an array or bytes.
func isObject(v any) bool {
	_, ok := asObject(v)
	return ok
}

// isSchemaObject reports whether value is a schema object (not true, false, an array or a primitive).
func isSchemaObject(value any) (jsonObject, bool) { return asObject(value) }

// asNumber reads any Go numeric kind as a JavaScript number.
func asNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

// isInteger is Number.isInteger.
func isInteger(v float64) bool { return !math.IsInf(v, 0) && !math.IsNaN(v) && v == math.Trunc(v) }

func isContainer(v any) bool {
	switch v.(type) {
	case *Object, map[string]any, []any, []byte:
		return true
	}
	return false
}

// stringValue is the string held by v, if it is one.
func stringValue(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// numberMember is `typeof node[key] === "number" ? node[key] : undefined`.
func (o jsonObject) numberMember(key string) (float64, bool) {
	value, _ := o.get(key)
	return asNumber(value)
}

func (o jsonObject) stringMember(key string) (string, bool) {
	value, _ := o.get(key)
	return stringValue(value)
}

func (o jsonObject) arrayMember(key string) ([]any, bool) {
	value, _ := o.get(key)
	list, ok := value.([]any)
	return list, ok
}

func (o jsonObject) objectMember(key string) (jsonObject, bool) {
	value, _ := o.get(key)
	return asObject(value)
}

// utf16Len is the length of s in UTF-16 code units, JavaScript's string length. A lone surrogate is held as WTF-8 (see internal/jsstring).
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, size := jsstring.DecodeRuneInString(s[i:])
		i += size
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// codePoints is the number of Unicode code points of s: JSON Schema string lengths count code points, not UTF-16 units. A high surrogate unit starts a pair only when a low surrogate unit follows (DecodeRuneInString combines adjacent halves); an unpaired one, held as WTF-8, is a code point of its own, so "\uD800a" counts 2.
func codePoints(s string) int {
	n := 0
	for i := 0; i < len(s); n++ {
		_, size := jsstring.DecodeRuneInString(s[i:])
		i += size
	}
	return n
}

// strictEquals is `a === b` for primitives; containers are never strictly equal here.
func strictEquals(a, b any) bool {
	if x, ok := asNumber(a); ok {
		y, ok := asNumber(b)
		return ok && x == y
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	}
	return false
}

// sameValueZero is the comparison of Set and Array.prototype.includes: strict equality, except that NaN equals NaN.
func sameValueZero(a, b any) bool {
	if x, ok := asNumber(a); ok {
		if y, ok := asNumber(b); ok && math.IsNaN(x) && math.IsNaN(y) {
			return true
		}
	}
	return strictEquals(a, b)
}

// jsonStringify is JSON.stringify of a JSON value.
func jsonStringify(v any) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *strings.Builder, v any) {
	if n, ok := asNumber(v); ok {
		b.Write(jsnumber.JSON(n))
		return
	}
	switch x := v.(type) {
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		writeJSONString(b, x)
	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, item)
		}
		b.WriteByte(']')
	case []byte:
		b.WriteByte('{')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`"` + strconv.Itoa(i) + `":` + strconv.Itoa(int(item)))
		}
		b.WriteByte('}')
	case *Object, map[string]any:
		o, _ := asObject(x)
		b.WriteByte('{')
		for i, key := range o.keys() {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, key)
			b.WriteByte(':')
			writeJSON(b, o.value(key))
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

// writeJSONString quotes s as JSON.stringify does: no escaping of <, >, & or U+2028, and a lone surrogate (WTF-8 in s) as its lowercase \u escape.
func writeJSONString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := jsstring.DecodeRuneInString(s[i:])
		i += size
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
		case r < 0x20 || (r >= 0xd800 && r <= 0xdfff):
			b.WriteString(`\u`)
			b.WriteByte(hex[r>>12&15])
			b.WriteByte(hex[r>>8&15])
			b.WriteByte(hex[r>>4&15])
			b.WriteByte(hex[r&15])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

// jsToString is the string conversion Array.prototype.join applies to an element: null reads as "".
func jsToString(v any) string {
	if n, ok := asNumber(v); ok {
		return jsnumber.String(n)
	}
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = jsToString(item)
		}
		return strings.Join(parts, ",")
	case []byte:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = strconv.Itoa(int(item))
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// numberString is the JavaScript string of a number.
func numberString(n float64) string { return jsnumber.String(n) }
