package sdk

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

// cancelProbe dispatches one provider_stream_simple request to a handler over a pipe whose frames the test reads by hand. A pipe's write returns when all of it was read, and a
// small frame is one write: once the test has read a frame's header and until it reads the body, the extension is inside the write of that frame.
type cancelProbe struct {
	tb   testing.TB
	host net.Conn
}

func startCancelProbe(tb testing.TB, requestContext context.Context, handler ProviderStreamSimpleFunc) *cancelProbe {
	tb.Helper()
	client, server := net.Pipe()
	extension := New("cancel-probe")
	extension.conn = newConn(client)
	extension.conn.start()
	extension.providerMu.Lock()
	extension.providerStreams = map[string]ProviderStreamSimpleFunc{"probe": handler}
	extension.providerMu.Unlock()
	args := json.RawMessage(`{"model":{},"context":{},"options":{}}`)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		extension.dispatchProviderStream(Context{ext: extension, ctx: requestContext}, "request-1", &requestMsg{Method: "provider_stream_simple", Tool: "probe", Args: args})
	}()
	tb.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-dispatched
		extension.requestWG.Wait()
		<-extension.conn.done
	})
	return &cancelProbe{tb: tb, host: server}
}

// header reads the length of the next frame.
func (p *cancelProbe) header() uint32 {
	p.tb.Helper()
	var header [4]byte
	if _, err := io.ReadFull(p.host, header[:]); err != nil {
		p.tb.Fatal(err)
	}
	return binary.BigEndian.Uint32(header[:])
}

// body reads the frame whose header gave size.
func (p *cancelProbe) body(size uint32) envelope {
	p.tb.Helper()
	data := make([]byte, size)
	if _, err := io.ReadFull(p.host, data); err != nil {
		p.tb.Fatal(err)
	}
	var message envelope
	if err := json.Unmarshal(data, &message); err != nil {
		p.tb.Fatal(err)
	}
	return message
}

// event reads the frame whose header gave size, which must be a stream event of the request.
func (p *cancelProbe) event(size uint32) {
	p.tb.Helper()
	if message := p.body(size); message.Type != msgNotify || message.Notify.Method != "provider_stream_event" {
		p.tb.Fatalf("the extension sent a %s frame, want a provider_stream_event", message.Type)
	}
}

// answer reads frames, the first being the one whose header gave size, up to the request's response. It returns the response's error message, its result and the number of
// stream events read on the way.
func (p *cancelProbe) answer(size uint32) (failure, result string, events int) {
	p.tb.Helper()
	for {
		message := p.body(size)
		switch {
		case message.Type == msgNotify && message.Notify.Method == "provider_stream_event":
			events++
		case message.Type == msgResponse:
			if message.Response.Error != nil {
				failure = message.Response.Error.Message
			}
			if result = strings.TrimSpace(string(message.Response.Result)); result == "null" {
				result = ""
			}
			return failure, result, events
		}
		size = p.header()
	}
}

// The host cancels the request while the extension writes an event, and the author, which waited for the cancel, ends its stream before that write returns. When the forwarder
// stops, the request's context is done and the stream has ended: both. The request is answered with the cancellation's error and no result, every time, and the event the
// author pushed after the cancel is not forwarded. (A select on the context and the stream's end answered the result one time in two.)
func TestProviderStreamEndedAfterTheCancelAnswersTheCancellation(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan struct{})
	probe := startCancelProbe(t, requestContext, func(ctx Context, _, _, _ map[string]any) (*ModelEventStream, error) {
		message, block := snapshotMessage1()
		stream := CreateAssistantMessageEventStream()
		stream.Push(map[string]any{"type": "start", "partial": message})
		go func() {
			defer close(ended)
			<-ctx.Done()
			block["text"] = "pushed after the cancel"
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
		}()
		return stream, nil
	})
	start := probe.header()
	cancel()
	<-ended
	probe.event(start)
	failure, result, later := probe.answer(probe.header())
	if later != 0 {
		t.Errorf("the extension forwarded %d events after the cancel, want none", later)
	}
	if failure != context.Canceled.Error() || result != "" {
		t.Errorf("the cancelled request was answered with %s, want no result and the error %q", describeAnswer(failure, result), context.Canceled.Error())
	}
}

// The author ended its stream and the extension forwarded all of it before the host cancelled: the cancel arrives while the extension writes its answer. The answer is the
// result: a cancel does not undo a stream that has ended and been forwarded. The Node, Rust, Lisp and Python SDKs read the cancel once after their forwarding loop as well.
func TestProviderStreamCancelledAfterItEndedAndWasForwardedAnswersTheResult(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := startCancelProbe(t, requestContext, func(Context, map[string]any, map[string]any, map[string]any) (*ModelEventStream, error) {
		message, block := snapshotMessage1()
		stream := CreateAssistantMessageEventStream()
		stream.Push(map[string]any{"type": "start", "partial": message})
		block["text"] = "whole"
		stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
		return stream, nil
	})
	probe.event(probe.header())
	probe.event(probe.header())
	// The next frame is the first of the answer: the extension has decided what it answers.
	first := probe.header()
	cancel()
	failure, result, later := probe.answer(first)
	if later != 0 {
		t.Errorf("the extension forwarded %d more events, want none: the stream had ended", later)
	}
	var message struct {
		Content []struct{ Text string } `json:"content"`
	}
	if failure != "" || json.Unmarshal([]byte(result), &message) != nil || len(message.Content) != 1 || message.Content[0].Text != "whole" {
		t.Errorf("the request whose stream ended before the cancel was answered with %s, want the message of the done event and no error", describeAnswer(failure, result))
	}
}

func describeAnswer(failure, result string) string {
	if result == "" {
		result = "no result"
	} else {
		result = "the result " + result
	}
	if failure == "" {
		return result + " and no error"
	}
	return fmt.Sprintf("%s and the error %q", result, failure)
}
