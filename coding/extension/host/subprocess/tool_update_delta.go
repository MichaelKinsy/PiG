package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
)

// A tool that reports partial results hands each tool_execution_update the whole result so far, and Pi's handler receives that object by reference. A tool that prints M bytes in n chunks would send an extension process n frames that grow to M bytes: O(n*M) bytes in all. The host sends the first update of a tool call to an extension whole (a keyframe) and every later one as the operations that turn the payload the extension last received for that call into the new one, as message_update does (message_update_delta.go); the runtime applies them to its copy and hands each handler a fresh full object, so a handler sees the same partialResult it saw before. An update then costs O(what it added) on the wire and, to encode, the structure of the result around its long strings.

// toolUpdateStreamCap is the number of tool calls whose last update the host keeps for an extension. A call's replica is released when the host evicts it (EventStream.Evict), because the host sees a call end only when the extension subscribed to tool_execution_end.
const toolUpdateStreamCap = 8 // pig additive (D112): a bounded replica cache; an evicted call gets a whole keyframe again, so the extension sees the same payloads

// orderedWire is an object whose members were swapped for the delta and encode as the wire value they stand for (extension.WireMembers).
type orderedWire struct {
	names  []string
	values []any
}

// MarshalJSON writes the members with the extension codec, as the value they replace writes its own.
func (o orderedWire) MarshalJSON() ([]byte, error) {
	var out []byte
	out = append(out, '{')
	for index, name := range o.names {
		key, err := sdkjson.Marshal(name) //perf:ok each member is encoded once, in order; there is no outer re-encode
		if err != nil {
			return nil, err
		}
		value, err := sdkjson.Marshal(o.values[index]) //perf:ok each member is encoded once, in order; there is no outer re-encode
		if err != nil {
			return nil, err
		}
		if index > 0 {
			out = append(out, ',')
		}
		out = append(out, key...)
		out = append(out, ':')
		out = append(out, value...)
	}
	return append(out, '}'), nil
}

// streamedToolUpdate returns the event when it travels as a delta: a tool_execution_update with no boundary wire.
func streamedToolUpdate(name string, event any, boundary *boundarywire.Cache) *extension.ToolExecutionUpdateEvent {
	if name != "tool_execution_update" || boundary != nil {
		return nil
	}
	switch typed := event.(type) {
	case extension.ToolExecutionUpdateEvent:
		return &typed
	case *extension.ToolExecutionUpdateEvent:
		if typed != nil {
			return typed
		}
	}
	return nil
}

// sparseToolUpdate encodes the event with its long strings swapped and parses the result. validated holds long strings already known to be valid UTF-8.
func sparseToolUpdate(event extension.ToolExecutionUpdateEvent, validated []string) (*deltaNode, []string, error) {
	// The previous payload's table is only read: what this payload validates is appended to a copy.
	s := &sparse{ok: true, strict: true, validated: validated[:len(validated):len(validated)]}
	event.Type = s.str(event.Type)
	event.ToolCallID = s.str(event.ToolCallID)
	event.ToolName = s.str(event.ToolName)
	event.ParentToolCallID = s.str(event.ParentToolCallID)
	if event.WireArgs == nil {
		event.Args = s.value(event.Args)
	}
	if event.WirePartialResult != nil {
		event.WirePartialResult = s.value(event.WirePartialResult)
	} else {
		event.PartialResult = s.value(event.PartialResult)
	}
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

// toolUpdateStreams are the payloads an extension connection last received for tool_execution_update, one for each tool call, which the next update of the call applies to. One mutex orders the frames of the connection, as messageUpdateStream's does.
type toolUpdateStreams struct {
	mu      sync.Mutex
	conn    *Conn
	streams map[string]*toolUpdateState
	// recent lists the calls of streams, the least recently updated first.
	recent []string
}

// toolUpdateState is the payload a connection last received for one tool call.
type toolUpdateState struct {
	seq  uint64
	base *deltaNode
	// validated holds the long strings of the last payload prepared, all valid UTF-8; the next payload's texts extend them.
	validated []string
}

// preparedToolUpdate is a tool_execution_update payload ready to queue.
type preparedToolUpdate struct {
	key   string
	seq   uint64
	next  *deltaNode
	valid []string
	evict string
	// evicts is false when no call is released.
	evicts bool
}

// order returns the send order that fills request with the keyframe or patch for update. An event the delta cannot carry is sent whole, outside the stream, and leaves the extension's copy as it is.
func (m *toolUpdateStreams) order(conn *Conn, update extension.ToolExecutionUpdateEvent, request *RequestPayload, original any, boundary *boundarywire.Cache) *sendOrder {
	var prepared preparedToolUpdate
	var inStream bool
	return &sendOrder{
		mu: &m.mu,
		prepare: func() error {
			var err error
			if prepared, request.Args, request.Stream, inStream, err = m.prepare(conn, update, original, boundary); err != nil {
				return fmt.Errorf("marshal event args: %w", err)
			}
			return nil
		},
		sent: func(err error) {
			if inStream {
				m.sent(conn, prepared, err)
			}
		},
	}
}

// prepare encodes the event for conn as a keyframe or a patch. The caller holds the mutex until the frame is queued and then calls sent. A false inStream means the event is sent whole, outside the stream.
func (m *toolUpdateStreams) prepare(conn *Conn, update extension.ToolExecutionUpdateEvent, original any, boundary *boundarywire.Cache) (prepared preparedToolUpdate, args json.RawMessage, stream *EventStream, inStream bool, err error) {
	key := update.ToolCallID
	var state toolUpdateState
	if held := m.streams[key]; held != nil && m.conn == conn {
		state = *held
	}
	next, valid, err := sparseToolUpdate(update, state.validated)
	if err != nil {
		if !errors.Is(err, errDeltaUnsupported) {
			return preparedToolUpdate{}, nil, nil, false, err
		}
		// The payload cannot be a delta: send it whole, outside the stream, and keep the runtime's copy.
		whole, err := marshalEventWire(boundaryEventWire(wireEventPayload(original), boundary))
		return preparedToolUpdate{}, whole, nil, false, err
	}
	prepared = preparedToolUpdate{key: key, seq: state.seq + 1, next: next, valid: valid}
	stream = &EventStream{Seq: prepared.seq, Key: key}
	if state.base == nil {
		if m.conn == conn && len(m.recent) >= toolUpdateStreamCap {
			prepared.evict, prepared.evicts = m.recent[0], true
			stream.Evict = []string{prepared.evict}
		}
		args, err = marshalEventWire(boundaryEventWire(wireEventPayload(original), boundary))
		return prepared, args, stream, true, err
	}
	stream.Base = state.seq
	stream.Ops = append(append([]byte{'['}, diffDelta(nil, nil, state.base, next)...), ']')
	return prepared, nil, stream, true, nil
}

// sent records the outcome of queueing the frame. A frame that may not have reached the runtime leaves no baseline, so the next one of every call is a keyframe.
func (m *toolUpdateStreams) sent(conn *Conn, prepared preparedToolUpdate, err error) {
	if err != nil {
		m.reset(nil)
		return
	}
	if m.conn != conn {
		m.reset(conn)
	}
	if prepared.evicts {
		delete(m.streams, prepared.evict)
		m.recent = slices.DeleteFunc(m.recent, func(key string) bool { return key == prepared.evict })
	}
	if _, held := m.streams[prepared.key]; held {
		m.recent = slices.DeleteFunc(m.recent, func(key string) bool { return key == prepared.key })
	}
	m.recent = append(m.recent, prepared.key)
	m.streams[prepared.key] = &toolUpdateState{seq: prepared.seq, base: prepared.next, validated: prepared.valid}
}

// reset forgets every call's copy and binds the streams to conn.
func (m *toolUpdateStreams) reset(conn *Conn) {
	m.conn = conn
	m.streams = map[string]*toolUpdateState{}
	m.recent = nil
}

// invalidate forgets the extension's copies, so the next payload of every call is a keyframe.
func (m *toolUpdateStreams) invalidate() {
	m.mu.Lock()
	m.reset(nil)
	m.mu.Unlock()
}
