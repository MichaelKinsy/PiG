package ai

import (
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// streamingArgumentsParser is parseStreamingJsonArguments for one streamed tool call whose argument text only grows. Each call returns what parseStreamingJsonArguments returns for the same text, but reads the new bytes instead of the whole text again: the members a ',' has followed are kept, and an unterminated string keeps its decoded prefix.
type streamingArgumentsParser struct {
	input string
	// mode is the state of the scan of input: unset until the first call, resumable while the text is an unclosed JSON object, and whole once the text needs the whole-text parser.
	mode streamingArgumentsMode

	scanned  int
	depth    int
	inString bool
	escaped  bool

	// resumeAt is the index of the trimmed text just past the last committed ','; object, keys and order are the committed members. entered is false until the first member is committed.
	resumeAt int
	entered  bool
	object   map[string]any
	keys     []string
	order    schemaObjectOrder

	strings resumableString
}

type streamingArgumentsMode uint8

const (
	streamingArgumentsUnset streamingArgumentsMode = iota
	streamingArgumentsResumable
	streamingArgumentsWhole
)

// parse is parseStreamingJsonArguments(input). input is normally the text of the previous call with more bytes appended; any other text restarts the parser.
func (parser *streamingArgumentsParser) parse(input string) (JsonObject, schemaObjectOrder) {
	if !growsFrom(parser.input, input) {
		*parser = streamingArgumentsParser{}
	}
	parser.input = input
	if parser.mode == streamingArgumentsUnset {
		parser.start()
	}
	if parser.mode == streamingArgumentsResumable {
		parser.scan()
	}
	if parser.mode != streamingArgumentsResumable {
		return parseStreamingJsonArguments(input)
	}
	return parser.resume()
}

// growsFrom reports whether next is previous with bytes appended. Strings that share a buffer compare equal without reading it.
func growsFrom(previous, next string) bool {
	return strings.HasPrefix(next, previous)
}

// start finds the opening '{'. Only an object that is still open reaches the partial parser first: a strict parse of an unclosed text fails, and so does a strict parse of its repairJSON rewrite, which keeps every string boundary and so every nesting.
func (parser *streamingArgumentsParser) start() {
	parser.mode = streamingArgumentsWhole
	for index := 0; index < len(parser.input); index++ {
		switch parser.input[index] {
		case ' ', '\n', '\r', '\t':
		case '{':
			parser.mode = streamingArgumentsResumable
			parser.scanned = index + 1
			parser.depth = 1
			parser.resumeAt = 1
			parser.object = map[string]any{}
			parser.strings.start, parser.strings.poisoned = -1, -1
			return
		default:
			return
		}
	}
}

// scan advances the nesting depth over the new bytes. Once the object closes, or a bracket mismatch could make a text complete, the whole-text parser decides.
func (parser *streamingArgumentsParser) scan() {
	for ; parser.scanned < len(parser.input); parser.scanned++ {
		char := parser.input[parser.scanned]
		if parser.inString {
			switch {
			case parser.escaped:
				parser.escaped = false
			case char == '\\':
				parser.escaped = true
			case char == '"':
				parser.inString = false
			}
			continue
		}
		switch char {
		case '"':
			parser.inString = true
		case '{', '[':
			parser.depth++
		case '}', ']':
			parser.depth--
			if parser.depth <= 0 {
				parser.mode = streamingArgumentsWhole
				return
			}
		}
	}
}

// resume parses the top-level object from the last committed member, as parseStreamingJsonArguments does for a text that is still an open object: partialParse of the JS-trimmed text.
func (parser *streamingArgumentsParser) resume() (JsonObject, schemaObjectOrder) {
	partial := partialJSONParser{input: jsstring.Trim(parser.input), index: parser.resumeAt, order: maps.Clone(parser.order), strings: &parser.strings}
	if !parser.entered {
		// parseObj skips the blanks after the opening brace once, before its first member.
		partial.skipBlank()
	}
	parser.strings.clamp(len(partial.input))
	object := maps.Clone(parser.object)
	keys := parser.keys[:len(parser.keys):len(parser.keys)]
	value, _ := partial.parseMembers("", object, keys, func(object map[string]any, keys []string) {
		parser.entered = true
		parser.resumeAt = partial.index
		parser.object = maps.Clone(object)
		parser.keys = keys[:len(keys):len(keys)]
		parser.order = maps.Clone(partial.order)
	})
	return JsonObject(nonFiniteToNull(value.(map[string]any)).(map[string]any)), partial.order
}

// resumableString decodes one unterminated JSON string across calls on a growing input. Its decoded prefix ends at a position where nothing after it can change how the bytes before it decode.
type resumableString struct {
	// start is the input index of the opening quote of the string being decoded, or -1.
	start int
	// safeEnd is the input index up to which the string is in decoded.
	safeEnd int
	decoded strings.Builder
	// poisoned is the start of a string that holds a byte the whole-text decoder must judge, or -1.
	poisoned int
}

func (str *resumableString) begin(start int) {
	str.start, str.safeEnd = start, start+1
	str.decoded.Reset()
}

// clamp forgets a string that the trimmed input no longer reaches.
func (str *resumableString) clamp(length int) {
	if str.start >= length || str.safeEnd > length {
		str.start = -1
	}
}

// resume decodes the string at start as parseStrText does, reading only the bytes after safeEnd.
func (str *resumableString) resume(parser *partialJSONParser, start int) (any, bool) {
	input := parser.input
	index, boundary := str.safeEnd, str.safeEnd
scan:
	for index < len(input) {
		char := input[index]
		switch {
		case char == '"':
			value, ok := str.finish(input[str.safeEnd:index], true)
			if !ok {
				return str.poison(parser, start)
			}
			parser.index = index + 1
			return value, true
		case char < 0x20:
			return str.poison(parser, start)
		case char == '\\':
			width, status := escapeWidth(input[index:])
			switch status {
			case escapeInvalid:
				return str.poison(parser, start)
			case escapeIncomplete:
				break scan
			}
			index += width
			boundary = index
		case char < utf8.RuneSelf:
			index++
			boundary = index
		default:
			if r, size := utf8.DecodeRuneInString(input[index:]); r == utf8.RuneError && size == 1 && !utf8.FullRuneInString(input[index:]) {
				break scan
			} else {
				index += size
				boundary = index
			}
		}
	}
	if boundary > str.safeEnd {
		if _, ok := str.finish(input[str.safeEnd:boundary], false); !ok {
			return str.poison(parser, start)
		}
		str.safeEnd = boundary
	}
	parser.index = len(input)
	return parser.unterminatedTail(str, start)
}

// unterminatedTail is the value of the string whose input ends inside the bytes after safeEnd: the decoded prefix followed by what the whole-text decoder makes of those bytes.
func (parser *partialJSONParser) unterminatedTail(str *resumableString, start int) (any, bool) {
	tail := parser.input[str.safeEnd:]
	if tail == "" {
		return str.decoded.String(), true
	}
	// The tail alone, behind an opening quote, decodes exactly as the same bytes do inside the whole string: it holds the last backslash of the input, which parseStr cuts at when the text as it stands does not parse.
	rest := partialJSONParser{input: `"` + tail}
	value, ok := rest.parseStr()
	if !ok {
		return str.poison(parser, start)
	}
	return str.decoded.String() + value.(string), true
}

// poison hands the string to the whole-text decoder for good, whatever the input grows into.
func (str *resumableString) poison(parser *partialJSONParser, start int) (any, bool) {
	str.poisoned, str.start = start, -1
	str.decoded.Reset()
	parser.index = start
	return parser.parseStrText()
}

// finish appends the decoded bytes of raw, a run of whole string units, to the prefix. closed reports that raw ends at the closing quote and returns the whole value.
func (str *resumableString) finish(raw string, closed bool) (any, bool) {
	var piece string
	if strings.IndexByte(raw, '\\') < 0 && utf8.ValidString(raw) {
		piece = raw
	} else {
		value, ok := jsonParseValue(`"` + raw + `"`)
		if !ok {
			return nil, false
		}
		piece, _ = value.(string)
	}
	if !closed {
		str.decoded.WriteString(piece)
		return nil, true
	}
	if str.decoded.Len() == 0 {
		return piece, true
	}
	return str.decoded.String() + piece, true
}

type escapeStatus uint8

const (
	escapeComplete escapeStatus = iota
	escapeIncomplete
	escapeInvalid
)

// escapeWidth is the width of the escape sequence that begins with the backslash at the start of text. A \uD800-\uDBFF escape takes the \uDC00-\uDFFF escape after it into one unit, so it waits until that escape is readable.
func escapeWidth(text string) (int, escapeStatus) {
	if len(text) < 2 {
		return 0, escapeIncomplete
	}
	switch text[1] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return 2, escapeComplete
	case 'u':
		code, status := unicodeEscape(text)
		if status != escapeComplete {
			return 0, status
		}
		if code < 0xD800 || code > 0xDBFF {
			return 6, escapeComplete
		}
		if len(text) < 7 {
			return 0, escapeIncomplete
		}
		if text[6] != '\\' {
			return 6, escapeComplete
		}
		if len(text) < 8 {
			return 0, escapeIncomplete
		}
		if text[7] != 'u' {
			return 6, escapeComplete
		}
		low, status := unicodeEscape(text[6:])
		switch {
		case status == escapeIncomplete:
			return 0, escapeIncomplete
		case status == escapeComplete && low >= 0xDC00 && low <= 0xDFFF:
			return 12, escapeComplete
		}
		return 6, escapeComplete
	}
	return 0, escapeInvalid
}

// unicodeEscape reads the four hex digits of the \u escape at the start of text. Fewer than four digits that are all hex are incomplete.
func unicodeEscape(text string) (rune, escapeStatus) {
	var code rune
	for index := 2; index < 6; index++ {
		if index >= len(text) {
			return 0, escapeIncomplete
		}
		digit, ok := hexDigit(text[index])
		if !ok {
			return 0, escapeInvalid
		}
		code = code<<4 | digit
	}
	return code, escapeComplete
}

func hexDigit(char byte) (rune, bool) {
	switch {
	case char >= '0' && char <= '9':
		return rune(char - '0'), true
	case char >= 'a' && char <= 'f':
		return rune(char-'a') + 10, true
	case char >= 'A' && char <= 'F':
		return rune(char-'A') + 10, true
	}
	return 0, false
}
