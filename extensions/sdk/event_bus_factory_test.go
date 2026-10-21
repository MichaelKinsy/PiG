package sdk

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pi's factory runs before the runner binds, and pi.events.emit in it is the synchronous EventEmitter emit (event-bus.ts:13-34): the listeners of the extensions loaded before hear it at once, then the factory's own, each before emit returns, and an emit on "error" that nobody hears throws. The native SDKs have no connection while the factory runs, so the first factory-time bus call dials the host (as the parseFrontmatter helper does) and Run adopts that connection.

// factoryEmit calls emit on a goroutine, as a factory does before Run, and returns the host the factory dialled and the channel its result arrives on.
func factoryEmit(t *testing.T, emit func() error) (*mockHost, <-chan error) {
	t.Helper()
	host := newMockHost(t)
	t.Cleanup(host.close)
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	result := make(chan error, 1)
	go func() { result <- emit() }()
	host.accept(t)
	// A peer that never answers fails the test instead of hanging it.
	_ = host.nc.SetDeadline(time.Now().Add(5 * time.Second))
	return host, result
}

func awaitEmit(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Emit did not return")
		return nil
	}
}

// The emit reaches the host before Emit returns; the factory's own listener, which the host does not hold until Run registers it, runs inline and has finished when Emit returns. Run then reuses the connection the emit dialled and registers the listener.
func TestEventBusFactoryEmitReachesTheHostAndOwnListenersAtOnce(t *testing.T) {
	var mu sync.Mutex
	var heard []any
	ext := New("bus")
	if _, err := ext.Events().On("ch", func(_ Context, data any) error {
		mu.Lock()
		heard = append(heard, data)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host, result := factoryEmit(t, func() error { return ext.Events().Emit("ch", map[string]any{"n": 1}) })
	emit, parent := readBusCall(t, host, "events.emit", "null")
	if emit.Channel != "ch" || !emit.Value || parent != "" || string(emit.JSON) != `{"n":1}` {
		t.Fatalf("events.emit = %+v parent %q, want channel ch, value true, json {\"n\":1}, no parent", emit, parent)
	}
	if err := awaitEmit(t, result); err != nil {
		t.Fatalf("Emit = %v", err)
	}
	mu.Lock()
	got, _ := json.Marshal(heard)
	mu.Unlock()
	if string(got) != `[{"n":1}]` {
		t.Fatalf("own listener heard %s before Emit returned, want [{\"n\":1}]", got)
	}

	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	on, _ := readBusCall(t, host, "events.on", "null")
	if on.Channel != "ch" || on.HandlerID == "" {
		t.Fatalf("events.on = %+v, want the factory's listener registered when Run connects", on)
	}
	readyBusExtension(t, host)
	stopBusExtension(t, host, done)
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != 1 {
		t.Fatalf("the listener heard %d emits, want 1: Run must not deliver the factory's emit again", len(heard))
	}
}

// Only the listeners subscribed before the emit hear it, in subscription order; a listener's error and panic are reported and reach neither the emitter nor the listeners after it (event-bus.ts safeHandler); an unsubscribed listener hears nothing.
func TestEventBusFactoryEmitRunsListenersInOrderAndIsolatesFailures(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(name string) EventBusHandler {
		return func(Context, any) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}
	}
	ext := New("bus")
	bus := ext.Events()
	mustOn := func(handler EventBusHandler) func() {
		off, err := bus.On("ch", handler)
		if err != nil {
			t.Fatal(err)
		}
		return off
	}
	mustOn(record("first"))
	mustOn(func(Context, any) error { return errors.New("boom") })
	mustOn(func(Context, any) error { panic("kaboom") })
	off := mustOn(record("unsubscribed"))
	off()
	mustOn(record("last"))
	host, result := factoryEmit(t, func() error {
		err := bus.Emit("ch", nil)
		// A listener subscribed after the emit does not hear it.
		_, subscribeErr := bus.On("ch", record("late"))
		return errors.Join(err, subscribeErr)
	})
	readBusCall(t, host, "events.emit", "null")
	if err := awaitEmit(t, result); err != nil {
		t.Fatalf("Emit = %v, want nil: a listener's failure is reported, not returned", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(order, ","); got != "first,last" {
		t.Fatalf("listeners ran %q, want first,last", got)
	}
}

// An emit on "error" that nobody hears fails, as EventEmitter throws it: the host holds no listener (it says so) and neither does the factory. A listener of the factory's own handles it, even though the host does not know that listener yet.
func TestEventBusFactoryEmitOnErrorFailsWhenNobodyHears(t *testing.T) {
	ext := New("bus")
	host, result := factoryEmit(t, func() error { return ext.Events().Emit("error", map[string]any{"x": 1}) })
	readBusCall(t, host, "events.emit", `{"unhandledError":true}`)
	err := awaitEmit(t, result)
	if err == nil || !strings.Contains(err.Error(), "unhandled error event") {
		t.Fatalf("Emit = %v, want the unhandled error event", err)
	}

	heard := make(chan any, 1)
	handled := New("handled")
	if _, onErr := handled.Events().On("error", func(_ Context, data any) error { heard <- data; return nil }); onErr != nil {
		t.Fatal(onErr)
	}
	// The same socket serves the second extension: the connection the first emit dialled is the one for this socket.
	result2 := make(chan error, 1)
	go func() { result2 <- handled.Events().Emit("error", "bad") }()
	readBusCall(t, host, "events.emit", `{"unhandledError":true}`)
	if err := awaitEmit(t, result2); err != nil {
		t.Fatalf("Emit with an own error listener = %v, want nil", err)
	}
	select {
	case data := <-heard:
		if data != "bad" {
			t.Fatalf("error listener heard %v", data)
		}
	default:
		t.Fatal("the own error listener did not run")
	}
}

// With no host to reach (no PIG_EXT_SOCKET), a factory-time emit fails rather than queueing: the delivery Pi makes at once has nowhere to go.
func TestEventBusFactoryEmitWithoutAHostFails(t *testing.T) {
	t.Setenv("PIG_EXT_SOCKET", "")
	err := New("bus").Events().Emit("ch", nil)
	if !errors.Is(err, errBusNotConnected) {
		t.Fatalf("Emit = %v, want errBusNotConnected", err)
	}
}

// A listener the factory's emit runs gets a Context whose host calls are the bus's: a nested emit reaches the listeners subscribed before it, and any other call fails at once instead of waiting for a host that serves only the bus until the extension registers.
func TestEventBusFactoryListenerContextServesOnlyTheBus(t *testing.T) {
	var mu sync.Mutex
	var heard []string
	var callErr error
	ext := New("bus")
	bus := ext.Events()
	if _, err := bus.On("inner", func(_ Context, data any) error {
		mu.Lock()
		heard = append(heard, "inner")
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.On("outer", func(ctx Context, _ any) error {
		if err := ctx.Events().Emit("inner", nil); err != nil {
			return err
		}
		_, callErr = ctx.GetToolsExpanded()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	host, result := factoryEmit(t, func() error { return bus.Emit("outer", nil) })
	// outer reaches the host, then the nested inner does.
	readBusCall(t, host, "events.emit", "null")
	readBusCall(t, host, "events.emit", "null")
	if err := awaitEmit(t, result); err != nil {
		t.Fatalf("Emit = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(heard, ",") != "inner" {
		t.Fatalf("nested emit reached %v, want the inner listener", heard)
	}
	if callErr == nil || !strings.Contains(callErr.Error(), "not registered") {
		t.Fatalf("a non-bus host call from the factory's listener = %v, want an error naming that the extension has not registered", callErr)
	}
}
