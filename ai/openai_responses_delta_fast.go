package ai

// parseResponsesDeltaFast decodes the common Responses delta event without reflection: an object whose type is a delta event, with a
// non-empty string delta, a small non-negative integer output_index or none, and otherwise only members respSSEEvent does not read
// (sequence_number, item_id, content_index, logprobs, obfuscation, ...). It answers false for anything else (another member respSSEEvent
// reads under any case folding encoding/json applies, a repeated member, a value of another type, malformed JSON), and the caller then
// decodes with json.Unmarshal, so the two agree wherever this answers true.
func parseResponsesDeltaFast(data string) (respSSEEvent, bool) {
	var event respSSEEvent
	p := chunkScanner{s: data}
	p.ws()
	if !p.byte('{') {
		return event, false
	}
	var seenType, seenIndex, seenDelta bool
	p.ws()
	if p.byte('}') {
		return event, false
	}
	for {
		p.ws()
		key, ok := p.str()
		if !ok {
			return event, false
		}
		p.ws()
		if !p.byte(':') {
			return event, false
		}
		p.ws()
		switch key {
		case "type":
			if seenType {
				return event, false
			}
			seenType = true
			if event.Type, ok = p.str(); !ok {
				return event, false
			}
		case "output_index":
			if seenIndex || p.peek() == '-' {
				return event, false
			}
			seenIndex = true
			index, ok := p.int()
			if !ok {
				return event, false
			}
			event.OutputIndex = responsesSlotKey{kind: responsesSlotNumber, number: float64(index)}
		case "delta":
			if seenDelta {
				return event, false
			}
			seenDelta = true
			if event.Delta, ok = p.str(); !ok || event.Delta == "" {
				return event, false
			}
		default:
			if foldsToResponsesEventField(key) || !p.skipValue(0) {
				return event, false
			}
		}
		p.ws()
		if p.byte(',') {
			continue
		}
		if !p.byte('}') {
			return event, false
		}
		break
	}
	p.ws()
	if p.i != len(p.s) || !seenDelta || !responsesDeltaEvents[event.Type] {
		return event, false
	}
	return event, true
}

// foldsToResponsesEventField reports that encoding/json might decode member key into a field of respSSEEvent: namesAlike for each field,
// read once. Without its underscores and dashes, and in lower case, an ASCII key must equal a field name so folded.
func foldsToResponsesEventField(key string) bool {
	var folded [16]byte
	n := 0
	for i := range len(key) {
		c := key[i]
		switch {
		case c >= 0x80:
			return true
		case c == '_' || c == '-':
			continue
		case 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		}
		if n == len(folded) {
			return false
		}
		folded[n] = c
		n++
	}
	switch string(folded[:n]) {
	case "type", "outputindex", "response", "item", "delta", "part", "arguments", "input", "error", "code", "message":
		return true
	}
	return false
}

// responsesSkipDepth bounds the nesting skipValue walks; deeper values take the reflective decode.
// pig additive (D112): Pi's OpenAI SDK parses every event's JSON whole; an event the fast path declines is decoded by json.Unmarshal, which gives the same event.
const responsesSkipDepth = 32

// skipValue skips one JSON value, checking it as strictly as the scanner's other readers.
func (p *chunkScanner) skipValue(depth int) bool {
	switch p.peek() {
	case '{', '[':
		if depth >= responsesSkipDepth {
			return false
		}
		closing := byte('}')
		if p.peek() == '[' {
			closing = ']'
		}
		object := closing == '}'
		p.i++
		p.ws()
		if p.byte(closing) {
			return true
		}
		for {
			p.ws()
			if object {
				if _, ok := p.str(); !ok {
					return false
				}
				p.ws()
				if !p.byte(':') {
					return false
				}
				p.ws()
			}
			if !p.skipValue(depth + 1) {
				return false
			}
			p.ws()
			if p.byte(',') {
				continue
			}
			return p.byte(closing)
		}
	default:
		return p.scalar()
	}
}
