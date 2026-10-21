package sdk

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pi's factory runs before the runner binds, and the listeners it subscribes join the loader's shared emitter at once (event-bus.ts:13-34), so they hear an emit another extension makes while the factory runs. The native SDKs have no connection while the factory runs, so the first factory-time bus call dials the host (as the parseFrontmatter helper does), Run adopts that connection, and a pump answers the host's dispatches on it until then.

// factoryHost is a host the factory dialled: PIG_EXT_SOCKET names it, and the first bus call of a goroutine connects it.
func factoryHost(t *testing.T) *mockHost {
	t.Helper()
	host := newMockHost(t)
	t.Cleanup(host.close)
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	return host
}

// onInFactory subscribes handler on a goroutine, as a factory does before Run, and returns the host call it made and the channel its result arrives on.
func onInFactory(t *testing.T, host *mockHost, ext *Extension, channel string, handler EventBusHandler) (busWire, <-chan error) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		_, err := ext.Events().On(channel, handler)
		result <- err
	}()
	host.accept(t)
	// A peer that never answers fails the test instead of hanging it.
	_ = host.nc.SetDeadline(time.Now().Add(5 * time.Second))
	on, parent := readBusCall(t, host, "events.on", "null")
	if on.Channel != channel || on.HandlerID == "" || !on.Value || parent != "" {
		t.Fatalf("events.on = %+v parent %q, want channel %s, a handler ID, value true, no parent", on, parent, channel)
	}
	return on, result
}

func awaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not return")
		return nil
	}
}

// dispatchTo sends the host's events.dispatch request for handlerID and returns the response's error text, "" for none.
func dispatchTo(t *testing.T, host *mockHost, id, handlerID, channel string, payload any) string {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"handlerId": handlerID, "channel": channel, "json": payload})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "events.dispatch", Args: args}})
	for {
		env := host.readEnvelope(t)
		if env.Type == msgResponse && env.ID == id {
			if env.Response.Error != nil {
				return env.Response.Error.Message
			}
			return ""
		}
	}
}

// A listener subscribed while the factory runs is registered with the host at once, hears the host's dispatch before Run, and Run reuses the connection without registering it again.
func TestEventBusFactoryListenerRegistersAtOnAndHearsBeforeRun(t *testing.T) {
	host := factoryHost(t)
	heard := make(chan any, 2)
	ext := New("bus")
	on, result := onInFactory(t, host, ext, "ch", func(_ Context, data any) error { heard <- data; return nil })
	if err := awaitResult(t, result); err != nil {
		t.Fatalf("On = %v", err)
	}
	if msg := dispatchTo(t, host, "d1", on.HandlerID, "ch", map[string]any{"n": 1}); msg != "" {
		t.Fatalf("dispatch answered with error %q", msg)
	}
	select {
	case data := <-heard:
		if encoded, _ := json.Marshal(data); string(encoded) != `{"n":1}` {
			t.Fatalf("heard %s, want {\"n\":1}", encoded)
		}
	default:
		t.Fatal("the response arrived before the listener ran")
	}

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	// The first thing Run sends is the register message: the listener is not registered a second time.
	if env := host.readEnvelope(t); env.Type != msgRegister {
		t.Fatalf("Run sent %s first, want register: the factory's listener must not be registered again", env.Type)
	}
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	if msg := dispatchTo(t, host, "d2", on.HandlerID, "ch", "again"); msg != "" {
		t.Fatalf("dispatch after ready answered with error %q", msg)
	}
	stopBusExtension(t, host, done)
	if len(heard) != 1 {
		t.Fatalf("the listener heard %d more payloads after ready, want 1", len(heard))
	}
}

// The reply to a factory's own emit: the host dispatches the second emit's listener, the factory's, on the factory's connection while the first emit's call is in flight, and the factory's goroutine, blocked in Emit, does not stop it hearing.
func TestEventBusFactoryNestedReplyReachesTheFactoryListenerDuringEmit(t *testing.T) {
	host := factoryHost(t)
	var mu sync.Mutex
	var heard []any
	ext := New("bus")
	on, onResult := onInFactory(t, host, ext, "ack", func(_ Context, data any) error {
		mu.Lock()
		heard = append(heard, data)
		mu.Unlock()
		return nil
	})
	if err := awaitResult(t, onResult); err != nil {
		t.Fatalf("On = %v", err)
	}
	emitResult := make(chan error, 1)
	go func() { emitResult <- ext.Events().Emit("cross", "ping") }()
	var call envelope
	for call.Type != msgCall {
		call = host.readEnvelope(t)
	}
	if call.Call.Method != "events.emit" {
		t.Fatalf("host call %s, want events.emit", call.Call.Method)
	}
	// An earlier extension's listener of cross emitted ack: the host dispatches it before it answers the emit.
	if msg := dispatchTo(t, host, "d1", on.HandlerID, "ack", "pong"); msg != "" {
		t.Fatalf("dispatch answered with error %q", msg)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage("null")}})
	if err := awaitResult(t, emitResult); err != nil {
		t.Fatalf("Emit = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != 1 || heard[0] != "pong" {
		t.Fatalf("the factory's listener heard %v, want [pong]", heard)
	}
}

// A listener's error or panic is the dispatch's failure and nothing else, as it is once the extension runs: the pump keeps serving the next dispatch.
func TestEventBusFactoryListenerFailureFailsOnlyItsDispatch(t *testing.T) {
	host := factoryHost(t)
	ext := New("bus")
	onFail, r1 := onInFactory(t, host, ext, "fail", func(Context, any) error { return errBusTest })
	if err := awaitResult(t, r1); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := ext.Events().On("panic", func(Context, any) error { panic("listener-panicked") })
		results <- err
	}()
	onPanic, _ := readBusCall(t, host, "events.on", "null")
	if err := awaitResult(t, results); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := ext.Events().On("ok", func(Context, any) error { return nil })
		results <- err
	}()
	onOK, _ := readBusCall(t, host, "events.on", "null")
	if err := awaitResult(t, results); err != nil {
		t.Fatal(err)
	}
	for i, c := range []struct{ id, channel, want string }{{onFail.HandlerID, "fail", "listener-failed"}, {onPanic.HandlerID, "panic", "listener-panicked"}, {onOK.HandlerID, "ok", ""}} {
		msg := dispatchTo(t, host, "d"+string(rune('0'+i)), c.id, c.channel, nil)
		if (c.want == "") != (msg == "") || !strings.Contains(msg, c.want) {
			t.Fatalf("%s: error %q, want %q", c.channel, msg, c.want)
		}
	}
}

// An unsubscribe before Run reaches the host over the factory's connection.
func TestEventBusFactoryUnsubscribeReachesTheHost(t *testing.T) {
	host := factoryHost(t)
	ext := New("bus")
	offResult := make(chan func(), 1)
	go func() {
		off, _ := ext.Events().On("ch", func(Context, any) error { return nil })
		offResult <- off
	}()
	host.accept(t)
	_ = host.nc.SetDeadline(time.Now().Add(5 * time.Second))
	on, _ := readBusCall(t, host, "events.on", "null")
	off := <-offResult
	go off()
	got, _ := readBusCall(t, host, "events.off", "null")
	if got.HandlerID != on.HandlerID {
		t.Fatalf("events.off for %q, want %q", got.HandlerID, on.HandlerID)
	}
}

// An emit on "error" that the host reports nobody heard fails, as EventEmitter throws it. The host holds the factory's own listeners too, so its flag is the only verdict.
func TestEventBusFactoryEmitOnErrorFailsWhenTheHostHeardNobody(t *testing.T) {
	host := factoryHost(t)
	ext := New("bus")
	result := make(chan error, 1)
	go func() { result <- ext.Events().Emit("error", map[string]any{"x": 1}) }()
	host.accept(t)
	_ = host.nc.SetDeadline(time.Now().Add(5 * time.Second))
	readBusCall(t, host, "events.emit", `{"unhandledError":true}`)
	err := awaitResult(t, result)
	if err == nil || !strings.Contains(err.Error(), "unhandled error event") {
		t.Fatalf("Emit = %v, want the unhandled error event", err)
	}
}

// With no host to reach (no PIG_EXT_SOCKET), a factory-time emit fails rather than queueing: the delivery Pi makes at once has nowhere to go; a listener is queued for Run.
func TestEventBusFactoryWithoutAHost(t *testing.T) {
	t.Setenv("PIG_EXT_SOCKET", "")
	ext := New("bus")
	if err := ext.Events().Emit("ch", nil); !errors.Is(err, errBusNotConnected) {
		t.Fatalf("Emit = %v, want errBusNotConnected", err)
	}
	if _, err := ext.Events().On("ch", func(Context, any) error { return nil }); err != nil {
		t.Fatalf("On = %v, want the listener queued for Run", err)
	}
	if len(ext.eventBus.pending) != 1 {
		t.Fatalf("%d queued listeners, want 1", len(ext.eventBus.pending))
	}
}

// A listener the host dispatches before the extension registers gets a Context whose host calls are the bus's: a nested emit reaches the host, and any other call fails at once instead of waiting for a host that serves only the bus until the extension registers.
func TestEventBusFactoryListenerContextServesOnlyTheBus(t *testing.T) {
	host := factoryHost(t)
	var callErr error
	nested := make(chan error, 1)
	ext := New("bus")
	on, onResult := onInFactory(t, host, ext, "outer", func(ctx Context, _ any) error {
		nested <- ctx.Events().Emit("inner", nil)
		_, callErr = ctx.GetToolsExpanded()
		return nil
	})
	if err := awaitResult(t, onResult); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"handlerId": on.HandlerID, "channel": "outer", "json": nil})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "d1", Request: &requestMsg{Method: "events.dispatch", Args: args}})
	// The listener's nested emit reaches the host while the dispatch is open.
	readBusCall(t, host, "events.emit", "null")
	if err := awaitResult(t, nested); err != nil {
		t.Fatalf("nested Emit = %v", err)
	}
	for {
		if env := host.readEnvelope(t); env.Type == msgResponse && env.ID == "d1" {
			break
		}
	}
	if callErr == nil || !strings.Contains(callErr.Error(), "not registered") {
		t.Fatalf("a non-bus host call from the factory's listener = %v, want an error naming that the extension has not registered", callErr)
	}
}
