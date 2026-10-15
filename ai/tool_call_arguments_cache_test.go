package ai

import (
	"bytes"
	"container/list"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// forgetArgumentEncodings empties the process-wide cache, so the next encoding of any arguments is the uncached one.
func forgetArgumentEncodings() {
	c := argumentEncodings
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
	c.recency.Init()
	c.held = 0
}

// toolCallEncodings is every text a call's arguments are written into: ArgumentsJSON, MarshalJSON and AppendStringify.
func toolCallEncodings(t *testing.T, call ToolCall) [3]string {
	t.Helper()
	arguments, err := call.ArgumentsJSON()
	if err != nil {
		t.Fatal(err)
	}
	marshaled, err := call.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	appended, err := call.AppendStringify(nil)
	if err != nil {
		t.Fatal(err)
	}
	return [3]string{string(arguments), string(marshaled), string(appended)}
}

// uncachedEncodings is toolCallEncodings with each text encoded from the arguments, none taken from the cache.
func uncachedEncodings(t *testing.T, call ToolCall) [3]string {
	t.Helper()
	var out [3]string
	for i := range out {
		forgetArgumentEncodings()
		out[i] = toolCallEncodings(t, call)[i]
	}
	forgetArgumentEncodings()
	return out
}

// randomArgumentText writes a random JSON object: integer-like and plain keys in a random order, strings with HTML characters,
// separators, controls and surrogate escapes, numbers JSON.stringify spells differently from Go, and nested objects and arrays.
func randomArgumentText(rng *rand.Rand, depth int) string {
	keys := []string{"path", "content", "10", "2", "a", "z", "<&>", "\u2028", "edits", "n"}
	var b strings.Builder
	b.WriteByte('{')
	members := rng.IntN(5)
	used := map[string]bool{}
	for range members {
		key := keys[rng.IntN(len(keys))]
		if used[key] {
			continue
		}
		used[key] = true
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%s", key, randomKeptArgumentValue(rng, depth))
	}
	b.WriteByte('}')
	return b.String()
}

func randomKeptArgumentValue(rng *rand.Rand, depth int) string {
	switch rng.IntN(8) {
	case 0:
		return []string{"0", "-0", "1.5", "1e21", "1e-7", "123456789012345678901234", "-3"}[rng.IntN(7)]
	case 1:
		return []string{"true", "false", "null"}[rng.IntN(3)]
	case 2:
		if depth < 3 {
			return randomArgumentText(rng, depth+1)
		}
		return `"leaf"`
	case 3:
		if depth < 3 {
			return "[" + randomKeptArgumentValue(rng, depth+1) + "," + randomKeptArgumentValue(rng, depth+1) + "]"
		}
		return "[]"
	default:
		return []string{`"plain"`, `"<a href=\"x\">&amp;</a>"`, `"line\u2028para\u2029"`, `"\u0000\u001f\b\f\n\r\t\\/"`, `"\ud800 lone"`,
			`"\udc00\ud800 reversed"`, `"\ud83d\ude00 pair"`, `"` + strings.Repeat("x\\n", 3000) + `"`}[rng.IntN(8)]
	}
}

// The kept bytes are what encoding the arguments writes, in every text a call is written into, for random arguments read from
// text (with a recorded member order or none) and for arguments built in Go (integer-like keys, negative zero, ill-formed UTF-8).
func TestKeptArgumentBytesMatchTheEncodedArguments(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var calls []ToolCall
	for range 400 {
		call := ToolCall{ID: "call", Name: "write"}
		call.SetStreamingArguments(randomArgumentText(rng, 0))
		calls = append(calls, call)
	}
	for _, text := range appendStringifyTexts {
		calls = append(calls, ToolCall{ID: "c", Name: "n", Arguments: JsonObject{"path": text, "z": math.Copysign(0, -1), "10": 1.0, "9": []any{text, 1e21}, text: map[string]any{"<": "&"}}})
	}
	for i, call := range calls {
		want := uncachedEncodings(t, call)
		if _, err := call.ArgumentsJSON(); err != nil {
			t.Fatal(err)
		}
		if got := toolCallEncodings(t, call); got != want {
			t.Fatalf("call %d: kept bytes write\n%q\nwant\n%q", i, got, want)
		}
		copied := call
		if got := toolCallEncodings(t, copied); got != want {
			t.Fatalf("call %d: a copy of the call writes\n%q\nwant\n%q", i, got, want)
		}
	}
}

// Pi stringifies the live arguments on every request, so a write to the map in place, to a nested map or slice, a replaced
// value of another type, or a replaced member order is written: the kept bytes no longer serve the call.
func TestKeptArgumentBytesFollowAWriteToTheArguments(t *testing.T) {
	text := `{"path":"a.go","edits":[{"old":"x","new":"y"}],"n":1.5}`
	cases := map[string]func(*ToolCall){
		"top-level value":    func(call *ToolCall) { call.Arguments["path"] = "b.go" },
		"added member":       func(call *ToolCall) { call.Arguments["extra"] = true },
		"removed member":     func(call *ToolCall) { delete(call.Arguments, "n") },
		"nested map":         func(call *ToolCall) { call.Arguments["edits"].([]any)[0].(map[string]any)["new"] = "z" },
		"slice element":      func(call *ToolCall) { call.Arguments["edits"].([]any)[0] = "replaced" },
		"replaced slice":     func(call *ToolCall) { call.Arguments["edits"] = append(call.Arguments["edits"].([]any), 2.0) },
		"number type":        func(call *ToolCall) { call.Arguments["n"] = 1 },
		"equal number":       func(call *ToolCall) { call.Arguments["n"] = 1.5 },
		"nested object type": func(call *ToolCall) { call.Arguments["edits"].([]any)[0] = JsonObject{"new": "y", "old": "x"} },
		"replaced map":       func(call *ToolCall) { call.Arguments = JsonObject{"path": "c.go"} },
		"member order":       func(call *ToolCall) { call.argumentOrder = schemaObjectOrder{"": {"n", "path", "edits"}} },
		"dropped order":      func(call *ToolCall) { call.argumentOrder = nil },
	}
	for name, write := range cases {
		t.Run(name, func(t *testing.T) {
			var call ToolCall
			call.SetStreamingArguments(`{"z":0,` + text[1:])
			if _, err := call.ArgumentsJSON(); err != nil {
				t.Fatal(err)
			}
			write(&call)
			got := toolCallEncodings(t, call)
			if want := uncachedEncodings(t, call); got != want {
				t.Fatalf("after the write the call writes\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// The bytes live beside the call, not in it: a streamed call still equals the same call built in Go.
func TestKeptArgumentBytesLeaveTheCallValueUnchanged(t *testing.T) {
	var streamed ToolCall
	streamed.SetStreamingArguments(`{"path":"a.go"}`)
	streamed.ID, streamed.Name = "c", "read"
	if _, err := streamed.ArgumentsJSON(); err != nil {
		t.Fatal(err)
	}
	if built := (ToolCall{ID: "c", Name: "read", Arguments: JsonObject{"path": "a.go"}}); !reflect.DeepEqual(streamed, built) {
		t.Fatalf("streamed %#v differs from built %#v", streamed, built)
	}
}

// The cache keeps the latest encodings within its limit and forgets the least recently used.
func TestTheArgumentEncodingCacheForgetsPastItsLimit(t *testing.T) {
	calls := make([]ToolCall, 4)
	const encodedSize = len(`{"c":""}`) + 991
	cache := &argumentEncodingCache{limit: 3 * (2*encodedSize + 256), entries: map[uintptr]*list.Element{}}
	for i := range calls {
		calls[i] = ToolCall{Arguments: JsonObject{"c": strings.Repeat(string(rune('a'+i)), 991)}}
		encoded, err := marshalInRecordedOrder(map[string]any(calls[i].Arguments), nil)
		if err != nil || len(encoded) != encodedSize {
			t.Fatalf("encoded %d bytes: %v", len(encoded), err)
		}
		cache.keep(calls[i].Arguments, nil, encoded)
	}
	if cache.recency.Len() != 3 || cache.held > cache.limit {
		t.Fatalf("cache holds %d entries, %d bytes (limit %d)", cache.recency.Len(), cache.held, cache.limit)
	}
	if cache.find(calls[0].Arguments, nil) != nil || cache.find(calls[3].Arguments, nil) == nil {
		t.Fatal("the cache did not forget the least recently used encoding")
	}
	if !bytes.HasPrefix(cache.find(calls[3].Arguments, nil), []byte(`{"c":"d`)) {
		t.Fatal("the cache served another call's bytes")
	}
}
