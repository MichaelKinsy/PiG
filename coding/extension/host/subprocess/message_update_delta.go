package subprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// A streamed assistant message reaches an extension process as one message_update per delta, and each carries the whole partial message twice (message and assistantMessageEvent.partial). Pi hands an in-process handler those objects by reference. Over the wire the host sends the first message_update of a connection whole (a keyframe) and every later one as the operations that turn the payload the extension last received into the new one; the runtime applies them to its copy and hands the handler a fresh full object, so a handler sees the same event it saw before. A streamed message of n deltas then costs O(n) bytes on the wire, not O(n^2).

// appendOpFraming is the text of an append operation around its value, for a path as deep as a tool call's argument.
const appendOpFraming = `{"op":"append","path":["message","content",0,"arguments","content"],"value":""}`

// bigStringBytes is the length from which a string is compared by value and sent as an append: a string shorter than the operation around its append is cheaper to send whole, as the JSON token the encoder wrote.
const bigStringBytes = len(appendOpFraming)

const (
	bigOpen  = "\x00pig-big:"
	bigClose = "\x00"
)

// bigTokenPrefix and bigTokenSuffix frame the JSON token a marked string encodes to.
var (
	bigTokenPrefix = []byte(`"\u0000pig-big:`)
	bigTokenSuffix = []byte(`\u0000"`)
)

type deltaKind uint8

const (
	deltaScalar deltaKind = iota // a number, boolean, null, or a short string, as the JSON token
	deltaBig                     // a long string, by value
	deltaObject
	deltaArray
)

// deltaNode is the JSON an event encoded to, with every long string held by value.
type deltaNode struct {
	kind  deltaKind
	raw   []byte
	str   string
	keys  [][]byte
	items []*deltaNode
}

var errDeltaUnsupported = errors.New("message_update delta: unsupported payload")

// sparse swaps the long strings of the snapshots it was handed for placeholders, so the event encodes to JSON whose size does not depend on them.
type sparse struct {
	table []string
	ok    bool
	// validated holds long strings known to be valid UTF-8: the previous payload's of the stream, then each this one validates.
	validated []string
	// strict has a short string checked for UTF-8 as a long one is: a payload whose encoder the keyframe does not share (a tool's partial result) must not write a lone unit differently from it.
	strict bool
}

func (s *sparse) str(value string) string {
	if len(value) < bigStringBytes {
		if strings.Contains(value, bigOpen) || (s.strict && !utf8.ValidString(value)) {
			s.ok = false
		}
		return value
	}
	// A string the encoders may write differently from one another (an unmatched UTF-16 unit) is sent whole by a keyframe.
	if !s.validUTF8(value) {
		s.ok = false
		return value
	}
	s.table = append(s.table, value)
	return bigOpen + strconv.Itoa(len(s.table)-1) + bigClose
}

// validUTF8 is utf8.ValidString(value) at the cost of the bytes no validated string covers. A streamed text grows in a buffer whose written bytes never change, so the text of a delta begins with the bytes of the previous delta's text: a validated string whose bytes start value's is a prefix of it, valid and ending on a code point boundary, and only the rest needs checking.
func (s *sparse) validUTF8(value string) bool {
	data := unsafe.StringData(value) //nolint:gosec // G103: compares string data addresses for identity; the pointers are never dereferenced
	covered := 0
	for _, known := range s.validated {
		if len(known) > covered && len(known) <= len(value) && unsafe.StringData(known) == data { //nolint:gosec // G103: compares string data addresses for identity; the pointers are never dereferenced
			covered = len(known)
		}
	}
	if !utf8.ValidString(value[covered:]) {
		return false
	}
	if covered < len(value) {
		s.validated = append(s.validated, value)
	}
	return true
}

// key accepts an object key that encodes as itself: a long key would be compared as a short string.
func (s *sparse) key(key string) {
	if len(key) >= bigStringBytes || strings.Contains(key, bigOpen) {
		s.ok = false
	}
}

func (s *sparse) value(value any) any {
	switch typed := value.(type) {
	case nil, bool, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return value
	case string:
		return s.str(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, member := range typed {
			s.key(key)
			out[key] = s.value(member)
		}
		return out
	case ai.JsonObject:
		return s.object(typed)
	case json.RawMessage, *ai.Usage:
		// Encoded as they are held: a raw value is not searched for long strings, so it is sent whole when it changes.
		return value
	case extension.WireMembers:
		names, members := typed.WireMembers()
		out := orderedWire{names: names, values: make([]any, len(members))}
		for index, member := range members {
			s.key(names[index])
			out.values[index] = s.value(member)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, member := range typed {
			out[index] = s.value(member)
		}
		return out
	default:
		s.ok = false
		return value
	}
}

func (s *sparse) object(object ai.JsonObject) ai.JsonObject {
	if object == nil {
		return nil
	}
	out := make(ai.JsonObject, len(object))
	for key, member := range object {
		s.key(key)
		out[key] = s.value(member)
	}
	return out
}

func (s *sparse) content(blocks []ai.AssistantContentBlock) []ai.AssistantContentBlock {
	if blocks == nil {
		return nil
	}
	out := make([]ai.AssistantContentBlock, len(blocks))
	for index, block := range blocks {
		out[index] = s.block(block)
	}
	return out
}

func (s *sparse) block(block ai.AssistantContentBlock) ai.AssistantContentBlock {
	switch typed := block.(type) {
	case ai.TextContent:
		typed.Text = s.str(typed.Text)
		typed.TextSignature = s.str(typed.TextSignature)
		return typed
	case ai.ThinkingContent:
		typed.Thinking = s.str(typed.Thinking)
		typed.ThinkingSignature = s.str(typed.ThinkingSignature)
		return typed
	case ai.ToolCall:
		typed.Arguments = s.object(typed.Arguments)
		typed.ThoughtSignature = s.str(typed.ThoughtSignature)
		return typed
	default:
		s.ok = false
		return block
	}
}

// aiMessage returns a copy of an owned snapshot with its long strings swapped.
func (s *sparse) aiMessage(message *ai.AssistantMessage) *ai.AssistantMessage {
	if message == nil {
		return nil
	}
	out := *message
	out.Content = s.content(message.Content)
	out.ErrorMessage = s.str(message.ErrorMessage)
	return &out
}

func (s *sparse) agentMessage(message *agent.AssistantMessage) *agent.AssistantMessage {
	out := *message
	out.Content = s.content(message.Content)
	out.ErrorMessage = s.str(message.ErrorMessage)
	return &out
}

// eventPartial applies swap to the message an assistant event carries and returns the event holding the result. ok is false for an event the delta does not know.
func eventPartial(event any, swap func(*ai.AssistantMessage) *ai.AssistantMessage) (any, bool) {
	switch typed := event.(type) {
	case ai.StartEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.TextStartEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.TextDeltaEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.TextEndEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.ThinkingStartEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.ThinkingDeltaEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.ThinkingEndEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.ToolCallStartEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.ToolCallDeltaEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.ToolCallEndEvent:
		typed.Partial = swap(typed.Partial)
		return typed, true
	case ai.DoneEvent:
		typed.Message = swap(typed.Message)
		return typed, true
	case ai.ErrorEvent:
		typed.Error = swap(typed.Error)
		return typed, true
	default:
		return nil, false
	}
}

// sparseEventFields swaps the long strings an assistant event carries besides its message.
func (s *sparse) sparseEventFields(event any) any {
	switch typed := event.(type) {
	case ai.TextEndEvent:
		typed.Content = s.str(typed.Content)
		return typed
	case ai.ThinkingEndEvent:
		typed.Content = s.str(typed.Content)
		return typed
	case ai.ToolCallEndEvent:
		typed.ToolCall = s.block(typed.ToolCall).(ai.ToolCall)
		return typed
	case ai.TextDeltaEvent:
		typed.Delta = s.str(typed.Delta)
		return typed
	case ai.ThinkingDeltaEvent:
		typed.Delta = s.str(typed.Delta)
		return typed
	case ai.ToolCallDeltaEvent:
		typed.Delta = s.str(typed.Delta)
		return typed
	default:
		return event
	}
}

// messageUpdateSnapshot is an event frozen into messages nobody else holds.
type messageUpdateSnapshot struct {
	event extension.MessageUpdateEvent
}

// snapshotMessageUpdate freezes the event: its two messages are observed once, at this call, and its payload is encoded from the copies. ok is false when the event is not an assistant message update the delta can carry.
func snapshotMessageUpdate(event extension.MessageUpdateEvent) (messageUpdateSnapshot, bool) {
	if event.Message.Assistant == nil {
		return messageUpdateSnapshot{}, false
	}
	frozen, ok := eventPartial(event.AssistantMessageEvent, (*ai.AssistantMessage).Observe)
	if !ok {
		return messageUpdateSnapshot{}, false
	}
	event.Message = agent.AgentMessage{Assistant: event.Message.Assistant.Observe()}
	event.AssistantMessageEvent = frozen
	return messageUpdateSnapshot{event: event}, true
}

// sparsePayload encodes the frozen event with its long strings swapped and parses the result. validated holds long strings already known to be valid UTF-8.
func (f messageUpdateSnapshot) sparsePayload(validated []string) (*deltaNode, []string, error) {
	// The previous payload's table is only read: what this payload validates is appended to a copy.
	s := &sparse{ok: true, validated: validated[:len(validated):len(validated)]}
	event := f.event
	event.Message = agent.AgentMessage{Assistant: s.agentMessage(f.event.Message.Assistant)}
	swapped, _ := eventPartial(f.event.AssistantMessageEvent, s.aiMessage)
	event.AssistantMessageEvent = s.sparseEventFields(swapped)
	if !s.ok {
		return nil, nil, errDeltaUnsupported
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, nil, err
	}
	parser := deltaParser{data: encoded, table: s.table, used: make([]bool, len(s.table))}
	root, err := parser.parse()
	if err != nil {
		return nil, nil, err
	}
	return root, s.table, nil
}

type deltaParser struct {
	data  []byte
	pos   int
	table []string
	used  []bool
	// nodes, items and keys are slabs the tree's nodes and their member lists are cut from, so a payload of many small nodes costs a few allocations.
	nodes []deltaNode
	items []*deltaNode
	keys  [][]byte
}

// Sizes of the slabs a parse cuts from, and of the member list a node starts with.
const (
	parseNodeSlab  = 32
	parseListSlab  = 64
	parseListStart = 4
)

func (p *deltaParser) newNode(kind deltaKind) *deltaNode {
	if len(p.nodes) == 0 {
		p.nodes = make([]deltaNode, parseNodeSlab)
	}
	node := &p.nodes[0]
	p.nodes = p.nodes[1:]
	node.kind = kind
	return node
}

// itemList returns an empty member list with room for parseListStart members; one that outgrows it moves to an array of its own.
func (p *deltaParser) itemList() []*deltaNode {
	if len(p.items) < parseListStart {
		p.items = make([]*deltaNode, parseListSlab)
	}
	list := p.items[:0:parseListStart]
	p.items = p.items[parseListStart:]
	return list
}

func (p *deltaParser) keyList() [][]byte {
	if len(p.keys) < parseListStart {
		p.keys = make([][]byte, parseListSlab)
	}
	list := p.keys[:0:parseListStart]
	p.keys = p.keys[parseListStart:]
	return list
}

func (p *deltaParser) parse() (*deltaNode, error) {
	node, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.data) {
		return nil, errDeltaUnsupported
	}
	// Every long string appears once: a placeholder the encoder dropped would leave its text out of the payload.
	for _, used := range p.used {
		if !used {
			return nil, errDeltaUnsupported
		}
	}
	return node, nil
}

func (p *deltaParser) space() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\n', '\r', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *deltaParser) value() (*deltaNode, error) {
	p.space()
	if p.pos >= len(p.data) {
		return nil, errDeltaUnsupported
	}
	switch p.data[p.pos] {
	case '{':
		p.pos++
		node := p.newNode(deltaObject)
		p.space()
		if p.pos < len(p.data) && p.data[p.pos] == '}' {
			p.pos++
			return node, nil
		}
		node.keys, node.items = p.keyList(), p.itemList()
		for {
			p.space()
			key, err := p.token()
			if err != nil || len(key) == 0 || key[0] != '"' {
				return nil, errDeltaUnsupported
			}
			p.space()
			if p.pos >= len(p.data) || p.data[p.pos] != ':' {
				return nil, errDeltaUnsupported
			}
			p.pos++
			member, err := p.value()
			if err != nil {
				return nil, err
			}
			node.keys = append(node.keys, key)
			node.items = append(node.items, member)
			p.space()
			if p.pos >= len(p.data) {
				return nil, errDeltaUnsupported
			}
			switch p.data[p.pos] {
			case ',':
				p.pos++
			case '}':
				p.pos++
				return node, nil
			default:
				return nil, errDeltaUnsupported
			}
		}
	case '[':
		p.pos++
		node := p.newNode(deltaArray)
		p.space()
		if p.pos < len(p.data) && p.data[p.pos] == ']' {
			p.pos++
			return node, nil
		}
		node.items = p.itemList()
		for {
			member, err := p.value()
			if err != nil {
				return nil, err
			}
			node.items = append(node.items, member)
			p.space()
			if p.pos >= len(p.data) {
				return nil, errDeltaUnsupported
			}
			switch p.data[p.pos] {
			case ',':
				p.pos++
			case ']':
				p.pos++
				return node, nil
			default:
				return nil, errDeltaUnsupported
			}
		}
	default:
		raw, err := p.token()
		if err != nil {
			return nil, err
		}
		if raw[0] == '"' && bytes.HasPrefix(raw, bigTokenPrefix) {
			index, ok := p.bigIndex(raw)
			if !ok {
				return nil, errDeltaUnsupported
			}
			node := p.newNode(deltaBig)
			node.str = p.table[index]
			return node, nil
		}
		if raw[0] == '"' && bytes.Contains(raw, []byte("pig-big")) {
			return nil, errDeltaUnsupported
		}
		node := p.newNode(deltaScalar)
		node.raw = raw
		return node, nil
	}
}

// bigIndex reads the table index a placeholder token names, once.
func (p *deltaParser) bigIndex(raw []byte) (int, bool) {
	if !bytes.HasSuffix(raw, bigTokenSuffix) || len(raw) < len(bigTokenPrefix)+len(bigTokenSuffix)+1 {
		return 0, false
	}
	digits := raw[len(bigTokenPrefix) : len(raw)-len(bigTokenSuffix)]
	index, err := strconv.Atoi(string(digits))
	if err != nil || index < 0 || index >= len(p.table) || p.used[index] {
		return 0, false
	}
	p.used[index] = true
	return index, true
}

// token reads a string or a bare scalar and returns its JSON text.
func (p *deltaParser) token() ([]byte, error) {
	p.space()
	start := p.pos
	if p.pos >= len(p.data) {
		return nil, errDeltaUnsupported
	}
	if p.data[p.pos] == '"' {
		p.pos++
		for p.pos < len(p.data) {
			switch p.data[p.pos] {
			case '\\':
				p.pos += 2
				continue
			case '"':
				p.pos++
				return p.data[start:p.pos], nil
			}
			p.pos++
		}
		return nil, errDeltaUnsupported
	}
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ',', '}', ']', ' ', '\n', '\r', '\t', ':':
			if p.pos == start {
				return nil, errDeltaUnsupported
			}
			return p.data[start:p.pos], nil
		}
		p.pos++
	}
	if p.pos == start {
		return nil, errDeltaUnsupported
	}
	return p.data[start:p.pos], nil
}

// encode writes the JSON the node holds.
func (n *deltaNode) encode(out []byte) []byte {
	switch n.kind {
	case deltaScalar:
		return append(out, n.raw...)
	case deltaBig:
		return appendJSONString(out, n.str)
	case deltaObject:
		out = append(out, '{')
		for index, key := range n.keys {
			if index > 0 {
				out = append(out, ',')
			}
			out = append(out, key...)
			out = append(out, ':')
			out = n.items[index].encode(out)
		}
		return append(out, '}')
	default:
		out = append(out, '[')
		for index, item := range n.items {
			if index > 0 {
				out = append(out, ',')
			}
			out = item.encode(out)
		}
		return append(out, ']')
	}
}

// appendJSONString writes value as a JSON string. A string reaching here is valid UTF-8.
func appendJSONString(out []byte, value string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return append(out, encoded...)
}

// diffPathCap is the room a diff's path buffer starts with: a payload's paths are a few elements deep.
//
// pig additive (D112): Pi loads extensions in process and has no delta frames; the cap only sizes a host-side path buffer.
const diffPathCap = 128

// diffDelta appends the operations that turn old into new. path is the path of old, in a buffer with room for the paths below it. A path element is a quoted key or an array index.
func diffDelta(ops []byte, path []byte, old, next *deltaNode) []byte {
	if old.kind != next.kind {
		return appendSetOp(ops, path, next)
	}
	switch next.kind {
	case deltaScalar:
		if bytes.Equal(old.raw, next.raw) {
			return ops
		}
		return appendSetOp(ops, path, next)
	case deltaBig:
		if old.str == next.str {
			return ops
		}
		// Every long string passed the UTF-8 check (sparse.str), so an append never splits a code point and the runtime's string, extended by the tail, is the full payload's.
		if len(next.str) > len(old.str) && extends(next.str, old.str) {
			return appendOp(ops, "append", path, appendJSONString(nil, next.str[len(old.str):]))
		}
		return appendSetOp(ops, path, next)
	case deltaObject:
		if len(next.keys) < len(old.keys) {
			return appendSetOp(ops, path, next)
		}
		for index, key := range old.keys {
			if !bytes.Equal(key, next.keys[index]) {
				return appendSetOp(ops, path, next)
			}
		}
		for index := range next.keys {
			member := pushMemberPath(path, next.keys[index])
			if index < len(old.keys) {
				ops = diffDelta(ops, member, old.items[index], next.items[index])
			} else {
				ops = appendSetOp(ops, member, next.items[index])
			}
			popMemberPath(path)
		}
		return ops
	default:
		if len(next.items) < len(old.items) {
			return appendSetOp(ops, path, next)
		}
		var digits [20]byte
		for index := range next.items {
			member := pushMemberPath(path, strconv.AppendInt(digits[:0], int64(index), 10))
			if index < len(old.items) {
				ops = diffDelta(ops, member, old.items[index], next.items[index])
			} else {
				ops = appendSetOp(ops, member, next.items[index])
			}
			popMemberPath(path)
		}
		return ops
	}
}

// extends reports whether next begins with old. A streamed text that grew in place shares old's bytes, which no string's owner changes, so the comparison costs nothing; any other string is compared.
func extends(next, old string) bool {
	if unsafe.StringData(next) == unsafe.StringData(old) { //nolint:gosec // G103: compares string data addresses for identity; the pointers are never dereferenced
		return true
	}
	return strings.HasPrefix(next, old)
}

// pushMemberPath returns the path of the member element of the value at path, a JSON array of its elements, in path's own array when it has room. The caller pops the member once it is done with it (popMemberPath), before it pushes the next sibling, and never keeps path or the member beyond that: diffDelta copies a path into the operation it writes.
func pushMemberPath(path []byte, element []byte) []byte {
	if len(path) == 0 {
		path = append(path, '[')
	} else {
		path = append(path[:len(path)-1], ',')
	}
	path = append(path, element...)
	return append(path, ']')
}

// popMemberPath restores the closing bracket pushMemberPath of a member overwrote in path's array.
func popMemberPath(path []byte) {
	if len(path) > 0 {
		path[len(path)-1] = ']'
	}
}

func appendSetOp(ops []byte, path []byte, value *deltaNode) []byte {
	return appendOp(ops, "set", path, value.encode(nil))
}

func appendOp(ops []byte, name string, path []byte, value []byte) []byte {
	if len(ops) > 0 {
		ops = append(ops, ',')
	}
	ops = append(ops, `{"op":"`...)
	ops = append(ops, name...)
	ops = append(ops, `","path":`...)
	if len(path) == 0 {
		ops = append(ops, "[]"...)
	} else {
		ops = append(ops, path...)
	}
	ops = append(ops, `,"value":`...)
	ops = append(ops, value...)
	return append(ops, '}')
}

// messageUpdateStream is the payload an extension connection last received for message_update, which the next delta applies to. One mutex orders the frames of the connection: a patch is computed against the state the previous frame left and queued before the next one is computed.
type messageUpdateStream struct {
	mu   sync.Mutex
	conn *Conn
	seq  uint64
	base *deltaNode
	// validated holds the long strings of the last payload prepared, all valid UTF-8; the next payload's texts extend them.
	validated []string
}

// prepared is a message_update payload ready to queue.
type preparedMessageUpdate struct {
	args   json.RawMessage
	stream *EventStream
	next   *deltaNode
	seq    uint64
}

// prepare is prepareShared for a stream that shares no payload with other extensions.
func (m *messageUpdateStream) prepare(conn *Conn, event extension.MessageUpdateEvent) (prepared preparedMessageUpdate, ok bool, err error) {
	return m.prepareShared(nil, conn, event)
}

// prepareShared encodes the event for conn as a keyframe or a patch, reusing what share holds for the same event at the same publication. The caller holds the stream's mutex until the frame is queued and then calls sent. A false ok means the event is sent whole and outside the stream.
func (m *messageUpdateStream) prepareShared(share *messageUpdateShare, conn *Conn, event extension.MessageUpdateEvent) (prepared preparedMessageUpdate, ok bool, err error) {
	base := m.base
	if m.conn != conn {
		base = nil
	}
	frame, ok, err := share.frame(event, m.validated, base)
	if err != nil || !ok {
		return preparedMessageUpdate{}, false, err
	}
	m.validated = frame.validated
	seq := m.seq + 1
	if base == nil {
		return preparedMessageUpdate{args: frame.args, stream: &EventStream{Seq: seq}, next: frame.next, seq: seq}, true, nil
	}
	return preparedMessageUpdate{stream: &EventStream{Seq: seq, Base: m.seq, Ops: frame.ops}, next: frame.next, seq: seq}, true, nil
}

// messageUpdateShare is the message_update payload a host prepared last. The host hands every extension subscribed to message_update the same event. While neither of its messages has published since (ai.AssistantMessage.Publication), the frozen event, its sparse payload and its keyframe are the same for each extension, and so is the patch against the same base, so they are prepared once per publication rather than once per extension. An event whose messages are not live views, or that holds a mutable value (toolcall_end's tool call), is prepared for each extension.
type messageUpdateShare struct {
	mu      sync.Mutex
	key     messageUpdateKey
	payload *messageUpdatePayload
	// prepared counts the payloads prepared.
	prepared uint64
}

// messageUpdateKey is an event and the publications its two messages observed.
type messageUpdateKey struct {
	kind        string
	message     *agent.AssistantMessage
	event       any
	messageCell uintptr
	messageSeq  uint64
	partialCell uintptr
	partialSeq  uint64
}

// messageUpdateKeyOf returns the key of event at this call. ok is false when the event's frozen payload is not a function of its key: a message that is not a live view, or an event the key cannot compare.
func messageUpdateKeyOf(event extension.MessageUpdateEvent) (messageUpdateKey, bool) {
	partial, ok := comparableEventPartial(event.AssistantMessageEvent)
	if !ok || event.Message.Assistant == nil {
		return messageUpdateKey{}, false
	}
	key := messageUpdateKey{kind: event.Type, message: event.Message.Assistant, event: event.AssistantMessageEvent}
	key.messageCell, key.messageSeq = event.Message.Assistant.Publication()
	key.partialCell, key.partialSeq = partial.Publication()
	return key, key.messageCell != 0 && key.partialCell != 0
}

// comparableEventPartial returns the message of an assistant event whose other fields are immutable values that == compares.
func comparableEventPartial(event any) (*ai.AssistantMessage, bool) {
	switch typed := event.(type) {
	case ai.StartEvent:
		return typed.Partial, true
	case ai.TextStartEvent:
		return typed.Partial, true
	case ai.TextDeltaEvent:
		return typed.Partial, true
	case ai.TextEndEvent:
		return typed.Partial, true
	case ai.ThinkingStartEvent:
		return typed.Partial, true
	case ai.ThinkingDeltaEvent:
		return typed.Partial, true
	case ai.ThinkingEndEvent:
		return typed.Partial, true
	case ai.ToolCallStartEvent:
		return typed.Partial, true
	case ai.ToolCallDeltaEvent:
		return typed.Partial, true
	case ai.DoneEvent:
		return typed.Message, true
	case ai.ErrorEvent:
		return typed.Error, true
	default:
		return nil, false
	}
}

// messageUpdatePayload is an event frozen and encoded for the delta, with the frames made from it so far.
type messageUpdatePayload struct {
	// carried is false for an event the delta does not carry: it is sent whole, outside the stream.
	carried   bool
	frozen    messageUpdateSnapshot
	next      *deltaNode
	validated []string
	args      json.RawMessage
	patchBase *deltaNode
	patch     []byte
}

// messageUpdateFrame is the keyframe (args) or the patch (ops) of a payload for one extension.
type messageUpdateFrame struct {
	args      json.RawMessage
	ops       []byte
	next      *deltaNode
	validated []string
}

// prepareMessageUpdatePayload freezes and encodes event. validated holds long strings already known to be valid UTF-8.
func prepareMessageUpdatePayload(event extension.MessageUpdateEvent, validated []string) (*messageUpdatePayload, error) {
	frozen, ok := snapshotMessageUpdate(event)
	if !ok {
		return &messageUpdatePayload{}, nil
	}
	next, validated, err := frozen.sparsePayload(validated)
	if err != nil {
		if !errors.Is(err, errDeltaUnsupported) {
			return nil, err
		}
		// The payload cannot be a delta: send it whole, outside the stream, and keep the runtime's copy.
		return &messageUpdatePayload{}, nil
	}
	return &messageUpdatePayload{carried: true, frozen: frozen, next: next, validated: validated}, nil
}

// frame returns the keyframe of event when base is nil and otherwise the patch from base. A nil share prepares the payload for this call alone. ok is false when the event is sent whole, outside the stream.
func (share *messageUpdateShare) frame(event extension.MessageUpdateEvent, validated []string, base *deltaNode) (messageUpdateFrame, bool, error) {
	if share == nil {
		payload, err := prepareMessageUpdatePayload(event, validated)
		if err != nil {
			return messageUpdateFrame{}, false, err
		}
		return payload.frame(base)
	}
	share.mu.Lock()
	defer share.mu.Unlock()
	key, shareable := messageUpdateKeyOf(event)
	payload := share.payload
	if !shareable || payload == nil || share.key != key {
		var err error
		if payload, err = prepareMessageUpdatePayload(event, validated); err != nil {
			return messageUpdateFrame{}, false, err
		}
		share.prepared++
		share.key, share.payload = messageUpdateKey{}, nil
		// A publication while the messages were observed leaves a snapshot of no single state: it is not shared.
		if after, _ := messageUpdateKeyOf(event); shareable && after == key {
			share.key, share.payload = key, payload
		}
	}
	return payload.frame(base)
}

func (payload *messageUpdatePayload) frame(base *deltaNode) (messageUpdateFrame, bool, error) {
	if !payload.carried {
		return messageUpdateFrame{}, false, nil
	}
	frame := messageUpdateFrame{next: payload.next, validated: payload.validated}
	if base == nil {
		if payload.args == nil {
			args, err := json.Marshal(payload.frozen.event)
			if err != nil {
				return messageUpdateFrame{}, false, err
			}
			payload.args = args
		}
		frame.args = payload.args
		return frame, true, nil
	}
	if payload.patch == nil || payload.patchBase != base {
		payload.patchBase, payload.patch = base, append(append([]byte{'['}, diffDelta(nil, make([]byte, 0, diffPathCap), base, payload.next)...), ']')
	}
	frame.ops = payload.patch
	return frame, true, nil
}

// sent records the outcome of queueing the frame. A frame that may not have reached the runtime leaves no baseline, so the next one is a keyframe.
func (m *messageUpdateStream) sent(conn *Conn, prepared preparedMessageUpdate, err error) {
	if err != nil {
		m.conn, m.base, m.seq = nil, nil, prepared.seq
		return
	}
	m.conn, m.base, m.seq = conn, prepared.next, prepared.seq
}

// streamedMessageUpdate returns the event when it travels as a delta: an assistant message_update with no boundary wire.
func streamedMessageUpdate(name string, event any, boundary *boundarywire.Cache) *extension.MessageUpdateEvent {
	if name != "message_update" || boundary != nil {
		return nil
	}
	switch typed := event.(type) {
	case extension.MessageUpdateEvent:
		return &typed
	case *extension.MessageUpdateEvent:
		if typed != nil {
			return typed
		}
	}
	return nil
}

// order returns the send order that fills request with the keyframe or patch for update, sharing the payload through share with the host's other extensions. An event the delta cannot carry is sent whole, outside the stream, and leaves the extension's copy as it is.
func (m *messageUpdateStream) order(share *messageUpdateShare, conn *Conn, update extension.MessageUpdateEvent, request *RequestPayload, original any, boundary *boundarywire.Cache) *sendOrder {
	var prepared preparedMessageUpdate
	var inStream bool
	return &sendOrder{
		mu: &m.mu,
		prepare: func() error {
			var err error
			if prepared, inStream, err = m.prepareShared(share, conn, update); err != nil {
				return fmt.Errorf("marshal event args: %w", err)
			}
			if !inStream {
				request.Stream = nil
				request.Args, err = json.Marshal(boundaryEventWire(wireEventPayload(original), boundary))
				if err != nil {
					return fmt.Errorf("marshal event args: %w", err)
				}
				return nil
			}
			request.Args, request.Stream = prepared.args, prepared.stream
			return nil
		},
		sent: func(err error) {
			if inStream {
				m.sent(conn, prepared, err)
			}
		},
	}
}

// invalidate forgets the extension's copy, so the next payload is a keyframe.
func (m *messageUpdateStream) invalidate() {
	m.mu.Lock()
	m.conn, m.base = nil, nil
	m.mu.Unlock()
}
