package sdk

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"testing/synctest"
)

// Pi 0.87.1 packages/ai/src/utils/event-stream.ts:44-57,73-86 retains queued terminal events and their results; packages/ai/src/api/lazy.ts:31-38 forwards events before ending. A call_result cannot overtake earlier creation or terminal notifications.
func TestProviderStreamResultDoesNotOvertakeNotifications(t *testing.T) {
	for _, method := range []string{"stream", "streamSimple", "fetchDeferred"} {
		for _, reason := range []string{"stop", "error", "aborted"} {
			for _, started := range []bool{false, true} {
				name := method + "/" + reason + "/buffered-creation"
				if started {
					name = method + "/" + reason + "/live-stream"
				}
				t.Run(name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						testProviderStreamNotificationOrder(t, method, reason, started)
					})
				})
			}
		}
	}
}

func testProviderStreamNotificationOrder(t *testing.T, method, reason string, started bool) {
	t.Helper()
	client, server := net.Pipe()
	extension := New("stream-order")
	extension.conn = newConn(client)
	host := newConn(server)
	extension.conn.start()
	defer func() { _ = client.Close(); _ = server.Close(); extension.requestWG.Wait(); <-extension.conn.done }()
	type outcome struct {
		stream *ModelEventStream
		err    error
	}
	returned := make(chan outcome, 1)
	go func() {
		stream, err := (Context{ext: extension, ctx: context.Background()}).providerStream(providerObjectDeclaration{Handle: "captured"}, method, map[string]any{"id": "model", "provider": "owner", "api": "test"}, map[string]any{"messages": []any{}}, ProviderStreamOptions{})
		returned <- outcome{stream, err}
	}()
	frame, err := host.readFrame()
	if err != nil {
		t.Fatal(err)
	}
	var call envelope
	if err := json.Unmarshal(frame, &call); err != nil {
		t.Fatal(err)
	}
	var args struct {
		StreamID string `json:"streamId"`
	}
	if err := json.Unmarshal(call.Call.Args, &args); err != nil {
		t.Fatal(err)
	}
	applyNext := func() {
		t.Helper()
		extension.handleNotify(<-extension.conn.incoming)
		extension.markNotifyHandled()
	}
	if err := host.notify("model_stream_event", map[string]any{"streamId": args.StreamID, "started": true}); err != nil {
		t.Fatal(err)
	}
	var result *outcome
	if started {
		applyNext()
		// Creation returns without waiting for a terminal event or call_result.
		value := <-returned
		result = &value
	}
	message := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "queued terminal"}}, "stopReason": reason}
	event := map[string]any{"type": "done", "reason": reason, "message": message}
	if reason != "stop" {
		message["errorMessage"] = "provider " + reason
		event = map[string]any{"type": "error", "reason": reason, "error": message}
	}
	if err := host.notify("model_stream_event", map[string]any{"streamId": args.StreamID, "event": event}); err != nil {
		t.Fatal(err)
	}
	if err := host.send(envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage("null")}}); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	if !started {
		select {
		case early := <-returned:
			result = &early
			t.Errorf("Provider stream returned before queued creation was applied: %v", early.err)
		default:
		}
		applyNext()
	}
	applyNext()
	if result == nil {
		value := <-returned
		result = &value
	}
	if result.err != nil {
		t.Errorf("queued creation/terminal lost to call_result: %v", result.err)
		return
	}
	if got := result.stream.Result(); !reflect.DeepEqual(got, message) {
		t.Errorf("terminal result = %+v, want %+v", got, message)
	}
	var events []map[string]any
	for event := range result.stream.Events(context.Background()) {
		events = append(events, event)
	}
	if want := []map[string]any{event}; !reflect.DeepEqual(events, want) {
		t.Errorf("stream events = %+v, want %+v", events, want)
	}
	extension.requestWG.Wait()
	if len(extension.modelStreams) != 0 || len(extension.providerObjectCallbacks) != 0 {
		t.Fatal("completed stream retained its registration or callbacks")
	}
}
