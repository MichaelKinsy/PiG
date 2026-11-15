package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

const snapshotBurst = 100

// runProviderStream dispatches one provider_stream_simple request to a handler through a real connection and returns the text each forwarded event shows and the response's result.
func runProviderStream(tb testing.TB, handler ProviderStreamSimpleFunc) (texts []string, result map[string]any, failure string) {
	tb.Helper()
	client, server := net.Pipe()
	extension := New("snapshot-probe")
	extension.conn = newConn(client)
	host := newConn(server)
	extension.conn.start()
	defer func() { _ = client.Close(); _ = server.Close(); extension.requestWG.Wait(); <-extension.conn.done }()
	extension.providerMu.Lock()
	extension.providerStreams = map[string]ProviderStreamSimpleFunc{"probe": handler}
	extension.providerMu.Unlock()
	args := json.RawMessage(`{"model":{},"context":{},"options":{}}`)
	go extension.dispatchProviderStream(Context{ext: extension, ctx: context.Background()}, "request-1", &requestMsg{Method: "provider_stream_simple", Tool: "probe", Args: args})
	for {
		frame, err := host.readFrame()
		if err != nil {
			tb.Fatal(err)
		}
		var message envelope
		if err := json.Unmarshal(frame, &message); err != nil {
			tb.Fatal(err)
		}
		switch {
		case message.Type == msgNotify && message.Notify.Method == "provider_stream_event":
			var args struct {
				Result struct {
					Partial *struct {
						Content []struct{ Text string } `json:"content"`
					} `json:"partial"`
					Message *struct {
						Content []struct{ Text string } `json:"content"`
					} `json:"message"`
				} `json:"result"`
			}
			if err := json.Unmarshal(message.Notify.Args, &args); err != nil {
				tb.Fatal(err)
			}
			switch {
			case args.Result.Partial != nil:
				texts = append(texts, args.Result.Partial.Content[0].Text)
			case args.Result.Message != nil:
				texts = append(texts, args.Result.Message.Content[0].Text)
			default:
				texts = append(texts, "")
			}
		case message.Type == msgResponse:
			if message.Response.Error != nil {
				return texts, nil, message.Response.Error.Message
			}
			if err := json.Unmarshal(message.Response.Result, &result); err != nil {
				tb.Fatal(err)
			}
			return texts, result, ""
		}
	}
}

func snapshotMessage1() (message, block map[string]any) {
	block = map[string]any{"type": "text", "text": ""}
	message = map[string]any{"role": "assistant", "content": []any{block}, "stopReason": "stop", "timestamp": 1, "usage": map[string]any{"input": 0, "output": 0, "totalTokens": 0}}
	return message, block
}

// A provider's goroutine appends to its one partial message and pushes it, without yielding between the two, then changes the message again after the done event. The host must receive
// every event, and the result, as it was at the push: reading the author's map on the forwarding goroutine shows later text and is a data race (run with -race).
func TestProviderStreamForwardsEventsAsPushed(t *testing.T) {
	texts, result, failure := runProviderStream(t, func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
		message, block := snapshotMessage1()
		stream := CreateAssistantMessageEventStream()
		go func() {
			stream.Push(map[string]any{"type": "start", "partial": message})
			text := ""
			for range snapshotBurst {
				text += "x"
				block["text"] = text
				stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
			}
			block["text"] = text + "-AFTER"
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			block["text"] = "changed after done"
			message["stopReason"] = "error"
		}()
		return stream, nil
	})
	if failure != "" {
		t.Fatal(failure)
	}
	if len(texts) != snapshotBurst+2 {
		t.Fatalf("host received %d events, want %d", len(texts), snapshotBurst+2)
	}
	if texts[0] != "" {
		t.Errorf("start shows %q, want an empty message", texts[0])
	}
	for i := 1; i <= snapshotBurst; i++ {
		if want := strings.Repeat("x", i); texts[i] != want {
			t.Fatalf("text_delta %d shows %d characters, want %d", i, len(texts[i]), len(want))
		}
	}
	wantDone := strings.Repeat("x", snapshotBurst) + "-AFTER"
	if texts[snapshotBurst+1] != wantDone {
		t.Errorf("done shows %q, want %q", texts[snapshotBurst+1], wantDone)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != wantDone || result["stopReason"] != "stop" {
		t.Errorf("result = %v, want the message as it was at the done push", result)
	}
}

// End, like a done event, hands the host the result as it is when End returns.
func TestProviderStreamEndHandsTheHostTheResultAsItIs(t *testing.T) {
	_, result, failure := runProviderStream(t, func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
		message, block := snapshotMessage1()
		stream := CreateAssistantMessageEventStream()
		block["text"] = "ended"
		stream.End(message)
		block["text"] = "changed after End"
		return stream, nil
	})
	if failure != "" {
		t.Fatal(failure)
	}
	if content, _ := result["content"].([]any); len(content) != 1 || content[0].(map[string]any)["text"] != "ended" {
		t.Errorf("result = %v, want the message as it was at End", result)
	}
}

// An event that cannot be encoded ends the request with the encoder's error, as it did when the forwarder encoded it.
func TestProviderStreamEventThatCannotBeEncodedFailsTheRequest(t *testing.T) {
	texts, _, failure := runProviderStream(t, func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
		stream := CreateAssistantMessageEventStream()
		stream.Push(map[string]any{"type": "start", "partial": map[string]any{"content": []any{map[string]any{"text": "ok"}}}})
		stream.Push(map[string]any{"type": "text_delta", "partial": make(chan int)})
		stream.Push(map[string]any{"type": "text_end"})
		return stream, nil
	})
	if len(texts) != 1 || texts[0] != "ok" {
		t.Errorf("host received %q before the failure, want the first event only", texts)
	}
	if failure == "" || !strings.Contains(failure, "unsupported type") {
		t.Errorf("failure = %q, want the encoder's unsupported type error", failure)
	}
}

// The bytes a pushed event is queued as are what encoding the event would give, with the terminal event's message encoded once, and notifyResult writes an encoded result into the
// notification as notify writes the value.
func TestPushedEventWireEqualsEncodingTheEventAndNotifyResultEqualsNotify(t *testing.T) {
	for _, event := range []map[string]any{
		{"type": "done", "reason": "stop", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"text": "a<b>&\u2028"}}, "zz": 1.5}},
		{"type": "error", "reason": "error", "error": map[string]any{"errorMessage": "x"}, "aaa": []any{1}},
		{"type": "start", "partial": map[string]any{"content": []any{}}},
		{"type": "done", "reason": "stop"},
	} {
		stream := newModelEventStream()
		stream.enqueue(event, true)
		item := stream.queue[0]
		want, err := sdkjson.MarshalJS(event)
		if err != nil || item.err != nil {
			t.Fatal(err, item.err)
		}
		if !bytes.Equal(item.wire, want) {
			t.Errorf("wire = %s, want %s", item.wire, want)
		}
		client, server := net.Pipe()
		sender, host := newConn(client), newConn(server)
		sender.start()
		frames := make(chan []byte, 2)
		go func() {
			for range 2 {
				frame, err := host.readFrame()
				if err != nil {
					return
				}
				frames <- frame
			}
		}()
		if err := sender.notify("provider_stream_event", map[string]any{"request_id": "r\"1", "result": event}); err != nil {
			t.Fatal(err)
		}
		if err := sender.notifyResult("provider_stream_event", "r\"1", item.payload()); err != nil {
			t.Fatal(err)
		}
		first, second := <-frames, <-frames
		if !bytes.Equal(first, second) {
			t.Errorf("notifyResult wrote %s, notify wrote %s", second, first)
		}
		_ = client.Close()
		_ = server.Close()
	}
}

// BenchmarkProviderStreamPush is one provider response through the SDK: 100 text_delta events pushed (each after one more character was appended to the shared message) and a done
// event, encoded and written to the host. It measures a push and the forwarding of its event together, wherever the SDK encodes the event.
func BenchmarkProviderStreamPush(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		runProviderStream(b, func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
			message, block := snapshotMessage1()
			stream := CreateAssistantMessageEventStream()
			stream.Push(map[string]any{"type": "start", "partial": message})
			text := ""
			for range snapshotBurst {
				text += "x"
				block["text"] = text
				stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
			}
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			return stream, nil
		})
	}
}

// BenchmarkProviderStreamPushDistinctPartials pushes 1000 text_delta events whose partial messages are separate maps of 1 to 1000 characters, and a done event, so that an SDK
// that encodes an event at the push and one that encodes it later write the same bytes: the cost of the push path alone.
func BenchmarkProviderStreamPushDistinctPartials(b *testing.B) {
	b.ReportAllocs()
	texts := make([]string, 1000)
	for i := range texts {
		texts[i] = strings.Repeat("x", i+1)
	}
	for range b.N {
		runProviderStream(b, func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
			stream := CreateAssistantMessageEventStream()
			var last map[string]any
			for _, text := range texts {
				last, _ = snapshotMessage1()
				last["content"].([]any)[0].(map[string]any)["text"] = text
				stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": last})
			}
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": last})
			return stream, nil
		})
	}
}
