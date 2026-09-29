package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// pig additive (D19): cross-process references (xref). Each extension OS process is one realm. A realm exports its objects by reference; the Host routes every operation on a foreign reference to the owning realm through the synchronous host call path and keeps the lease ledger that lets owners drop exports no realm can reach. Wire shape and lifetime protocol: plans/0.3.x/gap-xproc.md sections 3-4.

const (
	callXrefHello = "xref.hello"
	callXrefOp    = "xref.op"

	methodXrefOp      = "xref.op"
	notifyXrefRelease = "xref.release"
	notifyXrefUnpin   = "xref.unpin"
)

type xrefRef struct {
	Realm string `json:"realm"`
	ID    string `json:"id"`
}

type xrefRealm struct {
	id      string
	process any // *packedProcessState, or *managedExt for a process with one member
	conns   map[*Conn]*managedExt
	order   []*Conn
}

type xrefLease struct {
	// received counts the owner's transmissions of the reference the Host has read since the last unpin. The owner keeps an export while its own transmission count exceeds the counts unpinned.
	received  uint64
	holds     int
	importers map[string]uint64
}

type xrefFrame struct {
	conn *Conn
	id   string
}

type xrefUnpin struct {
	ID    string `json:"id"`
	Count uint64 `json:"count"`
}

type xrefHub struct {
	mu       sync.Mutex
	realms   map[string]*xrefRealm
	byConn   map[*Conn]*xrefRealm
	leases   map[xrefRef]*xrefLease
	holds    map[xrefFrame][]xrefRef
	expected map[xrefFrame]struct{}
	unpins   *callLanes
}

func (x *xrefHub) initLocked() {
	if x.realms != nil {
		return
	}
	x.realms = make(map[string]*xrefRealm)
	x.byConn = make(map[*Conn]*xrefRealm)
	x.leases = make(map[xrefRef]*xrefLease)
	x.holds = make(map[xrefFrame][]xrefRef)
	x.expected = make(map[xrefFrame]struct{})
	x.unpins = newCallLanes()
}

// processIdentity names the OS process that hosts me. A retained process serves successive generations through different states, so the identity is the state that spawned it.
func processIdentity(me *managedExt) any {
	if me.packedProcess != nil {
		return me.packedProcess.spawner()
	}
	if me.procOwner != nil {
		return me.procOwner
	}
	return me
}

// bind records that conn belongs to realm id. One realm is one process: a second process claiming the same realm, or one process claiming two realms, is rejected.
func (x *xrefHub) bind(conn *Conn, me *managedExt, id string) error {
	if id == "" {
		return errors.New("xref.hello requires a realm")
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.initLocked()
	if current := x.byConn[conn]; current != nil {
		if current.id != id {
			return fmt.Errorf("connection already belongs to realm %s", current.id)
		}
		return nil
	}
	process := processIdentity(me)
	realm := x.realms[id]
	if realm == nil {
		for _, other := range x.realms {
			if other.process == process {
				return fmt.Errorf("process already belongs to realm %s", other.id)
			}
		}
		realm = &xrefRealm{id: id, process: process, conns: make(map[*Conn]*managedExt)}
		x.realms[id] = realm
	} else if realm.process != process {
		return errors.New("realm belongs to another process")
	}
	realm.conns[conn] = me
	realm.order = append(realm.order, conn)
	x.byConn[conn] = realm
	return nil
}

func (x *xrefHub) realmOf(conn *Conn) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if realm := x.byConn[conn]; realm != nil {
		return realm.id
	}
	return ""
}

// ownerConn returns a live connection of realm id.
func (x *xrefHub) ownerConn(id string) *Conn {
	x.mu.Lock()
	defer x.mu.Unlock()
	realm := x.realms[id]
	if realm == nil {
		return nil
	}
	for _, conn := range slices.Backward(realm.order) {
		if !conn.closed.Load() && !conn.closing.Load() {
			return conn
		}
	}
	return nil
}

// scanXrefRefs returns every reference in a transferred JSON value. Objects are never inlined, so a reference can appear only as a tagged value, including inside a by-value descriptor list. A unique symbol carries a realm and id and has an object's lifetime; well-known and registered symbols carry neither.
func scanXrefRefs(raw json.RawMessage) []xrefRef {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var refs []xrefRef
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["$x"] == "ref" || v["$x"] == "symbol" {
				realm, _ := v["realm"].(string)
				id, _ := v["id"].(string)
				if realm != "" && id != "" {
					refs = append(refs, xrefRef{Realm: realm, ID: id})
				}
				return
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(value)
	return refs
}

// inbound runs on conn's read loop. It holds references a frame carries until the Host has delivered them, and applies releases in wire order.
func (x *xrefHub) inbound(conn *Conn, env *Envelope) {
	switch {
	case env.Type == MsgCall && env.Call != nil && (env.Call.Method == callXrefOp || env.Call.Method == callEventsEmit):
		x.hold(conn, env.ID, transferredArgs(env.Call.Method, env.Call.Args))
	case env.Type == MsgResponse && env.Response != nil:
		frame := xrefFrame{conn, env.ID}
		x.mu.Lock()
		_, expected := x.expected[frame]
		delete(x.expected, frame)
		x.mu.Unlock()
		if expected {
			x.hold(conn, env.ID, env.Response.Result)
		}
	case env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == notifyXrefRelease:
		var args struct {
			Items []struct {
				Realm string `json:"realm"`
				ID    string `json:"id"`
				Count uint64 `json:"count"`
			} `json:"items"`
		}
		if json.Unmarshal(env.Notify.Args, &args) != nil {
			return
		}
		x.mu.Lock()
		importer := x.byConn[conn]
		if importer == nil {
			x.mu.Unlock()
			return
		}
		var unpins []func()
		for _, item := range args.Items {
			ref := xrefRef{Realm: item.Realm, ID: item.ID}
			lease := x.leases[ref]
			if lease == nil {
				continue
			}
			lease.importers[importer.id] -= min(item.Count, lease.importers[importer.id])
			if lease.importers[importer.id] == 0 {
				delete(lease.importers, importer.id)
			}
			if unpin := x.collectLocked(ref, lease); unpin != nil {
				unpins = append(unpins, unpin)
			}
		}
		x.mu.Unlock()
		for _, unpin := range unpins {
			unpin()
		}
	}
}

func transferredArgs(method string, args json.RawMessage) json.RawMessage {
	var fields struct {
		Args json.RawMessage `json:"args"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(args, &fields) != nil {
		return nil
	}
	if method == callXrefOp {
		return fields.Args
	}
	return fields.Data
}

func (x *xrefHub) hold(conn *Conn, id string, raw json.RawMessage) {
	refs := scanXrefRefs(raw)
	if len(refs) == 0 {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.initLocked()
	source := x.byConn[conn]
	for _, ref := range refs {
		lease := x.leases[ref]
		if lease == nil {
			lease = &xrefLease{importers: make(map[string]uint64)}
			x.leases[ref] = lease
		}
		lease.holds++
		if source != nil && source.id == ref.Realm {
			lease.received++
		}
	}
	frame := xrefFrame{conn, id}
	x.holds[frame] = append(x.holds[frame], refs...)
}

// held returns the references a frame carries; unhold ends that hold after the Host has delivered them.
func (x *xrefHub) held(conn *Conn, id string) []xrefRef {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.holds[xrefFrame{conn, id}]
}

func (x *xrefHub) unhold(conn *Conn, id string) {
	x.mu.Lock()
	frame := xrefFrame{conn, id}
	refs := x.holds[frame]
	delete(x.holds, frame)
	var unpins []func()
	for _, ref := range refs {
		lease := x.leases[ref]
		if lease == nil {
			continue
		}
		lease.holds--
		if unpin := x.collectLocked(ref, lease); unpin != nil {
			unpins = append(unpins, unpin)
		}
	}
	x.mu.Unlock()
	for _, unpin := range unpins {
		unpin()
	}
}

// deliver counts one transmission of each reference to importer. The owner realm resolves its own references to the original object and holds no lease.
func (x *xrefHub) deliver(importer string, refs []xrefRef) {
	if importer == "" || len(refs) == 0 {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, ref := range refs {
		if ref.Realm == importer {
			continue
		}
		if lease := x.leases[ref]; lease != nil {
			lease.importers[importer]++
		}
	}
}

func (x *xrefHub) expect(conn *Conn, id string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.initLocked()
	x.expected[xrefFrame{conn, id}] = struct{}{}
}

func (x *xrefHub) forget(conn *Conn, id string) {
	x.mu.Lock()
	delete(x.expected, xrefFrame{conn, id})
	x.mu.Unlock()
	x.unhold(conn, id)
}

// collectLocked removes a lease no realm can reach and returns the owner notification for the transmissions it accounted.
func (x *xrefHub) collectLocked(ref xrefRef, lease *xrefLease) func() {
	if lease.holds > 0 || len(lease.importers) > 0 {
		return nil
	}
	delete(x.leases, ref)
	if lease.received == 0 {
		return nil
	}
	owner := x.realms[ref.Realm]
	if owner == nil {
		return nil
	}
	var conn *Conn
	for _, candidate := range slices.Backward(owner.order) {
		if !candidate.closed.Load() {
			conn = candidate
			break
		}
	}
	if conn == nil {
		return nil
	}
	args, _ := json.Marshal(struct {
		Items []xrefUnpin `json:"items"`
	}{[]xrefUnpin{{ID: ref.ID, Count: lease.received}}})
	lanes := x.unpins
	return func() {
		lanes.push("", func() {
			_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyXrefUnpin, Args: args}})
		})
	}
}

// closed removes conn from its realm. When a realm has no connection left, its process has exited: leases it held as an importer are released, and references it owned fail their callers.
func (x *xrefHub) closed(conn *Conn) {
	x.mu.Lock()
	realm := x.byConn[conn]
	if realm == nil {
		x.mu.Unlock()
		return
	}
	delete(x.byConn, conn)
	delete(realm.conns, conn)
	for i, candidate := range realm.order {
		if candidate == conn {
			realm.order = append(realm.order[:i], realm.order[i+1:]...)
			break
		}
	}
	for frame := range x.expected {
		if frame.conn == conn {
			delete(x.expected, frame)
		}
	}
	var unpins []func()
	// A call this connection sent may never reach a handler, so its holds end with the connection.
	for frame, refs := range x.holds {
		if frame.conn != conn {
			continue
		}
		delete(x.holds, frame)
		for _, ref := range refs {
			lease := x.leases[ref]
			if lease == nil {
				continue
			}
			lease.holds--
			if unpin := x.collectLocked(ref, lease); unpin != nil {
				unpins = append(unpins, unpin)
			}
		}
	}
	if len(realm.conns) == 0 {
		delete(x.realms, realm.id)
		for ref, lease := range x.leases {
			if ref.Realm == realm.id {
				delete(x.leases, ref)
				continue
			}
			delete(lease.importers, realm.id)
			if unpin := x.collectLocked(ref, lease); unpin != nil {
				unpins = append(unpins, unpin)
			}
		}
	}
	x.mu.Unlock()
	for _, unpin := range unpins {
		unpin()
	}
}

// xrefStats reports ledger sizes for cleanup evidence.
type xrefStats struct {
	Realms, Leases, Holds, Expected int
}

func (x *xrefHub) stats() xrefStats {
	x.mu.Lock()
	defer x.mu.Unlock()
	return xrefStats{Realms: len(x.realms), Leases: len(x.leases), Holds: len(x.holds), Expected: len(x.expected)}
}

// handleXrefCall serves a call on conn, the connection of me that delivered it.
func (h *Host) handleXrefCall(ctx context.Context, me *managedExt, conn *Conn, callID string, call *CallPayload) (*CallResultPayload, error) {
	switch call.Method {
	case callXrefHello:
		var args struct {
			Realm string `json:"realm"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		if err := h.xref.bind(conn, me, args.Realm); err != nil {
			return nil, err
		}
		return &CallResultPayload{Result: json.RawMessage("null")}, nil
	case callXrefOp:
		defer h.xref.unhold(conn, callID)
		var args struct {
			Ref  xrefRef           `json:"ref"`
			Args []json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return nil, err
		}
		caller := h.xref.realmOf(conn)
		if caller == "" {
			return nil, errors.New("xref.op before xref.hello")
		}
		owner := h.xref.ownerConn(args.Ref.Realm)
		if owner == nil {
			return nil, errors.New("cross-process reference owner exited")
		}
		h.xref.deliver(args.Ref.Realm, h.xref.held(conn, callID))
		id := fmt.Sprintf("x%d", owner.nextID.Add(1))
		h.xref.expect(owner, id)
		defer h.xref.forget(owner, id)
		// A foreign operation is synchronous in its caller, as a property access is in Pi. Its owner services it even while that owner waits on this caller.
		response, err := owner.Request(context.WithoutCancel(ctx), &Envelope{Type: MsgRequest, ID: id, Request: &RequestPayload{Method: methodXrefOp, Args: call.Args}})
		if err != nil {
			return nil, fmt.Errorf("cross-process reference owner failed: %w", err)
		}
		if response.Response == nil {
			return nil, errors.New("cross-process reference owner returned no response")
		}
		if response.Response.Error != nil {
			return nil, response.Response.Error.ToError()
		}
		h.xref.deliver(caller, h.xref.held(owner, id))
		return &CallResultPayload{Result: response.Response.Result}, nil
	}
	return nil, fmt.Errorf("unknown xref operation %q", call.Method)
}
