package sdk

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func streamRequest(t *testing.T, stream eventStreamMsg, args string) *requestMsg {
	t.Helper()
	req := &requestMsg{Method: "event", Event: "message_update", Stream: &stream}
	if args != "" {
		req.Args = json.RawMessage(args)
	}
	return req
}

// TestEventStreamAppliesDeltasToTheLastPayload: a keyframe is kept, a delta applies to it, and the handler's payload is its own copy, so a handler that edits it does not change what the next delta applies to.
func TestEventStreamAppliesDeltasToTheLastPayload(t *testing.T) {
	e := &Extension{}
	first := streamRequest(t, eventStreamMsg{Seq: 1}, `{"type":"message_update","text":"ab","list":[1],"nested":{"k":"v"}}`)
	e.applyEventStream(first)
	if first.streamErr != nil {
		t.Fatal(first.streamErr)
	}
	first.streamData["text"] = "edited by the handler"
	first.streamData["nested"].(map[string]any)["k"] = "edited"
	first.streamData["list"].([]any)[0] = "edited"

	second := streamRequest(t, eventStreamMsg{Seq: 2, Base: 1, Ops: json.RawMessage(`[{"op":"append","path":["text"],"value":"cd"},{"op":"set","path":["list",1],"value":2},{"op":"set","path":["nested","new"],"value":null},{"op":"set","path":["added"],"value":{"x":[true]}}]`)}, "")
	e.applyEventStream(second)
	if second.streamErr != nil {
		t.Fatal(second.streamErr)
	}
	want := map[string]any{"type": "message_update", "text": "abcd", "list": []any{1.0, 2.0}, "nested": map[string]any{"k": "v", "new": nil}, "added": map[string]any{"x": []any{true}}}
	if !reflect.DeepEqual(second.streamData, want) {
		t.Fatalf("payload = %v, want %v", second.streamData, want)
	}
}

// TestEventStreamRefusesADeltaForAnotherPayload: a delta numbered against a payload the connection does not hold, or malformed, is an error for its request, and the replica is dropped so a stale one is never patched.
func TestEventStreamRefusesADeltaForAnotherPayload(t *testing.T) {
	cases := map[string]struct {
		stream eventStreamMsg
		fail   string
	}{
		"wrong base":         {eventStreamMsg{Seq: 3, Base: 2, Ops: json.RawMessage(`[]`)}, "delta applies to payload 2"},
		"append to a number": {eventStreamMsg{Seq: 2, Base: 1, Ops: json.RawMessage(`[{"op":"append","path":["n"],"value":"x"}]`)}, "append to a non-string"},
		"unknown member":     {eventStreamMsg{Seq: 2, Base: 1, Ops: json.RawMessage(`[{"op":"set","path":["a","b"],"value":1}]`)}, "bad path"},
		"array past end":     {eventStreamMsg{Seq: 2, Base: 1, Ops: json.RawMessage(`[{"op":"set","path":["list",5],"value":1}]`)}, "bad path"},
		"unknown op":         {eventStreamMsg{Seq: 2, Base: 1, Ops: json.RawMessage(`[{"op":"del","path":["text"],"value":1}]`)}, "unknown operation"},
		"ops not a list":     {eventStreamMsg{Seq: 2, Base: 1, Ops: json.RawMessage(`{}`)}, "decode ops"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := &Extension{}
			e.applyEventStream(streamRequest(t, eventStreamMsg{Seq: 1}, `{"text":"ab","n":1,"list":[1]}`))
			req := streamRequest(t, c.stream, "")
			e.applyEventStream(req)
			if req.streamErr == nil || !strings.Contains(req.streamErr.Error(), c.fail) {
				t.Fatalf("error = %v, want one containing %q", req.streamErr, c.fail)
			}
			next := streamRequest(t, eventStreamMsg{Seq: c.stream.Seq + 1, Base: 1, Ops: json.RawMessage(`[]`)}, "")
			e.applyEventStream(next)
			if next.streamErr == nil {
				t.Fatal("a delta applied to a replica that was dropped after a failure")
			}
		})
	}
}

// TestEventStreamNamesAreIndependent: each event name has its own numbering and payload.
func TestEventStreamNamesAreIndependent(t *testing.T) {
	e := &Extension{}
	a := &requestMsg{Method: "event", Event: "message_update", Stream: &eventStreamMsg{Seq: 1}, Args: json.RawMessage(`{"a":"x"}`)}
	b := &requestMsg{Method: "event", Event: "tool_execution_update", Stream: &eventStreamMsg{Seq: 7}, Args: json.RawMessage(`{"b":"y"}`)}
	e.applyEventStream(a)
	e.applyEventStream(b)
	patch := &requestMsg{Method: "event", Event: "message_update", Stream: &eventStreamMsg{Seq: 2, Base: 1, Ops: json.RawMessage(`[{"op":"append","path":["a"],"value":"z"}]`)}}
	e.applyEventStream(patch)
	if patch.streamErr != nil || !reflect.DeepEqual(patch.streamData, map[string]any{"a": "xz"}) {
		t.Fatalf("patch = %v, %v", patch.streamData, patch.streamErr)
	}
}

// streamKeyframe is a message_update payload with a streaming text block and a streaming tool call argument.
const streamKeyframe = `{"type":"message_update","message":{"content":[{"type":"text","text":""},{"type":"toolCall","arguments":{"content":""}}]},"assistantMessageEvent":{"type":"text_delta","delta":"","partial":{"content":[{"type":"text","text":""}]}}}`

// streamBytesPerDelta is the heap bytes one delta allocates while a stream appends deltas chunks of size bytes to three strings.
func streamBytesPerDelta(t *testing.T, deltas, size int) float64 {
	t.Helper()
	e := &Extension{}
	e.applyEventStream(streamRequest(t, eventStreamMsg{Seq: 1}, streamKeyframe))
	chunk := strings.Repeat("x", size)
	ops := json.RawMessage(fmt.Sprintf(`[{"op":"append","path":["message","content",0,"text"],"value":%q},{"op":"append","path":["message","content",1,"arguments","content"],"value":%q},{"op":"set","path":["assistantMessageEvent","delta"],"value":%q},{"op":"append","path":["assistantMessageEvent","partial","content",0,"text"],"value":%q}]`, chunk, chunk, chunk, chunk))
	reqs := make([]*requestMsg, deltas)
	for i := range reqs {
		reqs[i] = &requestMsg{Method: "event", Event: "message_update", Stream: &eventStreamMsg{Seq: uint64(i + 2), Base: uint64(i + 1), Ops: ops}}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, req := range reqs {
		e.applyEventStream(req)
	}
	runtime.ReadMemStats(&after)
	last := reqs[len(reqs)-1]
	if last.streamErr != nil {
		t.Fatal(last.streamErr)
	}
	if text := last.streamData["message"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string); len(text) != deltas*size {
		t.Fatalf("text is %d bytes, want %d", len(text), deltas*size)
	}
	return float64(after.TotalAlloc-before.TotalAlloc) / float64(deltas)
}

// TestEventStreamAppendCostIsPerDelta: appending to a streamed string costs the delta, not the string, so a stream ten times longer costs no more per delta.
func TestEventStreamAppendCostIsPerDelta(t *testing.T) {
	short := streamBytesPerDelta(t, 1000, 8)
	long := streamBytesPerDelta(t, 10000, 8)
	t.Logf("bytes allocated per delta: 1k deltas %.0f, 10k deltas %.0f, ratio %.2f", short, long, long/short)
	if long > 1.2*short {
		t.Fatalf("bytes per delta grew with the stream: 1k deltas %.0f, 10k deltas %.0f (ratio %.2f, want <= 1.2)", short, long, long/short)
	}
}

// referenceStreamOp applies one operation by plain string concatenation, the payload a delta produces by definition.
func referenceStreamOp(node any, path []any, op string, value any) any {
	if len(path) == 0 {
		return value
	}
	switch typed := node.(type) {
	case map[string]any:
		key := path[0].(string)
		if len(path) == 1 {
			if op == "append" {
				typed[key] = typed[key].(string) + value.(string)
			} else {
				typed[key] = value
			}
			return typed
		}
		typed[key] = referenceStreamOp(typed[key], path[1:], op, value)
		return typed
	case []any:
		index := int(path[0].(float64))
		if len(path) == 1 {
			switch {
			case op == "append":
				typed[index] = typed[index].(string) + value.(string)
			case index == len(typed):
				typed = append(typed, value)
			default:
				typed[index] = value
			}
			return typed
		}
		typed[index] = referenceStreamOp(typed[index], path[1:], op, value)
		return typed
	}
	panic("reference: bad path")
}

// cloneReference deep-copies a payload, strings included, so a later change to the bytes behind a delivered string shows up as a difference.
func cloneReference(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, member := range typed {
			out[key] = cloneReference(member)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, member := range typed {
			out[index] = cloneReference(member)
		}
		return out
	case string:
		return strings.Clone(typed)
	default:
		return value
	}
}

// TestEventStreamAppendsMatchConcatenation: over random deltas (multibyte text cut at any byte, several strings, nested arguments, strings replaced and appended again, array growth), every payload a handler receives equals plain concatenation, and no payload handed out earlier changes afterwards.
func TestEventStreamAppendsMatchConcatenation(t *testing.T) {
	source := []byte("héllo wörld ✓ 日本語テキスト 🙂🚀 ascii tail ")
	paths := [][]any{
		{"message", "content", 0.0, "text"},
		{"message", "content", 1.0, "arguments", "content"},
		{"message", "content", 1.0, "arguments", "path"},
		{"assistantMessageEvent", "partial", "content", 0.0, "text"},
	}
	for seed := uint64(1); seed <= 8; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed))
		keyframe := `{"type":"message_update","message":{"content":[{"type":"text","text":"start "},{"type":"toolCall","arguments":{"content":"","path":"a/"}}]},"assistantMessageEvent":{"partial":{"content":[{"type":"text","text":""}]},"list":[]}}`
		var reference any
		if err := json.Unmarshal([]byte(keyframe), &reference); err != nil {
			t.Fatal(err)
		}
		e := &Extension{}
		e.applyEventStream(streamRequest(t, eventStreamMsg{Seq: 1}, keyframe))
		type delivered struct{ view, copy map[string]any }
		var kept []delivered
		listLen := 0
		for seq := uint64(2); seq < 400; seq++ {
			var list []string
			for n := rng.IntN(4); n >= 0; n-- {
				path := paths[rng.IntN(len(paths))]
				encodedPath, _ := json.Marshal(path)
				switch rng.IntN(20) {
				case 0:
					encoded, _ := json.Marshal(string(source[:rng.IntN(len(source))]))
					list = append(list, fmt.Sprintf(`{"op":"set","path":%s,"value":%s}`, encodedPath, encoded))
				case 1:
					list = append(list, fmt.Sprintf(`{"op":"set","path":["assistantMessageEvent","list",%d],"value":"item"}`, listLen))
					listLen++
				default:
					start := rng.IntN(len(source))
					end := start + rng.IntN(len(source)-start+1)
					encoded, _ := json.Marshal(string(source[start:end]))
					list = append(list, fmt.Sprintf(`{"op":"append","path":%s,"value":%s}`, encodedPath, encoded))
				}
			}
			ops := "[" + strings.Join(list, ",") + "]"
			var decoded []streamOp
			if err := json.Unmarshal([]byte(ops), &decoded); err != nil {
				t.Fatal(err)
			}
			for _, op := range decoded {
				var value any
				if err := json.Unmarshal(op.Value, &value); err != nil {
					t.Fatal(err)
				}
				reference = referenceStreamOp(reference, op.Path, op.Op, value)
			}
			req := streamRequest(t, eventStreamMsg{Seq: seq, Base: seq - 1, Ops: json.RawMessage(ops)}, "")
			e.applyEventStream(req)
			if req.streamErr != nil {
				t.Fatalf("seed %d seq %d: %v", seed, seq, req.streamErr)
			}
			if !reflect.DeepEqual(req.streamData, reference) {
				t.Fatalf("seed %d seq %d: payload = %v, want %v", seed, seq, req.streamData, reference)
			}
			kept = append(kept, delivered{req.streamData, cloneReference(req.streamData).(map[string]any)})
		}
		for index, payload := range kept {
			if !reflect.DeepEqual(payload.view, payload.copy) {
				t.Fatalf("seed %d: payload %d changed after it was handed out", seed, index)
			}
		}
	}
}

// TestEventStreamKeysAreIndependentAndEvicted: the payloads of one event name are numbered and kept per key (a tool_execution_update has one for each tool call), a frame's evict list drops the named keys after it applied, and a delta for an evicted key is refused.
func TestEventStreamKeysAreIndependentAndEvicted(t *testing.T) {
	e := &Extension{}
	appendPlus := json.RawMessage(`[{"op":"append","path":["text"],"value":"+"}]`)
	apply := func(key string, stream eventStreamMsg, args string) *requestMsg {
		t.Helper()
		stream.Key = key
		req := &requestMsg{Method: "event", Event: "tool_execution_update", Stream: &stream}
		if args != "" {
			req.Args = json.RawMessage(args)
		}
		e.applyEventStream(req)
		return req
	}
	for _, key := range []string{"a", "b"} {
		if req := apply(key, eventStreamMsg{Seq: 1}, `{"text":"`+key+`"}`); req.streamErr != nil {
			t.Fatal(req.streamErr)
		}
	}
	for _, step := range []struct {
		key       string
		seq, base uint64
		want      string
	}{{"b", 2, 1, "b+"}, {"a", 2, 1, "a+"}, {"b", 3, 2, "b++"}} {
		req := apply(step.key, eventStreamMsg{Seq: step.seq, Base: step.base, Ops: appendPlus}, "")
		if req.streamErr != nil || req.streamData["text"] != step.want {
			t.Fatalf("%s: %v, %v, want %s", step.key, req.streamData, req.streamErr, step.want)
		}
	}
	if req := apply("c", eventStreamMsg{Seq: 1, Evict: []string{"a"}}, `{"text":"c"}`); req.streamErr != nil {
		t.Fatal(req.streamErr)
	}
	if len(e.eventReplicas) != 2 {
		t.Fatalf("replicas after eviction: %v", e.eventReplicas)
	}
	if stale := apply("a", eventStreamMsg{Seq: 3, Base: 2, Ops: appendPlus}, ""); stale.streamErr == nil {
		t.Fatal("a delta applied to an evicted call")
	}
	if req := apply("b", eventStreamMsg{Seq: 4, Base: 3, Ops: appendPlus}, ""); req.streamErr != nil || req.streamData["text"] != "b+++" {
		t.Fatalf("b after the eviction of a: %v, %v", req.streamData, req.streamErr)
	}
}
