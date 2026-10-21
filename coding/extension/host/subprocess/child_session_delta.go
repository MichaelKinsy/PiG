package subprocess

// The message_update events of a child session reach the extension as the payloads of one numbered sequence, as a message_update of the main session does (message_update_delta.go): the first is a keyframe, each later one the operations that turn the payload the extension last received into the new one. The session's events are already JSON here, so the payload is parsed from the event's own bytes rather than from the typed event: a text that grew is sent as an append of the bytes it grew by, without decoding it.

import (
	"bytes"
	"strconv"
)

// childNode is a JSON value of an event. A string of bigStringBytes or more is kept as its JSON token, quotes included, so a text that grew is told by its prefix.
type childNode struct {
	kind  deltaKind
	raw   []byte // deltaScalar: the token; deltaBig: the string token
	src   []byte // the JSON text of the whole value in the event it was parsed from; valid only until the event buffer is reused
	keys  [][]byte
	items []*childNode
}

// parse parses an event's JSON. It reports false for JSON this encoding does not carry: a long string with a UTF-16 escape, which the extension's decoder may join across an append differently from the whole. The nodes it returns live in the parser's arena and are valid until the next parse; retainChild takes what must outlive it.
func (p *childParser) parse(data []byte) (*childNode, bool) {
	p.data, p.pos = data, 0
	p.nodes, p.items, p.keys = p.nodes[:0], p.items[:0], p.keys[:0]
	node, ok := p.value()
	if !ok {
		return nil, false
	}
	p.space()
	return node, p.pos == len(data)
}

// childParser parses events into slabs it reuses, so a parse allocates only when an event outgrows the last.
type childParser struct {
	data []byte
	pos  int
	// nodes, items and keys are the slabs; stack and keyStack hold the members of the containers being parsed.
	nodes    []childNode
	items    []*childNode
	keys     [][]byte
	stack    []*childNode
	keyStack [][]byte
}

func (p *childParser) node(kind deltaKind) *childNode {
	p.nodes = append(p.nodes, childNode{kind: kind})
	return &p.nodes[len(p.nodes)-1]
}

func (p *childParser) space() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\n', '\r', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *childParser) value() (*childNode, bool) {
	p.space()
	if p.pos >= len(p.data) {
		return nil, false
	}
	start := p.pos
	switch p.data[p.pos] {
	case '{', '[':
		closer, kind := byte('}'), deltaObject
		if p.data[p.pos] == '[' {
			closer, kind = ']', deltaArray
		}
		p.pos++
		node := p.node(kind)
		stack, keyStack := len(p.stack), len(p.keyStack)
		defer func() {
			p.stack, p.keyStack = p.stack[:stack], p.keyStack[:keyStack]
		}()
		p.space()
		if p.pos < len(p.data) && p.data[p.pos] == closer {
			p.pos++
			node.src = p.data[start:p.pos]
			return node, true
		}
		for {
			if kind == deltaObject {
				p.space()
				if p.pos >= len(p.data) || p.data[p.pos] != '"' {
					return nil, false
				}
				key, ok := p.token()
				if !ok {
					return nil, false
				}
				p.space()
				if p.pos >= len(p.data) || p.data[p.pos] != ':' {
					return nil, false
				}
				p.pos++
				p.keyStack = append(p.keyStack, key)
			}
			member, ok := p.value()
			if !ok {
				return nil, false
			}
			p.stack = append(p.stack, member)
			p.space()
			if p.pos >= len(p.data) {
				return nil, false
			}
			switch p.data[p.pos] {
			case ',':
				p.pos++
			case closer:
				p.pos++
				node.src = p.data[start:p.pos]
				first := len(p.items)
				p.items = append(p.items, p.stack[stack:]...)
				node.items = p.items[first:len(p.items):len(p.items)]
				if kind == deltaObject {
					firstKey := len(p.keys)
					p.keys = append(p.keys, p.keyStack[keyStack:]...)
					node.keys = p.keys[firstKey:len(p.keys):len(p.keys)]
				}
				return node, true
			default:
				return nil, false
			}
		}
	default:
		token, ok := p.token()
		if !ok {
			return nil, false
		}
		node := p.node(deltaScalar)
		node.raw, node.src = token, token
		if token[0] == '"' && len(token) >= bigStringBytes {
			if bytes.Contains(token, []byte(`\ud`)) || bytes.Contains(token, []byte(`\uD`)) {
				return nil, false
			}
			node.kind = deltaBig
		}
		return node, true
	}
}

// token reads a string or a bare scalar and returns its JSON text.
func (p *childParser) token() ([]byte, bool) {
	start := p.pos
	if p.pos >= len(p.data) {
		return nil, false
	}
	if p.data[p.pos] == '"' {
		p.pos++
		for {
			end := bytes.IndexByte(p.data[p.pos:], '"')
			if end < 0 {
				return nil, false
			}
			p.pos += end
			// The quote ends the string unless an odd number of backslashes escapes it.
			slashes := 0
			for at := p.pos - 1; at > start && p.data[at] == '\\'; at-- {
				slashes++
			}
			p.pos++
			if slashes%2 == 0 {
				return p.data[start:p.pos], true
			}
		}
	}
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ',', '}', ']', ' ', '\n', '\r', '\t', ':':
			return p.data[start:p.pos], p.pos > start
		}
		p.pos++
	}
	return p.data[start:p.pos], p.pos > start
}

// grew reports whether the string token next is old with text added: both are complete tokens, so the shared bytes end at an escape boundary.
func grew(old, next []byte) bool {
	return len(next) > len(old) && bytes.HasPrefix(next, old[:len(old)-1])
}

// diffChild appends the operations that turn old into next.
func diffChild(ops, path []byte, old, next *childNode) []byte {
	set := func() []byte { return appendOp(ops, "set", path, next.src) }
	if old.kind != next.kind {
		return set()
	}
	switch next.kind {
	case deltaScalar:
		if bytes.Equal(old.raw, next.raw) {
			return ops
		}
		return set()
	case deltaBig:
		if bytes.Equal(old.raw, next.raw) {
			return ops
		}
		if grew(old.raw, next.raw) {
			value := make([]byte, 0, len(next.raw)-len(old.raw)+2)
			value = append(append(value, '"'), next.raw[len(old.raw)-1:]...)
			return appendOp(ops, "append", path, value)
		}
		return set()
	}
	if len(next.items) < len(old.items) {
		return set()
	}
	if next.kind == deltaObject {
		for index, key := range old.keys {
			if !bytes.Equal(key, next.keys[index]) {
				return set()
			}
		}
	}
	for index := range next.items {
		member := pushPath(path)
		if next.kind == deltaObject {
			member = append(member, next.keys[index]...)
		} else {
			member = strconv.AppendInt(member, int64(index), 10)
		}
		member = append(member, ']')
		if index < len(old.items) {
			ops = diffChild(ops, member, old.items[index], next.items[index])
		} else {
			ops = appendOp(ops, "set", member, next.items[index].src)
		}
		if len(path) > 0 {
			path[len(path)-1] = ']'
		}
	}
	return ops
}

// pushPath returns path, a JSON array of path elements, opened for one more element: the closing bracket becomes a comma in place, so the caller appends the element and the bracket, and puts the bracket back in path when it is done with the member. A path costs no allocation while it fits the capacity path has.
func pushPath(path []byte) []byte {
	if len(path) == 0 {
		return append(path, '[')
	}
	path[len(path)-1] = ','
	return path
}

// retainChild returns the node for next that owns its bytes, reusing old where they agree: the event buffer is reused, and a grown text extends the buffer its old token held, so keeping a payload costs what it added.
func retainChild(old, next *childNode) *childNode {
	if old == nil || old.kind != next.kind {
		return copyChild(next)
	}
	switch next.kind {
	case deltaScalar:
		if bytes.Equal(old.raw, next.raw) {
			return old
		}
		// The node owns its bytes: a changed token overwrites them in place.
		old.raw = append(old.raw[:0], next.raw...)
		return old
	case deltaBig:
		if bytes.Equal(old.raw, next.raw) {
			return old
		}
		if grew(old.raw, next.raw) {
			old.raw = append(old.raw[:len(old.raw)-1], next.raw[len(old.raw)-1:]...)
			return old
		}
		old.raw = append(old.raw[:0], next.raw...)
		return old
	default:
		if len(next.items) < len(old.items) {
			return copyChild(next)
		}
		if next.kind == deltaObject {
			for index, key := range old.keys {
				if !bytes.Equal(key, next.keys[index]) {
					return copyChild(next)
				}
			}
			for index := len(old.keys); index < len(next.keys); index++ {
				old.keys = append(old.keys, bytes.Clone(next.keys[index]))
			}
		}
		for index := range next.items {
			if index < len(old.items) {
				old.items[index] = retainChild(old.items[index], next.items[index])
			} else {
				old.items = append(old.items, copyChild(next.items[index]))
			}
		}
		return old
	}
}

// copyChild returns a node that owns its bytes.
func copyChild(node *childNode) *childNode {
	out := &childNode{kind: node.kind, raw: bytes.Clone(node.raw)}
	if len(node.items) != 0 {
		out.items = make([]*childNode, len(node.items))
		for index, item := range node.items {
			out.items[index] = copyChild(item)
		}
	}
	if len(node.keys) != 0 {
		out.keys = make([][]byte, len(node.keys))
		for index, key := range node.keys {
			out.keys[index] = bytes.Clone(key)
		}
	}
	return out
}

// childUpdateStream is the message_update payload the extension last received for one session, and its number. Only the session's forward touches it, under eventMu.
type childUpdateStream struct {
	seq    uint64
	base   *childNode
	parser childParser
	// ops and path are the scratch of the operations and of the path being walked.
	ops, path []byte
}

// messageUpdatePrefix is how a session's message_update event begins: Pi's JSON writes `type` first.
var messageUpdatePrefix = []byte(`{"type":"message_update"`)

// frame returns what a message_update's notification carries beyond the event: for a keyframe (patch false) the event itself; for a patch, base, the number of the payload the operations apply to, and ops, the operations without their enclosing brackets, valid until the next frame. seq numbers the event. ok is false when the event travels whole, outside the stream.
func (s *childUpdateStream) frame(event []byte, seq uint64) (base uint64, ops []byte, patch, ok bool) {
	next, parsed := s.parser.parse(event)
	if !parsed {
		s.base = nil
		return 0, nil, false, false
	}
	previous, previousSeq := s.base, s.seq
	if previous != nil {
		// The operations come first: retaining next changes previous in place.
		if cap(s.path) < 256 {
			s.path = make([]byte, 0, 256)
		}
		s.ops = diffChild(s.ops[:0], s.path[:0], previous, next)
		ops, base, patch = s.ops, previousSeq, true
	}
	s.base, s.seq = retainChild(previous, next), seq
	return base, ops, patch, true
}
