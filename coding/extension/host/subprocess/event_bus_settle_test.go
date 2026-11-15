package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// wiredPipeConn returns a started Conn whose inbound frames take the production route (Host.observeXref: an events.quiesce notify reaches the event bus and a close reaches its close hook) and the extension side of its pipe.
func wiredPipeConn(t *testing.T, h *Host, name string) (*Conn, net.Conn) {
	t.Helper()
	hostSide, extSide := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	conn := NewConn(name, hostSide)
	h.observeXref(conn)
	conn.Start(ctx)
	t.Cleanup(func() {
		cancel()
		_ = extSide.Close()
		<-conn.Done()
	})
	return conn, extSide
}

func quiesceFrame(emission string) *Envelope {
	return &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyEventsQuiesce, Args: mustJSONObject(map[string]string{"emission": emission})}}
}

// Listener realm behaviours for serveListenerRealm.
const (
	// realmNested is a realm already inside its own wait for an earlier emit of the same synchronous burst: it quiesces at once, before its dispatch response (the order of one connection's frames is the order the Host applies them in).
	realmNested = "nested"
	// realmLockstep is an idle realm: it answers the dispatch and quiesces only after the Host's events.proceed, which only the emitter's events.settle causes.
	realmLockstep = "lockstep"
	// realmErrors answers its dispatch with an error.
	realmErrors = "errors"
	// realmCloses exits while its dispatch request is outstanding.
	realmCloses = "closes"
)

// serveListenerRealm plays a listener realm on the extension side of a connection and reports the arguments of each events.dispatch request it receives.
func serveListenerRealm(side net.Conn, behaviour string) <-chan dispatchArgs {
	dispatched := make(chan dispatchArgs, 8)
	go func() {
		for {
			env, err := readEnvelopeFrom(side)
			if err != nil {
				return
			}
			switch {
			case env.Type == MsgRequest && env.Request != nil && env.Request.Method == methodEventsDispatch:
				var args dispatchArgs
				_ = json.Unmarshal(env.Request.Args, &args)
				dispatched <- args
				response := &Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{}}
				switch behaviour {
				case realmNested:
					_ = writeEnvelopeTo(side, quiesceFrame(args.Emission))
				case realmErrors:
					response.Response.Error = &ErrorInfo{Message: "listener failed"}
				case realmCloses:
					_ = side.Close()
					return
				}
				_ = writeEnvelopeTo(side, response)
			case env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == notifyEventsProceed:
				var args struct {
					Emission string `json:"emission"`
				}
				_ = json.Unmarshal(env.Notify.Args, &args)
				_ = writeEnvelopeTo(side, quiesceFrame(args.Emission))
			case env.Type == MsgPing:
				_ = writeEnvelopeTo(side, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			}
		}
	}()
	return dispatched
}

func awaitDispatched(t *testing.T, dispatched <-chan dispatchArgs, what string) dispatchArgs {
	t.Helper()
	select {
	case args := <-dispatched:
		return args
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return dispatchArgs{}
	}
}

// Pi event-bus.ts:15-17 and 19-25: emit calls every listener's synchronous prefix in one loop, and every continuation a prefix queued runs before any microtask the emitter queues after emit returns. The emitter's events.settle waits for every listener realm to report its microtask checkpoint, so the emission must not count as settled while the dispatch loop still has listeners to reach: a first realm that quiesced (already inside its own wait), failed or exited must not close the emission before the second realm is registered as pending. Nothing here waits on a clock: the first realm's report is applied before the Host writes the second realm's request.
func TestDispatchEventEmissionStaysOpenUntilTheLoopHasRegisteredEveryListenerRealm(t *testing.T) {
	for _, first := range []string{realmNested, realmErrors, realmCloses} {
		t.Run(first, func(t *testing.T) {
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			emitter, _ := startedPipeConn(t, "emitter")
			connA, sideA := wiredPipeConn(t, h, "listener-a")
			connB, sideB := wiredPipeConn(t, h, "listener-b")
			h.eventBus.add(&busListener{conn: connA, realm: "ra", handlerID: "a", channel: "ch"})
			h.eventBus.add(&busListener{conn: connB, realm: "rb", handlerID: "b", channel: "ch"})
			dispatchedA, dispatchedB := serveListenerRealm(sideA, first), serveListenerRealm(sideB, realmLockstep)
			id, emission := h.eventBus.newEmission(emitter)

			h.dispatchEvent(t.Context(), "ch", newChannelPayload("ch"), nil, "re", id, emission)

			if got := awaitDispatched(t, dispatchedA, "realm a"); got.Emission != id {
				t.Fatalf("realm a got emission %q, want %q", got.Emission, id)
			}
			if got := awaitDispatched(t, dispatchedB, "realm b"); got.Emission != id {
				t.Fatalf("realm b got emission %q, want %q", got.Emission, id)
			}
			h.eventBus.mu.Lock()
			pending, closed := emission.pending[connB], emission.closed
			h.eventBus.mu.Unlock()
			if pending != 1 {
				t.Fatalf("pending count of realm b = %d after the loop, want 1", pending)
			}
			select {
			case <-emission.done:
				t.Fatalf("the emission settled while realm b had not quiesced (closed=%v): the emitter's events.settle would return before realm b ran its continuations", closed)
			default:
			}

			// The emitter's settle releases realm b, which quiesces, and the emission then settles.
			settled := make(chan error, 1)
			go func() { settled <- h.eventBus.settle(t.Context(), id) }()
			select {
			case err := <-settled:
				if err != nil {
					t.Fatalf("settle: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("settle did not return after realm b quiesced")
			}
			select {
			case <-emission.done:
			default:
				t.Fatal("settle returned with the emission still open")
			}
		})
	}
}

// With no foreign listener left pending when the loop ends, the emission is settled at once and the emit answers without an emission id, as before.
func TestDispatchEventEmissionSettlesWhenTheLoopEndsWithNoPendingListener(t *testing.T) {
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	emitter, _ := startedPipeConn(t, "emitter")
	conn, side := wiredPipeConn(t, h, "listener")
	h.eventBus.add(&busListener{conn: conn, realm: "ra", handlerID: "a", channel: "ch"})
	dispatched := serveListenerRealm(side, realmNested)
	id, emission := h.eventBus.newEmission(emitter)

	h.dispatchEvent(t.Context(), "ch", newChannelPayload("ch"), nil, "re", id, emission)

	awaitDispatched(t, dispatched, "the realm")
	h.eventBus.mu.Lock()
	pending := len(emission.pending)
	h.eventBus.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending realms = %d after the loop, want none", pending)
	}
	select {
	case <-emission.done:
	default:
		t.Fatal("the emission stayed open with no pending listener")
	}
}

// The routed emit call: with a nested realm and an idle realm the emit answers with an emission id, the emission is still open, and events.settle returns once the idle realm has quiesced; with only the nested realm the emit answers null and keeps no emission.
func TestHostEventBusEmitCallSettlesAfterEveryListenerRealm(t *testing.T) {
	for _, test := range []struct {
		name      string
		listeners []string
	}{
		{"nested then idle", []string{realmNested, realmLockstep}},
		{"nested only", []string{realmNested}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			emitterConn, emitterPeer := wiredPipeConn(t, h, "emitter")
			emitter := withConn(&managedExt{config: ExtConfig{Name: "emitter"}, host: h, nodeRealm: true}, emitterConn)
			h.runCall(emitter, emitterConn, "hello", &CallPayload{Method: callXrefHello, Args: []byte(`{"realm":"re"}`)}, nil)
			readFramed(t, emitterPeer)
			var served []<-chan dispatchArgs
			for i, behaviour := range test.listeners {
				name := string(rune('a' + i))
				conn, side := wiredPipeConn(t, h, "listener-"+name)
				member := withConn(&managedExt{config: ExtConfig{Name: "listener-" + name}, host: h, nodeRealm: true}, conn)
				h.runCall(member, conn, "hello", &CallPayload{Method: callXrefHello, Args: []byte(`{"realm":"r` + name + `"}`)}, nil)
				readFramed(t, side)
				h.runCall(member, conn, "on", &CallPayload{Method: callEventsOn, Args: []byte(`{"channel":"ch","handlerId":"` + name + `"}`)}, nil)
				if env := readFramed(t, side); env.CallResult == nil || env.CallResult.Error != nil {
					t.Fatalf("events.on %s = %+v", name, env)
				}
				served = append(served, serveListenerRealm(side, behaviour))
			}

			h.runCall(emitter, emitterConn, "emit", &CallPayload{Method: callEventsEmit, Args: []byte(`{"channel":"ch"}`)}, nil)
			env := readFramed(t, emitterPeer)
			if env.CallResult == nil || env.CallResult.Error != nil {
				t.Fatalf("events.emit = %+v", env)
			}
			for i, dispatched := range served {
				awaitDispatched(t, dispatched, "listener "+test.listeners[i])
			}
			var result struct {
				Emission string `json:"emission"`
			}
			if string(env.CallResult.Result) == "null" {
				if len(test.listeners) != 1 {
					t.Fatalf("events.emit answered null with an idle realm pending")
				}
				return
			}
			if err := json.Unmarshal(env.CallResult.Result, &result); err != nil || result.Emission == "" {
				t.Fatalf("events.emit result = %s, want an emission id", env.CallResult.Result)
			}
			if len(test.listeners) == 1 {
				t.Fatalf("events.emit answered %s with no realm pending", env.CallResult.Result)
			}
			h.eventBus.mu.Lock()
			emission := h.eventBus.emissions[result.Emission]
			h.eventBus.mu.Unlock()
			if emission == nil {
				t.Fatal("the emission was forgotten before events.settle")
			}
			select {
			case <-emission.done:
				t.Fatal("the emission settled before the idle realm quiesced")
			default:
			}
			// runCall serves the call on this goroutine, so a settle that never returns must fail through the bounded read below, not hang the test.
			go h.runCall(emitter, emitterConn, "settle", &CallPayload{Method: callEventsSettle, Args: mustJSONObject(map[string]string{"emission": result.Emission})}, nil)
			if env := readFramed(t, emitterPeer); env.CallResult == nil || env.CallResult.Error != nil {
				t.Fatalf("events.settle = %+v", env)
			}
			select {
			case <-emission.done:
			default:
				t.Fatal("events.settle returned with the emission still open")
			}
		})
	}
}
