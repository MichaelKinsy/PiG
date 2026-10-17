package sdk

import (
	"bytes"
	"errors"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// jsObject is a JSON object as JSON.parse builds it: members in insertion order, a duplicate key keeping the position of its first occurrence and the
// value of its last. A Go map has no order, so a helper that prints an object's members (Pi's serializeConversation prints a tool call's arguments in
// property order) reads the raw JSON through this type.
type jsObject struct {
	keys   []string
	values map[string]any
}

// parseOrderedJSON is JSON.parse for a value that is then read in property order: an object is a *jsObject, an array []any, a number float64 (a number
// outside the double range is an infinity, as JSON.parse reads it), and a string, bool or nil as is. An unpaired surrogate escape is U+FFFD (language
// mechanic: a Go string holds no unpaired surrogate).
func parseOrderedJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := readOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return value, nil
}

func readOrderedValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		if token == '{' {
			object := &jsObject{values: map[string]any{}}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyToken.(string)
				value, err := readOrderedValue(dec)
				if err != nil {
					return nil, err
				}
				if _, duplicate := object.values[key]; !duplicate {
					object.keys = append(object.keys, key)
				}
				object.values[key] = value
			}
			_, err := dec.Token()
			return object, err
		}
		array := []any{}
		for dec.More() {
			value, err := readOrderedValue(dec)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := dec.Token()
		return array, err
	case json.Number:
		number, _ := strconv.ParseFloat(string(token), 64)
		return number, nil
	}
	return token, nil
}

// orderedKeys is the order of an object's own string keys that Object.keys, Object.entries and JSON.stringify use: the array-index keys ("0", "1", ... up
// to 2^32 - 2) ascending, then every other key in insertion order.
func (object *jsObject) orderedKeys() []string {
	var indexes, others []string
	for _, key := range object.keys {
		if isArrayIndex(key) {
			indexes = append(indexes, key)
		} else {
			others = append(others, key)
		}
	}
	slices.SortFunc(indexes, func(a, b string) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		return strings.Compare(a, b)
	})
	return append(indexes, others...)
}

func isArrayIndex(key string) bool {
	if key == "0" {
		return true
	}
	if key == "" || key[0] < '1' || key[0] > '9' || len(key) > 10 {
		return false
	}
	for i := 1; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	number, _ := strconv.ParseUint(key, 10, 64)
	return number <= math.MaxUint32-1
}

// jsonStringify is JSON.stringify for a value parseOrderedJSON returned.
func jsonStringify(value any) string {
	var out strings.Builder
	writeJSON(&out, value)
	return out.String()
}

func writeJSON(out *strings.Builder, value any) {
	switch value := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(value))
	case float64:
		out.WriteString(jsNumberString(value))
	case string:
		writeJSONString(out, value)
	case []any:
		out.WriteByte('[')
		for i, element := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			writeJSON(out, element)
		}
		out.WriteByte(']')
	case *jsObject:
		out.WriteByte('{')
		for i, key := range value.orderedKeys() {
			if i > 0 {
				out.WriteByte(',')
			}
			writeJSONString(out, key)
			out.WriteByte(':')
			writeJSON(out, value.values[key])
		}
		out.WriteByte('}')
	}
}

// writeJSONString is QuoteJSONString: a quote, a backslash and the control characters are escaped (\b \f \n \r \t short, the rest as lowercase \u00xx) and
// every other character is written as it is, except an unpaired surrogate (WTF-8 in a Go string), which is the lowercase \uXXXX escape.
func writeJSONString(out *strings.Builder, text string) {
	out.WriteByte('"')
	units := jsUnits(text)
	for i := 0; i < len(units); i++ {
		r := rune(units[i])
		if utf16.IsSurrogate(r) {
			if r < 0xDC00 && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
				out.WriteRune(utf16.DecodeRune(r, rune(units[i+1])))
				i++
			} else {
				out.WriteString(`\u` + strconv.FormatUint(uint64(r), 16))
			}
			continue
		}
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\b':
			out.WriteString(`\b`)
		case r == '\f':
			out.WriteString(`\f`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20:
			out.WriteString(`\u00`)
			out.WriteByte("0123456789abcdef"[r>>4])
			out.WriteByte("0123456789abcdef"[r&0xf])
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
}
