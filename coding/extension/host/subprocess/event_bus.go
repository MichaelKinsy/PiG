package subprocess

// Ports packages/coding-agent/src/core/event-bus.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
)

// pig additive (D19): the Host's ordered pi.events registry for several Node realms. EventEmitter keeps one ordered listener array per channel, clones it for each emit, and calls each listener in turn; each listener's synchronous prefix runs before the next. The Host keeps that array and runs each listener's prefix in its own realm through a restricted synchronous request, while the emitter waits in its synchronous emit call. A realm that is the Host's only Node realm keeps the listeners in its own EventEmitter until a second realm appears.

const (
	callEventsOn   = "events.on"
	callEventsOff  = "events.off"
	callEventsEmit = "events.emit"
	// callEventsSettle is the emitter's first microtask after a routed emit. It waits until every foreign listener realm of that emission has drained the microtasks its synchronous prefix queued, so a listener's first post-await continuation precedes the emitter's later microtasks, as in Pi's single queue.
	callEventsSettle = "events.settle"

	methodEventsDispatch = "events.dispatch"
	methodEventsMigrate  = "events.migrate"
	notifyEventsRelease  = "events.release"
	notifyEventsQuiesce  = "events.quiesce"
	notifyEventsProceed  = "events.proceed"

	eventBusRoutedEnv = "PIG_EVENT_BUS=routed"
)

// loadingBusCall reports whether a call may arrive while its factory is still loading, before the register handshake: the shared event bus, and the registry reads and checks of a Node factory's `pi` object, which Pi answers synchronously when the factory calls them, and its exec, which Pi spawns directly.
func loadingBusCall(method string) bool {
	switch method {
	case callXrefHello, callXrefOp, callEventsOn, callEventsOff, callEventsEmit, callEventsSettle, CallGetMcpServers, CallCheckMcpServer, "exec", CallLoadingExec:
		return true
	}
	return false
}

// observeXref attaches reference accounting, bus cleanup, event-loop turn
// forwarding, the view diagnostic and the inbound observer to a connection
// before it starts reading.
func (h *Host) observeXref(conn *Conn) {
	conn.onLoopTurned = func() { h.forwardLoopTurned(conn) }
	conn.reportView = func(surface string, err error) { h.reportViewRejection(conn, surface, err) }
	observe := h.inboundObserver
	conn.inbound = func(env *Envelope) {
		if observe != nil {
			observe(conn.name, env)
		}
		if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == notifyEventsQuiesce {
			h.eventBus.quiesce(conn, env.Notify.Args)
			return
		}
		h.xref.inbound(conn, env)
	}
	conn.onClosed = func() {
		h.eventBus.closed(conn)
		h.forgetLocalRealm(conn)
		h.xref.closed(conn)
	}
}

type busListener struct {
	conn      *Conn
	realm     string
	handlerID string
	channel   string
	// value marks a native listener: it receives the payload as JSON, not as cross-process references.
	value     bool
	snapshots int
	removed   bool
	released  bool
	// untrack removes the listener once; the Host's ExtensionRuntime retains it until the runtime is invalidated.
	// upstream: loader.ts:201 trackEventBusSubscription
	untrack func()
}

// busEmission counts foreign dispatches of one routed emit whose realms have not yet reported their microtask checkpoint.
type busEmission struct {
	emitter *Conn
	pending map[*Conn]int
	done    chan struct{}
	closed  bool
}

func (e *busEmission) settleLocked() {
	if !e.closed && len(e.pending) == 0 {
		e.closed = true
		close(e.done)
	}
}

type busHandlerKey struct {
	realm, handlerID string
}

type hostEventBus struct {
	// routeMu serializes the switch to the Host registry and each native realm's first binding.
	routeMu sync.Mutex
	mu      sync.Mutex
	routed  bool
	// routeDone is set once a native realm's first call has finished moving the in-heap node listeners to the registry.
	routeDone bool
	listeners map[string][]*busListener
	handlers  map[busHandlerKey]*busListener
	// local lists live Node processes that still keep their listeners in their own EventEmitter.
	local        map[any]*managedExt
	emissions    map[string]*busEmission
	nextEmission uint64
}

func (b *hostEventBus) newEmission(emitter *Conn) (string, *busEmission) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.initLocked()
	b.nextEmission++
	id := fmt.Sprint(b.nextEmission)
	emission := &busEmission{emitter: emitter, pending: make(map[*Conn]int), done: make(chan struct{})}
	b.emissions[id] = emission
	return id, emission
}

func (b *hostEventBus) addPending(emission *busEmission, conn *Conn, delta int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	emission.pending[conn] += delta
	if emission.pending[conn] <= 0 {
		delete(emission.pending, conn)
	}
	emission.settleLocked()
}

func (b *hostEventBus) quiesce(conn *Conn, raw json.RawMessage) {
	var args struct {
		Emission string `json:"emission"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return
	}
	b.mu.Lock()
	emission := b.emissions[args.Emission]
	b.mu.Unlock()
	if emission != nil {
		b.addPending(emission, conn, -1)
	}
}

// settle lets the emission's waiting listener realms run their continuations, then waits for their microtask checkpoints and forgets the emission.
func (b *hostEventBus) settle(ctx context.Context, id string) error {
	b.mu.Lock()
	emission := b.emissions[id]
	var waiting []*Conn
	if emission != nil {
		for conn := range emission.pending {
			waiting = append(waiting, conn)
		}
	}
	b.mu.Unlock()
	if emission == nil {
		return nil
	}
	proceed(id, waiting)
	defer func() {
		b.mu.Lock()
		delete(b.emissions, id)
		b.mu.Unlock()
	}()
	select {
	case <-emission.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *hostEventBus) initLocked() {
	if b.listeners == nil {
		b.listeners = make(map[string][]*busListener)
		b.emissions = make(map[string]*busEmission)
		b.handlers = make(map[busHandlerKey]*busListener)
		b.local = make(map[any]*managedExt)
	}
}

// busRoutedForSpawn reports whether a newly spawned Node realm starts with the Host registry.
func (h *Host) busRoutedForSpawn() bool {
	h.eventBus.mu.Lock()
	defer h.eventBus.mu.Unlock()
	return h.eventBus.routed
}

// noteNodeRealm records a spawned Node realm's starting mode.
func (h *Host) noteNodeRealm(me *managedExt, routed bool) {
	if routed {
		return
	}
	h.eventBus.mu.Lock()
	defer h.eventBus.mu.Unlock()
	h.eventBus.initLocked()
	h.eventBus.local[processIdentity(me)] = me
}

// planEventBus runs before cells are staged. When the plan and the Node realms that survive it make more than one realm, every realm uses the Host registry: new realms start routed and surviving in-heap realms hand their listeners over first, in their EventEmitter order.
func (h *Host) planEventBus(ctx context.Context, cells []CellSpec) {
	planned := map[string]bool{}
	newRealms := 0
	for _, cell := range cells {
		for _, cfg := range cell.Extensions {
			planned[cfg.Name] = true
		}
		// A prebuilt Node launcher spawns a Node realm even when its config names no language.
		node := cell.Strategy == CellStrategyPackedNode || cell.Language == "node"
		for _, cfg := range cell.Extensions {
			node = node || (cfg.Path != "" && usesNodeRuntime(cfg.Path, cfg.RuntimeLanguage))
		}
		if node {
			newRealms++
		}
	}
	h.mu.Lock()
	surviving := map[any]*managedExt{}
	replaced := map[any]bool{}
	for name, me := range h.exts {
		if conn := me.connection(); conn == nil || conn.closed.Load() || !me.nodeRealm {
			continue
		}
		process := processIdentity(me)
		if planned[name] {
			replaced[process] = true
		}
		surviving[process] = me
	}
	h.mu.Unlock()
	for process := range replaced {
		delete(surviving, process)
	}
	h.eventBus.mu.Lock()
	h.eventBus.initLocked()
	if !h.eventBus.routed && newRealms+len(surviving) < 2 {
		h.eventBus.mu.Unlock()
		return
	}
	h.eventBus.routed = true
	var migrate []*managedExt
	for process, me := range h.eventBus.local {
		delete(h.eventBus.local, process)
		if surviving[process] != nil {
			migrate = append(migrate, me)
		}
	}
	h.eventBus.mu.Unlock()
	for _, me := range migrate {
		if err := h.migrateEventBus(ctx, me); err != nil {
			reportEventBusError("migrate", err)
		}
	}
}

func (h *Host) migrateEventBus(ctx context.Context, me *managedExt) error {
	conn := me.connection()
	response, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: methodEventsMigrate}})
	if err != nil {
		return err
	}
	if response.Response == nil || response.Response.Error != nil {
		if response.Response != nil {
			return response.Response.Error.ToError()
		}
		return errors.New("event bus migration returned no response")
	}
	var result struct {
		Realm     string `json:"realm"`
		Listeners []struct {
			Channel   string `json:"channel"`
			HandlerID string `json:"handlerId"`
			Extension string `json:"extension"`
		} `json:"listeners"`
	}
	if err := json.Unmarshal(response.Response.Result, &result); err != nil {
		return err
	}
	for _, listener := range result.Listeners {
		h.mu.Lock()
		member := h.exts[listener.Extension]
		if listener.Extension == me.config.Name {
			// me may still be loading, before the Host lists it.
			member = me
		}
		var memberConn *Conn
		if member != nil {
			memberConn = member.connection()
		}
		h.mu.Unlock()
		if memberConn == nil || processIdentity(member) != processIdentity(me) {
			continue
		}
		if err := h.xref.bind(memberConn, member, result.Realm); err != nil {
			return err
		}
		h.eventBus.add(&busListener{conn: memberConn, realm: result.Realm, handlerID: listener.HandlerID, channel: listener.Channel})
	}
	return nil
}

func (b *hostEventBus) add(listener *busListener) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.initLocked()
	b.listeners[listener.channel] = append(b.listeners[listener.channel], listener)
	b.handlers[busHandlerKey{listener.realm, listener.handlerID}] = listener
}

func (b *hostEventBus) snapshot(channel string) []*busListener {
	b.mu.Lock()
	defer b.mu.Unlock()
	listeners := slices.Clone(b.listeners[channel])
	for _, listener := range listeners {
		listener.snapshots++
	}
	return listeners
}

// finish ends a dispatch snapshot and releases removed listeners no other snapshot can still call.
func (b *hostEventBus) finish(listeners []*busListener) {
	b.mu.Lock()
	var released []*busListener
	for _, listener := range listeners {
		listener.snapshots--
		if listener.removed && listener.snapshots == 0 && !listener.released {
			listener.released = true
			released = append(released, listener)
		}
	}
	b.mu.Unlock()
	for _, listener := range released {
		releaseBusHandler(listener)
	}
}

func releaseBusHandler(listener *busListener) {
	args, _ := json.Marshal(struct {
		HandlerID string `json:"handlerId"`
	}{listener.handlerID})
	_ = listener.conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyEventsRelease, Args: args}})
}

// lookup returns the registered listener for key, or nil.
func (b *hostEventBus) lookup(key busHandlerKey) *busListener {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.handlers[key]
}

// removeBusListener unregisters the listener, releases its handler once no dispatch snapshot holds it, and emits removeListener as EventEmitter does after removing a listener.
func (h *Host) removeBusListener(ctx context.Context, listener *busListener) {
	removed, release := h.eventBus.remove(listener)
	if removed == nil {
		return
	}
	if release {
		releaseBusHandler(removed)
	}
	h.dispatchEvent(ctx, "removeListener", newChannelPayload(removed.channel), nil, removed.realm, "", nil)
}

// remove unregisters listener and reports whether no dispatch snapshot can still call it. It returns nil when listener is no longer the one registered under its key: a retained unsubscribe that runs after its connection closed must not remove a later listener that reuses the realm and handler id.
func (b *hostEventBus) remove(listener *busListener) (*busListener, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := busHandlerKey{listener.realm, listener.handlerID}
	if b.handlers[key] != listener {
		return nil, false
	}
	delete(b.handlers, key)
	listener.removed = true
	release := listener.snapshots == 0
	listener.released = release
	b.listeners[listener.channel] = slices.DeleteFunc(b.listeners[listener.channel], func(candidate *busListener) bool { return candidate == listener })
	if len(b.listeners[listener.channel]) == 0 {
		delete(b.listeners, listener.channel)
	}
	return listener, release
}

// forgetLocalRealm drops the in-heap realm whose current connection closed. Holding h.mu keeps each member's connection stable while the comparison runs; eventBus.mu is never held at the same time.
func (h *Host) forgetLocalRealm(conn *Conn) {
	h.eventBus.mu.Lock()
	local := maps.Clone(h.eventBus.local)
	h.eventBus.mu.Unlock()
	var closed []any
	h.mu.Lock()
	for process, me := range local {
		if me.connection() == conn {
			closed = append(closed, process)
		}
	}
	h.mu.Unlock()
	if len(closed) == 0 {
		return
	}
	h.eventBus.mu.Lock()
	for _, process := range closed {
		if h.eventBus.local[process] == local[process] {
			delete(h.eventBus.local, process)
		}
	}
	h.eventBus.mu.Unlock()
}

// closed drops a closed connection's listeners and forgets their runtime-retained unsubscribes, so the runtime does not keep a closed generation's listeners until it is invalidated. A process that exits has no Pi counterpart, so no removeListener event is emitted.
func (b *hostEventBus) closed(conn *Conn) {
	type release struct {
		id    string
		conns []*Conn
	}
	var releases []release
	var dropped []*busListener
	b.mu.Lock()
	for key, listener := range b.handlers {
		if listener.conn != conn {
			continue
		}
		dropped = append(dropped, listener)
		delete(b.handlers, key)
		listener.removed = true
		b.listeners[listener.channel] = slices.DeleteFunc(b.listeners[listener.channel], func(candidate *busListener) bool { return candidate == listener })
		if len(b.listeners[listener.channel]) == 0 {
			delete(b.listeners, listener.channel)
		}
	}
	for id, emission := range b.emissions {
		delete(emission.pending, conn)
		if emission.emitter == conn {
			// An emitter that exits never settles; its waiting listener realms continue.
			delete(b.emissions, id)
			var waiting []*Conn
			for pending := range emission.pending {
				waiting = append(waiting, pending)
			}
			releases = append(releases, release{id, waiting})
		}
		emission.settleLocked()
	}
	b.mu.Unlock()
	for _, r := range releases {
		proceed(r.id, r.conns)
	}
	// The listener is already unregistered, so its unsubscribe only drops the runtime's retained entry.
	for _, listener := range dropped {
		if listener.untrack != nil {
			listener.untrack()
		}
	}
}

func proceed(id string, conns []*Conn) {
	args := mustJSONObject(map[string]string{"emission": id})
	for _, conn := range conns {
		_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyEventsProceed, Args: args}})
	}
}

// handleEventBusCall serves a call on conn, the connection of me that delivered it. A native realm (Go, Rust, Python) marks its calls "value": its payloads cross as JSON under "json" and it is bound to a realm here, with no xref.hello.
func (h *Host) handleEventBusCall(ctx context.Context, me *managedExt, conn *Conn, callID string, call *CallPayload) (*CallResultPayload, error) {
	if call.Method == callEventsEmit {
		defer h.xref.unhold(conn, callID)
	}
	var args struct {
		Channel   *string         `json:"channel"`
		HandlerID string          `json:"handlerId"`
		Data      json.RawMessage `json:"data"`
		JSON      json.RawMessage `json:"json"`
		Value     bool            `json:"value"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, fmt.Errorf("parse %s: %w", call.Method, err)
	}
	// Pi's emit cannot be interrupted between listeners; a dispatch ends only when its listener's realm answers or exits.
	dispatchCtx := context.WithoutCancel(ctx)
	realm := h.xref.realmOf(conn)
	if args.Value {
		var err error
		if realm, err = h.joinNativeRealm(dispatchCtx, conn, me); err != nil {
			return nil, err
		}
	}
	if realm == "" {
		return nil, fmt.Errorf("%s before xref.hello", call.Method)
	}
	switch call.Method {
	case callEventsOn:
		if args.Channel == nil || args.HandlerID == "" {
			return nil, errors.New("events.on requires channel and handlerId")
		}
		// EventEmitter emits newListener before adding the listener.
		h.dispatchEvent(dispatchCtx, "newListener", newChannelPayload(*args.Channel), nil, realm, "", nil)
		listener := &busListener{conn: conn, realm: realm, handlerID: args.HandlerID, channel: *args.Channel, value: args.Value}
		listener.untrack = h.providerRuntime.TrackEventBusSubscription(func() { h.removeBusListener(dispatchCtx, listener) })
		h.eventBus.add(listener)
	case callEventsOff:
		if listener := h.eventBus.lookup(busHandlerKey{realm, args.HandlerID}); listener != nil {
			listener.untrack()
		}
	case callEventsEmit:
		if args.Channel == nil {
			return nil, errors.New("events.emit requires channel")
		}
		payload := h.newEmitPayload(args.Value, args.Data, args.JSON)
		id, emission := h.eventBus.newEmission(conn)
		handled := h.dispatchEvent(dispatchCtx, *args.Channel, payload, h.xref.held(conn, callID), realm, id, emission)
		h.eventBus.mu.Lock()
		foreign := len(emission.pending) != 0
		if !foreign {
			delete(h.eventBus.emissions, id)
		}
		h.eventBus.mu.Unlock()
		if !handled && *args.Channel == "error" {
			return &CallResultPayload{Result: json.RawMessage(`{"unhandledError":true}`)}, nil
		}
		if foreign && args.Value {
			// A native emitter has no microtask queue to settle in, so its emission settles before the call returns.
			if err := h.eventBus.settle(ctx, id); err != nil {
				return nil, err
			}
		} else if foreign {
			return &CallResultPayload{Result: mustJSONObject(map[string]string{"emission": id})}, nil
		}
	case callEventsSettle:
		var settle struct {
			Emission string `json:"emission"`
		}
		if err := json.Unmarshal(call.Args, &settle); err != nil {
			return nil, err
		}
		if err := h.eventBus.settle(ctx, settle.Emission); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown event bus operation %q", call.Method)
	}
	return &CallResultPayload{Result: json.RawMessage("null")}, nil
}

// dispatchEvent calls a snapshot of channel's listeners in order and reports whether the snapshot had any.
func (h *Host) dispatchEvent(ctx context.Context, channel string, payload *busPayload, refs []xrefRef, emitter, emissionID string, emission *busEmission) bool {
	listeners := h.eventBus.snapshot(channel)
	defer h.eventBus.finish(listeners)
	for _, listener := range listeners {
		var (
			args    json.RawMessage
			err     error
			foreign bool
		)
		if listener.value {
			args, err = h.nativeDispatchArgs(ctx, listener, channel, payload)
		} else {
			h.xref.deliver(listener.realm, refs)
			foreign = emission != nil && listener.realm != emitter
			var marker string
			if foreign {
				marker = emissionID
				h.eventBus.addPending(emission, listener.conn, 1)
			}
			args, err = json.Marshal(struct {
				HandlerID string          `json:"handlerId"`
				Channel   string          `json:"channel"`
				Data      json.RawMessage `json:"data"`
				Emission  string          `json:"emission,omitempty"`
			}{listener.handlerID, channel, payload.encoded, marker})
		}
		if err == nil {
			var response *Envelope
			response, err = listener.conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: methodEventsDispatch, Args: args}})
			if err == nil && response.Response != nil && response.Response.Error != nil {
				err = response.Response.Error.ToError()
			}
		}
		if err != nil {
			if foreign {
				h.eventBus.addPending(emission, listener.conn, -1)
			}
			reportEventBusError(channel, err)
		}
	}
	return len(listeners) != 0
}

func mustJSONObject(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func reportEventBusError(channel string, err error) {
	fmt.Fprintf(os.Stderr, "Event handler error (%s): %v\n", channel, err)
}
