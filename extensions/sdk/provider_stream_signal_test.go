package sdk

import (
	"context"
	"encoding/json"
	"net"
	"testing"
)

// startArmedCancelProbe is startCancelProbe with the request armed as the main loop arms it (armRequestScope): the handler's Context holds the request's signal, a child of
// the request's context. The returned function delivers a cancel frame of the request as the main loop does (cancelRequest): signal-only, the abort of a turn, or ordinary.
func startArmedCancelProbe(tb testing.TB, handler ProviderStreamSimpleFunc) (*cancelProbe, func(signalOnly bool)) {
	tb.Helper()
	client, server := net.Pipe()
	extension := New("cancel-probe")
	extension.conn = newConn(client)
	extension.conn.start()
	extension.providerMu.Lock()
	extension.providerStreams = map[string]ProviderStreamSimpleFunc{"probe": handler}
	extension.providerMu.Unlock()
	ctx, scope := extension.armRequestScope("request-1")
	args := json.RawMessage(`{"model":{},"context":{},"options":{}}`)
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		defer scope.end()
		extension.dispatchProviderStream(ctx, "request-1", &requestMsg{Method: "provider_stream_simple", Tool: "probe", Args: args})
	}()
	tb.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-dispatched
		extension.requestWG.Wait()
		<-extension.conn.done
	})
	return &cancelProbe{tb: tb, host: server}, func(signalOnly bool) {
		extension.cancelRequest(envelope{Type: msgCancel, ID: "request-1", Cancel: &cancelMsg{RequestID: "request-1", Reason: context.Canceled.Error(), SignalOnly: signalOnly}})
	}
}

// Pi's consumer of a response has no check of the abort signal (packages/agent/src/agent-loop.ts:424-478): what the author's stream gives after the abort decides the turn's
// message. The host's cancel of a provider_stream_simple request at the abort of a turn is signal-only: the author's signal (Context.Done) is done, the request goes on, the
// event the author pushed after it is forwarded, and the request is answered with the author's result. (The forwarder stopped at the signal and answered the cancellation.)
func TestProviderStreamGoesOnAfterASignalOnlyCancelAndAnswersTheAuthorsResult(t *testing.T) {
	probe, cancel := startArmedCancelProbe(t, func(ctx Context, _, _, _ map[string]any) (*ModelEventStream, error) {
		message, block := snapshotMessage1()
		stream := CreateAssistantMessageEventStream()
		stream.Push(map[string]any{"type": "start", "partial": message})
		go func() {
			<-ctx.Done()
			block["text"] = "pushed after the abort"
			stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "pushed after the abort", "partial": message})
			stream.End(message)
		}()
		return stream, nil
	})
	probe.event(probe.header())
	cancel(true)
	failure, result, later := probe.answer(probe.header())
	if later != 1 {
		t.Errorf("the extension forwarded %d events after the signal-only cancel, want 1: the text_delta its author pushed after the abort", later)
	}
	var message struct {
		Content []struct{ Text string } `json:"content"`
	}
	if failure != "" || json.Unmarshal([]byte(result), &message) != nil || len(message.Content) != 1 || message.Content[0].Text != "pushed after the abort" {
		t.Errorf("the request whose signal was aborted was answered with %s, want the author's result (the message with the text \"pushed after the abort\") and no error", describeAnswer(failure, result))
	}
}

// An ordinary cancel ends the request, also after a signal-only cancel: the forwarder stops, and the request is answered with the cancellation's error and no result.
func TestProviderStreamOrdinaryCancelAfterASignalOnlyCancelAnswersTheCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	aborted := make(chan struct{})
	probe, cancel := startArmedCancelProbe(t, func(ctx Context, _, _, _ map[string]any) (*ModelEventStream, error) {
		message, _ := snapshotMessage1()
		stream := CreateAssistantMessageEventStream()
		stream.Push(map[string]any{"type": "start", "partial": message})
		go func() {
			<-ctx.Done()
			close(aborted)
			<-release
			stream.End(message)
		}()
		return stream, nil
	})
	probe.event(probe.header())
	cancel(true)
	<-aborted
	// The ordinary cancel is delivered while the test reads: a request already answered holds the connection's write until its frame is read.
	go cancel(false)
	failure, result, later := probe.answer(probe.header())
	if later != 0 {
		t.Errorf("the extension forwarded %d events after the cancels, want none", later)
	}
	if failure != context.Canceled.Error() || result != "" {
		t.Errorf("the request an ordinary cancel ended was answered with %s, want no result and the error %q", describeAnswer(failure, result), context.Canceled.Error())
	}
}
