package ai

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// parseOpenAIChunkFast decodes the common streamed chat-completion chunk without reflection: an object whose members
// are the ones oaiChunk reads, or scalars that decoding ignores, with one text delta. It answers false for anything
// else (an unknown or misspelled-case member, a tool call, usage, an error, a value of an unexpected type, malformed
// JSON), and the caller then decodes with json.Unmarshal, so the two agree wherever this answers true. The chunk owns
// its slices; openAIChunkDecoder.decode reuses them instead.
func parseOpenAIChunkFast(data string) (oaiChunk, bool) {
	return new(openAIChunkDecoder).decode(data)
}

// openAIChunkDecoder is parseOpenAIChunkFast for a stream of records: the chunk's slices and pointers (choices, tool
// calls, functions, indexes, the model bytes) live in arrays that the next decode overwrites, so a chunk is valid only
// until then. Its strings are substrings of the record or decoded copies, and stay valid.
type openAIChunkDecoder struct {
	choices   []oaiChunkChoice
	calls     []oaiToolCall
	functions []oaiToolCallFunction
	indexes   []int
	model     []byte
	// parsesAsValue is validJSONParsesAsValue of the record the last decode read: false when it holds a number with an exponent or of more
	// than 300 characters. A record the decode reads is valid JSON, so this stands in for the provider event observer's own scan.
	parsesAsValue bool
}

//perf:hot
func (d *openAIChunkDecoder) decode(data string) (oaiChunk, bool) {
	var chunk oaiChunk
	d.choices, d.calls, d.functions, d.indexes = d.choices[:0], d.calls[:0], d.functions[:0], d.indexes[:0]
	p := chunkScanner{s: data, arena: d}
	p.ws()
	if !p.byte('{') {
		return chunk, false
	}
	var seen [5]bool
	var modelRaw string
	p.ws()
	if p.peek() == '}' {
		p.i++
	} else {
		for {
			p.ws()
			key, ok := p.str()
			if !ok {
				return chunk, false
			}
			p.ws()
			if !p.byte(':') {
				return chunk, false
			}
			p.ws()
			slot := -1
			switch key {
			case "id":
				slot = 0
			case "model":
				slot = 1
			case "choices":
				slot = 2
			case "usage":
				slot = 3
			case "error":
				slot = 4
			default:
				if foldsToChunkField(key) {
					return chunk, false
				}
			}
			if slot >= 0 {
				if seen[slot] {
					return chunk, false
				}
				seen[slot] = true
			}
			switch slot {
			case 0:
				id, ok := p.str()
				if !ok {
					return chunk, false
				}
				chunk.ID = id
			case 1:
				start := p.i
				if _, ok := p.str(); !ok {
					return chunk, false
				}
				modelRaw = p.s[start:p.i]
			case 2:
				choices, ok := p.choices(d.choices[:0])
				if !ok {
					return chunk, false
				}
				if len(choices) > 0 {
					d.choices = choices
				}
				chunk.Choices = choices
			case 3:
				if !p.literal("null") {
					return chunk, false
				}
			case 4:
				return chunk, false
			default:
				if !p.scalar() {
					return chunk, false
				}
			}
			p.ws()
			if p.byte(',') {
				continue
			}
			if !p.byte('}') {
				return chunk, false
			}
			break
		}
	}
	p.ws()
	if p.i != len(p.s) {
		return oaiChunk{}, false
	}
	// The strings are substrings of data, which the caller owns for as long as it keeps them; the model bytes are copied into the decoder's array.
	if modelRaw != "" {
		d.model = append(d.model[:0], modelRaw...)
		chunk.Model = d.model
	}
	d.parsesAsValue = !p.inexact
	return chunk, true
}

// foldsToChunkField reports a member name that encoding/json matches to a field of the chunk case-insensitively although it is not spelled like it.
func foldsToChunkField(key string) bool {
	for _, field := range [...]string{"id", "model", "choices", "usage", "error"} {
		if namesAlike(key, field) {
			return true
		}
	}
	return false
}

func foldsToChoiceField(key string) bool {
	for _, field := range [...]string{"delta", "finish_reason", "index", "usage"} {
		if namesAlike(key, field) {
			return true
		}
	}
	return false
}

func foldsToDeltaField(key string) bool {
	for _, field := range [...]string{"role", "content", "tool_calls", "reasoning_details", "reasoning_content", "reasoning", "reasoning_text"} {
		if namesAlike(key, field) {
			return true
		}
	}
	return false
}

// choices reads the array of choice objects.
func (p *chunkScanner) choices(choices []oaiChunkChoice) ([]oaiChunkChoice, bool) {
	if !p.byte('[') {
		return nil, false
	}
	p.ws()
	if p.byte(']') {
		return []oaiChunkChoice{}, true
	}
	for {
		p.ws()
		choice, ok := p.choice()
		if !ok {
			return nil, false
		}
		choices = append(choices, choice)
		p.ws()
		if p.byte(',') {
			continue
		}
		if !p.byte(']') {
			return nil, false
		}
		return choices, true
	}
}

func (p *chunkScanner) choice() (oaiChunkChoice, bool) {
	var choice oaiChunkChoice
	if !p.byte('{') {
		return choice, false
	}
	var seen [3]bool
	p.ws()
	if p.byte('}') {
		return choice, true
	}
	for {
		p.ws()
		key, ok := p.str()
		if !ok {
			return choice, false
		}
		p.ws()
		if !p.byte(':') {
			return choice, false
		}
		p.ws()
		slot := -1
		switch key {
		case "delta":
			slot = 0
		case "finish_reason":
			slot = 1
		case "index":
			slot = 2
		case "usage":
			return choice, false
		default:
			if foldsToChoiceField(key) {
				return choice, false
			}
		}
		if slot >= 0 {
			if seen[slot] {
				return choice, false
			}
			seen[slot] = true
		}
		switch slot {
		case 0:
			delta, ok := p.delta()
			if !ok {
				return choice, false
			}
			choice.Delta = delta
		case 1:
			if p.literal("null") {
				break
			}
			reason, ok := p.str()
			if !ok {
				return choice, false
			}
			choice.FinishReason = reason
		case 2:
			index, ok := p.int()
			if !ok {
				return choice, false
			}
			choice.Index = index
		default:
			if !p.scalar() {
				return choice, false
			}
		}
		p.ws()
		if p.byte(',') {
			continue
		}
		return choice, p.byte('}')
	}
}

func (p *chunkScanner) delta() (oaiDelta, bool) {
	var delta oaiDelta
	if !p.byte('{') {
		return delta, false
	}
	var seen [6]bool
	p.ws()
	if p.byte('}') {
		return delta, true
	}
	for {
		p.ws()
		key, ok := p.str()
		if !ok {
			return delta, false
		}
		p.ws()
		if !p.byte(':') {
			return delta, false
		}
		p.ws()
		var target *string
		slot := -1
		switch key {
		case "role":
			slot, target = 0, &delta.Role
		case "content":
			slot, target = 1, &delta.Content
		case "reasoning_content":
			slot, target = 2, &delta.ReasoningContent
		case "reasoning":
			slot, target = 3, &delta.Reasoning
		case "reasoning_text":
			slot, target = 4, &delta.ReasoningText
		case "tool_calls":
			if seen[5] {
				return delta, false
			}
			seen[5] = true
			if !p.literal("null") {
				calls, ok := p.toolCalls()
				if !ok {
					return delta, false
				}
				delta.ToolCalls = calls
			}
			p.ws()
			if p.byte(',') {
				continue
			}
			return delta, p.byte('}')
		default:
			if foldsToDeltaField(key) {
				return delta, false
			}
		}
		if slot >= 0 {
			if seen[slot] {
				return delta, false
			}
			seen[slot] = true
			if !p.literal("null") {
				text, ok := p.str()
				if !ok {
					return delta, false
				}
				*target = text
			}
		} else if !p.scalar() {
			return delta, false
		}
		p.ws()
		if p.byte(',') {
			continue
		}
		return delta, p.byte('}')
	}
}

// chunkScanner reads JSON text strictly: it answers false at the first byte that is not valid JSON of the shape asked for.
type chunkScanner struct {
	s string
	i int
	// arena holds the slices and pointers an OpenAI chunk decode returns; nil allocates them.
	arena *openAIChunkDecoder
	// inexact marks a number with an exponent or of more than 300 characters, which may overflow a float64.
	inexact bool
}

func (p *chunkScanner) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *chunkScanner) byte(c byte) bool {
	if p.i < len(p.s) && p.s[p.i] == c {
		p.i++
		return true
	}
	return false
}

func (p *chunkScanner) ws() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\r', '\n':
			p.i++
		default:
			return
		}
	}
}

func (p *chunkScanner) literal(word string) bool {
	if strings.HasPrefix(p.s[p.i:], word) {
		p.i += len(word)
		return true
	}
	return false
}

// scalar skips a string, number, true, false or null.
func (p *chunkScanner) scalar() bool {
	switch p.peek() {
	case '"':
		_, ok := p.str()
		return ok
	case 't':
		return p.literal("true")
	case 'f':
		return p.literal("false")
	case 'n':
		return p.literal("null")
	default:
		return p.number()
	}
}

// number skips a JSON number.
func (p *chunkScanner) number() bool {
	start := p.i
	p.byte('-')
	switch c := p.peek(); {
	case c == '0':
		p.i++
	case c >= '1' && c <= '9':
		p.digits()
	default:
		return false
	}
	if p.byte('.') && !p.digits() {
		return false
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		p.inexact = true
		p.i++
		if c := p.peek(); c == '+' || c == '-' {
			p.i++
		}
		if !p.digits() {
			return false
		}
	}
	if p.i-start > 300 {
		p.inexact = true
	}
	return true
}

func (p *chunkScanner) digits() bool {
	start := p.i
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	return p.i > start
}

// int reads a JSON integer that fits an int without an exponent or fraction.
func (p *chunkScanner) int() (int, bool) {
	neg := p.byte('-')
	start := p.i
	if !p.digits() || (p.s[start] == '0' && p.i-start > 1) || p.i-start > 15 {
		return 0, false
	}
	if c := p.peek(); c == '.' || c == 'e' || c == 'E' {
		return 0, false
	}
	n := 0
	for _, c := range []byte(p.s[start:p.i]) {
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}

// str reads a JSON string. A string with no escape is a substring of the input. Invalid UTF-8, a control character and a
// lone surrogate escape answer false (encoding/json replaces the first and rejects the second). Plain ASCII runs are skipped
// eight bytes at a time, and only a string with a byte from 0x80 is checked for valid UTF-8.
func (p *chunkScanner) str() (string, bool) {
	if !p.byte('"') {
		return "", false
	}
	start := p.i
	ascii := true
	for i := jv.PlainEnd(p.s, start); i < len(p.s); i = jv.PlainEnd(p.s, i+1) {
		switch c := p.s[i]; {
		case c == '"':
			text := p.s[start:i]
			if !ascii && !utf8.ValidString(text) {
				return "", false
			}
			p.i = i + 1
			return text, true
		case c == '\\':
			return p.escaped(start)
		case c < 0x20:
			return "", false
		}
		ascii = false
	}
	return "", false
}

// escaped finishes a string whose first escape was found after start. The text is decoded into a stack buffer and copied once into the string.
func (p *chunkScanner) escaped(start int) (string, bool) {
	var stack [256]byte
	b := stack[:0]
	i := start
	for i < len(p.s) {
		c := p.s[i]
		switch {
		case c == '"':
			p.i = i + 1
			if !utf8.Valid(b) {
				return "", false
			}
			return string(b), true
		case c < 0x20:
			return "", false
		case c != '\\':
			run := i + 1
			for run < len(p.s) && p.s[run] != '"' && p.s[run] != '\\' && p.s[run] >= 0x20 {
				run++
			}
			b = append(b, p.s[i:run]...)
			i = run
			continue
		}
		i++
		if i >= len(p.s) {
			return "", false
		}
		switch p.s[i] {
		case '"', '\\', '/':
			b = append(b, p.s[i])
		case 'b':
			b = append(b, '\b')
		case 'f':
			b = append(b, '\f')
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'u':
			r, ok := hex4(p.s, i+1)
			if !ok {
				return "", false
			}
			i += 4
			if r >= 0xD800 && r < 0xE000 {
				if r >= 0xDC00 || i+6 >= len(p.s) || p.s[i+1] != '\\' || p.s[i+2] != 'u' {
					return "", false
				}
				low, ok := hex4(p.s, i+3)
				if !ok || low < 0xDC00 || low >= 0xE000 {
					return "", false
				}
				r = 0x10000 + (r-0xD800)<<10 + (low - 0xDC00)
				i += 6
			}
			b = utf8.AppendRune(b, r)
		default:
			return "", false
		}
		i++
	}
	return "", false
}

func hex4(s string, at int) (rune, bool) {
	if at+4 > len(s) {
		return 0, false
	}
	var r rune
	for _, c := range []byte(s[at : at+4]) {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

// namesAlike reports that encoding/json might match member name key to the field name, under any case folding it
// applies, with or without the underscores and dashes a folding may ignore. A name with a non-ASCII byte might fold to
// anything, so it counts as alike.
func namesAlike(key, field string) bool {
	i, j := 0, 0
	for {
		for i < len(key) && (key[i] == '_' || key[i] == '-') {
			i++
		}
		for j < len(field) && (field[j] == '_' || field[j] == '-') {
			j++
		}
		if i == len(key) || j == len(field) {
			break
		}
		a, b := key[i], field[j]
		if a >= 0x80 {
			return true
		}
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if a != b {
			return strings.ContainsFunc(key, func(r rune) bool { return r >= 0x80 })
		}
		i++
		j++
	}
	if i == len(key) && j == len(field) {
		return true
	}
	return strings.ContainsFunc(key, func(r rune) bool { return r >= 0x80 })
}

// simpleJSONString returns the text of raw when it is a JSON string with no escape and valid UTF-8, the case json.Unmarshal would decode to the same text, and reports whether it is.
func simpleJSONString(raw []byte) ([]byte, bool) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return nil, false
	}
	inner := raw[1 : len(raw)-1]
	for _, c := range inner {
		if c == '\\' || c == '"' || c < 0x20 {
			return nil, false
		}
	}
	if !utf8.Valid(inner) {
		return nil, false
	}
	return inner, true
}

// toolCalls reads a delta's tool_calls array of function calls into the arena: the calls are a slice of its array, capped so that an append to them cannot overwrite the arena.
func (p *chunkScanner) toolCalls() ([]oaiToolCall, bool) {
	if !p.byte('[') {
		return nil, false
	}
	p.ws()
	if p.byte(']') {
		return []oaiToolCall{}, true
	}
	start := len(p.arena.calls)
	for {
		p.ws()
		call, ok := p.toolCall()
		if !ok {
			return nil, false
		}
		p.arena.calls = append(p.arena.calls, call)
		p.ws()
		if p.byte(',') {
			continue
		}
		calls := p.arena.calls[start:len(p.arena.calls):len(p.arena.calls)]
		return calls, p.byte(']')
	}
}

func (p *chunkScanner) toolCall() (oaiToolCall, bool) {
	var call oaiToolCall
	if !p.byte('{') {
		return call, false
	}
	var seen [4]bool
	p.ws()
	if p.byte('}') {
		return call, true
	}
	for {
		p.ws()
		key, ok := p.str()
		if !ok {
			return call, false
		}
		p.ws()
		if !p.byte(':') {
			return call, false
		}
		p.ws()
		slot := -1
		switch key {
		case "id":
			slot = 0
		case "type":
			slot = 1
		case "function":
			slot = 2
		case "index":
			slot = 3
		default:
			for _, field := range [...]string{"id", "type", "function", "custom", "index"} {
				if namesAlike(key, field) {
					return call, false
				}
			}
		}
		if slot >= 0 {
			if seen[slot] {
				return call, false
			}
			seen[slot] = true
		}
		switch slot {
		case 0, 1:
			text, ok := p.str()
			if !ok {
				return call, false
			}
			if slot == 0 {
				call.ID = text
			} else {
				call.Type = text
			}
		case 2:
			if p.literal("null") {
				break
			}
			function, ok := p.toolCallFunction()
			if !ok {
				return call, false
			}
			call.Function = function
		case 3:
			index, ok := p.int()
			if !ok {
				return call, false
			}
			p.arena.indexes = append(p.arena.indexes, index)
			call.Index = &p.arena.indexes[len(p.arena.indexes)-1]
		default:
			if !p.scalar() {
				return call, false
			}
		}
		p.ws()
		if p.byte(',') {
			continue
		}
		return call, p.byte('}')
	}
}

func (p *chunkScanner) toolCallFunction() (*oaiToolCallFunction, bool) {
	p.arena.functions = append(p.arena.functions, oaiToolCallFunction{})
	function := &p.arena.functions[len(p.arena.functions)-1]
	if !p.byte('{') {
		return nil, false
	}
	var seen [2]bool
	p.ws()
	if p.byte('}') {
		return function, true
	}
	for {
		p.ws()
		key, ok := p.str()
		if !ok {
			return nil, false
		}
		p.ws()
		if !p.byte(':') {
			return nil, false
		}
		p.ws()
		var target *string
		switch key {
		case "name":
			target = &function.Name
		case "arguments":
			target = &function.Arguments
		default:
			if namesAlike(key, "name") || namesAlike(key, "arguments") || !p.scalar() {
				return nil, false
			}
		}
		if target != nil {
			slot := 0
			if key == "arguments" {
				slot = 1
			}
			if seen[slot] {
				return nil, false
			}
			seen[slot] = true
			if !p.literal("null") {
				text, ok := p.str()
				if !ok {
					return nil, false
				}
				*target = text
			}
		}
		p.ws()
		if p.byte(',') {
			continue
		}
		if !p.byte('}') {
			return nil, false
		}
		return function, true
	}
}
