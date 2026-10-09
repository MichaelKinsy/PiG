package ai

import (
	"encoding/json"
	"maps"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
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
	arguments, _ := parseStreamingJsonArguments(input)
	return arguments
}

// parseStreamingJsonArguments is parseStreamingJsonObject with the member order of the parsed text. The order is nil when every object of the result is in sorted order.
func parseStreamingJsonArguments(input string) (JsonObject, schemaObjectOrder) {
	if strings.TrimSpace(input) == "" {
		return JsonObject{}, nil
	}
	candidates := []string{input, repairJSON(input)}
	for _, candidate := range candidates {
		var strict JsonObject
		if json.Unmarshal([]byte(candidate), &strict) != nil {
			strict = nil
			if json.Valid([]byte(candidate)) {
				// Valid JSON that encoding/json refuses is a number out of float64 range, which JSON.parse reads as an infinity.
				if value, ok := jsonParseValue(candidate); ok {
					if object, isObject := value.(map[string]any); isObject {
						strict = JsonObject(nonFiniteToNull(object).(map[string]any))
					}
				}
			}
		}
		if strict != nil {
			strict = JsonObject(nonFiniteToNull(map[string]any(strict)).(map[string]any))
			order, err := readSchemaObjectOrder([]byte(candidate))
			if err != nil {
				order = nil
			}
			return strict, order
		}
	}
	// parseStreamingJson: partialParse(text), and only when it throws partialParse(repairJson(text)). Either result is returned as parsed; a result that
	// is not an object cannot be a tool-argument object, so it is the empty object.
	for _, candidate := range candidates {
		parser := partialJSONParser{input: jsstring.Trim(candidate)}
		if parser.input == "" {
			continue
		}
		if value, ok := parser.parseAny(""); ok {
			if object, ok := value.(map[string]any); ok && object != nil {
				return JsonObject(nonFiniteToNull(object).(map[string]any)), parser.order
			}
			return JsonObject{}, nil
		}
	}
	return JsonObject{}, nil
}

// partialJSONParser is partial-json 0.1.7's _parseJSON (the parser behind Pi's parseStreamingJson) with every Allow flag set. It reads text by
// UTF-16-agnostic byte index: every delimiter it tests is ASCII, and substring bounds come only from indexes it has already walked.
type partialJSONParser struct {
	input string
	index int
	order schemaObjectOrder
	// readBeyond records that a result depended on text after the value it was read from: parseNum's lastIndexOf("e") looks at the whole input.
	readBeyond bool
	// strings, when set, resumes the decoding of an unterminated string across calls on a growing input.
	strings *resumableString
}

// nonFiniteToNull replaces the Infinity, -Infinity and NaN that partial-json produces (and JSON.parse produces for 1e400) with null, and -0 with 0.
// Pi keeps either number in memory but every serialization of it, JSON.stringify, writes null or 0 (as String writes "0" for -0). Go's encoding/json
// refuses to serialize a non-finite float at all, so a tool-argument object that held one could not be sent or stored, and it writes -0 as "-0".
func nonFiniteToNull(value any) any {
	switch typed := value.(type) {
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return nil
		}
		if typed == 0 {
			return 0.0
		}
	case []any:
		for i, item := range typed {
			typed[i] = nonFiniteToNull(item)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = nonFiniteToNull(item)
		}
	}
	return value
}

// jsSubstring is String.prototype.substring: bounds are clamped to the string and swapped when start is past end.
func jsSubstring(text string, start, end int) string {
	start, end = min(max(start, 0), len(text)), min(max(end, 0), len(text))
	if start > end {
		start, end = end, start
	}
	return text[start:end]
}

// jsonParseValue is JSON.parse on text: JSON grammar, surrounding JSON whitespace allowed, a number out of range read as an infinity.
func jsonParseValue(text string) (any, bool) {
	if !json.Valid([]byte(text)) {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	return numbersToFloats(value), true
}

func numbersToFloats(value any) any {
	switch typed := value.(type) {
	case json.Number:
		number, _ := strconv.ParseFloat(typed.String(), 64) // an out-of-range literal parses to the infinity JSON.parse gives
		return number
	case []any:
		for i, item := range typed {
			typed[i] = numbersToFloats(item)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = numbersToFloats(item)
		}
	}
	return value
}

func (parser *partialJSONParser) charAt(index int) (byte, bool) {
	if index < 0 || index >= len(parser.input) {
		return 0, false
	}
	return parser.input[index], true
}

// parseAny is parseAny: false is a thrown PartialJSON or MalformedJSON. path keys the member order the parser records; only a container reads it.
func (parser *partialJSONParser) parseAny(path string) (any, bool) {
	parser.skipBlank()
	text := parser.input
	if parser.index >= len(text) {
		return nil, false
	}
	switch text[parser.index] {
	case '"':
		return parser.parseStr()
	case '{':
		return parser.parseObj(path)
	case '[':
		return parser.parseArr(path)
	}
	rest := text[parser.index:]
	literal := func(name string, value any) (any, bool) {
		if strings.HasPrefix(rest, name) || (len(rest) < len(name) && strings.HasPrefix(name, rest)) {
			parser.index += len(name)
			return value, true
		}
		return nil, false
	}
	if value, ok := literal("null", nil); ok {
		return value, true
	}
	if value, ok := literal("true", true); ok {
		return value, true
	}
	if value, ok := literal("false", false); ok {
		return value, true
	}
	if value, ok := literal("Infinity", math.Inf(1)); ok {
		return value, true
	}
	const negativeInfinity = "-Infinity"
	if strings.HasPrefix(rest, negativeInfinity) || (len(rest) > 1 && len(rest) < len(negativeInfinity) && strings.HasPrefix(negativeInfinity, rest)) {
		parser.index += len(negativeInfinity)
		return math.Inf(-1), true
	}
	if value, ok := literal("NaN", math.NaN()); ok {
		return value, true
	}
	return parser.parseNum()
}

func (parser *partialJSONParser) parseStr() (any, bool) {
	if parser.strings != nil && parser.strings.start == parser.index {
		return parser.strings.resume(parser, parser.index)
	}
	return parser.parseStrText()
}

// parseStrText reads the string that begins at parser.index from its whole text.
func (parser *partialJSONParser) parseStrText() (any, bool) {
	text := parser.input
	start := parser.index
	escape := false
	parser.index++
	for parser.index < len(text) && (text[parser.index] != '"' || (escape && text[parser.index-1] == '\\')) {
		escape = text[parser.index] == '\\' && !escape
		parser.index++
	}
	if character, ok := parser.charAt(parser.index); ok && character == '"' {
		parser.index++
		value, ok := jsonParseValue(jsSubstring(text, start, parser.index-boolToInt(escape)))
		return value, ok
	}
	if parser.strings != nil && parser.strings.poisoned != start && text[start] == '"' {
		// An unterminated string is the one a growing input extends next. Like parseStr, the decoder takes the first byte for the opening quote; it resumes only a real one.
		parser.strings.begin(start)
		return parser.strings.resume(parser, start)
	}
	return parser.unterminatedStr(start, escape)
}

// unterminatedStr is the tail of parseStr for a string that runs to the end of the input; escape is whether the input ends inside an escape.
func (parser *partialJSONParser) unterminatedStr(start int, escape bool) (any, bool) {
	text := parser.input
	if value, ok := jsonParseValue(jsSubstring(text, start, parser.index-boolToInt(escape)) + `"`); ok {
		return value, true
	}
	return jsonParseValue(jsSubstring(text, start, strings.LastIndex(text, `\`)) + `"`)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (parser *partialJSONParser) parseObj(path string) (any, bool) {
	parser.index++
	parser.skipBlank()
	return parser.parseMembers(path, map[string]any{}, nil, nil)
}

// parseMembers reads the members of the object at path from parser.index, adding them to object and keys. onMember, when set, runs after each member that a ',' follows, unless a value read beyond its own text: nothing appended to the input changes that member or the parser state at that point.
func (parser *partialJSONParser) parseMembers(path string, object map[string]any, keys []string, onMember func(object map[string]any, keys []string)) (any, bool) {
	defer func() {
		if enumerated := unsortedKeyOrder(keys); enumerated != nil {
			if parser.order == nil {
				parser.order = schemaObjectOrder{}
			}
			parser.order[path] = enumerated
		}
	}()
	for {
		if character, ok := parser.charAt(parser.index); ok && character == '}' {
			break
		}
		parser.skipBlank()
		if parser.index >= len(parser.input) {
			return object, true
		}
		key, ok := parser.parseStr()
		if !ok {
			return object, true
		}
		parser.skipBlank()
		parser.index++ // the colon, whatever it is
		name, _ := key.(string)
		_, repeated := object[name]
		var replaced schemaObjectOrder
		if repeated && parser.order != nil {
			// obj[key] = value keeps the first position and the new value's member order replaces the old one's.
			replaced = parser.order.dropSubtree(schemaPath(path, name))
		}
		value, ok := parser.parseAny(parser.childPath(path, name))
		if !ok {
			// The value threw: the object is returned as it is, and the caller reads on from here.
			maps.Copy(parser.order, replaced)
			return object, true
		}
		if name != "__proto__" { // obj["__proto__"] = value sets the prototype, so the member is lost
			if !repeated {
				keys = append(keys, name)
			}
			object[name] = value
		}
		parser.skipBlank()
		if character, ok := parser.charAt(parser.index); ok && character == ',' {
			parser.index++
			if onMember != nil && !parser.readBeyond {
				onMember(object, keys)
			}
		}
	}
	parser.index++
	return object, true
}

func (parser *partialJSONParser) parseArr(path string) (any, bool) {
	parser.index++
	values := []any{}
	for {
		if character, ok := parser.charAt(parser.index); ok && character == ']' {
			break
		}
		value, ok := parser.parseAny(parser.childPath(path, strconv.Itoa(len(values))))
		if !ok {
			return values, true
		}
		values = append(values, value)
		parser.skipBlank()
		if character, ok := parser.charAt(parser.index); ok && character == ',' {
			parser.index++
		}
	}
	parser.index++
	return values, true
}

func (parser *partialJSONParser) parseNum() (any, bool) {
	text := parser.input
	if parser.index == 0 {
		if text == "-" {
			return nil, false
		}
		if value, ok := jsonParseValue(text); ok {
			return value, true
		}
		parser.readBeyond = true
		return jsonParseValue(jsSubstring(text, 0, strings.LastIndex(text, "e")))
	}
	start := parser.index
	if character, ok := parser.charAt(parser.index); ok && character == '-' {
		parser.index++
	}
	for parser.index < len(text) && !strings.ContainsRune(",]}", rune(text[parser.index])) {
		parser.index++
	}
	literal := jsSubstring(text, start, parser.index)
	if value, ok := jsonParseValue(literal); ok {
		return value, true
	}
	if literal == "-" {
		return nil, false
	}
	parser.readBeyond = true
	return jsonParseValue(jsSubstring(text, start, strings.LastIndex(text, "e")))
}

func (parser *partialJSONParser) skipBlank() {
	for parser.index < len(parser.input) && strings.ContainsRune(" \n\r\t", rune(parser.input[parser.index])) {
		parser.index++
	}
}

// childPath is the path of the member the parser is about to read, or "" when that member is not a container and never reads it.
func (parser *partialJSONParser) childPath(parent, segment string) string {
	parser.skipBlank()
	if parser.index < len(parser.input) && (parser.input[parser.index] == '{' || parser.input[parser.index] == '[') {
		return schemaPath(parent, segment)
	}
	return ""
}
