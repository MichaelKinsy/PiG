package host

// Ports packages/chord/src/connection.ts: one end of a link on a host, the relay of holds between links, and the
// link options.
//
// Connection state lives on the host's loop: frames and the close arrive from the link through host.run, and the
// awaits of upstream (routes, roots, relayed calls) run as owned tasks off the loop.

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// LinkOptions are the options of a link. Nil members are absent.
type LinkOptions struct {
	// PingInterval is the time between pings (default 15 s).
	PingInterval *time.Duration
	// PingTimeout closes the link when nothing arrived for this long (default 45 s). Everything held through it is
	// released.
	PingTimeout *time.Duration
	// Window is the unacknowledged frame bytes (JSON text length) beyond which state frames wait for the peer (default
	// 256 KiB). It applies only when both ends acknowledge frames and the link reports sizes; math.MaxInt never holds
	// state back.
	Window *int
	// PendingCap is the bytes of state ops that may wait for the peer on this link (default 4 windows). Beyond, the
	// oldest waiting streams are replaced by their current value (a reset), so memory stays bounded however long the
	// peer stalls.
	PendingCap *int
	// OnProtocolMismatch is called when the peer speaks another link protocol version (Peer is nil if it did not say): the
	// link closes, and should not be reconnected with the same code. A browser reloads its page, for instance.
	OnProtocolMismatch func(versions ProtocolVersions)
}

// ProtocolVersions are the link protocol versions of the two ends of a mismatch.
type ProtocolVersions struct {
	Local int
	Peer  *int
}

const defaultWindow = 256 * 1024

// protocolVersionReason is the close reason of a link whose peer speaks another protocol version.
var protocolVersionReason = regexp.MustCompile(`^protocol-version (\d+)$`)

// pendingWindows is the default cap of waiting state ops per link, in windows.
const pendingWindows = 4

// slot is what waits on a link. A unit slot (flush set) is lazy: the shape and snapshots are built from the current
// values when it is sent. A state slot keeps a snapshot, reset or error (base) and encoded ops to apply after it, in
// order (as they arrived; the receiving decoder validates them), or, once lazy, only the promise of a reset built at
// flush.
type slot struct {
	unit  bool
	hold  int
	flush func()
	// state slots
	m       string
	base    StateFrame
	hasBase bool
	ops     []delta.WireOp
	size    int
	lazy    bool
	// overflow: how the stream recovers when its ops must be dropped: resend (the provider resends the current value
	// as a reset at flush) or resync (a relay, which has no value, asks downstream for one).
	resend func()
	resync func()
}

// overflow is how a waiting stream recovers when its ops must be dropped.
type overflow struct {
	resend func()
	resync func()
}

func stateKey(hold int, m string) string { return "s" + strconv.Itoa(hold) + "\x00" + m }
func unitKey(hold int) string            { return "u" + strconv.Itoa(hold) }

// AcceptOptions are the options of accepting a link.
type AcceptOptions struct {
	LinkOptions
	// Principal is the principal of whatever connects, from the app's authenticator. Every request on the link acts
	// as it.
	Principal Principal
	// Kind is what the peer is, decided here and never by its hello: "client" (default when empty; a browser: no
	// roots, no code versions, not a reload participant, not addressable) or "host" (a child host; needs Name).
	Kind string
	// Name is a child host's name: its hello must match, and it is reachable at this name.
	Name *string
	// Restamp is called on invalidatePrincipal: a new principal (true) keeps the link, false closes it. Nil closes it.
	Restamp func(principal Principal) (Principal, bool)
	// MaxCalls is the calls the peer may have in progress on this link (nil: no limit). Further ones never run: they
	// are answered at once with unavailable (reason too_many_calls), and the link stays open.
	MaxCalls *int
}

// Peer is what is on the other end of a connection.
type Peer struct {
	kind string
	// name is configured (downstream hosts) or announced by the trusted parent (upstream); nil when unknown.
	name *string
}

// pending is a settle-once waiter (a promise's resolve and reject).
type pending struct {
	resolve func(value any)
	reject  func(err error)
}

type votePending struct {
	resolve func(report *WireReloadReport)
	reject  func(err error)
}

func unavailable(message string) *ChordError {
	return NewChordError(CodeUnavailable, message, ChordErrorOptions{})
}

// feed is a state member's stream to the consumer: its subscription and delta encoder.
type feed struct {
	unsubscribe func()
	encoder     *delta.Encoder
	// buffer holds the events that arrived while a unit or reset was being built (replayed after it is sent); buffering
	// is false once they were replayed.
	buffer    []StateEvent[any]
	buffering bool
}

// served is a hold the peer made on this host: served locally (local) or relayed to another link.
type served struct {
	local bool
	relay *RelayRef
	// the fields below are for a local hold
	id        int
	ref       *RefImpl
	principal Principal
	// origin is the client connection it comes from (see HoldFrame.Origin); empty for a host's own holds.
	origin string
	// wantsState: the consumer wants state (hold without state: false, or sub).
	wantsState bool
	// again: the consumer had values before (re-hold): its first unit waits for the window like any other.
	again bool
	// unitSent: a unit was sent.
	unitSent bool
	// feeds are the members whose snapshot the consumer has, with their streams.
	feeds *orderedMap[string, *feed]
}

// ─── relay ───────────────────────────────────────────────────────────────────

type relayCloseOptions struct {
	releaseDown bool
	notify      bool
}

// RelayRef is a hold relayed from an origin link to a downstream link. It owns everything done through it: state and
// calls queued while the route resolves, and the downstream twins once forwarded. close undoes all of it exactly once
// (cancels calls downstream, drops what waits for the origin, releases the downstream hold, rejects waiters).
type RelayRef struct {
	origin    *Connection
	frame     HoldFrame
	principal Principal
	// originTag is the client connection the hold comes from (forwarded downstream); empty for a host's own holds.
	originTag string
	// generation is the origin's authorization generation when the hold arrived; a revocation bumps it.
	generation int
	down       *Connection
	downID     int
	closed     bool
	// stateWanted: the origin wants state (forwarded as state: false, sub, unsub).
	stateWanted bool
	// urgent are the members of the first unit, still to forward past the origin's window.
	urgent map[string]struct{}
	shaped bool
	// awaiting are the members whose ops this relay dropped: downstream ops are ignored until their reset arrives.
	awaiting map[string]struct{}
	calls    *orderedMap[int, *RelayCall]
	waiters  []*Promise[*Connection]
	settle   map[*Promise[*Connection]]pending
}

func newRelayRef(origin *Connection, frame HoldFrame, principal Principal, originTag string) *RelayRef {
	return &RelayRef{
		origin: origin, frame: frame, principal: principal, originTag: originTag, generation: origin.generation,
		stateWanted: !frame.NoState, awaiting: map[string]struct{}{},
		calls:  newOrderedMap[int, *RelayCall](),
		settle: map[*Promise[*Connection]]pending{},
	}
}

func (relay *RelayRef) originID() int { return relay.frame.ID }

// current: still authorized as it was when it arrived.
func (relay *RelayRef) current() bool {
	return !relay.closed && relay.origin.open() && relay.origin.generation == relay.generation
}

// ready is the downstream connection once the route resolved; it rejects if the relay closes first.
func (relay *RelayRef) ready() *Promise[*Connection] {
	loop := &relay.origin.host.loop
	if relay.closed {
		return rejectedPromise[*Connection](loop, NewChordError(CodeGone, "relayed hold closed", ChordErrorOptions{}))
	}
	if relay.down != nil {
		return resolvedPromise(loop, relay.down)
	}
	promise, resolve, reject := newPromise[*Connection](loop)
	relay.waiters = append(relay.waiters, promise)
	relay.settle[promise] = pending{resolve: func(value any) { resolve(value.(*Connection)) }, reject: reject}
	return promise
}

// open forwards the hold to down.
func (relay *RelayRef) open(down *Connection) {
	if relay.closed {
		return
	}
	relay.down = down
	relay.downID = down.nextID()
	down.relayedRefs.Set(relay.downID, relay)
	down.send(HoldFrame{
		ID: relay.downID, At: relay.frame.At, Token: relay.frame.Token, Input: relay.frame.Input,
		Principal: relay.principal, HasPrincipal: true, Origin: relay.originTag, NoState: !relay.stateWanted,
		Again: relay.frame.Again,
	})
	for _, waiter := range relay.waiters {
		relay.settle[waiter].resolve(down)
	}
	relay.waiters = nil
	clear(relay.settle)
}

// wantState: the origin starts or stops the hold's state.
func (relay *RelayRef) wantState(wanted bool) {
	if relay.closed || wanted == relay.stateWanted {
		return
	}
	relay.stateWanted = wanted
	if !wanted {
		relay.origin.dropHoldState(relay.originID())
		clear(relay.awaiting)
	}
	if relay.down != nil {
		if wanted {
			relay.down.send(SubFrame{Hold: relay.downID})
		} else {
			relay.down.send(UnsubFrame{Hold: relay.downID})
		}
	}
}

// resync: the origin could not keep up and dropped what waited for member m (or asked for it again): ask downstream
// for a reset, and ignore its ops until the reset arrives.
func (relay *RelayRef) resync(m string) {
	if relay.closed {
		return
	}
	relay.awaiting[m] = struct{}{}
	if relay.down != nil {
		relay.down.send(ResyncFrame{Hold: relay.downID, M: m})
	}
}

// shape is a shape from downstream: forwarded at once; the members of the first unit then skip the origin's window.
func (relay *RelayRef) shape(frame ShapeFrame) {
	if relay.closed {
		return
	}
	if !relay.shaped {
		relay.shaped = true
		if !relay.frame.Again {
			relay.urgent = map[string]struct{}{}
			for _, m := range frame.State {
				relay.urgent[m] = struct{}{}
			}
		}
	}
	frame.ID = relay.originID()
	relay.origin.send(frame)
}

// state is a state frame from downstream.
func (relay *RelayRef) state(frame StateFrame) {
	if relay.closed {
		return
	}
	m := frame.M
	if _, waiting := relay.awaiting[m]; waiting {
		if frame.As == "ops" {
			return
		}
		delete(relay.awaiting, m)
	}
	frame.Hold = relay.originID()
	if _, first := relay.urgent[m]; first {
		delete(relay.urgent, m)
		relay.origin.send(frame)
		return
	}
	relay.origin.sendState(frame, overflow{resync: func() { relay.resync(m) }})
}

// close closes the relay. releaseDown: the downstream hold is still there (origin released, disconnected, or was
// revoked): cancel its calls and release it. notify: tell the origin the hold is gone.
func (relay *RelayRef) close(err error, options relayCloseOptions) {
	if relay.closed {
		return
	}
	relay.closed = true
	for _, call := range relay.calls.Values() {
		call.cancel(err, options.releaseDown)
	}
	relay.origin.dropHoldState(relay.originID())
	if current, ok := relay.origin.served.Get(relay.originID()); ok && !current.local {
		relay.origin.served.Delete(relay.originID())
	}
	if relay.down != nil {
		relay.down.relayedRefs.Delete(relay.downID)
		if options.releaseDown {
			relay.down.send(ReleaseFrame{ID: relay.downID})
		}
	}
	for _, waiter := range relay.waiters {
		relay.settle[waiter].reject(err)
	}
	relay.waiters = nil
	clear(relay.settle)
	if options.notify {
		relay.origin.send(GoneFrame{ID: relay.originID(), Error: ToWireError(err)})
	}
}

// RelayCall is one relayed call: settles once, by result, cancellation or the relay closing; removes both mappings.
type RelayCall struct {
	relay    *RelayRef
	originID int
	downID   int
	pending  pending
	settled  bool
}

func newRelayCall(relay *RelayRef, originID, downID int, waiter pending) *RelayCall {
	call := &RelayCall{relay: relay, originID: originID, downID: downID, pending: waiter}
	relay.calls.Set(originID, call)
	if relay.down != nil {
		relay.down.relayedCalls.Set(downID, call)
	}
	return call
}

func (call *RelayCall) finish(err error, value any) {
	if call.settled {
		return
	}
	call.settled = true
	call.relay.calls.Delete(call.originID)
	if call.relay.down != nil {
		call.relay.down.relayedCalls.Delete(call.downID)
	}
	if err == nil {
		call.pending.resolve(value)
	} else {
		call.pending.reject(err)
	}
}

// cancel cancels downstream (when the downstream hold still exists) and settles with err.
func (call *RelayCall) cancel(err error, forward bool) {
	if call.settled {
		return
	}
	if forward && call.relay.down != nil {
		call.relay.down.send(CancelFrame{ID: call.downID})
	}
	call.finish(err, nil)
}

// ─── connection ──────────────────────────────────────────────────────────────

type connectionConfig struct {
	// originID is a client connection's opaque id: holds that come over it (directly or relayed) carry it as their origin.
	originID string
	// role is "upstream" (the link to this host's parent) or "downstream" (a link this host accepted).
	role      string
	principal Principal
	peer      Peer
	restamp   func(principal Principal) (Principal, bool)
	options   LinkOptions
	// maxCalls is AcceptOptions.MaxCalls; nil is no limit.
	maxCalls *int
}

// Connection is one end of a link on a host: consumer of remote references, provider for the peer's holds, and relay.
//
// # State
//
// A hold implies the state of its variant (unless it says state: false). The provider answers with a unit: the shape,
// then a snapshot of each state member the consumer does not have yet (the shape's State lists them), then the members'
// ops and resets. A reload or a variant change sends another unit with the members that are new; members that left the
// variant stop. The consumer applies a shape once its snapshots are here (RemoteBinding).
//
// # Flow control
//
// Problem: state is pushed as fast as it changes, but a peer may read slowly (a phone behind a relay), and a relay may
// hide that: Cloudflare forwards to browsers without backpressure. Everything sent then queues in front of the next
// call result. Example: a browser opens three long transcripts (500 KiB of snapshots each) over a 5 Mbit/s path; a
// result sent next waits 2.4 s, and the snapshot of a transcript the user already left is still sent in full.
//
// Solution: the peer acknowledges what it processed (AckFrame, a cumulative frame count), and the sender counts the
// sizes of the frames not yet acknowledged. While they exceed the window, state waits here in slots, flushed in
// creation order as acknowledgements come:
//   - a member's stream has one slot: ops are appended (encoded batches of one stream may be concatenated, see delta's
//     encoder), a snapshot, reset or error replaces it;
//   - a hold's unit has one slot, built lazily from the current values when it is sent, so a newer unit merges into it;
//     while it waits, the members it introduces have no stream yet.
//
// The first unit of a hold that is not a re-hold (Again) is sent at once, past the window: a Get waits for it, and must
// not wait behind other holds' state. Results, shapes, gone, reload control and pings are sent at once too: they are
// bounded by the requests the peer makes. Hence the one ordering exception of a link: while state waits, a result can
// arrive before state changes made before it. Frames of one stream stay in order.
//
// Waiting ops are capped per link (PendingCap): beyond, the oldest streams are replaced by their current value (a reset
// built at flush), or, on a relay, by a reset asked of the provider (resync), so memory stays bounded however long the
// peer stalls. The window bounds what is unacknowledged when a frame is sent, so one frame (a large snapshot) may exceed
// it. Both hellos must announce acks; with an older peer nothing is acknowledged and nothing waits. Links that do not
// report sizes (IPC) never fill the window.
type Connection struct {
	host *HostImpl
	link Link
	role string
	peer Peer
	// originID is a client connection's opaque id: holds that come over it (directly or relayed) carry it as their
	// origin; empty for any other link.
	originID  string
	principal Principal
	restamp   func(principal Principal) (Principal, bool)
	// generation is bumped when the stamped principal is revoked or re-stamped: relays from before are refused.
	generation int
	// peerCodeVersion is the code version a downstream host peer runs (from its hello, then from commits).
	peerCodeVersion int
	// synced: a downstream host peer is usable once it runs this host's current code version (see HostImpl.peerHello).
	synced bool
	// fencedCodeVersions are code versions this child announced while behind this host (fenced): this host never ran them.
	fencedCodeVersions map[int]struct{}
	// admitted are roots of fenced code versions, by the code version of this host they were admitted in.
	admitted    map[string]int
	parentName  *string
	syncWaiters []pending
	nextIDValue int
	lastSeen    time.Time
	stopPinger  func()
	// maxCalls bounds incoming (math.MaxInt: no limit).
	maxCalls int
	// flow control (see the type comment)
	window     int
	pendingCap int
	// peerAcks: whether the peer acknowledges frames; until its hello is here (ackUnknown), frames are counted in case
	// it does.
	peerAcks peerAcks
	// onProtocolMismatch is LinkOptions.OnProtocolMismatch.
	onProtocolMismatch func(versions ProtocolVersions)
	// mismatched: the peer speaks another protocol version; nothing it sends is processed any more.
	mismatched bool
	sent       int
	acked      int
	// unacked are the sizes of the frames sent after the last acknowledged one.
	unacked      []int
	unackedBytes int
	pending      *orderedMap[string, *slot]
	// pendingBytes are the bytes of ops waiting in pending.
	pendingBytes int
	received     int
	stopAck      func()
	// consumer side: what this host asked of the peer
	remotes *orderedMap[int, *RemoteBinding]
	calls   *orderedMap[int, pending]
	// provider side: what the peer asked of this host
	served   *orderedMap[int, *served]
	incoming *orderedMap[int, context.CancelCauseFunc]
	// downstream side of relays: ids this host used on this link
	relayedRefs  *orderedMap[int, *RelayRef]
	relayedCalls *orderedMap[int, *RelayCall]
	// votes are reload votes awaited from this (child) peer.
	votes map[int]votePending
}

// newConnection connects a link to host, on the loop.
func newConnection(host *HostImpl, link Link, config connectionConfig) *Connection {
	connection := &Connection{
		host: host, link: link, role: config.role, peer: config.peer, principal: config.principal,
		restamp: config.restamp, fencedCodeVersions: map[int]struct{}{}, admitted: map[string]int{}, nextIDValue: 1,
		lastSeen: time.Now(),
		remotes:  newOrderedMap[int, *RemoteBinding](), calls: newOrderedMap[int, pending](),
		served:   newOrderedMap[int, *served](),
		incoming: newOrderedMap[int, context.CancelCauseFunc](), relayedRefs: newOrderedMap[int, *RelayRef](),
		relayedCalls: newOrderedMap[int, *RelayCall](),
		votes:        map[int]votePending{}, pending: newOrderedMap[string, *slot](), originID: config.originID,
		window: defaultWindow, maxCalls: math.MaxInt,
	}
	if config.options.Window != nil {
		connection.window = *config.options.Window
	}
	connection.pendingCap = saturatingMul(connection.window, pendingWindows)
	if config.options.PendingCap != nil {
		connection.pendingCap = *config.options.PendingCap
	}
	if config.maxCalls != nil {
		connection.maxCalls = *config.maxCalls
	}
	connection.onProtocolMismatch = config.options.OnProtocolMismatch
	connection.synced = connection.role != "downstream" || connection.peer.kind != "host"
	// A transport hands frames and its close to the host without waiting for it: inline when it calls from the loop (a
	// close or the queued frames Receive delivers, as upstream's are synchronous there), queued on the loop otherwise,
	// so a busy host never holds up the transport or other links.
	handOff := func(fn func()) {
		if host.onLoop() {
			fn()
			return
		}
		host.post(fn)
	}
	if memory, ok := link.(*MemoryLink); ok {
		memory.setReceiver(&host.loop)
	}
	link.OnClose(func(reason string) { handOff(func() { connection.closed(reason) }) })
	link.Receive(func(frame Frame) { handOff(func() { connection.receive(frame) }) })
	connection.send(HelloFrame{Protocol: ProtocolVersion, Name: host.Name(), Kind: host.kind, CodeVersion: host.currentCodeVersion(), Acks: true})
	interval, timeout := 15*time.Second, 45*time.Second
	if config.options.PingInterval != nil {
		interval = *config.options.PingInterval
	}
	if config.options.PingTimeout != nil {
		timeout = *config.options.PingTimeout
	}
	// A link that was already closed closed this connection above; it needs no keepalive.
	if link.Closed() {
		return connection
	}
	n := 0
	var tick func()
	tick = func() {
		if connection.stopPinger == nil {
			return
		}
		if time.Since(connection.lastSeen) > timeout {
			link.Close("ping timeout")
			return
		}
		n++
		connection.send(PingFrame{N: n})
		connection.stopPinger = host.after(interval, tick)
	}
	connection.stopPinger = host.after(interval, tick)
	return connection
}

func (connection *Connection) open() bool { return !connection.link.Closed() }

// name is the peer's name: configured for downstream hosts, announced by the parent upstream; "" when unknown.
func (connection *Connection) name() string {
	name := connection.peer.name
	if connection.role == "upstream" {
		name = connection.parentName
	}
	if name == nil {
		return ""
	}
	return *name
}

func (connection *Connection) displayName() string {
	if name := connection.name(); name != "" || connection.knowsName() {
		return name
	}
	return "peer"
}

func (connection *Connection) knowsName() bool {
	if connection.role == "upstream" {
		return connection.parentName != nil
	}
	return connection.peer.name != nil
}

// peerAcks is whether the peer acknowledges frames: unknown until its hello.
type peerAcks uint8

const (
	acksUnknown peerAcks = iota
	acksYes
	acksNo
)

// send sends frame and counts its size for flow control. A transport failure, which Pi throws to the sender, is
// reported.
func (connection *Connection) send(frame Frame) {
	size, err := connection.link.Send(frame)
	if err != nil {
		connection.host.report(err)
		return
	}
	if connection.peerAcks == acksNo {
		return
	}
	connection.sent++
	connection.unacked = append(connection.unacked, size)
	connection.unackedBytes += size
}

// sendState sends a state frame, or keeps it waiting (see the type comment).
func (connection *Connection) sendState(frame StateFrame, fallback overflow) {
	key := stateKey(frame.Hold, frame.M)
	waiting, ok := connection.pending.Get(key)
	if !ok && !connection.full() {
		connection.send(frame)
		return
	}
	if ok && waiting.unit {
		return
	}
	if !ok {
		waiting = &slot{hold: frame.Hold, m: frame.M, resend: fallback.resend, resync: fallback.resync}
		connection.pending.Set(key, waiting)
	}
	if frame.As != "ops" {
		connection.pendingBytes -= waiting.size
		waiting.base, waiting.hasBase = frame, true
		waiting.ops, waiting.size = nil, 0
		waiting.lazy = false
		return
	}
	// A reset built at flush already covers these ops.
	if waiting.lazy {
		return
	}
	size, err := opsLength(frame.Ops)
	if err != nil {
		connection.host.report(err)
		return
	}
	if saturatingAdd(connection.pendingBytes, size) > connection.pendingCap {
		connection.makeRoom(size, waiting)
	}
	if current, ok := connection.pending.Get(key); waiting.lazy || !ok || current != waiting {
		return
	}
	waiting.ops = append(waiting.ops, frame.Ops...)
	waiting.size += size
	connection.pendingBytes += size
}

func saturatingAdd(a, b int) int {
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

// saturatingMul is a * b for a window of math.MaxInt (JavaScript's Infinity) times a count.
func saturatingMul(a, b int) int {
	if a > math.MaxInt/b { //portlint:allow numbers an overflow guard for Infinity (math.MaxInt) windows; JS multiplies doubles and Infinity stays Infinity
		return math.MaxInt
	}
	return a * b
}

// makeRoom replaces the oldest waiting streams by resets until size more bytes fit; current last, if needed.
func (connection *Connection) makeRoom(size int, current *slot) {
	for _, waiting := range connection.pending.Values() {
		if saturatingAdd(connection.pendingBytes, size) <= connection.pendingCap {
			return
		}
		if !waiting.unit && waiting != current && waiting.size > 0 {
			connection.drop(waiting)
		}
	}
	if saturatingAdd(connection.pendingBytes, size) > connection.pendingCap {
		connection.drop(current)
	}
}

// drop drops what waits for a stream: a lazy reset replaces it (provider), or the relay asks downstream for one.
func (connection *Connection) drop(waiting *slot) {
	connection.pendingBytes -= waiting.size
	waiting.base, waiting.hasBase = StateFrame{}, false
	waiting.ops, waiting.size = nil, 0
	if waiting.resend != nil {
		waiting.lazy = true
		return
	}
	connection.pending.Delete(stateKey(waiting.hold, waiting.m))
	waiting.resync()
}

// resetLater sends member m of a served hold again as a reset, through the window (resync).
func (connection *Connection) resetLater(hold int, m string, resend func()) {
	key := stateKey(hold, m)
	waiting, ok := connection.pending.Get(key)
	if ok && waiting.unit {
		return
	}
	if !ok {
		connection.pending.Set(key, &slot{hold: hold, m: m, lazy: true, resend: resend})
	} else {
		connection.pendingBytes -= waiting.size
		waiting.base, waiting.hasBase = StateFrame{}, false
		waiting.ops, waiting.size = nil, 0
		waiting.lazy = true
	}
	connection.flush()
}

// queueUnit queues a hold's lazy unit (a waiting one already covers this one), and sends what the window allows.
func (connection *Connection) queueUnit(hold int, flush func()) {
	key := unitKey(hold)
	if !connection.pending.Has(key) {
		connection.pending.Set(key, &slot{unit: true, hold: hold, flush: flush})
	}
	connection.flush()
}

// dropState forgets what waits for a stream that ended.
func (connection *Connection) dropState(hold int, m string) {
	key := stateKey(hold, m)
	waiting, ok := connection.pending.Get(key)
	if !ok || waiting.unit {
		return
	}
	connection.pendingBytes -= waiting.size
	connection.pending.Delete(key)
}

// dropHoldState forgets everything that waits for a hold (released, closed, or its state stopped).
func (connection *Connection) dropHoldState(hold int) {
	for _, key := range connection.pending.Keys() {
		waiting, _ := connection.pending.Get(key)
		if waiting.hold != hold {
			continue
		}
		if !waiting.unit {
			connection.pendingBytes -= waiting.size
		}
		connection.pending.Delete(key)
	}
}

// waiting is what waits for the peer: slots and bytes of ops (for tests and diagnostics).
func (connection *Connection) waiting() (slots, bytes int) {
	return connection.pending.Len(), connection.pendingBytes
}

// opsLength is JSON.stringify(ops).length.
func opsLength(ops []delta.WireOp) (int, error) {
	values := make([]any, len(ops))
	for index, op := range ops {
		values[index] = []any(op)
	}
	text, err := chordjson.Marshal(values)
	if err != nil {
		return 0, err
	}
	return utf16Length(text), nil
}

func (connection *Connection) full() bool {
	return connection.peerAcks == acksYes && connection.unackedBytes >= connection.window
}

func (connection *Connection) onAck(n int) {
	if connection.peerAcks != acksYes || n < connection.acked || n > connection.sent {
		connection.violation(fmt.Sprintf("ack %d outside %d..%d", n, connection.acked, connection.sent))
		return
	}
	count := n - connection.acked
	for _, size := range connection.unacked[:count] {
		connection.unackedBytes -= size
	}
	connection.unacked = connection.unacked[count:]
	connection.acked = n
	connection.flush()
}

// flush sends waiting slots in creation order while the window allows.
func (connection *Connection) flush() {
	for connection.pending.Len() > 0 && !connection.full() {
		key := connection.pending.keys[0]
		waiting, _ := connection.pending.Get(key)
		connection.pending.Delete(key)
		if waiting.unit {
			waiting.flush()
			continue
		}
		connection.pendingBytes -= waiting.size
		if waiting.lazy {
			if waiting.resend != nil {
				waiting.resend()
			}
			continue
		}
		if waiting.hasBase {
			connection.send(waiting.base)
		}
		if len(waiting.ops) > 0 {
			connection.send(StateFrame{Hold: waiting.hold, M: waiting.m, As: "ops", Ops: waiting.ops})
		}
	}
}

// scheduleAck acknowledges once the frames that arrived together are processed: one ack per burst, not per frame.
func (connection *Connection) scheduleAck() {
	if connection.peerAcks != acksYes || connection.stopAck != nil || !connection.open() {
		return
	}
	connection.stopAck = connection.host.after(0, func() {
		if connection.stopAck == nil {
			return
		}
		connection.stopAck = nil
		if _, err := connection.link.Send(AckFrame{N: connection.received}); err != nil {
			connection.host.report(err)
		}
	})
}

func (connection *Connection) nextID() int {
	id := connection.nextIDValue
	connection.nextIDValue++
	return id
}

// whenSynced resolves once the peer is usable (a host peer runs this host's code version); it rejects if the link closes
// first.
func (connection *Connection) whenSynced() *Promise[struct{}] {
	loop := &connection.host.loop
	if !connection.open() {
		return rejectedPromise[struct{}](loop, unavailable("link to "+connection.displayName()+" closed"))
	}
	if connection.synced {
		return resolvedPromise(loop, struct{}{})
	}
	promise, resolve, reject := newPromise[struct{}](loop)
	connection.syncWaiters = append(connection.syncWaiters, pending{resolve: func(any) { resolve(struct{}{}) }, reject: reject})
	return promise
}

func (connection *Connection) markSynced(codeVersion int) {
	connection.peerCodeVersion = codeVersion
	connection.synced = true
	for _, waiter := range connection.syncWaiters {
		waiter.resolve(nil)
	}
	connection.syncWaiters = nil
}

// revoke revokes the stamped principal: relays made under it close at once (also those still resolving a route).
// keep false is Pi's undefined next principal: the link closes.
func (connection *Connection) revoke(next Principal, keep bool) {
	connection.generation++
	err := NewChordError(CodeGone, "principal invalidated", ChordErrorOptions{})
	for _, entry := range connection.served.Values() {
		if !entry.local {
			entry.relay.close(err, relayCloseOptions{releaseDown: true, notify: true})
		}
	}
	if !keep {
		connection.link.Close("principal invalidated")
	} else {
		connection.principal = next
	}
}

// protocolMismatch stops talking to a peer of another protocol version: it reports it and closes the link. The close
// reason tells the peer this side's version (`protocol-version <n>`); a peer that noticed first told us the same way
// (closed).
func (connection *Connection) protocolMismatch(peer *int, closeLink bool) {
	if connection.mismatched {
		return
	}
	connection.mismatched = true
	if connection.onProtocolMismatch != nil {
		if err := callReporting(func() { connection.onProtocolMismatch(ProtocolVersions{Local: ProtocolVersion, Peer: peer}) }); err != nil {
			connection.host.report(err)
		}
	}
	if closeLink {
		connection.link.Close(fmt.Sprintf("protocol-version %d", ProtocolVersion))
	}
}

// violation: a protocol violation: the peer cannot be trusted with anything on this link any more.
func (connection *Connection) violation(reason string) {
	connection.host.report(NewChordError(CodeInvalid, "link to "+connection.displayName()+": "+reason, ChordErrorOptions{Reason: "protocol"}))
	connection.link.Close("protocol error: " + reason)
}

// ─── consumer side (RemoteChannel) ───

func (connection *Connection) hold(binding *RemoteBinding, again bool) int {
	id := connection.nextID()
	connection.remotes.Set(id, binding)
	binding.bound(connection, id)
	connection.send(HoldFrame{
		ID: id, At: binding.at, Token: binding.contract.Description, Input: binding.input,
		Principal: binding.principal, HasPrincipal: binding.hasPrincipal, NoState: !binding.stateOn, Again: again,
	})
	return id
}

func (connection *Connection) release(id int) {
	connection.remotes.Delete(id)
	connection.send(ReleaseFrame{ID: id})
}

func (connection *Connection) wantState(hold int, wanted bool) {
	if !connection.remotes.Has(hold) {
		return
	}
	if wanted {
		connection.send(SubFrame{Hold: hold})
	} else {
		connection.send(UnsubFrame{Hold: hold})
	}
}

func (connection *Connection) call(hold int, member string, args any, hasArgs bool, root *RootRef, signal context.Context) *Promise[any] {
	id := connection.nextID()
	promise, resolve, reject := newPromise[any](&connection.host.loop)
	if signal.Err() != nil {
		reject(NewChordError(CodeAborted, "call aborted", ChordErrorOptions{}))
		return promise
	}
	settled := false
	stop := context.AfterFunc(signal, func() {
		connection.host.run(func() {
			if settled {
				return
			}
			settled = true
			connection.calls.Delete(id)
			connection.send(CancelFrame{ID: id})
			reject(NewChordError(CodeAborted, "call aborted", ChordErrorOptions{Cause: context.Cause(signal)}))
		})
	})
	connection.calls.Set(id, pending{
		resolve: func(value any) {
			if !settled {
				settled = true
				stop()
				resolve(value)
			}
		},
		reject: func(err error) {
			if !settled {
				settled = true
				stop()
				reject(err)
			}
		},
	})
	// Clients never carry roots or code versions: the server starts roots for them.
	frame := CallFrame{ID: id, Hold: hold, Member: member, Args: args, HasArgs: hasArgs}
	if root != nil && connection.host.kind == "host" {
		connection.host.rootSent(root.RootID, connection)
		frame.Root = root
	}
	connection.send(frame)
	return promise
}

// ─── receiving (on the loop) ───

func (connection *Connection) receive(frame Frame) {
	if connection.mismatched {
		return
	}
	connection.lastSeen = time.Now()
	if _, malformed := frame.(MalformedFrame); malformed {
		connection.violation("malformed frame")
		return
	}
	if ack, isAck := frame.(AckFrame); isAck {
		connection.onAck(ack.N)
		return
	}
	connection.received++
	if err := callReporting(func() {
		if err := connection.dispatch(frame); err != nil {
			panic(err)
		}
	}); err != nil {
		connection.host.report(err)
	}
	connection.scheduleAck()
}

func (connection *Connection) dispatch(frame Frame) error {
	switch frame := frame.(type) {
	case HelloFrame:
		connection.onHello(frame)
	case PingFrame:
		connection.send(PongFrame(frame))
	case PongFrame, ByeFrame, AckFrame:
	case HoldFrame:
		connection.onRef(frame)
	case ReleaseFrame:
		connection.dropServed(frame.ID, true)
	case CallFrame:
		connection.onCall(frame)
	case CancelFrame:
		if cancel, ok := connection.incoming.Get(frame.ID); ok {
			cancel(NewChordError(CodeAborted, "cancelled by caller", ChordErrorOptions{}))
		}
	case SubFrame:
		connection.onWantState(frame.Hold, true)
	case UnsubFrame:
		connection.onWantState(frame.Hold, false)
	case ResyncFrame:
		connection.onResync(frame.Hold, frame.M)
	case ShapeFrame:
		connection.onShape(frame)
	case GoneFrame:
		connection.onGone(frame)
	case ResultFrame:
		connection.onResult(frame)
	case StateFrame:
		connection.onState(frame)
	case RootEndFrame:
		// Honored only from the link the root came over (see HostImpl.foreignRootEnded).
		delete(connection.admitted, frame.RootID)
		connection.host.foreignRootEnded(frame.RootID, connection)
	case InvalidateFrame, PrepareFrame, CommitFrame, AbortFrame, RetireFrame:
		// Control frames only from the parent.
		if connection.role != "upstream" {
			connection.violation(frame.T() + " from a child")
			return nil
		}
		switch frame := frame.(type) {
		case InvalidateFrame:
			connection.host.goTask(func() {
				if err := connection.host.InvalidatePrincipal(PrincipalMatch{Pattern: frame.Pattern}); err != nil {
					connection.host.report(err)
				}
			})
		case RetireFrame:
			connection.host.allowRetire(frame.CodeVersion)
		default:
			return connection.host.onReloadFrame(connection, frame)
		}
	case VoteFrame:
		if connection.role != "downstream" || connection.peer.kind != "host" {
			connection.violation("vote from a non-participant")
			return nil
		}
		vote, ok := connection.votes[frame.Reload]
		delete(connection.votes, frame.Reload)
		if !ok {
			return nil
		}
		if frame.Error == nil {
			vote.resolve(frame.Report)
		} else {
			vote.reject(FromWireError(*frame.Error))
		}
	}
	return nil
}

// onHello: the peer's hello must agree with what this side decided about it. A downstream host announces its code version:
// the host either finds it in step or fences and reconciles it (never renumbers).
func (connection *Connection) onHello(frame HelloFrame) {
	// A hello without a version is an older Chord's: version 1.
	peer := frame.Protocol
	if peer == 0 {
		peer = 1
	}
	if peer != ProtocolVersion {
		connection.protocolMismatch(&peer, true)
		return
	}
	if connection.peerAcks == acksUnknown {
		connection.peerAcks = acksNo
		if frame.Acks {
			connection.peerAcks = acksYes
		} else {
			connection.unacked, connection.unackedBytes = nil, 0
		}
	}
	if connection.role == "upstream" {
		if frame.Kind != "host" {
			connection.violation("parent is not a host")
			return
		}
		if connection.parentName == nil {
			name := frame.Name
			connection.parentName = &name
		}
		return
	}
	if frame.Kind != connection.peer.kind || (connection.peer.name != nil && frame.Name != *connection.peer.name) {
		name := ""
		if connection.peer.name != nil {
			name = *connection.peer.name
		}
		connection.violation(fmt.Sprintf("hello %s %s does not match the accepted %s %s", frame.Kind, frame.Name, connection.peer.kind, name))
		return
	}
	if connection.peer.kind == "host" {
		connection.host.peerHello(connection, frame.CodeVersion)
	}
}

// principalOf is principal stamping: a downstream link acts as the principal it was accepted with, whatever the
// frame says. Only the parent may forward a principal (relayed requests); its own requests act as the principal given to Connect.
func (connection *Connection) principalOf(frame HoldFrame) Principal {
	if connection.role == "downstream" {
		return connection.principal
	}
	if frame.HasPrincipal && frame.Principal != nil {
		return frame.Principal
	}
	return connection.principal
}

// originOf is the client connection a hold comes from: this link's own id for a client link (whatever the frame says),
// the id the parent forwarded for a relayed hold, none for a host's own holds.
func (connection *Connection) originOf(frame HoldFrame) string {
	if connection.role == "downstream" {
		return connection.originID
	}
	return frame.Origin
}

func (connection *Connection) onRef(frame HoldFrame) {
	if connection.served.Has(frame.ID) {
		connection.violation(fmt.Sprintf("duplicate hold id %d", frame.ID))
		return
	}
	principal := connection.principalOf(frame)
	origin := connection.originOf(frame)
	client := origin != ""
	if frame.At != connection.host.Name() {
		if connection.role == "upstream" {
			connection.send(GoneFrame{ID: frame.ID, Error: ToWireError(unavailable(connection.host.Name() + " does not relay"))})
			return
		}
		relay := newRelayRef(connection, frame, principal, origin)
		connection.served.Set(frame.ID, &served{relay: relay})
		// Pi: this.host.relay(relay).catch(...) (connection.ts:497): the relay starts here, on the loop.
		connection.host.relay(relay).then(func(_ struct{}, err error) {
			if err != nil {
				relay.close(err, relayCloseOptions{releaseDown: true, notify: true})
			}
		})
		return
	}
	ref, err := connection.acquireServed(frame, principal, client)
	if err != nil {
		if client {
			err = clientError(err)
		}
		connection.send(GoneFrame{ID: frame.ID, Error: ToWireError(err)})
		return
	}
	entry := &served{
		local: true, id: frame.ID, ref: ref, principal: principal, origin: origin, wantsState: !frame.NoState,
		again: frame.Again, feeds: newOrderedMap[string, *feed](),
	}
	connection.served.Set(frame.ID, entry)
	shapeListener := func(Shape) { connection.unit(entry) }
	ref.shapeListeners.Set(&shapeListener, struct{}{})
	goneListener := func(err error) {
		if now, ok := connection.served.Get(frame.ID); !ok || now != entry {
			return
		}
		connection.dropServed(frame.ID, false)
		connection.send(GoneFrame{ID: frame.ID, Error: ToWireError(err)})
	}
	ref.goneListeners.Set(&goneListener, struct{}{})
	if ref.status == RefLive {
		connection.unit(entry)
	}
}

// stateMembers are the state members of shape the consumer's contract knows.
func stateMembers(ref *RefImpl, shape *Shape) []string {
	var members []string
	for _, name := range shape.Members {
		if description, ok := ref.describe(shape.Variant, name); ok && description.Kind == MemberState {
			members = append(members, name)
		}
	}
	return members
}

// unit: the shape changed (live, reload, variant) or state was asked for: members that left lose their stream, and a
// unit goes out with the shape and the members the consumer lacks. The first unit of a hold (not a re-hold) is urgent.
func (connection *Connection) unit(entry *served) {
	if now, ok := connection.served.Get(entry.id); !ok || now != entry {
		return
	}
	shape := entry.ref.shape()
	if shape == nil {
		return
	}
	members := map[string]bool{}
	if entry.wantsState {
		for _, name := range stateMembers(entry.ref, shape) {
			members[name] = true
		}
	}
	for _, name := range entry.feeds.Keys() {
		if members[name] {
			continue
		}
		feed, _ := entry.feeds.Get(name)
		feed.unsubscribe()
		entry.feeds.Delete(name)
		connection.dropState(entry.id, name)
	}
	urgent := entry.feeds.Len() == 0 && (!entry.again || entry.unitSent)
	if urgent {
		connection.sendUnit(entry)
	} else {
		connection.queueUnit(entry.id, func() { connection.sendUnit(entry) })
	}
}

// sendUnit builds and sends a unit from the current shape and values.
func (connection *Connection) sendUnit(entry *served) {
	connection.pending.Delete(unitKey(entry.id))
	if now, ok := connection.served.Get(entry.id); !ok || now != entry {
		return
	}
	shape := entry.ref.shape()
	if shape == nil {
		return
	}
	entry.unitSent = true
	var members []string
	if entry.wantsState {
		members = stateMembers(entry.ref, shape)
	}
	type attached struct {
		name string
		feed *feed
	}
	var snaps []StateFrame
	var feeds []attached
	for _, name := range members {
		if entry.feeds.Has(name) {
			continue
		}
		frame, feed, ok := connection.attach(entry, name, "snap")
		if !ok {
			continue
		}
		snaps = append(snaps, frame)
		feeds = append(feeds, attached{name, feed})
	}
	listed := make([]string, len(snaps))
	for index, frame := range snaps {
		listed[index] = frame.M
	}
	connection.send(ShapeFrame{ID: entry.id, Variant: shape.Variant, Members: shape.Members, Contract: shape.Contract, State: listed})
	for _, frame := range snaps {
		connection.send(frame)
	}
	for _, attachment := range feeds {
		connection.replay(entry, attachment.name, attachment.feed)
	}
}

// attach subscribes member name for the consumer: its snapshot (as a frame of type as), and a feed whose later events
// wait in its buffer until the snapshot is sent (replay). False if the member is not readable state.
func (connection *Connection) attach(entry *served, name, as string) (StateFrame, *feed, bool) {
	readable, isReadable := entry.ref.member(name).(Readable[any])
	if !isReadable {
		return StateFrame{}, nil, false
	}
	current := &feed{unsubscribe: func() {}, encoder: delta.NewEncoder(), buffering: true}
	var first *StateEvent[any]
	current.unsubscribe = readable.Events(func(event StateEvent[any]) {
		switch {
		case first == nil:
			first = &event
		case current.buffering:
			current.buffer = append(current.buffer, event)
		default:
			connection.feedEvent(entry, name, current, event)
		}
	})
	entry.feeds.Set(name, current)
	var value any
	if first != nil {
		value = first.Value
	}
	return connection.snapshot(entry, name, as, value, first != nil), current, true
}

// replay sends the events that arrived while the snapshot was built, now that it is sent.
func (connection *Connection) replay(entry *served, name string, current *feed) {
	buffered := current.buffer
	current.buffer, current.buffering = nil, false
	for _, event := range buffered {
		connection.feedEvent(entry, name, current, event)
	}
}

// snapshot is a member's state frame for a snapshot or reset: validated against the schema the consumer is promised.
// present is false for a value that is `undefined` (the member produced no event).
func (connection *Connection) snapshot(entry *served, name, as string, value any, present bool) StateFrame {
	if err := validateSnapshot(entry.ref, name, value); err != nil {
		return StateFrame{Hold: entry.id, M: name, As: "error", Error: wireErrorOf(err)}
	}
	return StateFrame{Hold: entry.id, M: name, As: as, Value: value, HasValue: present}
}

// feedEvent is a later event of a member's stream: ops are encoded, a reset starts a new stream.
func (connection *Connection) feedEvent(entry *served, name string, current *feed, event StateEvent[any]) {
	if now, _ := entry.feeds.Get(name); now != current {
		return
	}
	fallback := overflow{resend: func() { connection.resend(entry, name) }}
	if event.Type == StateEventOps {
		ops, err := current.encoder.Encode(event.Ops)
		if err != nil {
			connection.host.report(err)
			return
		}
		connection.sendState(StateFrame{Hold: entry.id, M: name, As: "ops", Ops: ops}, fallback)
		return
	}
	current.encoder = delta.NewEncoder()
	connection.sendState(connection.snapshot(entry, name, "reset", event.Value, true), fallback)
}

// resend sends member name again, now, as a reset from its current value (a lazy slot is flushed, or resync).
func (connection *Connection) resend(entry *served, name string) {
	current, ok := entry.feeds.Get(name)
	if now, served := connection.served.Get(entry.id); !ok || !served || now != entry {
		return
	}
	current.unsubscribe()
	entry.feeds.Delete(name)
	frame, attached, ok := connection.attach(entry, name, "reset")
	if !ok {
		return
	}
	connection.send(frame)
	connection.replay(entry, name, attached)
}

// onWantState: the consumer starts (sub) or stops (unsub) the state of a hold.
func (connection *Connection) onWantState(hold int, wanted bool) {
	entry, ok := connection.served.Get(hold)
	if !ok {
		return
	}
	if !entry.local {
		entry.relay.wantState(wanted)
		return
	}
	if entry.wantsState == wanted {
		return
	}
	entry.wantsState = wanted
	if wanted {
		connection.unit(entry)
		return
	}
	for _, feed := range entry.feeds.Values() {
		feed.unsubscribe()
	}
	entry.feeds.Clear()
	connection.dropHoldState(hold)
}

// onResync: the consumer (a relay that dropped ops) asks for member m again as a reset.
func (connection *Connection) onResync(hold int, m string) {
	entry, ok := connection.served.Get(hold)
	if !ok {
		return
	}
	if !entry.local {
		entry.relay.resync(m)
		return
	}
	if !entry.feeds.Has(m) {
		return
	}
	connection.resetLater(hold, m, func() { connection.resend(entry, m) })
}

func (connection *Connection) acquireServed(frame HoldFrame, principal Principal, client bool) (*RefImpl, error) {
	token, err := contractFromDescription(frame.Token)
	if err != nil {
		return nil, err
	}
	if token.Scope != ScopeRemote {
		return nil, Invalid("token", token.Name+" is local; only remote types cross links", nil)
	}
	// Addressed here, so the input must place it here (also by this host's contract, checked when acquiring).
	at, err := resolveHost(token.Name, token.Description.Host, frame.Input)
	if err != nil {
		return nil, err
	}
	if at != connection.host.Name() {
		return nil, Invalid("placement", fmt.Sprintf("%s with this input does not live on %s", token.Name, connection.host.Name()), nil)
	}
	owner := RefOwner("connection")
	if client {
		owner = "client"
	}
	acquired, err := connection.host.acquire(token, frame.Input, &GetOptions{Principal: principal, HasPrincipal: true}, owner, nil, false)
	if err != nil {
		return nil, err
	}
	ref, ok := acquired.(*RefImpl)
	if !ok {
		return nil, unavailable("not a local reference")
	}
	return ref, nil
}

// dropServed forgets a served hold: unsubscribes its feeds and releases it (locally or downstream).
func (connection *Connection) dropServed(id int, release bool) {
	entry, ok := connection.served.Get(id)
	if !ok {
		return
	}
	connection.served.Delete(id)
	if !entry.local {
		entry.relay.close(NewChordError(CodeReleased, "relayed hold released", ChordErrorOptions{}), relayCloseOptions{releaseDown: release})
		return
	}
	for _, feed := range entry.feeds.Values() {
		feed.unsubscribe()
	}
	entry.feeds.Clear()
	connection.dropHoldState(id)
	if release {
		entry.ref.Release()
	}
}

func (connection *Connection) onCall(frame CallFrame) {
	if connection.incoming.Has(frame.ID) {
		connection.violation(fmt.Sprintf("duplicate call id %d", frame.ID))
		return
	}
	if connection.incoming.Len() >= connection.maxCalls {
		err := NewChordError(CodeUnavailable,
			fmt.Sprintf("Too many calls in progress on this connection (%d); wait for earlier ones to finish", connection.maxCalls),
			ChordErrorOptions{Reason: "too_many_calls"})
		connection.send(ResultFrame{ID: frame.ID, Error: wireErrorOf(err)})
		return
	}
	entry, ok := connection.served.Get(frame.Hold)
	if !ok {
		connection.send(ResultFrame{ID: frame.ID, Error: wireErrorOf(NewChordError(CodeGone, "unknown hold", ChordErrorOptions{}))})
		return
	}
	//portlint:allow ctxbackground connection.ts:579 `new AbortController()`: the incoming call's own controller, aborted by a cancel frame or the link closing
	signal, cancel := context.WithCancelCause(context.Background())
	connection.incoming.Set(frame.ID, cancel)
	// Roots are honored from hosts only; a request without one (or from a client) starts one here.
	var root *RootRef
	if connection.peer.kind == "host" {
		root = connection.admitRoot(frame.Root)
	}
	principal := entry.principal
	if !entry.local {
		principal = entry.relay.principal
	}
	run := func(ctx Ctx) (any, error) {
		if entry.local {
			args, err := unpack(frame.Args)
			if err != nil {
				return nil, err
			}
			return entry.ref.object.call(entry.ref, frame.Member, args, ctx.(*CtxImpl))
		}
		// The root's signal: a cancel from the origin and the root's code version retiring both reach it.
		return relayCall(entry.relay, frame, RootRef{RootID: ctx.RootID(), CodeVersion: ctx.CodeVersion()}, ctx.Signal(), connection.host)
	}
	connection.host.goTask(func() {
		var value any
		var err error
		if root == nil {
			value, err = connection.host.Root(run, RootOptions{Signal: signal, Principal: principal})
		} else {
			value, err = connection.host.joinRoot(root.RootID, root.CodeVersion, run, joinOptions{principal: principal, signal: signal, from: connection})
		}
		connection.host.run(func() {
			if err != nil {
				connection.send(ResultFrame{ID: frame.ID, Error: wireErrorOf(err)})
			} else {
				connection.send(ResultFrame{ID: frame.ID, Value: value, HasValue: value != nil})
			}
			connection.incoming.Delete(frame.ID)
		})
	})
}

func wireErrorOf(err error) *WireError {
	wire := ToWireError(err)
	return &wire
}

// admitRoot: a child that connected behind this host runs roots of an code version this host never had (it was fenced and
// is being, or was, reconciled). Such a root is admitted in this host's current code version at its first call here, and
// keeps that code version for its later calls (superseded once this host retired it). Other unknown code versions stay superseded.
func (connection *Connection) admitRoot(root *RootRef) *RootRef {
	if root == nil || connection.role != "downstream" {
		return root
	}
	if _, fenced := connection.fencedCodeVersions[root.CodeVersion]; !fenced || connection.host.hasCodeVersion(root.CodeVersion) {
		return root
	}
	codeVersion, ok := connection.admitted[root.RootID]
	if !ok {
		codeVersion = connection.host.currentCodeVersion()
		connection.admitted[root.RootID] = codeVersion
	}
	return &RootRef{RootID: root.RootID, CodeVersion: codeVersion}
}

func (connection *Connection) onShape(frame ShapeFrame) {
	if relay, ok := connection.relayedRefs.Get(frame.ID); ok {
		relay.shape(frame)
		return
	}
	binding, ok := connection.remotes.Get(frame.ID)
	if !ok {
		return
	}
	binding.shaped(Shape{Variant: frame.Variant, Members: frame.Members, Contract: frame.Contract}, frame.State)
}

func (connection *Connection) onGone(frame GoneFrame) {
	if relay, ok := connection.relayedRefs.Get(frame.ID); ok {
		relay.close(FromWireError(frame.Error), relayCloseOptions{notify: true})
		return
	}
	binding, ok := connection.remotes.Get(frame.ID)
	if !ok {
		return
	}
	connection.remotes.Delete(frame.ID)
	binding.gone(FromWireError(frame.Error))
}

func (connection *Connection) onResult(frame ResultFrame) {
	if relayed, ok := connection.relayedCalls.Get(frame.ID); ok {
		if frame.Error == nil {
			relayed.finish(nil, frame.Value)
		} else {
			relayed.finish(FromWireError(*frame.Error), frame.Value)
		}
		return
	}
	waiter, ok := connection.calls.Get(frame.ID)
	connection.calls.Delete(frame.ID)
	if !ok {
		return
	}
	if frame.Error != nil {
		waiter.reject(FromWireError(*frame.Error))
		return
	}
	value, err := unpack(frame.Value)
	if err != nil {
		waiter.reject(err)
		return
	}
	waiter.resolve(value)
}

func (connection *Connection) onState(frame StateFrame) {
	if relay, ok := connection.relayedRefs.Get(frame.Hold); ok {
		relay.state(frame)
		return
	}
	binding, ok := connection.remotes.Get(frame.Hold)
	if !ok {
		return
	}
	if _, isPacked := frame.Value.(packed); !isPacked {
		binding.state(frame.M, frame)
		return
	}
	value, err := unpack(frame.Value)
	if err != nil {
		binding.state(frame.M, StateFrame{As: "error", Error: wireErrorOf(err)})
		return
	}
	frame.Value = value
	binding.state(frame.M, frame)
}

// ─── lifetime ───

func (connection *Connection) closed(reason string) {
	if told := protocolVersionReason.FindStringSubmatch(reason); told != nil {
		//portlint:allow numbers the digits are a protocol version this package wrote into a close reason
		if version, err := strconv.Atoi(told[1]); err == nil && version != ProtocolVersion {
			connection.protocolMismatch(&version, false)
		}
	}
	if connection.stopPinger != nil {
		connection.stopPinger()
		connection.stopPinger = nil
	}
	if connection.stopAck != nil {
		connection.stopAck()
		connection.stopAck = nil
	}
	connection.pending.Clear()
	connection.pendingBytes = 0
	err := unavailable("link to " + connection.displayName() + " closed: " + reason)
	// As origin: everything the peer held here, relayed or not, is released (downstream calls cancelled).
	for _, id := range connection.served.Keys() {
		connection.dropServed(id, true)
	}
	for _, cancel := range connection.incoming.Values() {
		cancel(err)
	}
	connection.incoming.Clear()
	for _, waiter := range connection.calls.Values() {
		waiter.reject(err)
	}
	connection.calls.Clear()
	for _, vote := range connection.votes {
		vote.reject(err)
	}
	clear(connection.votes)
	for _, waiter := range connection.syncWaiters {
		waiter.reject(err)
	}
	connection.syncWaiters = nil
	// As downstream of relays: their origins lose the hold.
	for _, relay := range connection.relayedRefs.Values() {
		relay.close(err, relayCloseOptions{notify: true})
	}
	connection.relayedRefs.Clear()
	connection.relayedCalls.Clear()
	bindings := connection.remotes.Values()
	connection.remotes.Clear()
	for _, binding := range bindings {
		// An upstream link may be replaced (reconnect); a downstream host that went away is gone.
		if connection.role == "upstream" {
			binding.detached()
		} else {
			binding.gone(err)
		}
	}
	connection.host.connectionClosed(connection)
}

// relayCall is a call through a relay, off the loop: it waits for the route, then forwards with the root it runs
// in. It settles exactly once; a cancel (or the origin going away) is forwarded downstream and removes both mappings.
func relayCall(relay *RelayRef, frame CallFrame, root RootRef, signal context.Context, host *HostImpl) (any, error) {
	abortError := func() error {
		return NewChordError(CodeAborted, "call cancelled", ChordErrorOptions{Cause: context.Cause(signal)})
	}
	if signal.Err() != nil {
		return nil, abortError()
	}
	var ready *Promise[*Connection]
	host.run(func() { ready = relay.ready() })
	down, err := ready.Await(signal)
	if err != nil {
		if signal.Err() != nil && !ready.Settled() {
			return nil, abortError()
		}
		return nil, err
	}
	var result *Promise[any]
	var stopCancel func() bool
	// Stops following the signal once the call settled, as upstream's finally removes the listener.
	defer func() {
		if stopCancel != nil {
			stopCancel()
		}
	}()
	host.run(func() {
		if relay.closed {
			err = NewChordError(CodeGone, "relayed hold closed", ChordErrorOptions{})
			return
		}
		var resolve func(any)
		var reject func(error)
		result, resolve, reject = newPromise[any](&host.loop)
		call := newRelayCall(relay, frame.ID, down.nextID(), pending{resolve: resolve, reject: reject})
		stopCancel = context.AfterFunc(signal, func() { host.run(func() { call.cancel(abortError(), true) }) })
		host.rootSent(root.RootID, down)
		down.send(CallFrame{ID: call.downID, Hold: relay.downID, Member: frame.Member, Args: frame.Args, HasArgs: frame.HasArgs, Root: &root})
	})
	if err != nil {
		return nil, err
	}
	//portlint:allow ctxmapping signal aborts the relayed call through the AfterFunc above, which rejects result; the await itself has no abort, as in connection.ts
	return result.Await(context.Background())
}

// validateSnapshot validates a snapshot against the schema the consumer is promised: the provider's own schema for a
// direct binding, the consumer's contract for one through an accepts adapter (the adapter's view is what the
// consumer gets).
func validateSnapshot(ref *RefImpl, member string, value any) error {
	if value == nil {
		return nil
	}
	installed := ref.object.currentInstalled()
	if installed == nil {
		return nil
	}
	view := ref.currentView()
	var description MemberDescription
	var ok bool
	if view != nil && !sameValue(view, installed.implementation) {
		kind, _ := variantOf(view)
		description, ok = ref.contract.Description.member(kind, member)
	} else {
		kind, _ := variantOf(installed.implementation)
		description, ok = installed.service.contract.Description.member(kind, member)
	}
	if ok && description.Kind == MemberState {
		return assertValid("state", ref.addr+"."+member, description.Schema, value)
	}
	return nil
}

// clientError: clients (browsers) holding an outdated contract are told to reload their code.
func clientError(err error) error {
	if chordError, ok := err.(*ChordError); ok && chordError.Code == CodeContractMismatch { //nolint:errorlint // Pi's instanceof does not unwrap
		return NewChordError(CodeUpgradeRequired, chordError.Message, ChordErrorOptions{Cause: chordError})
	}
	return err
}
