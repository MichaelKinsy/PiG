package chordjson

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"unicode/utf8"
)

var objectType = reflect.TypeFor[Object]()

// Decode parses JSON text into a strict JSON value whose objects are *Object, keeping each object's keys in document order, as JSON.parse does. A repeated key keeps its first position and takes its last value. Invalid text returns encoding/json's error.
func Decode(data []byte) (Value, error) {
	return DecodeUnquoting(data, nil)
}

// DecodeUnquoting is Decode with escaped string text decoded by unquote instead of encoding/json, for a caller whose
// strings follow another mapping, such as WTF-8 for lone UTF-16 units. A nil unquote is encoding/json's.
func DecodeUnquoting(data []byte, unquote func(raw []byte, text *string) error) (Value, error) {
	if unquote == nil {
		unquote = unquoteJSON
	}
	parser := decoder{data: data, unquote: unquote}
	parser.space()
	value, ok := parser.value(0)
	if ok {
		parser.space()
		ok = parser.at == len(parser.data)
	}
	if ok {
		return value, nil
	}
	var reference any
	if err := json.Unmarshal(data, &reference); err != nil {
		return nil, err
	}
	return nil, errors.New("chordjson: invalid JSON")
}

// maxDepth bounds nesting as encoding/json does.
const maxDepth = 10000

type decoder struct {
	data    []byte
	at      int
	unquote func(raw []byte, text *string) error
}

func unquoteJSON(raw []byte, text *string) error { return json.Unmarshal(raw, text) }

func (parser *decoder) space() {
	for parser.at < len(parser.data) {
		switch parser.data[parser.at] {
		case ' ', '\t', '\n', '\r':
			parser.at++
		default:
			return
		}
	}
}

func (parser *decoder) literal(text string) bool {
	if len(parser.data)-parser.at < len(text) || string(parser.data[parser.at:parser.at+len(text)]) != text {
		return false
	}
	parser.at += len(text)
	return true
}

func (parser *decoder) value(depth int) (any, bool) {
	if parser.at >= len(parser.data) || depth > maxDepth {
		return nil, false
	}
	switch character := parser.data[parser.at]; {
	case character == '{':
		return parser.object(depth + 1)
	case character == '[':
		return parser.array(depth + 1)
	case character == '"':
		return parser.string()
	case character == 't':
		return true, parser.literal("true")
	case character == 'f':
		return false, parser.literal("false")
	case character == 'n':
		return nil, parser.literal("null")
	case character == '-' || character >= '0' && character <= '9':
		return parser.number()
	}
	return nil, false
}

func (parser *decoder) object(depth int) (any, bool) {
	parser.at++
	object := &Object{}
	parser.space()
	if parser.at < len(parser.data) && parser.data[parser.at] == '}' {
		parser.at++
		return object, true
	}
	for {
		parser.space()
		if parser.at >= len(parser.data) || parser.data[parser.at] != '"' {
			return nil, false
		}
		key, ok := parser.string()
		if !ok {
			return nil, false
		}
		parser.space()
		if parser.at >= len(parser.data) || parser.data[parser.at] != ':' {
			return nil, false
		}
		parser.at++
		parser.space()
		value, ok := parser.value(depth)
		if !ok {
			return nil, false
		}
		object.Set(key.(string), value)
		parser.space()
		if parser.at >= len(parser.data) {
			return nil, false
		}
		switch parser.data[parser.at] {
		case ',':
			parser.at++
		case '}':
			parser.at++
			return object, true
		default:
			return nil, false
		}
	}
}

func (parser *decoder) array(depth int) (any, bool) {
	parser.at++
	items := []any{}
	parser.space()
	if parser.at < len(parser.data) && parser.data[parser.at] == ']' {
		parser.at++
		return items, true
	}
	for {
		parser.space()
		item, ok := parser.value(depth)
		if !ok {
			return nil, false
		}
		items = append(items, item)
		parser.space()
		if parser.at >= len(parser.data) {
			return nil, false
		}
		switch parser.data[parser.at] {
		case ',':
			parser.at++
		case ']':
			parser.at++
			return items, true
		default:
			return nil, false
		}
	}
}

// string decodes a quoted string. Text without escapes or invalid UTF-8 is copied directly; other text goes through encoding/json, which replaces invalid UTF-8 as before.
func (parser *decoder) string() (any, bool) {
	start := parser.at
	parser.at++
	plain := true
	for parser.at < len(parser.data) {
		switch character := parser.data[parser.at]; {
		case character == '"':
			parser.at++
			raw := parser.data[start:parser.at]
			if plain && utf8.Valid(raw) {
				return string(raw[1 : len(raw)-1]), true
			}
			var text string
			if parser.unquote(raw, &text) != nil {
				return nil, false
			}
			return text, true
		case character == '\\':
			plain = false
			parser.at += 2
		case character < 0x20:
			return nil, false
		default:
			parser.at++
		}
	}
	return nil, false
}

func (parser *decoder) digits() int {
	start := parser.at
	for parser.at < len(parser.data) && parser.data[parser.at] >= '0' && parser.data[parser.at] <= '9' {
		parser.at++
	}
	return parser.at - start
}

func (parser *decoder) number() (any, bool) {
	start := parser.at
	if parser.data[parser.at] == '-' {
		parser.at++
	}
	if parser.at < len(parser.data) && parser.data[parser.at] == '0' {
		parser.at++
	} else if parser.digits() == 0 {
		return nil, false
	}
	if parser.at < len(parser.data) && parser.data[parser.at] == '.' {
		parser.at++
		if parser.digits() == 0 {
			return nil, false
		}
	}
	if parser.at < len(parser.data) && (parser.data[parser.at] == 'e' || parser.data[parser.at] == 'E') {
		parser.at++
		if parser.at < len(parser.data) && (parser.data[parser.at] == '+' || parser.data[parser.at] == '-') {
			parser.at++
		}
		if parser.digits() == 0 {
			return nil, false
		}
	}
	number, err := strconv.ParseFloat(string(parser.data[start:parser.at]), 64)
	if err != nil {
		return nil, false
	}
	return number, true
}

func jsonKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	}
	return "object"
}
