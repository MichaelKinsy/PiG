package sdk

import (
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

// A shutdown before ready ends the run with no error and closes the connection, as a shutdown after ready does: Pi's session shutdown never fails an extension (runner.ts emitSessionShutdownEvent), and the Node runtime returns. The Rust, Python and Lisp SDKs do the same.
func TestShutdownBeforeReadyEndsTheRunWithoutError(t *testing.T) {
	host, done := startBusExtension(t, New("shutdown-before-ready"))
	for host.readEnvelope(t).Type != msgRegister {
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "test"}})
	requireRunEndedAndClosed(t, host, done)
}

// The listener dispatches the host sent before the shutdown are answered once each, under their own IDs, before the run ends.
func TestShutdownBeforeReadyAnswersTheDispatchesBeforeIt(t *testing.T) {
	ext := New("shutdown-dispatches")
	heard := recordingListener(t, ext)
	host, done := startBusExtension(t, ext)
	on, _ := readBusCall(t, host, "events.on", "null")
	for host.readEnvelope(t).Type != msgRegister {
	}
	host.writeEnvelope(t, dispatchFrame(t, "d1", on.HandlerID, `{"n":"d1"}`))
	host.writeEnvelope(t, dispatchFrame(t, "d2", on.HandlerID, `{"n":"d2"}`))
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "test"}})
	requireDispatchesAnsweredOnce(t, host, "d1", "d2")
	requireRunEndedAndClosed(t, host, done)
	requireHeard(t, heard, "d1", "d2")
}

// A shutdown the host sends while the factory's own listener subscription waits for its result does not end the wait: the call returns, the run registers, and then ends on the shutdown. The dispatches the host sent before it are answered too.
func TestShutdownDuringTheFactorySubscriptionEndsTheRunAfterItRegisters(t *testing.T) {
	ext := New("shutdown-during-subscription")
	heard := recordingListener(t, ext)
	host, done := startBusExtension(t, ext)
	var call envelope
	for call = host.readEnvelope(t); call.Type != msgCall; call = host.readEnvelope(t) {
	}
	var on busWire
	if err := json.Unmarshal(call.Call.Args, &on); err != nil {
		t.Fatal(err)
	}
	host.writeEnvelope(t, dispatchFrame(t, "d1", on.HandlerID, `{"n":"d1"}`))
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "test"}})
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	if env := host.readEnvelope(t); env.Type != msgRegister {
		t.Fatalf("after the subscription's result the run sent %s, want register", env.Type)
	}
	requireDispatchesAnsweredOnce(t, host, "d1")
	requireRunEndedAndClosed(t, host, done)
	requireHeard(t, heard, "d1")
}

// A shutdown the host sends while a factory-time host call waits (the call dialled the connection Run later takes) does not fail the call: it returns the host's result, and Run registers and then ends on the shutdown.
func TestShutdownDuringTheFactoryEndsTheRunAfterItRegisters(t *testing.T) {
	host := factoryHost(t)
	ext := New("shutdown-in-factory")
	result := make(chan error, 1)
	go func() {
		_, err := ext.Events().On("ch", func(Context, any) error { return nil })
		result <- err
	}()
	host.accept(t)
	_ = host.nc.SetDeadline(time.Now().Add(5 * time.Second))
	var call envelope
	for call = host.readEnvelope(t); call.Type != msgCall; call = host.readEnvelope(t) {
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "test"}})
	// The ping follows the shutdown on the connection, so the factory's pump has read the shutdown once the pong arrives.
	host.writeEnvelope(t, envelope{Type: msgPing, Ping: &pingMsg{Nonce: "after-shutdown"}})
	if env := host.readEnvelope(t); env.Type != msgPong {
		t.Fatalf("answer to the ping = %s, want pong", env.Type)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	if err := awaitResult(t, result); err != nil {
		t.Fatalf("the factory's call = %v, want the host's result", err)
	}
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	if env := host.readEnvelope(t); env.Type != msgRegister {
		t.Fatalf("Run sent %s, want register", env.Type)
	}
	requireRunEndedAndClosed(t, host, done)
}

// recordingListener subscribes a listener, as a factory does before Run, that makes no host call and keeps each payload it hears.
func recordingListener(t *testing.T, ext *Extension) <-chan any {
	t.Helper()
	heard := make(chan any, 4)
	if _, err := ext.Events().On("ch", func(_ Context, data any) error { heard <- data; return nil }); err != nil {
		t.Fatal(err)
	}
	return heard
}

// requireHeard requires that the listener heard exactly the payloads {"n": id} of ids, one each.
func requireHeard(t *testing.T, heard <-chan any, ids ...string) {
	t.Helper()
	got := map[string]int{}
	for range ids {
		select {
		case data := <-heard:
			payload, _ := data.(map[string]any)
			id, _ := payload["n"].(string)
			got[id]++
		default:
			t.Fatalf("the listener heard %v, want one payload for each of %v", got, ids)
		}
	}
	for _, id := range ids {
		if got[id] != 1 {
			t.Fatalf("the listener heard %v, want one payload for each of %v", got, ids)
		}
	}
	if len(heard) != 0 {
		t.Fatalf("the listener heard %d more payloads", len(heard))
	}
}

// requireDispatchesAnsweredOnce reads until the extension's answers to the dispatches ids arrive, and requires one answer for each, with no error: a listener that ran answers with none, so an unknown-handler reply, a refusal or a cancellation fails.
func requireDispatchesAnsweredOnce(t *testing.T, host *mockHost, ids ...string) {
	t.Helper()
	answered := map[string]int{}
	for len(answered) < len(ids) {
		env := host.readEnvelope(t)
		if env.Type != msgResponse {
			continue
		}
		if env.Response == nil || env.Response.Error != nil {
			t.Fatalf("answer under %s = %+v, want success", env.ID, env.Response)
		}
		answered[env.ID]++
	}
	for _, id := range ids {
		if answered[id] != 1 {
			t.Fatalf("answers under %s = %d, want 1 (all: %v)", id, answered[id], answered)
		}
	}
}

func requireRunEndedAndClosed(t *testing.T, host *mockHost, done chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the host's shutdown")
	}
	if _, err := io.ReadFull(host.nc, make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read after the run = %v, want the extension's end of the connection", err)
	}
}
