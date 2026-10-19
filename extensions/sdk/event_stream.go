package sdk

import (
	"errors"
	"fmt"
	"math"
	"unsafe"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// eventStreamMsg is the request member that numbers one connection's payloads of an event name and key (EventStream in the host's protocol.go). A request with Args is a keyframe the extension keeps; one without Args applies Ops to the payload numbered Base. The handler receives the full payload either way. Evict names keys of the event whose payloads are dropped after this frame.
type eventStreamMsg struct {
	Seq   uint64          `json:"seq"`
	Base  uint64          `json:"base,omitempty"`
	Ops   json.RawMessage `json:"ops,omitempty"`
	Key   string          `json:"key,omitempty"`
	Evict []string        `json:"evict,omitempty"`
}

// eventReplica is the payload of an event name and key that this connection last received.
type eventReplica struct {
	seq  uint64
	root any
}

// applyEventStream replaces the request's payload with the full payload its stream member produces. The loop calls it as the frame is read, in arrival order, so the replica follows the host's frames whether or not a handler runs for them.
func (e *Extension) applyEventStream(req *requestMsg) {
	stream := req.Stream
	// A payload belongs to the sequence the event and the key name: tool_execution_update has one for each tool call.
	id := req.Event + "\x00" + stream.Key
	defer func() {
		for _, key := range stream.Evict {
			delete(e.eventReplicas, req.Event+"\x00"+key)
		}
	}()
	var root any
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &root); err != nil {
			delete(e.eventReplicas, id)
			req.streamErr = fmt.Errorf("event stream: decode keyframe: %w", err)
			return
		}
	} else {
		replica := e.eventReplicas[id]
		if replica == nil || replica.seq != stream.Base {
			delete(e.eventReplicas, id)
			req.streamErr = fmt.Errorf("%s delta applies to payload %d, the replica holds a different one", req.Event, stream.Base)
			return
		}
		var err error
		if root, err = applyStreamOps(replica.root, stream.Ops); err != nil {
			delete(e.eventReplicas, id)
			req.streamErr = err
			return
		}
	}
	if e.eventReplicas == nil {
		e.eventReplicas = map[string]*eventReplica{}
	}
	e.eventReplicas[id] = &eventReplica{seq: stream.Seq, root: root}
	data, _ := cloneStreamValue(root).(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	req.streamData = data
}

type streamOp struct {
	Op    string          `json:"op"`
	Path  []any           `json:"path"`
	Value json.RawMessage `json:"value"`
}

func applyStreamOps(root any, ops json.RawMessage) (any, error) {
	var list []streamOp
	if err := json.Unmarshal(ops, &list); err != nil {
		return nil, fmt.Errorf("event stream: decode ops: %w", err)
	}
	for _, op := range list {
		var value any
		if err := json.Unmarshal(op.Value, &value); err != nil {
			return nil, fmt.Errorf("event stream: decode value: %w", err)
		}
		var err error
		if root, err = applyStreamOp(root, op.Path, op.Op, value); err != nil {
			return nil, err
		}
	}
	return root, nil
}

var errStreamPath = errors.New("event stream: bad path")

// applyStreamOp returns node with the operation applied at path; a container is changed in place, and a slice that grew is returned for its parent to keep.
func applyStreamOp(node any, path []any, op string, value any) (any, error) {
	if len(path) == 0 {
		if op != "set" {
			return nil, errStreamPath
		}
		return value, nil
	}
	switch typed := node.(type) {
	case map[string]any:
		key, ok := path[0].(string)
		if !ok {
			return nil, errStreamPath
		}
		if len(path) == 1 {
			switch op {
			case "set":
				typed[key] = value
			case "append":
				appended, ok := appendStreamText(typed[key], value)
				if !ok {
					return nil, errors.New("event stream: append to a non-string")
				}
				typed[key] = appended
			default:
				return nil, fmt.Errorf("event stream: unknown operation %q", op)
			}
			return typed, nil
		}
		child, present := typed[key]
		if !present {
			return nil, errStreamPath
		}
		updated, err := applyStreamOp(child, path[1:], op, value)
		if err != nil {
			return nil, err
		}
		typed[key] = updated
		return typed, nil
	case []any:
		position, ok := path[0].(float64)
		if !ok || position < 0 || position != math.Trunc(position) || position > float64(len(typed)) {
			return nil, errStreamPath
		}
		index := int(position)
		if len(path) == 1 {
			switch op {
			case "set":
				if index == len(typed) {
					return append(typed, value), nil
				}
				typed[index] = value
			case "append":
				if index == len(typed) {
					return nil, errStreamPath
				}
				appended, ok := appendStreamText(typed[index], value)
				if !ok {
					return nil, errors.New("event stream: append to a non-string")
				}
				typed[index] = appended
			default:
				return nil, fmt.Errorf("event stream: unknown operation %q", op)
			}
			return typed, nil
		}
		if index == len(typed) {
			return nil, errStreamPath
		}
		updated, err := applyStreamOp(typed[index], path[1:], op, value)
		if err != nil {
			return nil, err
		}
		typed[index] = updated
		return typed, nil
	default:
		return nil, errStreamPath
	}
}

// streamText is a replica string that appends grow: its bytes are only ever added past the end, and a payload handed out holds a view of the prefix it had then, so no later append changes a string a handler kept. An append that outgrows the buffer copies it to one twice as large, so appending costs the delta, not the string.
type streamText struct {
	buf []byte
}

func (t *streamText) view() string {
	if len(t.buf) == 0 {
		return ""
	}
	return unsafe.String(&t.buf[0], len(t.buf)) //nolint:gosec // G103: bytes are only appended past the end, so the prefix a view covers never changes
}

// appendStreamText returns current with the string value appended, or false when either is not a string.
func appendStreamText(current, value any) (any, bool) {
	suffix, ok := value.(string)
	if !ok {
		return nil, false
	}
	switch typed := current.(type) {
	case *streamText:
		if needed := len(typed.buf) + len(suffix); needed > cap(typed.buf) {
			grown := make([]byte, len(typed.buf), max(2*cap(typed.buf), needed))
			copy(grown, typed.buf)
			typed.buf = grown
		}
		typed.buf = append(typed.buf, suffix...)
		return typed, true
	case string:
		buf := make([]byte, 0, 2*(len(typed)+len(suffix)))
		return &streamText{buf: append(append(buf, typed...), suffix...)}, true
	default:
		return nil, false
	}
}

// cloneStreamValue copies the containers of a decoded payload; strings are shared, as they are immutable, and an appended string is handed out as a view of its current bytes.
func cloneStreamValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, member := range typed {
			out[key] = cloneStreamValue(member)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, member := range typed {
			out[index] = cloneStreamValue(member)
		}
		return out
	case *streamText:
		return typed.view()
	default:
		return value
	}
}
