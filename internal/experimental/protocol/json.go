package protocol

// The Go Chord runtime carries JSON text; Pi's protocol codec carries JavaScript values. This boundary retains Object order and UTF-16 string identity while translating those representations.

import (
	"encoding/json"
	"math"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	textutil "github.com/MichaelKinsy/PiG/internal/text"
)

// FromJSON decodes a strict JSON payload into the value representation used by the CBOR codec. It preserves lone UTF-16 units as WTF-8 so EncodeCbor rejects them instead of silently replacing them. An absent payload is not null and is rejected.
func FromJSON(data json.RawMessage) (any, error) {
	if !json.Valid(data) {
		return nil, invalidProtocolJSON()
	}
	reader := protocolJSONReader{data: data}
	value, err := reader.value(0)
	if err != nil {
		return nil, err
	}
	return value, nil
}

// ToJSON serializes a protocol JSON value for Go service APIs. It rejects undefined, binary, cyclic, and non-finite values instead of omitting or replacing them. Omitted wire results must be handled by the caller before this conversion.
func ToJSON(value any) (json.RawMessage, error) {
	if !isProtocolJSON(value, map[uintptr]bool{}, 0) {
		return nil, invalidProtocolJSON()
	}
	return appendProtocolJSON(nil, value)
}

func invalidProtocolJSON() error {
	return &ProtocolValidationError{Message: "Invalid JSON protocol value"}
}

type protocolJSONReader struct {
	data   []byte
	offset int
}

func (reader *protocolJSONReader) space() {
	for reader.offset < len(reader.data) {
		switch reader.data[reader.offset] {
		case ' ', '\t', '\r', '\n':
			reader.offset++
		default:
			return
		}
	}
}
func (reader *protocolJSONReader) value(depth int) (any, error) {
	if depth > 512 {
		return nil, invalidProtocolJSON()
	}
	reader.space()
	switch reader.data[reader.offset] {
	case 'n':
		reader.offset += 4
		return nil, nil
	case 't':
		reader.offset += 4
		return true, nil
	case 'f':
		reader.offset += 5
		return false, nil
	case '"':
		return reader.string(), nil
	case '[':
		reader.offset++
		reader.space()
		array := []any{}
		if reader.data[reader.offset] == ']' {
			reader.offset++
			return array, nil
		}
		for {
			value, err := reader.value(depth + 1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
			reader.space()
			separator := reader.data[reader.offset]
			reader.offset++
			if separator == ']' {
				return array, nil
			}
		}
	case '{':
		reader.offset++
		reader.space()
		object := Object{}
		positions := map[string]int{}
		if reader.data[reader.offset] == '}' {
			reader.offset++
			return object, nil
		}
		for {
			reader.space()
			key := reader.string()
			reader.space()
			reader.offset++
			value, err := reader.value(depth + 1)
			if err != nil {
				return nil, err
			}
			if index, present := positions[key]; present {
				object[index].Value = value
			} else {
				positions[key] = len(object)
				object = append(object, Property{key, value})
			}
			reader.space()
			separator := reader.data[reader.offset]
			reader.offset++
			if separator == '}' {
				return normalizedCborObject(object)
			}
		}
	default:
		start := reader.offset
		for reader.offset < len(reader.data) {
			char := reader.data[reader.offset]
			if char == ',' || char == ']' || char == '}' || char == ' ' || char == '\t' || char == '\r' || char == '\n' {
				break
			}
			reader.offset++
		}
		value, err := strconv.ParseFloat(string(reader.data[start:reader.offset]), 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return nil, invalidProtocolJSON()
		}
		return value, nil
	}
}

func (reader *protocolJSONReader) string() string {
	reader.offset++
	result := []byte{}
	for reader.data[reader.offset] != '"' {
		char := reader.data[reader.offset]
		reader.offset++
		if char != '\\' {
			if char < utf8.RuneSelf {
				result = append(result, char)
				continue
			}
			reader.offset--
			value, size := utf8.DecodeRune(reader.data[reader.offset:])
			result = utf8.AppendRune(result, value)
			reader.offset += size
			continue
		}
		escaped := reader.data[reader.offset]
		reader.offset++
		switch escaped {
		case '"', '\\', '/':
			result = append(result, escaped)
		case 'b':
			result = append(result, '\b')
		case 'f':
			result = append(result, '\f')
		case 'n':
			result = append(result, '\n')
		case 'r':
			result = append(result, '\r')
		case 't':
			result = append(result, '\t')
		case 'u':
			unit := reader.hexUnit()
			if unit >= 0xd800 && unit <= 0xdbff && reader.offset+6 <= len(reader.data) && reader.data[reader.offset] == '\\' && reader.data[reader.offset+1] == 'u' {
				low, _ := strconv.ParseUint(string(reader.data[reader.offset+2:reader.offset+6]), 16, 16)
				if low >= 0xdc00 && low <= 0xdfff {
					reader.offset += 6
					result = utf8.AppendRune(result, utf16.DecodeRune(rune(unit), rune(low)))
					continue
				}
			}
			if unit >= 0xd800 && unit <= 0xdfff {
				result = append(result, 0xe0|byte(unit>>12), 0x80|byte(unit>>6&0x3f), 0x80|byte(unit&0x3f))
			} else {
				result = utf8.AppendRune(result, rune(unit))
			}
		}
	}
	reader.offset++
	return string(result)
}
func (reader *protocolJSONReader) hexUnit() uint16 {
	unit, _ := strconv.ParseUint(string(reader.data[reader.offset:reader.offset+4]), 16, 16)
	reader.offset += 4
	return uint16(unit)
}

func appendProtocolJSON(out []byte, value any) ([]byte, error) {
	if number, ok := cborNumber(value); ok {
		if number == 0 {
			return append(out, '0'), nil
		}
		encoded, err := json.Marshal(number)
		if err != nil {
			return nil, err
		}
		return append(out, encoded...), nil
	}
	switch value := value.(type) {
	case nil:
		return append(out, "null"...), nil
	case bool:
		if value {
			return append(out, "true"...), nil
		}
		return append(out, "false"...), nil
	case string:
		return append(out, textutil.QuoteUTF16(textutil.UTF16Units(value))...), nil
	case []any:
		out = append(out, '[')
		for i, item := range value {
			if i > 0 {
				out = append(out, ',')
			}
			var err error
			out, err = appendProtocolJSON(out, item)
			if err != nil {
				return nil, err
			}
		}
		return append(out, ']'), nil
	case Object:
		entries, err := normalizedCborObject(value)
		if err != nil {
			return nil, err
		}
		out = append(out, '{')
		for i, entry := range entries {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, textutil.QuoteUTF16(textutil.UTF16Units(entry.Key.(string)))...)
			out = append(out, ':')
			out, err = appendProtocolJSON(out, entry.Value)
			if err != nil {
				return nil, err
			}
		}
		return append(out, '}'), nil
	default:
		return nil, invalidProtocolJSON()
	}
}
