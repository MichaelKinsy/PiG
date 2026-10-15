package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// orderedObject is a decoded JSON object that keeps its members' order, as a JavaScript object does.
type orderedObject struct {
	keys   []string
	values map[string]any
}

func decodeOrdered(t testing.TB, data []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	value := readOrdered(t, decoder)
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("trailing data after %s", data)
	}
	return value
}

func readOrdered(t testing.TB, decoder *json.Decoder) any {
	token, err := decoder.Token()
	if err != nil {
		t.Fatal(err)
	}
	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '{':
			object := &orderedObject{values: map[string]any{}}
			for decoder.More() {
				key, _ := decoder.Token()
				object.keys = append(object.keys, key.(string))
				object.values[key.(string)] = readOrdered(t, decoder)
			}
			_, _ = decoder.Token()
			return object
		default:
			array := []any{}
			for decoder.More() {
				array = append(array, readOrdered(t, decoder))
			}
			_, _ = decoder.Token()
			return array
		}
	default:
		return token
	}
}

// applyOps is the reference of what a runtime does with a patch.
func applyOps(t testing.TB, root any, opsJSON []byte) any {
	t.Helper()
	var ops []struct {
		Op    string            `json:"op"`
		Path  []json.RawMessage `json:"path"`
		Value json.RawMessage   `json:"value"`
	}
	if err := json.Unmarshal(opsJSON, &ops); err != nil {
		t.Fatalf("ops %s: %v", opsJSON, err)
	}
	for _, op := range ops {
		value := decodeOrdered(t, op.Value)
		if len(op.Path) == 0 {
			root = value
			continue
		}
		parent := root
		for _, element := range op.Path[:len(op.Path)-1] {
			parent = child(t, parent, element)
		}
		last := op.Path[len(op.Path)-1]
		switch typed := parent.(type) {
		case *orderedObject:
			var key string
			if err := json.Unmarshal(last, &key); err != nil {
				t.Fatalf("object path element %s", last)
			}
			switch op.Op {
			case "append":
				typed.values[key] = typed.values[key].(string) + value.(string)
			case "set":
				if _, ok := typed.values[key]; !ok {
					typed.keys = append(typed.keys, key)
				}
				typed.values[key] = value
			default:
				t.Fatalf("op %s", op.Op)
			}
		case []any:
			var index int
			if err := json.Unmarshal(last, &index); err != nil {
				t.Fatalf("array path element %s", last)
			}
			// Arrays are replaced through their parent: this reference needs a slot to write.
			grandparent, slot := locate(t, root, op.Path[:len(op.Path)-1])
			switch op.Op {
			case "append":
				typed[index] = typed[index].(string) + value.(string)
			case "set":
				if index == len(typed) {
					typed = append(typed, value)
				} else {
					typed[index] = value
				}
			}
			assign(t, &root, grandparent, slot, typed)
		default:
			t.Fatalf("parent %T", parent)
		}
	}
	return root
}

func child(t testing.TB, parent any, element json.RawMessage) any {
	switch typed := parent.(type) {
	case *orderedObject:
		var key string
		if err := json.Unmarshal(element, &key); err != nil {
			t.Fatalf("path element %s", element)
		}
		value, ok := typed.values[key]
		if !ok {
			t.Fatalf("no member %s", key)
		}
		return value
	case []any:
		var index int
		if err := json.Unmarshal(element, &index); err != nil || index >= len(typed) {
			t.Fatalf("path element %s of %d", element, len(typed))
		}
		return typed[index]
	}
	t.Fatalf("path through %T", parent)
	return nil
}

func locate(t testing.TB, root any, path []json.RawMessage) (parent any, slot json.RawMessage) {
	if len(path) == 0 {
		return nil, nil
	}
	parent = root
	for _, element := range path[:len(path)-1] {
		parent = child(t, parent, element)
	}
	return parent, path[len(path)-1]
}

func assign(t testing.TB, root *any, parent any, slot json.RawMessage, value any) {
	if parent == nil {
		*root = value
		return
	}
	switch typed := parent.(type) {
	case *orderedObject:
		var key string
		_ = json.Unmarshal(slot, &key)
		typed.values[key] = value
	case []any:
		var index int
		_ = json.Unmarshal(slot, &index)
		typed[index] = value
	}
}

// updateStep is one message_update of a scripted stream.
type updateStep struct {
	message ai.AssistantMessage
	event   func(*ai.AssistantMessage) any
}

func assistantEvent(message ai.AssistantMessage, build func(*ai.AssistantMessage) any) extension.MessageUpdateEvent {
	partial := message
	return extension.MessageUpdateEvent{
		Type: "message_update",
		Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: "assistant", Content: partial.Content, API: partial.API, Provider: partial.Provider, ModelID: partial.Model,
			Usage: &partial.Usage, StopReason: partial.StopReason, Timestamp: partial.Timestamp, ErrorMessage: partial.ErrorMessage,
		}},
		AssistantMessageEvent: build(&partial),
	}
}

func toolCallWithArguments(id, name, partialJSON string) ai.ToolCall {
	call := ai.ToolCall{ID: id, Name: name}
	call.SetStreamingArguments(partialJSON)
	return call
}

// streamScripts are streams whose consecutive payloads differ in every way a delta has to carry.
func streamScripts() map[string][]updateStep {
	base := ai.AssistantMessage{API: "openai-completions", Provider: "p", Model: "m", Timestamp: 1700000000000, StopReason: ai.StopReasonStop}
	long := strings.Repeat("0123456789abcdef", 40)
	scripts := map[string][]updateStep{}

	var text []updateStep
	for n := 0; n <= 30; n++ {
		message := base
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: strings.Repeat("word ", n*9)}}
		delta := "word "
		text = append(text, updateStep{message, func(p *ai.AssistantMessage) any { return ai.TextDeltaEvent{ContentIndex: 0, Delta: delta, Partial: p} }})
	}
	scripts["text grows across the long-string threshold"] = text

	var mixed []updateStep
	thinking, answer := "", ""
	for n := range 40 {
		message := base
		blocks := []ai.AssistantContentBlock{}
		if n < 15 {
			thinking += "think about it "
			blocks = append(blocks, ai.ThinkingContent{Thinking: thinking, ThinkingSignature: ""})
			message.Content = blocks
			mixed = append(mixed, updateStep{message, func(p *ai.AssistantMessage) any {
				return ai.ThinkingDeltaEvent{ContentIndex: 0, Delta: "think about it ", Partial: p}
			}})
			continue
		}
		answer += "and answer it, "
		blocks = append(blocks, ai.ThinkingContent{Thinking: thinking, ThinkingSignature: long}, ai.TextContent{Text: answer})
		message.Content = blocks
		mixed = append(mixed, updateStep{message, func(p *ai.AssistantMessage) any {
			return ai.TextDeltaEvent{ContentIndex: 1, Delta: "and answer it, ", Partial: p}
		}})
	}
	scripts["thinking then text with a signature"] = mixed

	var tools []updateStep
	var content strings.Builder
	for n := range 40 {
		fmt.Fprintf(&content, "line %d of the file\\n", n)
		args := fmt.Sprintf(`{"path": "big/source.ts", "content": "%s`, content.String())
		message := base
		message.StopReason = ai.StopReasonToolUse
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "writing it now, as asked of me"}, toolCallWithArguments("call_1", "write", args)}
		tools = append(tools, updateStep{message, func(p *ai.AssistantMessage) any {
			return ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: "line", Partial: p}
		}})
	}
	scripts["tool call arguments in the model's order"] = tools

	var keys []updateStep
	for _, args := range []string{
		`{"zeta": 1`, `{"zeta": 1, "alpha": "`, `{"zeta": 1, "alpha": "` + long, `{"zeta": 1, "alpha": "` + long + `x", "mid": [1, 2`,
		`{"zeta": 1, "alpha": "` + long + `xy", "mid": [1, 2, {"deep": "` + long, `{"zeta": 1, "alpha": "` + long + `xy", "mid": [1, 2, {"deep": "` + long + `"}]}`,
		`{"alpha": "short", "zeta": 2}`, `{"alpha": "short", "zeta": 2, "extra": null, "list": [1, 2, 3]}`, `{"alpha": "short", "zeta": 2, "extra": null, "list": [1, 2]}`,
		`{"alpha": "short", "zeta": 2, "extra": null}`, `{"alpha": "short"}`, `{"alpha": "short", "zeta": 2, "extra": null}`, `{"alpha": "` + long + `", "zeta": 2, "extra": null}`,
	} {
		message := base
		message.Content = []ai.AssistantContentBlock{toolCallWithArguments("call_1", "edit", args)}
		keys = append(keys, updateStep{message, func(p *ai.AssistantMessage) any {
			return ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: "x", Partial: p}
		}})
	}
	scripts["arguments reorder, shrink and change type"] = keys

	var replaced []updateStep
	for _, body := range []string{long, long + "a", long + "ab", "z" + long + "ab", "short", long + "again", "ä" + long + "é", "ä" + long + "éx", long, ""} {
		message := base
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: body}}
		replaced = append(replaced, updateStep{message, func(p *ai.AssistantMessage) any { return ai.TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: p} }})
	}
	scripts["text replaced, shortened and multibyte"] = replaced

	var lifecycle []updateStep
	message := base
	lifecycle = append(lifecycle, updateStep{message, func(p *ai.AssistantMessage) any { return ai.StartEvent{Partial: p} }})
	message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: ""}}
	lifecycle = append(lifecycle, updateStep{message, func(p *ai.AssistantMessage) any { return ai.TextStartEvent{ContentIndex: 0, Partial: p} }})
	message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: long}}
	lifecycle = append(lifecycle,
		updateStep{message, func(p *ai.AssistantMessage) any { return ai.TextDeltaEvent{ContentIndex: 0, Delta: long, Partial: p} }},
		updateStep{message, func(p *ai.AssistantMessage) any { return ai.TextEndEvent{ContentIndex: 0, Content: long, Partial: p} }})
	call := toolCallWithArguments("call_2", "bash", `{"command": "`+long+`"}`)
	message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: long}, call}
	lifecycle = append(lifecycle,
		updateStep{message, func(p *ai.AssistantMessage) any { return ai.ToolCallStartEvent{ContentIndex: 1, Partial: p} }},
		updateStep{message, func(p *ai.AssistantMessage) any {
			return ai.ToolCallEndEvent{ContentIndex: 1, ToolCall: call, Partial: p}
		}})
	message.Usage = ai.Usage{Input: 10, Output: 20, TotalTokens: 30}
	message.StopReason = ai.StopReasonToolUse
	lifecycle = append(lifecycle, updateStep{message, func(p *ai.AssistantMessage) any { return ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: p} }})
	failed := message
	failed.StopReason = ai.StopReasonError
	failed.ErrorMessage = "provider said: " + long
	lifecycle = append(lifecycle, updateStep{failed, func(p *ai.AssistantMessage) any { return ai.ErrorEvent{Reason: ai.StopReasonError, Error: p} }})
	scripts["every event type of a message"] = lifecycle

	var unmatched []updateStep
	for _, body := range []string{long, long + "a", long + "a\xed\xa0\x80", long + "a\xed\xa0\x80b", long + "a\xed\xa0\x80bc"} {
		message := base
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: body}}
		unmatched = append(unmatched, updateStep{message, func(p *ai.AssistantMessage) any { return ai.TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: p} }})
	}
	scripts["an unmatched UTF-16 unit is sent whole"] = unmatched
	return scripts
}

// TestMessageUpdateDeltaEqualsTheWholePayload: a runtime that applies each message_update delta holds, after every event, the object a whole payload decodes to, member order included.
func TestMessageUpdateDeltaEqualsTheWholePayload(t *testing.T) {
	var kinds = map[string]int{}
	for name, steps := range streamScripts() {
		t.Run(name, func(t *testing.T) {
			stream := &messageUpdateStream{}
			conn := &Conn{}
			var held any
			var heldSeq uint64
			for index, step := range steps {
				event := assistantEvent(step.message, step.event)
				whole, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				prepared, inStream, err := stream.prepare(conn, event)
				if err != nil {
					t.Fatal(err)
				}
				if !inStream {
					kinds["whole"]++
					continue
				}
				switch {
				case prepared.args != nil:
					kinds["keyframe"]++
					held = decodeOrdered(t, prepared.args)
				default:
					kinds["patch"]++
					if bytes.Contains(prepared.stream.Ops, []byte(`"op":"append"`)) {
						kinds["append"]++
					}
					if len(prepared.stream.Ops) > 2048 && name != "every event type of a message" && name != "arguments reorder, shrink and change type" && name != "text replaced, shortened and multibyte" {
						t.Errorf("step %d: a patch of %d bytes for one delta", index, len(prepared.stream.Ops))
					}
					if prepared.stream.Base != heldSeq {
						t.Fatalf("step %d: patch applies to %d, runtime holds %d", index, prepared.stream.Base, heldSeq)
					}
					held = applyOps(t, held, prepared.stream.Ops)
				}
				heldSeq = prepared.stream.Seq
				stream.sent(conn, prepared, nil)
				if want := decodeOrdered(t, whole); !reflect.DeepEqual(held, want) {
					t.Fatalf("step %d: the runtime's object differs from the whole payload\nops: %s\nheld: %s\nwant: %s", index, prepared.stream.Ops, dumpOrdered(held), dumpOrdered(want))
				}
			}
		})
	}
	if kinds["append"] < 20 {
		t.Fatalf("long strings must be sent as appends: %v", kinds)
	}
	if kinds["keyframe"] == 0 || kinds["patch"] == 0 || kinds["whole"] == 0 {
		t.Fatalf("the scripts must exercise keyframes, patches and whole payloads: %v", kinds)
	}
}

func dumpOrdered(value any) string {
	var out strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch typed := v.(type) {
		case *orderedObject:
			out.WriteByte('{')
			for i, key := range typed.keys {
				if i > 0 {
					out.WriteByte(',')
				}
				fmt.Fprintf(&out, "%q:", key)
				walk(typed.values[key])
			}
			out.WriteByte('}')
		case []any:
			out.WriteByte('[')
			for i, item := range typed {
				if i > 0 {
					out.WriteByte(',')
				}
				walk(item)
			}
			out.WriteByte(']')
		default:
			fmt.Fprintf(&out, "%#v", v)
		}
	}
	walk(value)
	return out.String()
}

// TestDeltaParserAcceptsEachPlaceholderOnce: a payload that names a long string twice, or leaves one out, is not a delta.
func TestDeltaParserAcceptsEachPlaceholderOnce(t *testing.T) {
	token := `"\u0000pig-big:0\u0000"`
	for name, data := range map[string]string{"used once": `[` + token + `]`, "used twice": `[` + token + `,` + token + `]`, "not used": `[]`, "out of range": `["\u0000pig-big:5\u0000"]`, "garbled": `["\u0000pig-big:x\u0000"]`, "embedded": `["a\u0000pig-big:0\u0000"]`} {
		parser := deltaParser{data: []byte(data), table: []string{strings.Repeat("x", 100)}, used: []bool{false}}
		_, err := parser.parse()
		if want := name == "used once"; (err == nil) != want {
			t.Errorf("%s: accepted %v (err %v), want %v", name, err == nil, err, want)
		}
	}
}

// After a message_update request fails, at the runtime or before it reached the socket, the next one is a keyframe: the runtime may not hold the payload the failed frame carried, so a delta against it would fail every later update of the stream.
func TestMessageUpdateAfterAFailedRequestIsAKeyframe(t *testing.T) {
	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("observe", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
	handler := managed.makeEventHandler("message_update", 1)
	base := ai.AssistantMessage{API: "openai-completions", Provider: "p", Model: "m", Timestamp: 1700000000000}
	event := func(n int) extension.MessageUpdateEvent {
		message := base
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: strings.Repeat("word ", 20+n)}}
		return assistantEvent(message, func(p *ai.AssistantMessage) any { return ai.TextDeltaEvent{Delta: "word ", Partial: p} })
	}
	// emit sends event n, answers its request with failure, and returns the request the runtime received.
	emit := func(n int, failure *ErrorInfo) *RequestPayload {
		t.Helper()
		done := make(chan error, 1)
		go func() { _, err := handler(event(n), t.Context()); done <- err }()
		request := readLivenessEnvelope(t, peer)
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Error: failure}})
		if err := <-done; (err != nil) != (failure != nil) {
			t.Fatalf("update %d: handler error %v, runtime failure %v", n, err, failure)
		}
		if request.Request == nil || request.Request.Stream == nil {
			t.Fatalf("update %d was not sent in the stream: %#v", n, request.Request)
		}
		return request.Request
	}
	keyframe := func(request *RequestPayload) bool { return len(request.Args) > 0 && request.Stream.Base == 0 }

	if first := emit(0, nil); !keyframe(first) {
		t.Fatalf("first update is not a keyframe: %+v", first.Stream)
	}
	if second := emit(1, &ErrorInfo{Message: "message_update delta: no payload 1"}); keyframe(second) {
		t.Fatalf("second update is a keyframe, want a delta: %+v", second.Stream)
	}
	if third := emit(2, nil); !keyframe(third) {
		t.Fatalf("the update after a failed one is a delta against a payload the runtime may not hold: %+v", third.Stream)
	}
	if fourth := emit(3, nil); keyframe(fourth) || fourth.Stream.Base != fourth.Stream.Seq-1 {
		t.Fatalf("the stream did not resume from the keyframe: %+v", fourth.Stream)
	}

	// A request that never reached the socket leaves no baseline either.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := handler(event(4), cancelled); err == nil {
		t.Fatal("an update with a cancelled context succeeded")
	}
	if sixth := emit(5, nil); !keyframe(sixth) {
		t.Fatalf("the update after an unsent one is a delta: %+v", sixth.Stream)
	}
}
