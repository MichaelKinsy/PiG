package sdk

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The wire the SDK speaks for upstream's pi.events (packages/coding-agent/src/core/event-bus.ts:12-33): events.on and events.emit calls flagged by value, events.off, and the Host's events.dispatch request.

type busWire struct {
	Channel   string          `json:"channel"`
	HandlerID string          `json:"handlerId"`
	Value     bool            `json:"value"`
	JSON      json.RawMessage `json:"json"`
}

// readBusCall returns the next call the extension sends, answering it with result.
func readBusCall(t *testing.T, host *mockHost, method, result string) (busWire, string) {
	t.Helper()
	for {
		env := host.readEnvelope(t)
		if env.Type != msgCall || env.Call == nil {
			continue
		}
		if env.Call.Method != method {
			t.Fatalf("host call %s, want %s", env.Call.Method, method)
		}
		var args busWire
		if err := json.Unmarshal(env.Call.Args, &args); err != nil {
			t.Fatal(err)
		}
		reply := &callResultMsg{}
		if result != "" {
			reply.Result = json.RawMessage(result)
		}
		host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: reply})
		return args, env.Call.ParentRequestID
	}
}

func startBusExtension(t *testing.T, ext *Extension) (*mockHost, chan error) {
	t.Helper()
	host := newMockHost(t)
	t.Cleanup(host.close)
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	// A peer that never answers fails the test instead of hanging it.
	_ = host.nc.SetDeadline(time.Now().Add(5 * time.Second))
	return host, done
}

func readyBusExtension(t *testing.T, host *mockHost) {
	t.Helper()
	for {
		env := host.readEnvelope(t)
		if env.Type == msgRegister {
			break
		}
	}
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
}

func stopBusExtension(t *testing.T, host *mockHost, done chan error) {
	t.Helper()
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A listener declared while the factory runs is registered before the register message, as a node factory's pi.events.on is (loader.ts runs the factory before the runtime registers), and carries the by-value flag.
func TestEventBusFactoryListenerIsRegisteredBeforeRegister(t *testing.T) {
	ext := New("bus")
	if _, err := ext.Events().On("ch", func(Context, any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	host, done := startBusExtension(t, ext)
	on, parent := readBusCall(t, host, "events.on", "null")
	if on.Channel != "ch" || on.HandlerID == "" || !on.Value || parent != "" {
		t.Fatalf("events.on = %+v parent %q, want channel ch, a handler ID, value true, no parent", on, parent)
	}
	readyBusExtension(t, host)
	stopBusExtension(t, host, done)
}

// Dispatch runs the handler with the payload decoded from the JSON the Host sends and answers after the handler returns (event-bus.ts:19-25: the listener's synchronous prefix is the unit of work).
func TestEventBusDispatchRunsHandlerThenAnswers(t *testing.T) {
	got := make(chan any, 1)
	ext := New("bus")
	if _, err := ext.Events().On("ch", func(_ Context, data any) error { got <- data; return nil }); err != nil {
		t.Fatal(err)
	}
	host, done := startBusExtension(t, ext)
	on, _ := readBusCall(t, host, "events.on", "null")
	readyBusExtension(t, host)
	args, _ := json.Marshal(map[string]any{"handlerId": on.HandlerID, "channel": "ch", "json": map[string]any{"a": []int{1, 2}}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "d1", Request: &requestMsg{Method: "events.dispatch", Args: args}})
	var response envelope
	for response.Type != msgResponse {
		response = host.readEnvelope(t)
	}
	if response.ID != "d1" || response.Response == nil || response.Response.Error != nil {
		t.Fatalf("response = %+v", response.Response)
	}
	select {
	case data := <-got:
		encoded, _ := json.Marshal(data)
		if string(encoded) != `{"a":[1,2]}` {
			t.Fatalf("payload = %s", encoded)
		}
	default:
		t.Fatal("the response arrived before the handler ran")
	}
	stopBusExtension(t, host, done)
}

// A handler's error or panic is the dispatch's failure and nothing else: the extension keeps serving (event-bus.ts:19-25 catches and prints, and never rethrows).
func TestEventBusHandlerErrorAndPanicFailOnlyTheirDispatch(t *testing.T) {
	ext := New("bus")
	_, _ = ext.Events().On("fail", func(Context, any) error { return errBusTest })
	_, _ = ext.Events().On("panic", func(Context, any) error { panic("listener-panicked") })
	_, _ = ext.Events().On("ok", func(Context, any) error { return nil })
	host, done := startBusExtension(t, ext)
	ids := map[string]string{}
	for _, channel := range []string{"fail", "panic", "ok"} {
		on, _ := readBusCall(t, host, "events.on", "null")
		ids[on.Channel] = on.HandlerID
		_ = channel
	}
	readyBusExtension(t, host)
	for i, c := range []struct{ channel, wantError string }{{"fail", "listener-failed"}, {"panic", "listener-panicked"}, {"ok", ""}} {
		args, _ := json.Marshal(map[string]any{"handlerId": ids[c.channel], "channel": c.channel, "json": nil})
		id := "d" + string(rune('0'+i))
		host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "events.dispatch", Args: args}})
		var response envelope
		for response.Type != msgResponse {
			response = host.readEnvelope(t)
		}
		message := ""
		if response.Response.Error != nil {
			message = response.Response.Error.Message
		}
		if (c.wantError == "") != (message == "") || !strings.Contains(message, c.wantError) {
			t.Fatalf("%s: error %q, want %q", c.channel, message, c.wantError)
		}
	}
	stopBusExtension(t, host, done)
}

type busTestError struct{}

func (busTestError) Error() string { return "listener-failed" }

var errBusTest = busTestError{}

// Emit sends the payload as JSON under the by-value flag and takes the calling request as the call's parent, so a handler's nested emit is ordered in its own lane and cancelled with it. The unhandled "error" rule surfaces as the emit's error (event-bus.ts:15-17, EventEmitter throws).
func TestEventBusEmitCarriesPayloadAndParent(t *testing.T) {
	ext := New("bus")
	emitted := make(chan error, 2)
	ext.Command("go", "", func(ctx Context, _ string) error {
		emitted <- ctx.Events().Emit("ch", map[string]any{"a": 1})
		emitted <- ctx.Events().Emit("error", "boom")
		return nil
	})
	host, done := startBusExtension(t, ext)
	readyBusExtension(t, host)
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "cmd1", Request: &requestMsg{Method: "command", Tool: "go"}})
	args, parent := readBusCall(t, host, "events.emit", "null")
	if args.Channel != "ch" || !args.Value || string(args.JSON) != `{"a":1}` || parent != "cmd1" {
		t.Fatalf("events.emit = %+v parent %q", args, parent)
	}
	if err := <-emitted; err != nil {
		t.Fatal(err)
	}
	readBusCall(t, host, "events.emit", `{"unhandledError":true}`)
	if err := <-emitted; err == nil {
		t.Fatal("an emit on error with no listener returned nil")
	}
	stopBusExtension(t, host, done)
}

// Unsubscribe removes the listener with one events.off call and is idempotent (event-bus.ts:27, EventEmitter.off).
func TestEventBusUnsubscribeSendsOneOff(t *testing.T) {
	ext := New("bus")
	unsubscribed := make(chan struct{})
	ext.Command("off", "", func(ctx Context, _ string) error {
		unsubscribe, err := ctx.Events().On("ch", func(Context, any) error { return nil })
		if err != nil {
			return err
		}
		unsubscribe()
		unsubscribe()
		close(unsubscribed)
		return nil
	})
	host, done := startBusExtension(t, ext)
	readyBusExtension(t, host)
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "cmd1", Request: &requestMsg{Method: "command", Tool: "off"}})
	on, parent := readBusCall(t, host, "events.on", "null")
	if parent != "cmd1" {
		t.Fatalf("events.on parent %q, want the command request", parent)
	}
	off, _ := readBusCall(t, host, "events.off", "null")
	if off.HandlerID != on.HandlerID {
		t.Fatalf("events.off %q, want %q", off.HandlerID, on.HandlerID)
	}
	select {
	case <-unsubscribed:
	case <-time.After(10 * time.Second):
		t.Fatal("the second unsubscribe waited for a host call it should not send")
	}
	stopBusExtension(t, host, done)
}

// A listener the Host dispatches to while the extension is still loading (after register, before ready) is served, as a node runtime serves it during its factory.
func TestEventBusDispatchDuringLoadIsServed(t *testing.T) {
	got := make(chan any, 1)
	ext := New("bus")
	if _, err := ext.Events().On("ch", func(_ Context, data any) error { got <- data; return nil }); err != nil {
		t.Fatal(err)
	}
	host, done := startBusExtension(t, ext)
	on, _ := readBusCall(t, host, "events.on", "null")
	for host.readEnvelope(t).Type != msgRegister {
	}
	args, _ := json.Marshal(map[string]any{"handlerId": on.HandlerID, "channel": "ch", "json": "early"})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "d1", Request: &requestMsg{Method: "events.dispatch", Args: args}})
	var response envelope
	for response.Type != msgResponse {
		response = host.readEnvelope(t)
	}
	if response.ID != "d1" || response.Response == nil || response.Response.Error != nil {
		t.Fatalf("response = %+v", response.Response)
	}
	if data := <-got; data != "early" {
		t.Fatalf("payload = %v", data)
	}
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	stopBusExtension(t, host, done)
}

// A listener served while the extension loads may unsubscribe itself; the Host then releases it with an events.release notify that can arrive before ready. The load does not fail on it, and the released listener is gone.
func TestEventBusReleaseDuringLoadIsServed(t *testing.T) {
	var unsubscribe func()
	ran := make(chan struct{}, 2)
	ext := New("bus")
	unsubscribe, err := ext.Events().On("ch", func(Context, any) error {
		unsubscribe()
		ran <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	host, done := startBusExtension(t, ext)
	on, _ := readBusCall(t, host, "events.on", "null")
	for host.readEnvelope(t).Type != msgRegister {
	}
	args, _ := json.Marshal(map[string]any{"handlerId": on.HandlerID, "channel": "ch", "json": 1})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "d1", Request: &requestMsg{Method: "events.dispatch", Args: args}})
	off, _ := readBusCall(t, host, "events.off", "null")
	if off.HandlerID != on.HandlerID {
		t.Fatalf("events.off %q, want %q", off.HandlerID, on.HandlerID)
	}
	var response envelope
	for response.Type != msgResponse {
		response = host.readEnvelope(t)
	}
	release, _ := json.Marshal(map[string]any{"handlerId": on.HandlerID})
	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "events.release", Args: release}})
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "d2", Request: &requestMsg{Method: "events.dispatch", Args: args}})
	response = envelope{}
	for response.Type != msgResponse {
		response = host.readEnvelope(t)
	}
	if response.ID != "d2" || response.Response == nil || response.Response.Error == nil || !strings.Contains(response.Response.Error.Message, "Unknown event bus handler") {
		t.Fatalf("dispatch after release = %+v, want an unknown handler", response.Response)
	}
	if len(ran) != 1 {
		t.Fatalf("listener ran %d times, want 1", len(ran))
	}
	stopBusExtension(t, host, done)
}

// A host that refuses events.on fails the subscription and nothing stays subscribed: a later dispatch for that listener is unknown. A payload with no JSON form fails Emit before any host call (upstream's JSON.stringify would throw for a BigInt or cycle in the emitter).
func TestEventBusErrorPaths(t *testing.T) {
	ext := New("bus")
	results := make(chan error, 3)
	ext.Command("go", "", func(ctx Context, _ string) error {
		_, err := ctx.Events().On("ch", func(Context, any) error { return nil })
		results <- err
		results <- ctx.Events().Emit("ch", make(chan int))
		_, err = ctx.Events().On("ch", nil)
		results <- err
		return nil
	})
	host, done := startBusExtension(t, ext)
	readyBusExtension(t, host)
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "cmd1", Request: &requestMsg{Method: "command", Tool: "go"}})
	for {
		env := host.readEnvelope(t)
		if env.Type == msgCall && env.Call.Method == "events.on" {
			host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Error: &errorInfo{Message: "refused"}}})
			break
		}
	}
	if err := <-results; err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("On error = %v, want the host's refusal", err)
	}
	if err := <-results; err == nil {
		t.Fatal("Emit of a payload with no JSON form returned nil")
	}
	if err := <-results; err == nil {
		t.Fatal("On with a nil handler returned nil")
	}
	stopBusExtension(t, host, done)
}
