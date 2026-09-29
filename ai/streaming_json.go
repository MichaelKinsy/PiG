package ai

import (
	"encoding/json"
	"strings"
)

// ParseStreamingJson incrementally reconstructs a streamed tool-argument object.
// Invalid or incomplete input returns the object prefix parsed so far, or an
// empty object when no object prefix is available.
func ParseStreamingJson(input string) JsonObject {
	return parseStreamingJsonObject(input)
}

// parseStreamingJsonObject mirrors Pi's total parseStreamingJson contract for
// streamed tool arguments: strict and repaired JSON first, partial parsing second,
// and an empty object when neither can produce an object.
func parseStreamingJsonObject(input string) JsonObject {
	if strings.TrimSpace(input) == "" {
		return JsonObject{}
	}
	candidates := []string{input, repairJSON(input)}
	for _, candidate := range candidates {
		var strict JsonObject
		if json.Unmarshal([]byte(candidate), &strict) == nil && strict != nil {
			return strict
		}
	}
	for _, candidate := range candidates {
		parser := partialJSONParser{input: candidate}
		if value, ok := parser.parseValue(); ok {
			if object, ok := value.(JsonObject); ok && object != nil {
				return object
			}
		}
	}
	return JsonObject{}
}

type partialJSONParser struct {
	input string
	index int
}

func (parser *partialJSONParser) parseValue() (any, bool) {
	parser.skipSpace()
	if parser.index >= len(parser.input) {
		return nil, false
	}
	switch parser.input[parser.index] {
	case '{':
		return parser.parseObject()
	case '[':
		return parser.parseArray()
	case '"':
		return parser.parseString()
	case 't':
		return parser.parseLiteral("true", true)
	case 'f':
		return parser.parseLiteral("false", false)
	case 'n':
		return parser.parseLiteral("null", nil)
	default:
		return parser.parseNumber()
	}
}

func (parser *partialJSONParser) parseObject() (any, bool) {
	parser.index++
	object := JsonObject{}
	for {
		parser.skipSpace()
		if parser.index >= len(parser.input) {
			return object, true
		}
		if parser.input[parser.index] == '}' {
			parser.index++
			return object, true
		}
		key, ok := parser.parseString()
		if !ok {
			return object, true
		}
		parser.skipSpace()
		if parser.index >= len(parser.input) || parser.input[parser.index] != ':' {
			return object, true
		}
		parser.index++
		value, ok := parser.parseValue()
		if !ok {
			return object, true
		}
		object[key.(string)] = value
		parser.skipSpace()
		if parser.index >= len(parser.input) {
			return object, true
		}
		if parser.input[parser.index] == ',' {
			parser.index++
			continue
		}
		if parser.input[parser.index] == '}' {
			parser.index++
			return object, true
		}
		return object, true
	}
}

func (parser *partialJSONParser) parseArray() (any, bool) {
	parser.index++
	values := []any{}
	for {
		parser.skipSpace()
		if parser.index >= len(parser.input) {
			return values, true
		}
		if parser.input[parser.index] == ']' {
			parser.index++
			return values, true
		}
		value, ok := parser.parseValue()
		if !ok {
			return values, true
		}
		values = append(values, value)
		parser.skipSpace()
		if parser.index >= len(parser.input) {
			return values, true
		}
		if parser.input[parser.index] == ',' {
			parser.index++
			continue
		}
		if parser.input[parser.index] == ']' {
			parser.index++
			return values, true
		}
		return values, true
	}
}

func (parser *partialJSONParser) parseString() (any, bool) {
	if parser.index >= len(parser.input) || parser.input[parser.index] != '"' {
		return nil, false
	}
	start := parser.index
	parser.index++
	escaped := false
	for parser.index < len(parser.input) {
		current := parser.input[parser.index]
		if current == '"' && !escaped {
			parser.index++
			var value string
			if json.Unmarshal([]byte(parser.input[start:parser.index]), &value) == nil {
				return value, true
			}
			return nil, false
		}
		if current == '\\' {
			escaped = !escaped
		} else {
			escaped = false
		}
		parser.index++
	}
	fragment := parser.input[start:parser.index]
	if escaped {
		fragment = strings.TrimSuffix(fragment, `\`)
	}
	var value string
	if json.Unmarshal([]byte(fragment+`"`), &value) == nil {
		return value, true
	}
	if slash := strings.LastIndex(fragment, `\`); slash > 0 {
		if json.Unmarshal([]byte(fragment[:slash]+`"`), &value) == nil {
			return value, true
		}
	}
	return nil, false
}

func (parser *partialJSONParser) parseLiteral(literal string, value any) (any, bool) {
	remaining := parser.input[parser.index:]
	length := len(literal)
	if len(remaining) < length {
		if strings.HasPrefix(literal, remaining) {
			parser.index = len(parser.input)
			return value, true
		}
		return nil, false
	}
	if remaining[:length] != literal {
		return nil, false
	}
	parser.index += length
	return value, true
}

func (parser *partialJSONParser) parseNumber() (any, bool) {
	start := parser.index
	for parser.index < len(parser.input) && !strings.ContainsRune(",]} \n\r\t", rune(parser.input[parser.index])) {
		parser.index++
	}
	fragment := parser.input[start:parser.index]
	var value any
	if json.Unmarshal([]byte(fragment), &value) == nil {
		return value, true
	}
	if exponent := strings.LastIndexAny(fragment, "eE"); exponent > 0 {
		if json.Unmarshal([]byte(fragment[:exponent]), &value) == nil {
			return value, true
		}
	}
	return nil, false
}

func (parser *partialJSONParser) skipSpace() {
	for parser.index < len(parser.input) && strings.ContainsRune(" \n\r\t", rune(parser.input[parser.index])) {
		parser.index++
	}
}
