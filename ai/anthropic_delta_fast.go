package ai

import "strings"

// decodeAnthropicDeltaFast decodes the common content_block_delta record, an object with a type, an integer index and a
// delta holding its type and one non-empty string member, without reflection. It answers false for anything else (an
// unknown or case-folded member name, a member of another type, an empty or absent text member that decodeAnthropicDeltaEvent
// reads a second time, a duplicate member, malformed JSON), and the caller then runs decodeAnthropicDeltaEventReflect, so the two agree wherever this answers true.
func decodeAnthropicDeltaFast(data string) (anthEventContentBlockDelta, bool) {
	var ev anthEventContentBlockDelta
	p := chunkScanner{s: data}
	p.ws()
	if !p.byte('{') {
		return ev, false
	}
	var seenIndex, seenDelta bool
	p.ws()
	if p.peek() == '}' {
		return ev, false
	}
	for {
		p.ws()
		key, ok := p.str()
		if !ok {
			return ev, false
		}
		p.ws()
		if !p.byte(':') {
			return ev, false
		}
		p.ws()
		switch key {
		case "index":
			if seenIndex {
				return ev, false
			}
			seenIndex = true
			value, ok := p.int()
			if !ok {
				return ev, false
			}
			ev.Index = anthBlockIndex{value: value, present: true}
		case "delta":
			if seenDelta {
				return ev, false
			}
			seenDelta = true
			if !p.anthropicDelta(&ev) {
				return ev, false
			}
		default:
			if namesAlike(key, "index") || namesAlike(key, "delta") || !p.scalar() {
				return ev, false
			}
		}
		p.ws()
		if p.byte(',') {
			continue
		}
		if !p.byte('}') {
			return ev, false
		}
		break
	}
	p.ws()
	if p.i != len(p.s) || !seenDelta {
		return ev, false
	}
	if member := anthropicDeltaMember(&ev); member != nil && *member == "" {
		return ev, false
	}
	return ev, true
}

func (p *chunkScanner) anthropicDelta(ev *anthEventContentBlockDelta) bool {
	if !p.byte('{') {
		return false
	}
	var seen [5]bool
	p.ws()
	if p.peek() == '}' {
		return false
	}
	for {
		p.ws()
		key, ok := p.str()
		if !ok {
			return false
		}
		p.ws()
		if !p.byte(':') {
			return false
		}
		p.ws()
		var target *string
		slot := -1
		switch key {
		case "type":
			slot, target = 0, &ev.Delta.Type
		case "text":
			slot, target = 1, &ev.Delta.Text
		case "thinking":
			slot, target = 2, &ev.Delta.Thinking
		case "partial_json":
			slot, target = 3, &ev.Delta.PartialJSON
		case "signature":
			slot, target = 4, &ev.Delta.Signature
		default:
			for _, field := range [...]string{"type", "text", "thinking", "partial_json", "signature"} {
				if namesAlike(key, field) {
					return false
				}
			}
		}
		if slot >= 0 {
			if seen[slot] {
				return false
			}
			seen[slot] = true
			text, ok := p.str()
			if !ok {
				return false
			}
			*target = strings.Clone(text)
		} else if !p.scalar() {
			return false
		}
		p.ws()
		if p.byte(',') {
			continue
		}
		return p.byte('}')
	}
}
