package agent

import "bytes"

// shapeKind is what a member's value must be for the decoded field to encode back to the same text.
type shapeKind uint8

const (
	shapeString           shapeKind = iota // any string
	shapeNonEmptyString                    // a string other than "" (an omitempty string field)
	shapeInteger                           // an integer number (an int field)
	shapeNumber                            // any number (a float64 field)
	shapeBool                              // true or false
	shapeTrue                              // true (an omitempty bool field)
	shapeUsage                             // a usageWire object
	shapeCost                              // an ai.UsageCost object
	shapeUserContent                       // a string, or an array of text and image blocks
	shapeAssistantContent                  // an array of text, thinking and toolCall blocks
	shapeResultContent                     // an array of text and image blocks
	shapeArguments                         // a tool call's arguments object, whose member order the call keeps
	shapeAny                               // any value, kept in its member order (orderedjson.Value)
	shapeUnmodelled                        // a member whose encoding the scan does not model
)

// shapeMember is one member an encoder writes, in the order it writes them. An optional member is written only for some field values; the kind then names the values for which it is written.
type shapeMember struct {
	name     string
	kind     shapeKind
	optional bool
}

var (
	userShape = []shapeMember{{"role", shapeString, false}, {"content", shapeUserContent, false}, {"timestamp", shapeInteger, false}}
	// assistantShape and assistantErrorShape follow the two structs AgentMessage.MarshalJSON writes an assistant with: providerThinkingLevel
	// after timestamp (anthropic-messages.ts:651), and the late members in Pi's creation order (responseId before responseModel, rawStopReason before errorMessage).
	assistantShape = []shapeMember{
		{"role", shapeString, false}, {"content", shapeAssistantContent, false}, {"api", shapeString, false},
		{"provider", shapeString, false}, {"model", shapeString, false},
		{"usage", shapeUsage, false}, {"stopReason", shapeString, false}, {"timestamp", shapeInteger, false},
		{"providerThinkingLevel", shapeNonEmptyString, true}, {"responseId", shapeNonEmptyString, true}, {"responseModel", shapeNonEmptyString, true},
		{"diagnostics", shapeUnmodelled, true}, {"deferred", shapeUnmodelled, true}, {"endTurn", shapeBool, true},
		{"rawStopReason", shapeNonEmptyString, true}, {"errorMessage", shapeNonEmptyString, true},
		{"durationMs", shapeInteger, true}, {"thinkingLevel", shapeNonEmptyString, true},
	}
	assistantErrorShape = []shapeMember{
		{"role", shapeString, false}, {"content", shapeAssistantContent, false}, {"api", shapeString, false},
		{"provider", shapeString, false}, {"model", shapeString, false}, {"usage", shapeUsage, false},
		{"stopReason", shapeString, false}, {"errorMessage", shapeNonEmptyString, false}, {"timestamp", shapeInteger, false},
		{"responseId", shapeNonEmptyString, true}, {"responseModel", shapeNonEmptyString, true},
		{"providerThinkingLevel", shapeNonEmptyString, true}, {"diagnostics", shapeUnmodelled, true},
		{"deferred", shapeUnmodelled, true}, {"rawStopReason", shapeNonEmptyString, true},
		{"endTurn", shapeBool, true}, {"durationMs", shapeInteger, true}, {"thinkingLevel", shapeNonEmptyString, true},
	}
	toolResultShape = []shapeMember{
		{"role", shapeString, false}, {"toolCallId", shapeString, false}, {"toolName", shapeString, false},
		{"content", shapeResultContent, false}, {"details", shapeAny, true}, {"usage", shapeUsage, true},
		{"isError", shapeBool, false}, {"durationMs", shapeInteger, true}, {"timestamp", shapeInteger, false},
		{"nestedCalls", shapeUnmodelled, true},
	}
	usageShape = []shapeMember{
		{"input", shapeInteger, false}, {"output", shapeInteger, false}, {"cacheRead", shapeInteger, false},
		{"cacheWrite", shapeInteger, false}, {"cacheWrite1h", shapeInteger, true}, {"reasoning", shapeInteger, true},
		{"totalTokens", shapeInteger, false}, {"cost", shapeCost, false},
	}
	costShape = []shapeMember{
		{"input", shapeNumber, false}, {"output", shapeNumber, false}, {"cacheRead", shapeNumber, false},
		{"cacheWrite", shapeNumber, false}, {"total", shapeNumber, false}, {"totalFromProvider", shapeBool, true},
	}
	// The block shapes follow ai.TextContent, ai.ThinkingContent, ai.ToolCall and the image struct of marshalAgentContentBlock. A decoded block has no streaming index.
	textBlockShape     = []shapeMember{{"type", shapeString, false}, {"text", shapeString, false}, {"textSignature", shapeNonEmptyString, true}}
	imageBlockShape    = []shapeMember{{"type", shapeString, false}, {"data", shapeString, false}, {"mimeType", shapeString, false}}
	thinkingBlockShape = []shapeMember{{"type", shapeString, false}, {"thinking", shapeString, false}, {"thinkingSignature", shapeString, true}, {"redacted", shapeTrue, true}}
	toolCallBlockShape = []shapeMember{
		{"type", shapeString, false}, {"id", shapeString, false}, {"name", shapeString, false},
		{"arguments", shapeArguments, false}, {"thoughtSignature", shapeNonEmptyString, true}, {"namespace", shapeNonEmptyString, true},
	}
)

// shapeScan reads the canonical JSON text of a message (jsonstringify.IsCanonical holds for it) member by member and proves that the fields decoded from it encode to that text once canonicalized, as every writer of a message canonicalizes it (the encoders escape <, >, & and U+2028, which JSON.stringify writes raw): each object's members are those the encoder writes, in its order, and each value has the one spelling its field keeps. It reports false whenever it cannot prove that.
type shapeScan struct {
	text []byte
	i    int
}

// encodesAs reports whether m's fields, decoded from the canonical text data, encode to data once canonicalized.
func (m *AgentMessage) encodesAs(data []byte) bool {
	scan := shapeScan{text: data}
	var ok bool
	switch {
	case m.User != nil:
		ok = scan.object(userShape)
	case m.Assistant != nil:
		shape := assistantShape
		if m.Assistant.ErrorBeforeTimestamp && m.Assistant.ErrorMessage != "" {
			shape = assistantErrorShape
		}
		ok = scan.object(shape)
	case m.ToolResult != nil:
		ok = scan.object(toolResultShape)
	case m.Custom != nil:
		// The encoder writes a custom message only with a non-empty string role.
		role, _ := m.Custom["role"].(string)
		ok = role != "" && scan.customMessage(customMessageMemberOrder[role])
	}
	return ok && scan.i == len(scan.text)
}

func (s *shapeScan) eat(c byte) bool {
	if s.i < len(s.text) && s.text[s.i] == c {
		s.i++
		return true
	}
	return false
}

func (s *shapeScan) peek() byte {
	if s.i < len(s.text) {
		return s.text[s.i]
	}
	return 0
}

// str reads a string token and returns its text between the quotes.
func (s *shapeScan) str() ([]byte, bool) {
	if !s.eat('"') {
		return nil, false
	}
	start := s.i
	for s.i < len(s.text) {
		switch s.text[s.i] {
		case '"':
			s.i++
			return s.text[start : s.i-1], true
		case '\\':
			s.i += 2
		default:
			s.i++
		}
	}
	return nil, false
}

// number reads a number token and reports whether it has a fraction.
func (s *shapeScan) number() (fraction, ok bool) {
	start := s.i
	for s.i < len(s.text) {
		switch c := s.text[s.i]; {
		case c == '.':
			fraction = true
		case c == '-' || c >= '0' && c <= '9':
		default:
			return fraction, s.i > start
		}
		s.i++
	}
	return fraction, s.i > start
}

// skip reads any value.
func (s *shapeScan) skip() bool {
	switch s.peek() {
	case '"':
		_, ok := s.str()
		return ok
	case '{', '[':
		depth := 0
		for s.i < len(s.text) {
			switch s.text[s.i] {
			case '"':
				if _, ok := s.str(); !ok {
					return false
				}
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					s.i++
					return true
				}
			}
			s.i++
		}
		return false
	case 't':
		return s.literal("true")
	case 'f':
		return s.literal("false")
	case 'n':
		return s.literal("null")
	}
	_, ok := s.number()
	return ok
}

func (s *shapeScan) literal(word string) bool {
	if bytes.HasPrefix(s.text[s.i:], []byte(word)) {
		s.i += len(word)
		return true
	}
	return false
}

// object reads an object whose members must follow shape.
func (s *shapeScan) object(shape []shapeMember) bool {
	if !s.eat('{') {
		return false
	}
	next := 0
	if !s.eat('}') {
		for {
			name, ok := s.str()
			if !ok || !s.eat(':') {
				return false
			}
			for next < len(shape) && shape[next].name != string(name) {
				if !shape[next].optional {
					return false
				}
				next++
			}
			if next == len(shape) || !s.value(shape[next].kind) {
				return false
			}
			next++
			if s.eat(',') {
				continue
			}
			if !s.eat('}') {
				return false
			}
			break
		}
	}
	for ; next < len(shape); next++ {
		if !shape[next].optional {
			return false
		}
	}
	return true
}

func (s *shapeScan) value(kind shapeKind) bool {
	switch kind {
	case shapeString:
		_, ok := s.str()
		return ok
	case shapeNonEmptyString:
		text, ok := s.str()
		return ok && len(text) > 0
	case shapeInteger:
		if c := s.peek(); c != '-' && (c < '0' || c > '9') {
			return false
		}
		fraction, ok := s.number()
		return ok && !fraction
	case shapeNumber:
		if c := s.peek(); c != '-' && (c < '0' || c > '9') {
			return false
		}
		_, ok := s.number()
		return ok
	case shapeBool:
		return s.literal("true") || s.literal("false")
	case shapeTrue:
		return s.literal("true")
	case shapeUsage:
		return s.object(usageShape)
	case shapeCost:
		return s.object(costShape)
	case shapeUserContent:
		if s.peek() == '"' {
			_, ok := s.str()
			return ok
		}
		return s.blocks(true, false)
	case shapeAssistantContent:
		return s.blocks(false, true)
	case shapeResultContent:
		return s.blocks(true, false)
	case shapeArguments:
		return s.peek() == '{' && s.skip()
	case shapeAny:
		return s.skip()
	}
	return false
}

// blocks reads an array of content blocks; image admits image blocks, assistant admits thinking and toolCall blocks.
func (s *shapeScan) blocks(image, assistant bool) bool {
	if !s.eat('[') {
		return false
	}
	if s.eat(']') {
		return true
	}
	for {
		var shape []shapeMember
		switch rest := s.text[s.i:]; {
		case bytes.HasPrefix(rest, []byte(`{"type":"text"`)):
			shape = textBlockShape
		case image && bytes.HasPrefix(rest, []byte(`{"type":"image"`)):
			shape = imageBlockShape
		case assistant && bytes.HasPrefix(rest, []byte(`{"type":"thinking"`)):
			shape = thinkingBlockShape
		case assistant && bytes.HasPrefix(rest, []byte(`{"type":"toolCall"`)):
			shape = toolCallBlockShape
		default:
			return false
		}
		if !s.object(shape) {
			return false
		}
		if s.eat(',') {
			continue
		}
		return s.eat(']')
	}
}

// customMessage reads a custom-role message as marshalCustomMessage writes it: the members first lists, in that order, then the others sorted; content blocks with contentBlockMemberOrder members first; details in its own order; any other object sorted.
func (s *shapeScan) customMessage(first []string) bool {
	return s.sortedObject(first, func(name []byte) bool {
		switch string(name) {
		case "details":
			return s.skip()
		case "content":
			if s.peek() == '[' {
				return s.array(contentBlockMemberOrder)
			}
		}
		return s.sortedValue()
	})
}

// sortedValue reads a value whose objects, at any depth, have their members sorted, as encoding a map[string]any writes them.
func (s *shapeScan) sortedValue() bool {
	switch s.peek() {
	case '{':
		return s.sortedObject(nil, func([]byte) bool { return s.sortedValue() })
	case '[':
		return s.array(nil)
	}
	return s.skip()
}

// array reads an array whose object elements have the members first lists first, in that order, then the others sorted.
func (s *shapeScan) array(first []string) bool {
	if !s.eat('[') {
		return false
	}
	if s.eat(']') {
		return true
	}
	for {
		var ok bool
		if s.peek() == '{' {
			ok = s.sortedObject(first, func([]byte) bool { return s.sortedValue() })
		} else {
			ok = s.sortedValue()
		}
		if !ok {
			return false
		}
		if s.eat(',') {
			continue
		}
		return s.eat(']')
	}
}

// sortedObject reads an object whose members are those first lists, in that order, then the others in ascending byte order; value reads each member's value.
func (s *shapeScan) sortedObject(first []string, value func(name []byte) bool) bool {
	if !s.eat('{') {
		return false
	}
	if s.eat('}') {
		return true
	}
	next := 0
	var previous []byte
	for {
		name, ok := s.str()
		if !ok || !s.eat(':') || bytes.IndexByte(name, '\\') >= 0 {
			return false
		}
		listed := -1
		for index, member := range first {
			if member == string(name) {
				listed = index
				break
			}
		}
		switch {
		case listed >= 0:
			if previous != nil || listed < next {
				return false
			}
			next = listed + 1
		case previous != nil && bytes.Compare(previous, name) >= 0:
			return false
		default:
			previous = name
		}
		if !value(name) {
			return false
		}
		if s.eat(',') {
			continue
		}
		return s.eat('}')
	}
}
