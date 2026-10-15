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

// pendingWindows: a waiting subscription keeps at most this many windows of ops; beyond, its current value replaces
// them.
const pendingWindows = 4

// pendingState is what waits for one subscription: a snapshot, reset or error (base), then encoded ops to apply after
// it, in order. Ops stay as they arrived (the receiving decoder validates them).
type pendingState struct {
	base    StateFrame
	hasBase bool
	ops     []delta.WireOp
	size    int
}

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

// served is a hold the peer made on this host: served locally or relayed to another link.
type served struct {
	local     bool
	ref       *RefImpl
	principal Principal
	feeds     *orderedMap[int, struct{}]
	relay     *RelayRef
}

// ─── relay ───────────────────────────────────────────────────────────────────

type relaySub struct {
	member string
	downID int
	hasID  bool
}

type relayCloseOptions struct {
	releaseDown bool
	notify      bool
}

// RelayRef is a hold relayed from an origin link to a downstream link. It owns everything done through it:
// subscriptions and calls queued while the route resolves, and the downstream twins once forwarded. close undoes all
// of it exactly once (cancels calls downstream, drops subscriptions, releases the downstream hold, rejects waiters).
type RelayRef struct {
	origin    *Connection
	frame     HoldFrame
	principal Principal
	client    bool
	// generation is the origin's authorization generation when the hold arrived; a revocation bumps it.
	generation int
	down       *Connection
	downID     int
	closed     bool
	calls      *orderedMap[int, *RelayCall]
	subs       *orderedMap[int, *relaySub]
	waiters    []*Promise[*Connection]
	settle     map[*Promise[*Connection]]pending
}

func newRelayRef(origin *Connection, frame HoldFrame, principal Principal, client bool) *RelayRef {
	return &RelayRef{
		origin: origin, frame: frame, principal: principal, client: client, generation: origin.generation,
		calls: newOrderedMap[int, *RelayCall](), subs: newOrderedMap[int, *relaySub](),
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

// open forwards the hold (and what queued meanwhile) to down.
func (relay *RelayRef) open(down *Connection) {
	if relay.closed {
		return
	}
	relay.down = down
	relay.downID = down.nextID()
	down.relayedRefs.Set(relay.downID, relay)
	frame := relay.frame
	frame.ID = relay.downID
	frame.Principal, frame.HasPrincipal = relay.principal, true
	frame.Client = relay.client || relay.frame.Client
	down.send(frame)
	for _, originSubID := range relay.subs.Keys() {
		if entry, _ := relay.subs.Get(originSubID); !entry.hasID {
			relay.forwardSub(originSubID, entry)
		}
	}
	for _, waiter := range relay.waiters {
		relay.settle[waiter].resolve(down)
	}
	relay.waiters = nil
	clear(relay.settle)
}

func (relay *RelayRef) subscribe(originSubID int, member string) {
	entry := &relaySub{member: member}
	relay.subs.Set(originSubID, entry)
	relay.origin.relaySubs.Set(originSubID, relay)
	if relay.down != nil {
		relay.forwardSub(originSubID, entry)
	}
}

func (relay *RelayRef) forwardSub(originSubID int, entry *relaySub) {
	down := relay.down
	entry.downID, entry.hasID = down.nextID(), true
	down.relayedSubs.Set(entry.downID, relayedSub{relay: relay, originSubID: originSubID})
	down.send(SubFrame{ID: entry.downID, Hold: relay.downID, Member: entry.member})
}

// resync: the origin could not keep up and dropped what waited for this subscription. It subscribes downstream anew,
// so the provider's snapshot (a reset for the origin's consumer) replaces the ops that were lost.
func (relay *RelayRef) resync(originSubID int) {
	entry, ok := relay.subs.Get(originSubID)
	relay.origin.dropState(originSubID)
	if !ok || !entry.hasID || relay.down == nil {
		return
	}
	relay.down.relayedSubs.Delete(entry.downID)
	relay.down.send(UnsubFrame{ID: entry.downID})
	relay.forwardSub(originSubID, entry)
}

func (relay *RelayRef) unsubscribe(originSubID int, forward bool) {
	entry, ok := relay.subs.Get(originSubID)
	if !ok {
		return
	}
	relay.subs.Delete(originSubID)
	relay.origin.relaySubs.Delete(originSubID)
	relay.origin.dropState(originSubID)
	if entry.hasID && relay.down != nil {
		relay.down.relayedSubs.Delete(entry.downID)
		if forward {
			relay.down.send(UnsubFrame{ID: entry.downID})
		}
	}
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
	for _, originSubID := range relay.subs.Keys() {
		relay.unsubscribe(originSubID, options.releaseDown)
	}
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

type relayedSub struct {
	relay       *RelayRef
	originSubID int
}

// ─── connection ──────────────────────────────────────────────────────────────

type connectionConfig struct {
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
// # Flow control
//
// Problem: state is pushed as fast as it changes, but a peer may read slowly (a phone behind a relay), and a relay may
// hide that: Cloudflare forwards to browsers without backpressure. Everything sent then queues in front of the next
// call result. Example: a browser opens three long transcripts (500 KiB of snapshots each) over a 5 Mbit/s path; a
// result sent next waits 2.4 s, and the snapshot of a transcript the user already left is still sent in full.
//
// Solution: the peer acknowledges what it processed (AckFrame, a cumulative frame count), and the sender counts the
// sizes of the frames not yet acknowledged. While they exceed the window, state frames wait here, one slot per
// subscription: ops are appended to the slot (encoded batches of one stream may be concatenated, see delta's
// encoder), and a snapshot, reset or error replaces it. Unsubscribing drops the slot. A slot whose ops outgrow
// pendingWindows windows is replaced by the current value (a reset), or, on a relay, by a fresh downstream
// subscription (RelayRef.resync), so memory stays bounded however long the peer stalls. Acknowledgements send the
// slots in the order they were created, as long as the window allows.
//
// Everything else (results, shapes, gone, reload control, pings) is sent at once: it is bounded by the requests the
// peer makes, and it must not wait behind state. Hence the one ordering exception of a link: while state waits, a
// result can arrive before state changes made before it. Frames of one subscription stay in order.
//
// The window bounds what is unacknowledged when a frame is sent, so one frame (a large snapshot) may exceed it. Both
// hellos must announce acks; with an older peer nothing is acknowledged and nothing waits. Links that do not report
// sizes (IPC) never fill the window.
type Connection struct {
	host      *HostImpl
	link      Link
	role      string
	peer      Peer
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
	window int
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
	pending      *orderedMap[int, *pendingState]
	received     int
	stopAck      func()
	// consumer side: what this host asked of the peer
	remotes *orderedMap[int, *RemoteRef]
	calls   *orderedMap[int, pending]
	subs    *orderedMap[int, func(frame StateSink)]
	// provider side: what the peer asked of this host
	served   *orderedMap[int, *served]
	incoming *orderedMap[int, context.CancelCauseFunc]
	feeds    *orderedMap[int, func()]
	// relaySubs: origin side of relays: the peer's sub ids that are relayed.
	relaySubs *orderedMap[int, *RelayRef]
	// downstream side of relays: ids this host used on this link
	relayedRefs  *orderedMap[int, *RelayRef]
	relayedCalls *orderedMap[int, *RelayCall]
	relayedSubs  *orderedMap[int, relayedSub]
	// votes are reload votes awaited from this (child) peer.
	votes map[int]votePending
}

// newConnection connects a link to host, on the loop.
func newConnection(host *HostImpl, link Link, config connectionConfig) *Connection {
	connection := &Connection{
		host: host, link: link, role: config.role, peer: config.peer, principal: config.principal,
		restamp: config.restamp, fencedCodeVersions: map[int]struct{}{}, admitted: map[string]int{}, nextIDValue: 1,
		lastSeen: time.Now(),
		remotes:  newOrderedMap[int, *RemoteRef](), calls: newOrderedMap[int, pending](),
		subs: newOrderedMap[int, func(StateSink)](), served: newOrderedMap[int, *served](),
		incoming: newOrderedMap[int, context.CancelCauseFunc](), feeds: newOrderedMap[int, func()](),
		relaySubs: newOrderedMap[int, *RelayRef](), relayedRefs: newOrderedMap[int, *RelayRef](),
		relayedCalls: newOrderedMap[int, *RelayCall](), relayedSubs: newOrderedMap[int, relayedSub](),
		votes: map[int]votePending{}, pending: newOrderedMap[int, *pendingState](),
		window: defaultWindow, maxCalls: math.MaxInt,
	}
	if config.options.Window != nil {
		connection.window = *config.options.Window
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

// sendState sends a state frame, or keeps it waiting (see the type comment). False: the slot is full, send a reset.
func (connection *Connection) sendState(frame StateFrame) bool {
	slot, ok := connection.pending.Get(frame.ID)
	if !ok && !connection.full() {
		connection.send(frame)
		return true
	}
	if !ok {
		slot = &pendingState{}
		connection.pending.Set(frame.ID, slot)
	}
	if frame.Ops == nil {
		slot.base, slot.hasBase = frame, true
		slot.ops, slot.size = nil, 0
		return true
	}
	size, err := opsLength(frame.Ops)
	if err != nil {
		connection.host.report(err)
		return true
	}
	if slot.size+size > connection.pendingLimit() {
		return false
	}
	slot.ops = append(slot.ops, frame.Ops...)
	slot.size += size
	return true
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

// pendingLimit is the ops one slot may keep: pendingWindows windows (saturating for a window of math.MaxInt).
func (connection *Connection) pendingLimit() int {
	if connection.window > math.MaxInt/pendingWindows {
		return math.MaxInt
	}
	return connection.window * pendingWindows
}

// dropState forgets what waits for a subscription that ended.
func (connection *Connection) dropState(id int) {
	connection.pending.Delete(id)
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
	for connection.pending.Len() > 0 && !connection.full() {
		id := connection.pending.keys[0]
		slot, _ := connection.pending.Get(id)
		connection.pending.Delete(id)
		if slot.hasBase {
			connection.send(slot.base)
		}
		if len(slot.ops) > 0 {
			connection.send(StateFrame{ID: id, Ops: slot.ops})
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

func (connection *Connection) hold(ref *RemoteRef) int {
	id := connection.nextID()
	connection.remotes.Set(id, ref)
	ref.bound(connection, id)
	connection.send(HoldFrame{
		ID: id, At: ref.at, Token: ref.contract.Description, Input: ref.input,
		Principal: ref.principal, HasPrincipal: ref.hasPrincipal, Client: ref.owner == "client",
	})
	return id
}

func (connection *Connection) release(id int) {
	connection.remotes.Delete(id)
	connection.send(ReleaseFrame{ID: id})
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

func (connection *Connection) sub(hold int, member string, sink func(frame StateSink)) int {
	id := connection.nextID()
	connection.subs.Set(id, sink)
	connection.send(SubFrame{ID: id, Hold: hold, Member: member})
	return id
}

func (connection *Connection) unsub(id int) {
	if connection.subs.Delete(id) {
		connection.send(UnsubFrame{ID: id})
	}
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
		connection.onSub(frame)
	case UnsubFrame:
		connection.onUnsub(frame.ID)
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

func (connection *Connection) isClient(frame HoldFrame) bool {
	if connection.role == "downstream" {
		return connection.peer.kind == "client"
	}
	return frame.Client
}

func (connection *Connection) onRef(frame HoldFrame) {
	if connection.served.Has(frame.ID) {
		connection.violation(fmt.Sprintf("duplicate hold id %d", frame.ID))
		return
	}
	principal := connection.principalOf(frame)
	client := connection.isClient(frame)
	if frame.At != connection.host.Name() {
		if connection.role == "upstream" {
			connection.send(GoneFrame{ID: frame.ID, Error: ToWireError(unavailable(connection.host.Name() + " does not relay"))})
			return
		}
		relay := newRelayRef(connection, frame, principal, client)
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
	entry := &served{local: true, ref: ref, principal: principal, feeds: newOrderedMap[int, struct{}]()}
	connection.served.Set(frame.ID, entry)
	shape := func() {
		current := ref.shape()
		if current == nil {
			return
		}
		if now, ok := connection.served.Get(frame.ID); !ok || now != entry {
			return
		}
		connection.send(ShapeFrame{ID: frame.ID, Variant: current.Variant, Members: current.Members, Contract: current.Contract, CodeVersion: current.CodeVersion})
	}
	shapeListener := func(Shape) { shape() }
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
		shape()
	}
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
	for _, feed := range entry.feeds.Keys() {
		connection.onUnsub(feed)
	}
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

func (connection *Connection) onSub(frame SubFrame) {
	if connection.feeds.Has(frame.ID) || connection.relaySubs.Has(frame.ID) {
		connection.violation(fmt.Sprintf("duplicate sub id %d", frame.ID))
		return
	}
	entry, ok := connection.served.Get(frame.Hold)
	if !ok {
		connection.send(StateFrame{ID: frame.ID, Error: wireErrorOf(NewChordError(CodeGone, "unknown hold", ChordErrorOptions{}))})
		return
	}
	if !entry.local {
		entry.relay.subscribe(frame.ID, frame.Member)
		return
	}
	readable, isReadable := entry.ref.member(frame.Member).(Readable[any])
	if !isReadable {
		connection.send(StateFrame{ID: frame.ID, Error: wireErrorOf(NewChordError(CodeNotInVariant, frame.Member+" is not state", ChordErrorOptions{}))})
		return
	}
	stream := delta.NewEncoder()
	send := func(event StateEvent[any]) {
		if event.Type == StateEventOps {
			ops, err := stream.Encode(event.Ops)
			if err != nil {
				connection.host.report(err)
				return
			}
			if connection.sendState(StateFrame{ID: frame.ID, Ops: ops}) {
				return
			}
			// Too many ops wait for a slow peer: the current value replaces them.
			event = StateEvent[any]{Type: StateEventReset, Value: event.Value}
		}
		// A loading provider reference has nothing yet: the hydrated value follows as a reset.
		if event.Value == nil {
			return
		}
		// Snapshots are validated against the effective schema once per (re)subscription; ops only in dev mode.
		if err := validateSnapshot(entry.ref, frame.Member, event.Value); err != nil {
			connection.sendState(StateFrame{ID: frame.ID, Error: wireErrorOf(err)})
			return
		}
		stream = delta.NewEncoder()
		if event.Type == StateEventSnap {
			connection.sendState(StateFrame{ID: frame.ID, Snap: event.Value, HasSnap: true})
		} else {
			connection.sendState(StateFrame{ID: frame.ID, Reset: event.Value, HasReset: true})
		}
	}
	unsubscribe := readable.Events(send)
	connection.feeds.Set(frame.ID, unsubscribe)
	entry.feeds.Set(frame.ID, struct{}{})
}

func (connection *Connection) onUnsub(id int) {
	connection.dropState(id)
	if unsubscribe, ok := connection.feeds.Get(id); ok {
		connection.feeds.Delete(id)
		unsubscribe()
	}
	if relay, ok := connection.relaySubs.Get(id); ok {
		relay.unsubscribe(id, true)
	}
}

func (connection *Connection) onShape(frame ShapeFrame) {
	if relay, ok := connection.relayedRefs.Get(frame.ID); ok {
		if !relay.closed {
			forwarded := frame
			forwarded.ID = relay.originID()
			relay.origin.send(forwarded)
		}
		return
	}
	ref, ok := connection.remotes.Get(frame.ID)
	if !ok {
		return
	}
	ref.shaped(Shape{Variant: frame.Variant, Members: frame.Members, Contract: frame.Contract, CodeVersion: frame.CodeVersion})
}

func (connection *Connection) onGone(frame GoneFrame) {
	if relay, ok := connection.relayedRefs.Get(frame.ID); ok {
		relay.close(FromWireError(frame.Error), relayCloseOptions{notify: true})
		return
	}
	ref, ok := connection.remotes.Get(frame.ID)
	if !ok {
		return
	}
	connection.remotes.Delete(frame.ID)
	ref.gone(FromWireError(frame.Error))
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
	if relayed, ok := connection.relayedSubs.Get(frame.ID); ok {
		forwarded := frame
		forwarded.ID = relayed.originSubID
		if !relayed.relay.origin.sendState(forwarded) {
			relayed.relay.resync(relayed.originSubID)
		}
		return
	}
	sink, ok := connection.subs.Get(frame.ID)
	if !ok {
		return
	}
	_, snapPacked := frame.Snap.(packed)
	_, resetPacked := frame.Reset.(packed)
	if !snapPacked && !resetPacked {
		sink(frame)
		return
	}
	unpacked := frame
	var err error
	if frame.HasSnap {
		unpacked.Snap, err = unpack(frame.Snap)
	} else {
		unpacked.Reset, err = unpack(frame.Reset)
	}
	if err != nil {
		unpacked = StateSink{Error: wireErrorOf(err)}
	}
	sink(unpacked)
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
	connection.subs.Clear()
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
	connection.relayedSubs.Clear()
	for _, ref := range connection.remotes.Values() {
		// An upstream link may be replaced (reconnect); a downstream host that went away is gone.
		if connection.role == "upstream" {
			ref.detached()
		} else {
			ref.gone(err)
		}
	}
	connection.remotes.Clear()
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
