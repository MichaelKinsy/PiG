package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// wireResult is a partial result that exposes its members as the host's tool results do.
type wireResult struct {
	names  []string
	values []any
}

func (w wireResult) WireMembers() ([]string, []any) { return w.names, w.values }

func (w wireResult) MarshalJSON() ([]byte, error) { return orderedWire(w).MarshalJSON() }

type wireBlock struct{ text string }

func (b wireBlock) WireMembers() ([]string, []any) {
	return []string{"type", "text"}, []any{"text", b.text}
}

func (b wireBlock) MarshalJSON() ([]byte, error) {
	names, values := b.WireMembers()
	return orderedWire{names, values}.MarshalJSON()
}

func wireText(texts ...string) wireResult {
	content := make([]any, len(texts))
	for index, text := range texts {
		content[index] = wireBlock{text}
	}
	return wireResult{names: []string{"content"}, values: []any{content}}
}

func toolUpdate(call string, partial any) extension.ToolExecutionUpdateEvent {
	event := extension.ToolExecutionUpdateEvent{Type: "tool_execution_update", ToolCallID: call, ToolName: "make", Args: map[string]any{"command": "make"}, WireArgs: json.RawMessage(`{"command":"make"}`)}
	if _, ok := partial.(wireResult); ok {
		event.WirePartialResult = partial
	} else {
		event.PartialResult = partial
	}
	return event
}

// runtimeReplicas is a runtime's copy of the tool_execution_update payloads, applied as the SDKs do (EventStream in protocol.go).
type runtimeReplicas struct {
	t        *testing.T
	held     map[string]any
	seq      map[string]uint64
	maxHeld  int
	keyframe int
	patches  int
}

func (r *runtimeReplicas) apply(prepared preparedToolUpdate, args json.RawMessage, stream *EventStream) any {
	r.t.Helper()
	key := stream.Key
	if args != nil {
		r.keyframe++
		r.held[key] = decodeOrdered(r.t, args)
	} else {
		r.patches++
		if _, ok := r.held[key]; !ok || r.seq[key] != stream.Base {
			r.t.Fatalf("patch of %q applies to %d, the runtime holds %d (held %v)", key, stream.Base, r.seq[key], ok)
		}
		r.held[key] = applyOps(r.t, r.held[key], stream.Ops)
	}
	r.seq[key] = stream.Seq
	value := r.held[key]
	for _, evicted := range stream.Evict {
		delete(r.held, evicted)
		delete(r.seq, evicted)
	}
	r.maxHeld = max(r.maxHeld, len(r.held))
	return value
}

func replay(t *testing.T, events []extension.ToolExecutionUpdateEvent) (replicas *runtimeReplicas, wholeCount int) {
	t.Helper()
	streams := &toolUpdateStreams{}
	conn := &Conn{}
	replicas = &runtimeReplicas{t: t, held: map[string]any{}, seq: map[string]uint64{}}
	for index, event := range events {
		whole, err := marshalEventWire(boundaryEventWire(wireEventPayload(event), nil))
		if err != nil {
			t.Fatal(err)
		}
		prepared, args, stream, inStream, err := streams.prepare(conn, event, event, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !inStream {
			wholeCount++
			if !bytes.Equal(args, whole) {
				t.Fatalf("update %d sent whole differs from the encoder's bytes", index)
			}
			continue
		}
		got := replicas.apply(prepared, args, stream)
		streams.sent(conn, prepared, nil)
		if want := decodeOrdered(t, whole); !reflect.DeepEqual(got, want) {
			t.Fatalf("update %d: the runtime's object differs from the whole payload\nops: %s\nheld: %s\nwant: %s", index, stream.Ops, dumpOrdered(got), dumpOrdered(want))
		}
		if len(streams.recent) > toolUpdateStreamCap || len(streams.streams) != len(streams.recent) {
			t.Fatalf("update %d: the host holds %d calls (%d listed), want at most %d", index, len(streams.streams), len(streams.recent), toolUpdateStreamCap)
		}
		if replicas.maxHeld > toolUpdateStreamCap {
			t.Fatalf("update %d: the runtime holds %d payloads, want at most %d", index, replicas.maxHeld, toolUpdateStreamCap)
		}
	}
	return replicas, wholeCount
}

// TestToolUpdateDeltaEqualsTheWholePayload: a runtime that applies each tool_execution_update delta holds, after every event, the object a whole payload decodes to, member order included, whatever the interleaving of parallel calls.
func TestToolUpdateDeltaEqualsTheWholePayload(t *testing.T) {
	long := strings.Repeat("output line of a long running command\n", 3)
	scripts := map[string]func() []extension.ToolExecutionUpdateEvent{
		"one call, text growing": func() []extension.ToolExecutionUpdateEvent {
			var events []extension.ToolExecutionUpdateEvent
			text := ""
			for range 40 {
				text += long
				events = append(events, toolUpdate("c1", wireText(text)))
			}
			return events
		},
		"two calls interleaved": func() []extension.ToolExecutionUpdateEvent {
			var events []extension.ToolExecutionUpdateEvent
			a, b := "", ""
			for step := range 40 {
				a += long + "a"
				events = append(events, toolUpdate("a", wireText(a)))
				if step%3 != 2 {
					b += long + "bé"
					events = append(events, toolUpdate("b", wireText(b, "second block")))
				}
			}
			return events
		},
		"more calls than the host keeps": func() []extension.ToolExecutionUpdateEvent {
			var events []extension.ToolExecutionUpdateEvent
			texts := make([]string, 3*toolUpdateStreamCap)
			for round := range 4 {
				for call := range texts {
					texts[call] += fmt.Sprintf("%s%d.%d ", long, call, round)
					events = append(events, toolUpdate(fmt.Sprintf("call-%d", call), wireText(texts[call])))
				}
			}
			return events
		},
		"map result, details and shrinking content": func() []extension.ToolExecutionUpdateEvent {
			var events []extension.ToolExecutionUpdateEvent
			text := ""
			for step := range 12 {
				text += long
				blocks := []any{map[string]any{"type": "text", "text": text}}
				if step%4 == 3 {
					blocks = blocks[:0]
				}
				partial := map[string]any{"content": blocks, "details": map[string]any{"progress": step, "label": strings.Repeat("l", 100+step)}}
				events = append(events, toolUpdate("m", partial))
			}
			return events
		},
		"members that change kind and order": func() []extension.ToolExecutionUpdateEvent {
			return []extension.ToolExecutionUpdateEvent{
				toolUpdate("k", wireResult{names: []string{"content", "details"}, values: []any{[]any{wireBlock{long}}, map[string]any{"n": 1}}}),
				toolUpdate("k", wireResult{names: []string{"details", "content"}, values: []any{map[string]any{"n": 2}, []any{wireBlock{long + "x"}}}}),
				toolUpdate("k", wireResult{names: []string{"content"}, values: []any{[]any{wireBlock{long + "x"}}}}),
				toolUpdate("k", wireResult{names: []string{"content", "isError"}, values: []any{[]any{wireBlock{"short"}}, true}}),
				toolUpdate("k", wireResult{names: []string{"content", "isError", "usage"}, values: []any{[]any{wireBlock{"short"}}, false, nil}}),
			}
		},
		"an unmatched UTF-16 unit is sent whole": func() []extension.ToolExecutionUpdateEvent {
			var events []extension.ToolExecutionUpdateEvent
			for _, body := range []string{long, long + "a", long + "a\xed\xa0\x80", "\xed\xa0\x80", long + "a\xed\xa0\x80b", long + "a\xed\xa0\x80bc"} {
				events = append(events, toolUpdate("u", wireText(body)))
			}
			return events
		},
		"an event without wire members": func() []extension.ToolExecutionUpdateEvent {
			event := toolUpdate("w", nil)
			event.WireArgs = nil
			event.Args = map[string]any{"n": 1}
			event.ParentToolCallID = "parent"
			next := event
			next.Args = map[string]any{"n": 2}
			return []extension.ToolExecutionUpdateEvent{event, next}
		},
	}
	var patches, keyframes int
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			replicas, whole := replay(t, script())
			patches += replicas.patches
			keyframes += replicas.keyframe
			if name == "an unmatched UTF-16 unit is sent whole" && whole == 0 {
				t.Fatal("a lone surrogate must be sent whole")
			}
			if name == "more calls than the host keeps" && replicas.maxHeld != toolUpdateStreamCap {
				t.Fatalf("the runtime held at most %d payloads, want the cap %d", replicas.maxHeld, toolUpdateStreamCap)
			}
		})
	}
	if patches == 0 || keyframes == 0 {
		t.Fatalf("the scripts must exercise keyframes and patches: %d, %d", keyframes, patches)
	}
}

// A growing result costs O(what each update added) on the wire: no patch of a text that grows by one line is larger than that line's append, whatever the text so far.
func TestToolUpdatePatchesAreTheSizeOfTheirDelta(t *testing.T) {
	streams := &toolUpdateStreams{}
	conn := &Conn{}
	chunk := strings.Repeat("output line of a long running command\n", 2)
	var text strings.Builder
	for index := range 2000 {
		text.WriteString(chunk)
		event := toolUpdate("c", wireText(text.String()))
		prepared, args, stream, inStream, err := streams.prepare(conn, event, event, nil)
		if err != nil || !inStream {
			t.Fatalf("update %d: %v (in stream %v)", index, err, inStream)
		}
		if index > 0 && (args != nil || len(stream.Ops) > 2*len(chunk)+300) {
			t.Fatalf("update %d: a patch of %d bytes (keyframe %v) for a delta of %d bytes", index, len(stream.Ops), args != nil, len(chunk))
		}
		streams.sent(conn, prepared, nil)
	}
}

// After a tool_execution_update request fails, at the runtime or before it reached the socket, the next one of every call is a keyframe: the runtime may not hold the payload the failed frame carried.
func TestToolUpdateAfterAFailedRequestIsAKeyframe(t *testing.T) {
	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("observe", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "observe"}, host: NewHost(t.TempDir())}, conn)
	handler := managed.makeEventHandler("tool_execution_update", 1)
	event := func(call string, n int) extension.ToolExecutionUpdateEvent {
		return toolUpdate(call, wireText(strings.Repeat("word ", 20+n)))
	}
	emit := func(call string, n int, failure *ErrorInfo) *RequestPayload {
		t.Helper()
		done := make(chan error, 1)
		go func() { _, err := handler(event(call, n), t.Context()); done <- err }()
		request := readLivenessEnvelope(t, peer)
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Error: failure}})
		if err := <-done; (err != nil) != (failure != nil) {
			t.Fatalf("update %d: handler error %v, runtime failure %v", n, err, failure)
		}
		if request.Request == nil || request.Request.Stream == nil || request.Request.Stream.Key != call {
			t.Fatalf("update %d was not sent in the stream of %s: %#v", n, call, request.Request)
		}
		return request.Request
	}
	keyframe := func(request *RequestPayload) bool { return len(request.Args) > 0 && request.Stream.Base == 0 }

	if first := emit("a", 0, nil); !keyframe(first) {
		t.Fatalf("first update is not a keyframe: %+v", first.Stream)
	}
	if other := emit("b", 0, nil); !keyframe(other) {
		t.Fatalf("first update of another call is not a keyframe: %+v", other.Stream)
	}
	if second := emit("a", 1, &ErrorInfo{Message: "tool_execution_update delta: no payload 1"}); keyframe(second) {
		t.Fatalf("second update is a keyframe, want a delta: %+v", second.Stream)
	}
	if third := emit("a", 2, nil); !keyframe(third) {
		t.Fatalf("the update after a failed one is a delta: %+v", third.Stream)
	}
	if fourth := emit("b", 1, nil); !keyframe(fourth) {
		t.Fatalf("the other call resumed from a payload the runtime may not hold: %+v", fourth.Stream)
	}
	if fifth := emit("a", 3, nil); keyframe(fifth) || fifth.Stream.Base != fifth.Stream.Seq-1 {
		t.Fatalf("the stream did not resume from the keyframe: %+v", fifth.Stream)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := handler(event("a", 4), cancelled); err == nil {
		t.Fatal("an update with a cancelled context succeeded")
	}
	if last := emit("a", 5, nil); !keyframe(last) {
		t.Fatalf("the update after an unsent one is a delta: %+v", last.Stream)
	}
}
