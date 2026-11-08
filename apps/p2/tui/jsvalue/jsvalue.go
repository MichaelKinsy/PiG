// Package jsvalue is the JSON values the TUI's tool renderers read: tool arguments and result details are JSON from the conversation view, and the
// order of an object's keys is part of what a card shows (`key=value` pairs in the order the model wrote them), which a Go map does not keep.
//
// Values are nil (null), bool, float64, string, []any and *Object.
package jsvalue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

// Object is a JSON object with its keys in insertion order.
type Object struct {
	Keys   []string
	Values map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object { return &Object{Values: map[string]any{}} }

// Set sets a member: in place when present, appended otherwise.
func (o *Object) Set(key string, value any) *Object {
	if _, ok := o.Values[key]; !ok {
		o.Keys = append(o.Keys, key)
	}
	o.Values[key] = value
	return o
}

// Get returns a member; ok is false when it is absent.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	value, ok := o.Values[key]
	return value, ok
}

// Lookup returns a member, nil when absent (a JS `object.key` read yields undefined, which the renderers treat as null).
func (o *Object) Lookup(key string) any {
	value, _ := o.Get(key)
	return value
}

// Obj builds an object from key, value pairs.
func Obj(pairs ...any) *Object {
	o := NewObject()
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// Parse decodes JSON text, keeping object key order.
func Parse(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after the JSON value")
	}
	return value, nil
}

func decodeValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			object := NewObject()
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				object.Set(key.(string), value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			list := []any{}
			for decoder.More() {
				value, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
	case json.Number:
		return jsnumber.FromJSON([]byte(typed)), nil
	}
	return token, nil
}

// Normalize converts a generic decoded JSON value (maps) to this package's, with map keys in sorted order; use Parse when the order matters.
func Normalize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sortStrings(keys)
		object := NewObject()
		for _, key := range keys {
			object.Set(key, Normalize(typed[key]))
		}
		return object
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = Normalize(item)
		}
		return out
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return value
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// Quote is JSON.stringify(text) for a string.
func Quote(text string) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return strings.TrimSuffix(out.String(), "\n")
}

// Stringify is JSON.stringify(value, null, indent) for the values of this package (indent 0 is compact). ok is false for a value that JSON.stringify
// returns undefined for.
func Stringify(value any, indent int) (string, bool) {
	var out strings.Builder
	writeValue(&out, value, indent, 0)
	return out.String(), true
}

func writeValue(out *strings.Builder, value any, indent, depth int) {
	newline := func(level int) {
		if indent > 0 {
			out.WriteByte('\n')
			out.WriteString(strings.Repeat(" ", indent*level))
		}
	}
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if typed {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case float64:
		if typed != typed || typed > 1.7976931348623157e308 || typed < -1.7976931348623157e308 {
			out.WriteString("null")
		} else {
			out.WriteString(jsnumber.String(typed))
		}
	case string:
		out.WriteString(Quote(typed))
	case []any:
		if len(typed) == 0 {
			out.WriteString("[]")
			return
		}
		out.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				out.WriteByte(',')
			}
			newline(depth + 1)
			writeValue(out, item, indent, depth+1)
		}
		newline(depth)
		out.WriteByte(']')
	case *Object:
		if typed == nil || len(typed.Keys) == 0 {
			out.WriteString("{}")
			return
		}
		out.WriteByte('{')
		for i, key := range typed.Keys {
			if i > 0 {
				out.WriteByte(',')
			}
			newline(depth + 1)
			out.WriteString(Quote(key))
			out.WriteByte(':')
			if indent > 0 {
				out.WriteByte(' ')
			}
			writeValue(out, typed.Values[key], indent, depth+1)
		}
		newline(depth)
		out.WriteByte('}')
	default:
		out.WriteString("null")
	}
}

// String is JS `String(value)` for a JSON value (arrays join their items with commas, an object is "[object Object]").
func String(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool, float64:
		text, _ := Stringify(typed, 0)
		if number, ok := typed.(float64); ok {
			return jsnumber.String(number)
		}
		return text
	case []any:
		parts := make([]string, len(typed))
		for i, item := range typed {
			if item != nil {
				parts[i] = String(item)
			}
		}
		return strings.Join(parts, ",")
	case *Object:
		return "[object Object]"
	}
	return fmt.Sprint(value)
}

// LocaleCompareNumeric is `a.localeCompare(b, "en", { numeric: true })` for the ASCII keys of registries: runs of digits compare by value, other text
// compares ignoring case first and then with lower case before upper case (ICU's tertiary order). Text outside ASCII compares by code point.
func LocaleCompareNumeric(a, b string) int {
	if result := compareNumericChunks(a, b, true); result != 0 {
		return result
	}
	return compareNumericChunks(a, b, false)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func compareNumericChunks(a, b string, foldCase bool) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			i, j := 0, 0
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			x, y := strings.TrimLeft(a[:i], "0"), strings.TrimLeft(b[:j], "0")
			if len(x) != len(y) {
				if len(x) < len(y) {
					return -1
				}
				return 1
			}
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
			a, b = a[i:], b[j:]
			continue
		}
		ca, cb := a[0], b[0]
		if foldCase {
			ca, cb = lower(ca), lower(cb)
		} else if lower(ca) == lower(cb) && ca != cb {
			// Lower case sorts before upper case.
			if ca > cb {
				return -1
			}
			return 1
		}
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		a, b = a[1:], b[1:]
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	}
	return 1
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
