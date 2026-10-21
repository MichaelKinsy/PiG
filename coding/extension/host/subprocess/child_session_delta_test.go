package subprocess

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// applyChildOps is the extension's side of the stream, spelled apart from the host's encoder: it decodes the payload and applies set and append at their paths.
func applyChildOps(t *testing.T, root any, ops []byte) any {
	t.Helper()
	var list []struct {
		Op    string          `json:"op"`
		Path  []any           `json:"path"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(ops, &list); err != nil {
		t.Fatalf("ops %s: %v", ops, err)
	}
	for _, op := range list {
		var value any
		if err := json.Unmarshal(op.Value, &value); err != nil {
			t.Fatalf("value %s: %v", op.Value, err)
		}
		root = setChildPath(t, root, op.Path, op.Op, value)
	}
	return root
}

// setChildPath returns node with the operation applied at path; a set at an array's length appends, which its parent must keep.
func setChildPath(t *testing.T, node any, path []any, op string, value any) any {
	t.Helper()
	if len(path) == 0 {
		return value
	}
	switch container := node.(type) {
	case map[string]any:
		key := path[0].(string)
		if len(path) == 1 && op == "append" {
			value = container[key].(string) + value.(string)
		}
		container[key] = setChildPath(t, container[key], path[1:], op, value)
		return container
	case []any:
		index := int(path[0].(float64))
		if index == len(container) {
			if len(path) != 1 || op != "set" {
				t.Fatalf("bad path %v", path)
			}
			return append(container, value)
		}
		if len(path) == 1 && op == "append" {
			value = container[index].(string) + value.(string)
		}
		container[index] = setChildPath(t, container[index], path[1:], op, value)
		return container
	}
	t.Fatalf("path %v through %T", path, node)
	return nil
}

// TestChildUpdateStreamReproducesEveryEvent feeds the stream a run of message_update events and checks that the extension's replica, built from the keyframe and the operations alone, equals each event.
func TestChildUpdateStreamReproducesEveryEvent(t *testing.T) {
	long := strings.Repeat("é\\\"line\n\t", 12)
	emoji := strings.Repeat("a", 90) + "\U0001F600"
	events := []map[string]any{
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": ""}}}, "n": 1},
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": long}}}, "n": 2},
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": long + long}}}, "n": 3},
		// a second block, and a key the first did not have
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": long + long}, map[string]any{"type": "thinking"}}, "extra": true}, "n": 4},
		// shrink, and a changed (not grown) long text
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.ToUpper(long)}}}, "n": 5},
		// a long text with a UTF-16 pair travels whole
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": emoji}}}, "n": 6},
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": emoji + "tail"}}}, "n": 7},
		{"type": "message_update", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": emoji + "tail" + long}}}, "n": 8},
		// the keys reorder
		{"type": "message_update", "n": 9, "message": map[string]any{"content": []any{}}},
	}
	var stream childUpdateStream
	var replica any
	var replicaSeq uint64
	for index, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		seq := uint64(index + 10)
		base, ops, patch, ok := stream.frame(encoded, seq)
		switch {
		case !ok:
			if err := json.Unmarshal(encoded, &replica); err != nil {
				t.Fatal(err)
			}
			replicaSeq = 0
		case !patch:
			if err := json.Unmarshal(encoded, &replica); err != nil {
				t.Fatal(err)
			}
			replicaSeq = seq
		default:
			if base != replicaSeq {
				t.Fatalf("event %d: base %d, replica %d", index, base, replicaSeq)
			}
			replica = applyChildOps(t, replica, append(append([]byte{'['}, ops...), ']'))
			replicaSeq = seq
		}
		var want any
		if err := json.Unmarshal(encoded, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(replica, want) {
			t.Fatalf("event %d: replica %v, want %v (ops %s)", index, replica, want, ops)
		}
	}
}

// TestChildUpdateStreamSendsAGrownTextAsItsTail checks what the extension receives for a streamed text: the tail, not the text.
func TestChildUpdateStreamSendsAGrownTextAsItsTail(t *testing.T) {
	var stream childUpdateStream
	text := strings.Repeat("x", 200)
	first := fmt.Sprintf(`{"type":"message_update","t":%q}`, text)
	if _, ops, patch, ok := stream.frame([]byte(first), 1); !ok || patch {
		t.Fatalf("the first update is a keyframe: ok=%v ops=%s", ok, ops)
	}
	base, ops, _, ok := stream.frame([]byte(fmt.Sprintf(`{"type":"message_update","t":%q}`, text+"yz")), 2)
	if !ok || base != 1 || string(ops) != `{"op":"append","path":["t"],"value":"yz"}` {
		t.Fatalf("base=%d ops=%s ok=%v", base, ops, ok)
	}
}
