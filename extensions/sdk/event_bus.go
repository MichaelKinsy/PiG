// SPDX-License-Identifier: MIT

package sdk

// Ports packages/coding-agent/src/core/event-bus.ts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// EventBusHandler receives one payload of a channel. It mirrors the handler of upstream's `pi.events.on(channel, handler)`: it runs as one unit of work, its error is reported and reaches neither the emitter nor the listeners after it, and a panic is treated as an error.
//
// The payload is the emitter's JSON value: the emitter's JSON.stringify view for a node emitter, the encoded value for a native one. Objects arrive as map[string]any, arrays as []any, numbers as float64, and an emit of undefined or nil arrives as nil. ctx is the request the Host dispatched, so a nested [Context.Events] Emit is ordered with it and cancelled with it.
type EventBusHandler func(ctx Context, data any) error

// EventBus is upstream's `pi.events`, shared with every other realm of the session: node extensions, and Go, Rust and Python extensions in any cell.
//
// Payloads cross by value as JSON. Listeners run in registration order, each listener's work finishes before Emit returns, and a listener that never returns blocks its emitter, as in upstream. Call [Context.Events] from a handler: a call made through [Extension.Events] is not tied to a request, so it waits behind the extension's other calls that are not tied to one.
type EventBus struct {
	ext   *Extension
	ctx   Context
	bound bool
}

// Events returns the extension's event bus. A listener subscribed while the factory runs is registered with the Host at once, as a node factory's `pi.events.on` is; with no Host to reach it waits for Run.
func (e *Extension) Events() EventBus { return EventBus{ext: e} }

// Events returns the event bus for calls made from this request.
func (c Context) Events() EventBus { return EventBus{ext: c.ext, ctx: c, bound: true} }

const (
	callEventsOn         = "events.on"
	callEventsOff        = "events.off"
	callEventsEmit       = "events.emit"
	methodEventsDispatch = "events.dispatch"
	notifyEventsRelease  = "events.release"
)

// busHandlerSeq numbers listeners across every extension of the process: the Host keys a listener by realm (one process) and ID.
var busHandlerSeq atomic.Uint64

type busSub struct {
	id      string
	channel string
	handler EventBusHandler
	// sent is set once the events.on call is made; off once the listener is unsubscribed. Both are guarded by busRegistry.mu.
	sent bool
	off  bool
}

// busRegistry holds an extension's listeners. A dispatch can arrive before events.on returns, and after events.off for a listener the Host had already snapshotted, so a listener stays until the Host releases it.
type busRegistry struct {
	mu   sync.Mutex
	live bool
	subs map[string]*busSub
	// pending holds the listeners subscribed where there is no Host to register them with (no PIG_EXT_SOCKET, as in a unit test), in subscription order. Run registers them before the extension registers.
	pending []*busSub
}

type busCallArgs struct {
	Channel   string          `json:"channel,omitempty"`
	HandlerID string          `json:"handlerId,omitempty"`
	Value     bool            `json:"value,omitempty"`
	JSON      json.RawMessage `json:"json,omitempty"`
}

var errBusNotConnected = errors.New("pi.events: the extension is not connected to the host yet")

func (b EventBus) call(method string, args busCallArgs) error {
	if b.bound {
		result, err := b.ctx.callHost(method, args)
		return callResultError(result, err)
	}
	conn := b.ext.busConn()
	if conn == nil {
		return errBusNotConnected
	}
	result, err := conn.call(method, args)
	return callResultError(result, err)
}

// On subscribes handler to channel and returns its idempotent unsubscribe function. A failure to register with the Host is returned, and nothing stays subscribed.
//
// Called while the factory runs, On registers the listener with the Host at once, over the connection the factory opened, as Pi's shared emitter holds it from that moment: it hears another extension's emit made while the factory runs. Where there is no Host (no PIG_EXT_SOCKET, as in a unit test) the listener is queued and registered when the extension runs.
func (b EventBus) On(channel string, handler EventBusHandler) (unsubscribe func(), err error) {
	if handler == nil {
		return func() {}, errors.New("pi.events.on: handler must not be nil")
	}
	registry := &b.ext.eventBus
	sub := &busSub{id: "go-" + strconv.FormatUint(busHandlerSeq.Add(1), 10), channel: channel, handler: handler}
	registry.mu.Lock()
	if registry.subs == nil {
		registry.subs = make(map[string]*busSub)
	}
	registry.subs[sub.id] = sub
	live := registry.live
	registry.mu.Unlock()
	forget := func() {
		registry.mu.Lock()
		delete(registry.subs, sub.id)
		registry.mu.Unlock()
	}
	var factory *conn
	if !live {
		factory, err = b.ext.factoryBusConn()
		if err != nil && !errors.Is(err, errNoFactoryHost) {
			forget()
			return func() {}, err
		}
		if factory == nil {
			registry.mu.Lock()
			if !registry.live {
				registry.pending = append(registry.pending, sub)
				registry.mu.Unlock()
				return b.ext.unsubscribe(sub), nil
			}
			registry.mu.Unlock()
		}
	}
	registry.mu.Lock()
	sub.sent = true
	registry.mu.Unlock()
	args := busCallArgs{Channel: channel, HandlerID: sub.id, Value: true}
	if factory != nil {
		var result *callResultMsg
		result, err = factory.call(callEventsOn, args)
		err = callResultError(result, err)
	} else {
		err = b.call(callEventsOn, args)
	}
	if err != nil {
		forget()
		return func() {}, err
	}
	return b.ext.unsubscribe(sub), nil
}

func (e *Extension) unsubscribe(sub *busSub) func() {
	return sync.OnceFunc(func() {
		registry := &e.eventBus
		registry.mu.Lock()
		sub.off = true
		sent := sub.sent
		if !sent {
			delete(registry.subs, sub.id)
		}
		registry.mu.Unlock()
		if !sent {
			return
		}
		if err := (EventBus{ext: e}).call(callEventsOff, busCallArgs{HandlerID: sub.id}); err != nil {
			fmt.Fprintf(os.Stderr, "extension: pi.events unsubscribe %q: %v\n", sub.channel, err)
		}
	})
}

// Emit delivers data to every listener of channel, in registration order, and returns when each has finished. Like EventEmitter, an emit on "error" with no listener fails.
//
// Called through [Extension.Events] while the factory runs (before [Extension.Run]), it does what Pi's synchronous emit does in a factory: the Host delivers it to every listener it holds, in registration order, the listeners of the extensions loaded before this one and then this extension's own, which [EventBus.On] registered with it at once; each finishes before Emit returns, and an emit on "error" that nobody hears fails. The first factory-time bus call connects to the Host, so a factory that runs with no Host (no PIG_EXT_SOCKET, as in a unit test) gets an error. A listener the factory's emit runs has a [Context] with no session state yet (its Cwd and Mode are empty), and until the extension registers the only host calls it can make are pi.events ones and the parse helpers (ParseFrontmatter, ParseDate): a nested [Context.Events] Emit reaches the same listeners. Use [Extension.OnLoaded] for work that needs the live session.
func (b EventBus) Emit(channel string, data any) error {
	encoded, err := json.MarshalJS(data)
	if err != nil {
		return fmt.Errorf("pi.events.emit %q: %w", channel, err)
	}
	args, err := json.MarshalJS(busCallArgs{Channel: channel, Value: true, JSON: encoded})
	if err != nil {
		return err
	}
	var result *callResultMsg
	if b.bound {
		result, err = b.ctx.callHost(callEventsEmit, json.RawMessage(args))
	} else {
		conn := b.ext.busConn()
		if conn == nil {
			if conn, err = b.ext.factoryBusConn(); err != nil {
				return fmt.Errorf("%w (%w)", errBusNotConnected, err)
			}
		}
		result, err = conn.call(callEventsEmit, json.RawMessage(args))
	}
	if err := callResultError(result, err); err != nil {
		return err
	}
	var outcome struct {
		UnhandledError bool `json:"unhandledError"`
	}
	if len(result.Result) != 0 && json.Unmarshal(result.Result, &outcome) == nil && outcome.UnhandledError {
		return fmt.Errorf("pi.events.emit %q: unhandled error event: %s", channel, encoded)
	}
	return nil
}

var errNoFactoryHost = errors.New("PIG_EXT_SOCKET is not set, so this extension was not launched by pig")

// busConn is the connection a bus call made outside a request rides: the running extension's, or the factory's while it loads. It is nil before the first factory-time call.
func (e *Extension) busConn() *conn {
	e.eventMu.Lock()
	defer e.eventMu.Unlock()
	if e.conn != nil {
		return e.conn
	}
	return e.loading
}

// factoryBusConn is the connection the factory's bus calls use before Run, opened by the first of them (factoryConnection); Run adopts it. While it is the factory's, a pump answers the events.dispatch requests the host sends it, so a listener the factory subscribed hears an emit another extension makes while the factory runs, even one made in reply to the factory's own emit. It is errNoFactoryHost when there is no Host to reach.
func (e *Extension) factoryBusConn() (*conn, error) {
	e.eventMu.Lock()
	defer e.eventMu.Unlock()
	if e.running {
		return e.conn, nil
	}
	if e.loading != nil {
		return e.loading, nil
	}
	c, err := factoryConnection()
	if err != nil {
		return nil, err
	}
	e.loading = c
	e.pump = &factoryPump{stop: make(chan struct{}), done: make(chan struct{})}
	go e.pump.serve(e, c)
	return c, nil
}

// factoryPump reads the factory's connection until Run takes it over.
type factoryPump struct {
	stop chan struct{}
	done chan struct{}
}

// stopFactoryPump ends the pump and waits until it has stopped reading, so that Run's loop is the only reader. An envelope the pump had not taken stays queued for the loop.
func (e *Extension) stopFactoryPump() {
	e.eventMu.Lock()
	pump := e.pump
	e.pump = nil
	e.eventMu.Unlock()
	if pump != nil {
		close(pump.stop)
		<-pump.done
	}
}

func (p *factoryPump) serve(e *Extension, c *conn) {
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		case env, ok := <-c.incoming:
			if !ok {
				return
			}
			e.serveLoadingEnvelope(c, env)
		}
	}
}

// serveLoadingEnvelope answers what the Host sends a connection whose extension has not registered: listener dispatches, each on its own goroutine so that a listener's nested emit does not stop the next dispatch, and the release of a listener a dispatch unsubscribed. A request the extension cannot serve yet is refused, so the Host does not wait for it.
func (e *Extension) serveLoadingEnvelope(c *conn, env envelope) {
	switch {
	case env.Type == msgRequest && env.Request != nil && env.Request.Method == methodEventsDispatch:
		ctx := Context{ext: e, ctx: context.Background(), loading: c}
		e.requestWG.Go(func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					_ = c.respond(env.ID, nil, fmt.Errorf("handler panicked: %v", recovered))
				}
			}()
			e.dispatchEventBus(ctx, env.ID, env.Request)
		})
	case env.Type == msgNotify && env.Notify != nil && env.Notify.Method == notifyEventsRelease:
		e.releaseEventListener(env.Notify.Args)
		e.markNotifyHandled()
	case env.Type == msgRequest:
		method := ""
		if env.Request != nil {
			method = env.Request.Method
		}
		_ = c.respond(env.ID, nil, fmt.Errorf("%s: the extension has not registered with the host yet", method))
	}
}

// registerPendingListeners sends the listeners subscribed before Run, in subscription order, and makes later subscriptions register at once.
func (e *Extension) registerPendingListeners(conn *conn) error {
	registry := &e.eventBus
	registry.mu.Lock()
	pending := registry.pending
	registry.pending = nil
	registry.live = true
	for _, sub := range pending {
		sub.sent = true
	}
	registry.mu.Unlock()
	for _, sub := range pending {
		registry.mu.Lock()
		off := sub.off
		registry.mu.Unlock()
		if off {
			continue
		}
		result, err := conn.call(callEventsOn, busCallArgs{Channel: sub.channel, HandlerID: sub.id, Value: true})
		if err := callResultError(result, err); err != nil {
			return fmt.Errorf("pi.events.on %q: %w", sub.channel, err)
		}
	}
	return nil
}

// resetEventBus forgets the listeners of a finished run: the Host dropped them when the connection closed.
func (e *Extension) resetEventBus() {
	registry := &e.eventBus
	registry.mu.Lock()
	registry.live = false
	registry.subs = nil
	registry.pending = nil
	registry.mu.Unlock()
	e.eventMu.Lock()
	e.running = false
	e.eventMu.Unlock()
}

func (e *Extension) releaseEventListener(args json.RawMessage) {
	var released struct {
		HandlerID string `json:"handlerId"`
	}
	if json.Unmarshal(args, &released) != nil {
		return
	}
	registry := &e.eventBus
	registry.mu.Lock()
	if sub := registry.subs[released.HandlerID]; sub != nil && sub.off {
		delete(registry.subs, released.HandlerID)
	}
	registry.mu.Unlock()
}

// dispatchEventBus runs one listener for an events.dispatch request and answers when it returns.
func (e *Extension) dispatchEventBus(ctx Context, id string, req *requestMsg) {
	var args struct {
		HandlerID string          `json:"handlerId"`
		Channel   string          `json:"channel"`
		JSON      json.RawMessage `json:"json"`
	}
	if err := json.Unmarshal(req.Args, &args); err != nil {
		_ = ctx.hostConnection().respond(id, nil, fmt.Errorf("parse %s: %w", methodEventsDispatch, err))
		return
	}
	registry := &e.eventBus
	registry.mu.Lock()
	sub := registry.subs[args.HandlerID]
	registry.mu.Unlock()
	if sub == nil || sub.channel != args.Channel {
		_ = ctx.hostConnection().respond(id, nil, fmt.Errorf("Unknown event bus handler: %s", args.HandlerID))
		return
	}
	var data any
	if len(args.JSON) != 0 {
		if err := json.Unmarshal(args.JSON, &data); err != nil {
			_ = ctx.hostConnection().respond(id, nil, fmt.Errorf("decode %s payload: %w", args.Channel, err))
			return
		}
	}
	_ = ctx.hostConnection().respond(id, nil, sub.handler(ctx, data))
}
