package modelgen

// Ports the JavaScript plain-object semantics packages/ai/catalog relies on: members keep insertion order (canonical array
// indices first, ascending), spread overwrites in place, and JSON.stringify drops nothing that is present. Catalog models are
// Obj values because the generated JSON is compared byte for byte with Pi's, member order included.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Obj is a JavaScript plain object: string keys in insertion order. Values are nil (JSON null), string, bool, float64,
// []any and *Obj; Set normalizes int and []string. An absent key is JavaScript's undefined.
type Obj struct {
	keys []string
	vals map[string]any
}

// NewObj returns an empty object.
func NewObj() *Obj { return &Obj{vals: map[string]any{}} }

// O builds an object from alternating keys and values: `{ key: value, ... }`.
func O(pairs ...any) *Obj {
	if len(pairs)%2 != 0 {
		panic("modelgen.O: odd number of arguments")
	}
	object := NewObj()
	for i := 0; i < len(pairs); i += 2 {
		object.Set(pairs[i].(string), pairs[i+1])
	}
	return object
}

// Spread is `{ ...parts[0], ...parts[1], ... }`: a key keeps the position of its first appearance and takes the last value.
// A nil part contributes nothing, as spreading undefined does. Values are shared, not copied, as in JavaScript.
func Spread(parts ...*Obj) *Obj {
	object := NewObj()
	for _, part := range parts {
		if part == nil {
			continue
		}
		for _, key := range part.keys {
			object.Set(key, part.vals[key])
		}
	}
	return object
}

// Strings is the array of strings value.
func Strings(values ...string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func normalize(value any) any {
	switch v := value.(type) {
	case nil, string, bool, float64, *Obj:
		if object, ok := v.(*Obj); ok && object == nil {
			panic("modelgen: nil *Obj stored as a value (use nil for null or leave the key absent)")
		}
		return v
	case int:
		return float64(v)
	case []string:
		return Strings(v...)
	case []any:
		return v
	}
	panic(fmt.Sprintf("modelgen: unsupported object value %T", value))
}

// Set assigns a member: `object[key] = value`. An existing key keeps its position.
func (o *Obj) Set(key string, value any) *Obj {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = normalize(value)
	return o
}

// Get reports the member and whether it is present.
func (o *Obj) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	value, ok := o.vals[key]
	return value, ok
}

// Has reports whether the key is present (`key in object`; an undefined value never occurs here).
func (o *Obj) Has(key string) bool {
	_, ok := o.Get(key)
	return ok
}

// Delete removes a member: `delete object[key]`.
func (o *Obj) Delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	o.keys = slices.DeleteFunc(o.keys, func(candidate string) bool { return candidate == key })
}

// Len is the number of members.
func (o *Obj) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Keys returns the keys in JavaScript enumeration order: canonical array indices ascending, then the others in insertion order.
func (o *Obj) Keys() []string {
	out := slices.Clone(o.keys)
	var indices []string
	for _, key := range out {
		if isArrayIndex(key) {
			indices = append(indices, key)
		}
	}
	if len(indices) == 0 {
		return out
	}
	slices.SortFunc(indices, func(a, b string) int {
		x, _ := strconv.ParseUint(a, 10, 32)
		y, _ := strconv.ParseUint(b, 10, 32)
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	})
	rest := slices.DeleteFunc(out, isArrayIndex)
	return append(indices, rest...)
}

func isArrayIndex(key string) bool {
	n, err := strconv.ParseUint(key, 10, 32)
	return err == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == key
}

// Clone is structuredClone: a deep copy.
func (o *Obj) Clone() *Obj {
	out := NewObj()
	for _, key := range o.keys {
		out.Set(key, cloneValue(o.vals[key]))
	}
	return out
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case *Obj:
		return v.Clone()
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = cloneValue(item)
		}
		return out
	}
	return value
}

// CloneAll clones every object of a list.
func CloneAll(objects []*Obj) []*Obj {
	out := make([]*Obj, len(objects))
	for i, object := range objects {
		out[i] = object.Clone()
	}
	return out
}

// Str returns a string member, "" when absent or not a string.
func (o *Obj) Str(key string) string {
	value, _ := o.member(key).(string)
	return value
}

// Bool returns a boolean member, false when absent or not a boolean.
func (o *Obj) Bool(key string) bool {
	value, _ := o.member(key).(bool)
	return value
}

// Num returns a number member, 0 when absent or not a number.
func (o *Obj) Num(key string) float64 {
	value, _ := o.member(key).(float64)
	return value
}

// Object returns an object member, nil when absent or not an object.
func (o *Obj) Object(key string) *Obj {
	value, _ := o.member(key).(*Obj)
	return value
}

// List returns an array member, nil when absent or not an array.
func (o *Obj) List(key string) []any {
	value, _ := o.member(key).([]any)
	return value
}

// StringList returns the strings of an array member.
func (o *Obj) StringList(key string) []string {
	var out []string
	for _, item := range o.List(key) {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// Includes is `array.includes(value)` for an array of strings.
func (o *Obj) Includes(key, value string) bool {
	return slices.Contains(o.StringList(key), value)
}

// MarshalJSON is JSON.stringify(object).
func (o *Obj) MarshalJSON() ([]byte, error) { return Stringify(o, ""), nil }

// Stringify is JSON.stringify(value, null, indent) for catalog values; indent is "" or the per-level indent.
func Stringify(value any, indent string) []byte {
	var out bytes.Buffer
	writeValue(&out, value, indent, "")
	return out.Bytes()
}

func writeValue(out *bytes.Buffer, value any, indent, current string) {
	switch v := value.(type) {
	case nil:
		out.WriteString("null")
	case string:
		out.WriteString(Quote(v))
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case float64:
		out.Write(jsnumber.JSON(v))
	case []any:
		if len(v) == 0 {
			out.WriteString("[]")
			return
		}
		inner := current + indent
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if indent != "" {
				out.WriteString("\n" + inner)
			}
			writeValue(out, item, indent, inner)
		}
		if indent != "" {
			out.WriteString("\n" + current)
		}
		out.WriteByte(']')
	case *Obj:
		if v.Len() == 0 {
			out.WriteString("{}")
			return
		}
		inner := current + indent
		out.WriteByte('{')
		for i, key := range v.Keys() {
			if i > 0 {
				out.WriteByte(',')
			}
			if indent != "" {
				out.WriteString("\n" + inner)
			}
			out.WriteString(Quote(key))
			out.WriteByte(':')
			if indent != "" {
				out.WriteByte(' ')
			}
			writeValue(out, v.vals[key], indent, inner)
		}
		if indent != "" {
			out.WriteString("\n" + current)
		}
		out.WriteByte('}')
	default:
		panic(fmt.Sprintf("modelgen: cannot stringify %T", value))
	}
}

// Quote is JSON.stringify(string): it escapes control characters, quotes and backslashes only.
func Quote(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// ParseJSON is JSON.parse for catalog sources: objects become *Obj in document order (a repeated key keeps its first position
// and takes its last value), numbers become float64.
func ParseJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after JSON value")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch v := token.(type) {
	case json.Delim:
		switch v {
		case '{':
			object := NewObj()
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				object.Set(keyToken.(string), value)
			}
			_, err := decoder.Token()
			return object, err
		case '[':
			list := []any{}
			for decoder.More() {
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			_, err := decoder.Token()
			return list, err
		}
	case json.Number:
		return jsnumber.FromJSON([]byte(v.String())), nil
	}
	return token, nil
}

func (o *Obj) member(key string) any {
	value, _ := o.Get(key)
	return value
}
