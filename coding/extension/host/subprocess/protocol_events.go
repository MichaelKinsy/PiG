package subprocess

// Ports packages/coding-agent/src/core/event-bus.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
)

// The pi.events bridge for native realms (Go, Rust, Python). A native realm has no cross-process references: it subscribes with events.on and emits with events.emit, both marked "value", and it receives events.dispatch requests carrying the payload as JSON under "json". The Host stays the one ordered registry, so a native listener sits between node listeners in registration order. Payloads cross by value: a native emit reaches a node listener as a fresh decoded value, and a node emit reaches a native listener as JSON.stringify of the emitter's value when that listener runs, read in the realm that owns it (the xref "json" operation).

var nativeRealmSeq atomic.Uint64

// busPayload is one emission's payload in each listener's form.
type busPayload struct {
	// encoded is the xref encoding a node listener decodes.
	encoded json.RawMessage
	// nativeView returns the JSON a native listener receives.
	nativeView func(context.Context) (json.RawMessage, error)
}

// jsonView computes the native view when a native listener is dispatched. EventEmitter hands one object to every listener in turn (event-bus.ts:15-17), so each native listener reads the payload as the listeners before it left it; a failure fails only that listener's delivery.
func (p *busPayload) jsonView(ctx context.Context) (json.RawMessage, error) {
	return p.nativeView(ctx)
}

// newChannelPayload is the payload of EventEmitter's newListener and removeListener: the channel name.
func newChannelPayload(channel string) *busPayload {
	data, _ := json.Marshal(channel)
	return &busPayload{encoded: data, nativeView: func(context.Context) (json.RawMessage, error) { return data, nil }}
}

// newEmitPayload builds the payload of an events.emit call.
func (h *Host) newEmitPayload(native bool, data, value json.RawMessage) *busPayload {
	if native {
		if len(bytes.TrimSpace(value)) == 0 {
			value = json.RawMessage("null")
		}
		encoded, _ := json.Marshal(struct {
			Tag   string          `json:"$x"`
			Value json.RawMessage `json:"v"`
		}{"json", value})
		return &busPayload{encoded: encoded, nativeView: func(context.Context) (json.RawMessage, error) { return value, nil }}
	}
	if len(data) == 0 {
		data = json.RawMessage(`{"$x":"undefined"}`)
	}
	return &busPayload{encoded: data, nativeView: func(ctx context.Context) (json.RawMessage, error) { return h.nodeJSONView(ctx, data) }}
}

// nativeDispatchArgs builds the events.dispatch arguments for a native listener.
func (h *Host) nativeDispatchArgs(ctx context.Context, listener *busListener, channel string, payload *busPayload) (json.RawMessage, error) {
	view, err := payload.jsonView(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		HandlerID string          `json:"handlerId"`
		Channel   string          `json:"channel"`
		JSON      json.RawMessage `json:"json"`
	}{listener.handlerID, channel, view})
}

// joinNativeRealm binds a native connection to its process's realm, assigning the realm on first use, and makes the Host registry the bus of every realm.
func (h *Host) joinNativeRealm(ctx context.Context, conn *Conn, me *managedExt) (string, error) {
	h.eventBus.mu.Lock()
	done := h.eventBus.routeDone
	h.eventBus.mu.Unlock()
	if done {
		if realm := h.xref.realmOf(conn); realm != "" {
			return realm, nil
		}
	}
	realm, err := h.bindNativeRealm(conn, me)
	if err != nil {
		return "", err
	}
	h.routeEventBus(ctx)
	return realm, nil
}

// bindNativeRealm returns the realm of conn's OS process. One process is one realm, as for a node process.
func (h *Host) bindNativeRealm(conn *Conn, me *managedExt) (string, error) {
	h.eventBus.routeMu.Lock()
	defer h.eventBus.routeMu.Unlock()
	if realm := h.xref.realmOf(conn); realm != "" {
		return realm, nil
	}
	process := processIdentity(me)
	id := ""
	h.xref.mu.Lock()
	h.xref.initLocked()
	for _, realm := range h.xref.realms {
		if realm.process == process {
			id = realm.id
			break
		}
	}
	h.xref.mu.Unlock()
	if id == "" {
		id = fmt.Sprintf("native-%d", nativeRealmSeq.Add(1))
	}
	if err := h.xref.bind(conn, me, id); err != nil {
		return "", err
	}
	return id, nil
}

// routeEventBus makes the Host registry the bus of every realm. A session with one node realm keeps that realm's in-heap EventEmitter until a native realm first subscribes or emits; the in-heap listeners then move to the registry in their EventEmitter order. Node realms spawned afterwards start routed.
func (h *Host) routeEventBus(ctx context.Context) {
	b := &h.eventBus
	b.routeMu.Lock()
	defer b.routeMu.Unlock()
	b.mu.Lock()
	b.initLocked()
	b.routed = true
	var migrate []*managedExt
	for process, me := range b.local {
		if conn := me.connection(); conn == nil || conn.closed.Load() {
			continue
		}
		delete(b.local, process)
		migrate = append(migrate, me)
	}
	b.mu.Unlock()
	for _, me := range migrate {
		if err := h.migrateEventBus(ctx, me); err != nil {
			reportEventBusError("migrate", err)
		}
	}
	b.mu.Lock()
	b.routeDone = true
	b.mu.Unlock()
}

// joinRoutedBus moves a registered Node realm onto the Host registry when routeEventBus switched the registry on while the realm was spawned in-heap but had no connection yet, so routeEventBus could not migrate it. Pi has one EventEmitter per loader (event-bus.ts:12-33), so that realm's listeners must hear native emits too.
func (h *Host) joinRoutedBus(ctx context.Context, me *managedExt) {
	if !me.nodeRealm {
		return
	}
	b := &h.eventBus
	b.routeMu.Lock()
	defer b.routeMu.Unlock()
	process := processIdentity(me)
	b.mu.Lock()
	late := b.routed && b.local[process] != nil
	if late {
		delete(b.local, process)
	}
	b.mu.Unlock()
	if !late {
		return
	}
	if err := h.migrateEventBus(context.WithoutCancel(ctx), me); err != nil {
		reportEventBusError("migrate", err)
	}
}

// nodeJSONView returns JSON.stringify of a node emitter's payload. A primitive is its own JSON; an object is read in the realm that owns it, once per native listener.
func (h *Host) nodeJSONView(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return data, nil
	}
	var tagged struct {
		Tag   string `json:"$x"`
		Realm string `json:"realm"`
		ID    string `json:"id"`
		Value string `json:"v"`
	}
	if err := json.Unmarshal(trimmed, &tagged); err != nil {
		return nil, err
	}
	switch tagged.Tag {
	case "undefined", "symbol", "intrinsic":
		// JSON.stringify yields no text for these; a listener receives null.
		return json.RawMessage("null"), nil
	case "number":
		if tagged.Value == "-0" {
			return json.RawMessage("0"), nil
		}
		return json.RawMessage("null"), nil
	case "bigint":
		return nil, errors.New("Do not know how to serialize a BigInt")
	case "ref":
		return h.ownerJSONView(ctx, xrefRef{Realm: tagged.Realm, ID: tagged.ID})
	}
	return nil, fmt.Errorf("unknown payload encoding %q", tagged.Tag)
}

// ownerJSONView runs JSON.stringify on ref in its owner. The owner services the request even while it waits in the emit that started this dispatch.
func (h *Host) ownerJSONView(ctx context.Context, ref xrefRef) (json.RawMessage, error) {
	owner := h.xref.ownerConn(ref.Realm)
	if owner == nil {
		return nil, errors.New("cross-process reference owner exited")
	}
	args, err := json.Marshal(struct {
		Ref  xrefRef           `json:"ref"`
		Op   string            `json:"op"`
		Args []json.RawMessage `json:"args"`
	}{ref, "json", []json.RawMessage{}})
	if err != nil {
		return nil, err
	}
	response, err := owner.Request(context.WithoutCancel(ctx), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: methodXrefOp, Args: args}})
	if err != nil {
		return nil, fmt.Errorf("cross-process reference owner failed: %w", err)
	}
	if response.Response == nil {
		return nil, errors.New("cross-process reference owner returned no response")
	}
	if response.Response.Error != nil {
		return nil, response.Response.Error.ToError()
	}
	var result struct {
		Value struct {
			Outcome struct {
				JSON  json.RawMessage `json:"json"`
				Error string          `json:"error"`
			} `json:"v"`
		} `json:"value"`
	}
	if err := json.Unmarshal(response.Response.Result, &result); err != nil {
		return nil, err
	}
	if result.Value.Outcome.Error != "" {
		return nil, errors.New(result.Value.Outcome.Error)
	}
	if len(result.Value.Outcome.JSON) == 0 || string(result.Value.Outcome.JSON) == "null" {
		return json.RawMessage("null"), nil
	}
	var text string
	if err := json.Unmarshal(result.Value.Outcome.JSON, &text); err != nil {
		return nil, err
	}
	return json.RawMessage(text), nil
}
