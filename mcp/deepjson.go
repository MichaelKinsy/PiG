package mcp

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
)

// Pi parses every message with JSON.parse, which has no nesting limit that matters here: a server's schema or result nested 12,000 levels deep
// arrives intact. encoding/json refuses a document nested deeper than 10,000 levels. deepFallback gives json.Unmarshal a fallback that, when that is
// why the document failed, reads it with every container below deepElideLevel replaced by a placeholder object, then puts the original text back into the
// json.RawMessage fields it filled (and into the values that keep their own bytes, deepRestorer). Fields that decode into `any` or a typed value
// see the placeholder where a container nested deeper than deepElideLevel would be; the types of this package keep such text only as raw bytes.

// deepElideLevel is the container depth below which the fallback replaces text; far under encoding/json's limit.
const deepElideLevel = 32

// deepRestorer is implemented by a decoded value that keeps its own copy of the text it was read from.
type deepRestorer interface {
	restoreDeep(restore func(json.RawMessage) json.RawMessage)
}

var rawMessageType = reflect.TypeFor[json.RawMessage]()

// deepFallback takes the result of json.Unmarshal(data, v) and, when that failed, reads the text again past encoding/json's nesting limit. Callers
// write `deepFallback(json.Unmarshal(data, v), data, v)`, so the decode runs in the caller's frame; the wrapper is small enough to inline, and the
// fallback is out of line.
func deepFallback(err error, data []byte, v any) error {
	if err == nil {
		return nil
	}
	return unmarshalDeep(data, v, err)
}

// decodeShallow is json.Unmarshal for the destination of deepFallback. That destination is a typed pointer every caller took the address of
// (`&fields`, `&result`), so the decode builds the caller's own type and no tree of interface values; the `any` is only how a pointer of any
// type reaches this package-wide helper. It is a variable so that any-trap, which reads the static type of a json call's argument, sees the
// callers' typed destinations and not this pass-through.
var decodeShallow = json.Unmarshal

// unmarshalDeep is the fallback of deepFallback, kept out of line.
//
//go:noinline
func unmarshalDeep(data []byte, v any, err error) error {
	shallow, spans, key, ok := elideDeep(data)
	if !ok || len(spans) == 0 {
		return err
	}
	if decodeShallow(shallow, v) != nil {
		return err
	}
	prefix := []byte(`{"` + key + `":`)
	restore := func(raw json.RawMessage) json.RawMessage {
		if !bytes.Contains(raw, prefix) {
			return raw
		}
		var out []byte
		for {
			i := bytes.Index(raw, prefix)
			if i < 0 {
				return append(out, raw...)
			}
			rest := raw[i+len(prefix):]
			end := bytes.IndexByte(rest, '}')
			n, convErr := strconv.Atoi(string(rest[:max(end, 0)]))
			if end < 0 || convErr != nil || n < 0 || n >= len(spans) {
				return append(out, raw...)
			}
			out = append(out, raw[:i]...)
			out = append(out, spans[n]...)
			raw = rest[end+1:]
		}
	}
	restoreValue(reflect.ValueOf(v), restore)
	return nil
}

func restoreValue(v reflect.Value, restore func(json.RawMessage) json.RawMessage) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		restoreValue(v.Elem(), restore)
	case reflect.Struct:
		if v.CanAddr() {
			if r, ok := v.Addr().Interface().(deepRestorer); ok {
				r.restoreDeep(restore)
			}
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				restoreValue(v.Field(i), restore)
			}
		}
	case reflect.Slice:
		if v.Type() == rawMessageType {
			if v.CanSet() && v.Len() > 0 {
				v.Set(reflect.ValueOf(restore(v.Interface().(json.RawMessage))))
			}
			return
		}
		fallthrough
	case reflect.Array:
		if !holdsRaw(v.Type().Elem()) {
			return
		}
		for i := range v.Len() {
			restoreValue(v.Index(i), restore)
		}
	case reflect.Map:
		if !holdsRaw(v.Type().Elem()) {
			return
		}
		iter := v.MapRange()
		for iter.Next() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(iter.Value())
			restoreValue(value, restore)
			v.SetMapIndex(iter.Key(), value)
		}
	}
}

// holdsRaw reports whether a value of type t can hold raw bytes to restore.
func holdsRaw(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Struct, reflect.Slice, reflect.Array, reflect.Map:
		return true
	}
	return false
}

func (b *ContentBlock) restoreDeep(restore func(json.RawMessage) json.RawMessage) {
	b.raw = restore(b.raw)
}

// elideDeep copies data with each container nested deeper than deepElideLevel replaced by the object {"<key>":N}, N the index of the original
// text in spans, and checks that data is JSON as JSON.parse reads it. It reports false when data is not valid. It is iterative: it does not
// depend on the depth of data.
func elideDeep(data []byte) (out []byte, spans [][]byte, key string, ok bool) {
	for n := 0; ; n++ {
		key = `\u0000pigdeep` + strconv.Itoa(n)
		if !bytes.Contains(data, []byte(key)) {
			break
		}
	}
	var (
		stack      []byte // '{' or '['
		pos        int
		copied     int
		eliding    bool
		elideStart int
	)
	space := func() {
		for pos < len(data) && (data[pos] == ' ' || data[pos] == '\t' || data[pos] == '\r' || data[pos] == '\n') {
			pos++
		}
	}
	const (
		stValue = iota
		stAfter
		stKey
		stFirst // after an opening bracket
	)
	state := stValue
	closeContainer := func() {
		stack = stack[:len(stack)-1]
		pos++
		if eliding && len(stack) == deepElideLevel {
			out = append(out, data[copied:elideStart]...)
			out = append(out, `{"`+key+`":`+strconv.Itoa(len(spans))+`}`...)
			spans = append(spans, data[elideStart:pos])
			copied, eliding = pos, false
		}
		state = stAfter
	}
	for {
		space()
		if pos >= len(data) {
			if len(stack) != 0 || state != stAfter {
				return nil, nil, "", false
			}
			return append(out, data[copied:]...), spans, key, true
		}
		c := data[pos]
		switch state {
		case stFirst:
			top := stack[len(stack)-1]
			if top == '{' && c == '}' || top == '[' && c == ']' {
				closeContainer()
				continue
			}
			if top == '{' {
				state = stKey
			} else {
				state = stValue
			}
		case stAfter:
			if len(stack) == 0 {
				return nil, nil, "", false
			}
			top := stack[len(stack)-1]
			switch {
			case c == ',':
				pos++
				if top == '{' {
					state = stKey
				} else {
					state = stValue
				}
			case top == '{' && c == '}', top == '[' && c == ']':
				closeContainer()
			default:
				return nil, nil, "", false
			}
			continue
		}
		switch state {
		case stKey:
			if c != '"' {
				return nil, nil, "", false
			}
			if pos = skipJSONString(data, pos); pos < 0 {
				return nil, nil, "", false
			}
			space()
			if pos >= len(data) || data[pos] != ':' {
				return nil, nil, "", false
			}
			pos++
			state = stValue
		case stValue:
			switch {
			case c == '{' || c == '[':
				stack = append(stack, c)
				if len(stack) > deepElideLevel && !eliding {
					eliding, elideStart = true, pos
				}
				pos++
				state = stFirst
			case c == '"':
				if pos = skipJSONString(data, pos); pos < 0 {
					return nil, nil, "", false
				}
				state = stAfter
			case c == '-' || '0' <= c && c <= '9':
				if pos = skipJSONNumber(data, pos); pos < 0 {
					return nil, nil, "", false
				}
				state = stAfter
			default:
				word := ""
				switch c {
				case 't':
					word = "true"
				case 'f':
					word = "false"
				case 'n':
					word = "null"
				}
				if word == "" || !bytes.HasPrefix(data[pos:], []byte(word)) {
					return nil, nil, "", false
				}
				pos += len(word)
				state = stAfter
			}
		}
	}
}

// skipJSONString returns the position after the string at pos, or -1 when it is malformed.
func skipJSONString(data []byte, pos int) int {
	for pos++; pos < len(data); pos++ {
		switch c := data[pos]; {
		case c == '"':
			return pos + 1
		case c < 0x20:
			return -1
		case c == '\\':
			pos++
			if pos >= len(data) {
				return -1
			}
			switch data[pos] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				for range 4 {
					pos++
					if pos >= len(data) || !isHexDigit(data[pos]) {
						return -1
					}
				}
			default:
				return -1
			}
		}
	}
	return -1
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// skipJSONNumber returns the position after the number at pos, or -1 when it is malformed.
func skipJSONNumber(data []byte, pos int) int {
	digits := func() bool {
		begin := pos
		for pos < len(data) && '0' <= data[pos] && data[pos] <= '9' {
			pos++
		}
		return pos > begin
	}
	if data[pos] == '-' {
		pos++
	}
	switch {
	case pos < len(data) && data[pos] == '0':
		pos++
	case pos < len(data) && '1' <= data[pos] && data[pos] <= '9':
		digits()
	default:
		return -1
	}
	if pos < len(data) && data[pos] == '.' {
		pos++
		if !digits() {
			return -1
		}
	}
	if pos < len(data) && (data[pos] == 'e' || data[pos] == 'E') {
		pos++
		if pos < len(data) && (data[pos] == '+' || data[pos] == '-') {
			pos++
		}
		if !digits() {
			return -1
		}
	}
	return pos
}
