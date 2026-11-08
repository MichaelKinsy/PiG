package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/src/next-node/http/upgrades.ts. Upgraded connections (slice 10 §2, D6, Q6): the links endpoint
// (`host.accept` over a WebSocket with a per-socket output cap) and plugin sockets (a WebSocket handed to the contribution's
// handler for its own protocol, in a scope lasting until it closes, kept alive by a heartbeat). Each connection is kept with
// what admitted it: when that leaves (its allowed host, its relay, its socket handler) it is terminated at once and its
// scope sealed (decision "Slice 10 fix round, allowed hosts only through contributions"); the component's close closes all
// (1001), terminating at its timeout those that never acknowledge.
//
// Forced Go differences.
//   - The upgrade is done on the request in hand (net/http hijacks its connection) instead of on a detached Node socket and
//     `head`: Link and Socket take the ResponseWriter and the request.
//   - A handler returns an error where Pi's throws or rejects; reading its socket's frames starts when it returned.
//   - Pi's timers are setTimeout; `afterFunc` is the clock a test replaces (time.AfterFunc by default).
//   - `ws`'s `WebSocketServer` per upgrade is gorilla's Upgrader, made per upgrade and dropped with it.
//   - Pi's hand-out (the router check, `links.options`, `handleUpgrade`, `#keep`) is one synchronous step, so a router change
//     never falls between the check and the keep; here it can, and `revoke`'s snapshot would lack the connection kept after
//     it. A hand-out notes the routers delivered before its router check (`admitting`) and the composition order of the router
//     it read; a revoke ends only a connection whose check read an older composition, and a connection kept after deliveries
//     since its check is ended when any composed after the one it read no longer admits it (`recheck`), as Pi's revoke at
//     each delivery would have.
//   - Pi's `once` (Node's EventEmitter) arms its wrapper as it registers it; WebSocket.once builds its listener whole before
//     attaching it, so a close on another goroutine cannot reach it first.

// UpgradeAdmission is what admitted an upgrade: the allowed host or the relay it came through (local: neither), and its
// principal (nil for a public socket).
type UpgradeAdmission struct {
	// Allowed is the allowed host, "" for none.
	Allowed   string
	Relay     *HttpRelay
	Principal data.Principal
}

// LinkOptions is what the links endpoint gives one socket: its parts' limits and its largest frame (1 MiB by default, 0).
type LinkOptions struct {
	Limits     link.PartLimits
	MaxPayload int
}

// SocketFailure is a plugin socket's handler failed: reported, the socket closed (1011).
type SocketFailure struct {
	Pattern string
	Plugin  string
	Error   error
}

// timer is the part of a timer the heartbeat uses.
type timer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

// afterFunc starts a timer calling f after d.
type afterFunc func(d time.Duration, f func()) timer

func realAfter(d time.Duration, f func()) timer { return time.AfterFunc(d, f) }

// upgraded is one upgraded connection, with what admitted it and how to seal its scope.
type upgraded struct {
	socket  *WebSocket
	allowed string
	relay   *HttpRelay
	// handler is the handler that took it (plugin sockets only).
	handler *HttpSocket
	// seal ends its work at once: the link closed, or the handler's scope sealed.
	seal func()
	// checked is the composition order of the router its upgrade's check read: the routers composed after it are the ones that
	// revoke it.
	checked uint64
}

// routed is a router the follower delivered and its number.
type routed struct {
	number uint64
	router *Router
}

// defaultLinkPayload is a link socket's largest frame unless `links.options` says otherwise: 1 MiB.
const defaultLinkPayload = 1024 * 1024

// Upgrades are the upgraded connections of one HTTP component.
type Upgrades struct {
	mu    sync.Mutex
	open  map[*upgraded]struct{}
	after afterFunc
	// closing is set by Close; pending counts the upgrades reserved and not yet kept. Reserving and Close's snapshot are one
	// critical section, so an upgrade is either kept before Close runs (and gets its 1001) or refused: Pi's single thread
	// gives that for free.
	closing bool
	pending int
	drained chan struct{}
	// deliveries counts the routers the follower delivered and history lists the latest of them, in order (down to the oldest an
	// upgrade in flight needs, and the newest): both change in one critical section, on the delivery, before any revoke of it
	// runs. inflight counts the upgrades between `admitting` and `settled` by their seen.
	deliveries uint64
	history    []routed
	inflight   map[uint64]int
}

// delivered counts a router the follower delivered.
func (u *Upgrades) delivered(router *Router) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.deliveries++
	u.history = append(u.history, routed{number: u.deliveries, router: router})
	u.prune()
}

// admitting is the deliveries counted so far: read by an upgrade before its router check, it is the delivery its connection
// is checked against from (a router delivered before the check is no later than the one the check read). The upgrade is in
// flight, and the deliveries from that number on are kept for it, until it calls `settled` with the same number.
func (u *Upgrades) admitting() uint64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.inflight[u.deliveries]++
	return u.deliveries
}

// settled ends an upgrade's time in flight (every `admitting` is settled, whatever the upgrade came to).
func (u *Upgrades) settled(seen uint64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.inflight[seen]--; u.inflight[seen] == 0 {
		delete(u.inflight, seen)
	}
	u.prune()
}

// prune drops the deliveries before the oldest an upgrade in flight started from and before the newest.
func (u *Upgrades) prune() {
	floor := u.deliveries
	for seen := range u.inflight {
		floor = min(floor, seen)
	}
	kept := 0
	for kept < len(u.history) && u.history[kept].number < floor {
		kept++
	}
	u.history = slices.Delete(u.history, 0, kept)
}

// recheck ends connection, kept just now, when a router delivered since its upgrade's check, and composed after the one the check
// read, no longer admits it: the revoke of that delivery may have taken its snapshot before the keep. Each one is judged, as Pi's
// revoke at each delivery was: a host removed and restored meanwhile ends the connection at its removal. A delivery composed
// before the router the check read ran, in Pi, before the check, and judges nothing (the check can have composed its router
// on read, ahead of its delivery, which then lists it later or never).
func (u *Upgrades) recheck(connection *upgraded) {
	u.mu.Lock()
	var since []*Router
	for _, each := range u.history {
		if each.router.order > connection.checked {
			since = append(since, each.router)
		}
	}
	u.mu.Unlock()
	if slices.ContainsFunc(since, connection.gone) {
		end(connection)
		return
	}
}

// NewUpgrades is an empty set of connections.
func NewUpgrades() *Upgrades {
	return &Upgrades{open: map[*upgraded]struct{}{}, inflight: map[uint64]int{}, after: realAfter}
}

// Link upgrades request to the links endpoint: `host.accept` over the socket (its output capped) as the admitted principal,
// with options' limits; ctx is the parent of the work on the link. checked is the router the check read.
func (u *Upgrades) Link(w nethttp.ResponseWriter, request *nethttp.Request, admission UpgradeAdmission, options LinkOptions, h host.Host, ctx context.Context, checked *Router) {
	maxPayload := options.MaxPayload
	if maxPayload == 0 {
		maxPayload = defaultLinkPayload
	}
	upgrade(w, request, maxPayload, func(socket *WebSocket) {
		end := link.WebSocketLink(limitedSocket(socket))
		connection := &upgraded{socket: socket, allowed: admission.Allowed, relay: admission.Relay, seal: end.Close, checked: checked.order}
		u.keep(connection)
		if err := h.Accept(end, host.LinkOptions{Principal: admission.Principal, Limits: options.Limits}, ctx); err != nil {
			socket.Terminate()
		}
		socket.Start()
		u.recheck(connection)
	})
}

// Socket upgrades request to the plugin socket match: its handler gets the socket and request in a `host.root` scope acting
// as the admitted principal that lasts until the socket closes; a handler that fails is reported and closes it (1011). checked is
// the router the check read.
func (u *Upgrades) Socket(w nethttp.ResponseWriter, r *nethttp.Request, admission UpgradeAdmission, match *SocketMatch, request HttpRequest, h host.Host, ctx context.Context, failed func(SocketFailure), checked *Router) {
	upgrade(w, r, match.MaxPayload, func(socket *WebSocket) {
		closedCtx, closeSocket := chordctx.WithCancel(chordctx.Background())
		seal := func() { closeSocket(errors.New("http: the socket closed")) }
		socket.OnceClose(func(CloseEvent) { seal() })
		connection := &upgraded{socket: socket, allowed: admission.Allowed, relay: admission.Relay, handler: match.Handler, seal: seal, checked: checked.order}
		u.keep(connection)
		u.heartbeat(socket)
		scope := chordctx.WithAbortSignal(chordctx.WithoutAbortSignal(ctx), closedCtx)
		run := func(callCtx host.Ctx) (any, error) {
			// Reading starts before the handler: its pongs and the peer's close are served while it runs; the messages wait.
			socket.hold()
			socket.Start()
			err := tryHandler(match.Handler, socket, request, callCtx)
			if err != nil {
				failed(SocketFailure{Pattern: match.Pattern, Plugin: match.Plugin, Error: err})
				socket.Close(1011, "internal error")
				return nil, nil
			}
			socket.release()
			<-closedCtx.Done()
			return nil, nil
		}
		// The scope sealed (its socket closed, the host closing) is no failure of the handler.
		go func() { _, _ = h.Root(run, scope, host.RootOptions{Principal: admission.Principal, Anonymous: admission.Principal == nil}) }()
		u.recheck(connection)
	})
}

func tryHandler(handler *HttpSocket, socket *WebSocket, request HttpRequest, ctx host.Ctx) (err error) {
	defer recoverAs(&err)
	return handler.Handle(socket, request, ctx)
}

// revoke: the router changed: connections through a host or a relay no longer there, and plugin sockets whose handler left,
// are terminated at once and their scopes sealed (no close handshake a peer could ignore). Only a connection whose upgrade
// read an older composition than router is revoked by it: in Pi the revoke runs at the delivery, so a connection admitted by
// a later router is never ended by an earlier one that a lagging revoke task reaches now.
func (u *Upgrades) revoke(router *Router) {
	for _, each := range u.snapshot() {
		if router.order > each.checked && each.gone(router) {
			end(each)
		}
	}
}

// gone is whether what admitted the connection (its allowed host, its relay, its socket handler) left router.
func (connection *upgraded) gone(router *Router) bool {
	relayLeft := false
	if connection.relay != nil {
		relayLeft = !slices.Contains(router.Relays, connection.relay)
	}
	gone := (connection.allowed != "" && !router.Hosts.Has(connection.allowed)) || relayLeft
	if connection.handler != nil {
		if _, ok := router.SocketHandlers[connection.handler]; !ok {
			gone = true
		}
	}
	return gone
}

// reserve admits an upgrade that is about to be handed out, false once Close ran: it is then refused (503). Every successful
// reserve is released after the upgrade's socket was kept (or the handshake failed).
func (u *Upgrades) reserve() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closing {
		return false
	}
	u.pending++
	return true
}

// release ends a reservation.
func (u *Upgrades) release() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending--
	if u.closing && u.pending == 0 && u.drained != nil {
		close(u.drained)
		u.drained = nil
	}
}

// Close closes every connection (1001, "going away"); those still open when deadline is closed are terminated. Upgrades
// reserved before it are waited for (a handshake takes no time) and closed with the rest; later ones are refused.
func (u *Upgrades) Close(deadline <-chan struct{}) {
	u.mu.Lock()
	u.closing = true
	var drained <-chan struct{}
	if u.pending > 0 {
		u.drained = make(chan struct{})
		drained = u.drained
	}
	u.mu.Unlock()
	if drained != nil {
		select {
		case <-drained:
		case <-deadline:
		}
	}
	open := u.snapshot()
	var closed sync.WaitGroup
	for _, each := range open {
		socket := each.socket
		closed.Add(1)
		var once sync.Once
		done := func(any) { once.Do(closed.Done) }
		if socket.ReadyState() == WebSocketClosedState {
			done(nil)
			continue
		}
		socket.OnceClose(func(CloseEvent) { done(nil) })
		// Closed between the check and the listener.
		if socket.ReadyState() == WebSocketClosedState {
			done(nil)
		}
	}
	for _, each := range open {
		each.socket.Close(1001, "going away")
	}
	all := make(chan struct{})
	go func() {
		closed.Wait()
		close(all)
	}()
	select {
	case <-all:
	case <-deadline:
	}
	for _, each := range u.snapshot() {
		end(each)
	}
}

func (u *Upgrades) snapshot() []*upgraded {
	u.mu.Lock()
	defer u.mu.Unlock()
	open := make([]*upgraded, 0, len(u.open))
	for each := range u.open {
		open = append(open, each)
	}
	return open
}

// keep keeps connection until its socket closes; an error (an oversized frame) terminates only that socket.
func (u *Upgrades) keep(connection *upgraded) {
	socket := connection.socket
	socket.OnError(func(error) { socket.Terminate() })
	u.mu.Lock()
	u.open[connection] = struct{}{}
	u.mu.Unlock()
	socket.OnceClose(func(CloseEvent) {
		u.mu.Lock()
		delete(u.open, connection)
		u.mu.Unlock()
	})
}

// upgrade completes the upgrade with a gorilla upgrader of its own (no compression), dropped with the upgrade: nothing is
// kept per frame size. It starts reading once then returned.
func upgrade(w nethttp.ResponseWriter, request *nethttp.Request, maxPayload int, then func(socket *WebSocket)) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*nethttp.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, request, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(int64(maxPayload))
	socket := newWebSocket(conn)
	then(socket)
}

// end terminates connection and seals its scope at once.
func end(connection *upgraded) {
	// Terminated first: a link's close would send a close frame.
	connection.socket.Terminate()
	connection.seal()
}

// silence is the silence after which a plugin socket is pinged, and after the ping terminated: 15 s each (the link
// heartbeat's).
const silence = 15 * time.Second

// heartbeat keeps a plugin socket honest (decision "plugin sockets get a heartbeat"): after 15 s without a frame from the
// peer it is pinged, after 15 s more terminated; browsers and gorilla answer pings by themselves.
func (u *Upgrades) heartbeat(socket *WebSocket) {
	var mu sync.Mutex
	pinged := false
	var timer timer
	mu.Lock()
	timer = u.after(silence, func() {
		mu.Lock()
		if pinged {
			mu.Unlock()
			socket.Terminate()
			return
		}
		pinged = true
		timer.Reset(silence)
		mu.Unlock()
		socket.Ping()
	})
	mu.Unlock()
	heard := func(any) {
		mu.Lock()
		pinged = false
		timer.Reset(silence)
		mu.Unlock()
	}
	// Any frame the peer sent is a sign of life, also a message a running handler holds back.
	socket.on(webSocketFrame, heard)
	socket.on(webSocketPong, heard)
	socket.on(webSocketPing, heard)
	socket.OnceClose(func(CloseEvent) {
		mu.Lock()
		timer.Stop()
		mu.Unlock()
	})
}

// maxUnsentBytes is the unsent data beyond the frame being sent at which a link socket is cut off: a client that stopped
// reading.
const maxUnsentBytes = 4 * 1024 * 1024

// limitable is what limitedSocket uses of a socket.
type limitable interface {
	ReadyState() int
	BufferedAmount() int64
	Send(data []byte, binary bool, done func(error))
	Terminate()
	Close(code int, reason string)
	on(event link.WebSocketEvent, fn func(data any)) (remove func())
}

type sentFrame struct{ bytes int }

// limited is a socket as the link sees it, its output capped.
type limited struct {
	socket limitable
	mu     sync.Mutex
	unsent map[*sentFrame]struct{}
}

// limitedSocket is socket as the link sees it, its output capped (p2's `limitedSocket`): before each send, what is still
// buffered beyond the largest frame being sent must stay under 4 MiB, or the socket is terminated (the client reconnects from
// snapshots); Chord's flow control keeps a reading client far below it.
func limitedSocket(socket limitable) link.WebSocketLike {
	return &limited{socket: socket, unsent: map[*sentFrame]struct{}{}}
}

func (l *limited) ReadyState() int { return l.socket.ReadyState() }

func (l *limited) SetBinaryType(string) {}

func (l *limited) Send(data []byte) error {
	if l.socket.ReadyState() != WebSocketOpenState {
		return nil
	}
	l.mu.Lock()
	sending := 0
	for frame := range l.unsent {
		if frame.bytes > sending {
			sending = frame.bytes
		}
	}
	l.mu.Unlock()
	if l.socket.BufferedAmount()-int64(sending) > maxUnsentBytes {
		l.socket.Terminate()
		return nil
	}
	// The largest server frame header is 10 bytes.
	frame := &sentFrame{bytes: len(data) + 10}
	l.mu.Lock()
	l.unsent[frame] = struct{}{}
	l.mu.Unlock()
	l.socket.Send(data, true, func(err error) {
		l.mu.Lock()
		delete(l.unsent, frame)
		l.mu.Unlock()
		if err != nil {
			l.socket.Terminate()
		}
	})
	return nil
}

func (l *limited) Close() { l.socket.Close(0, "") }

// AddEventListener is the link's view of the socket's events: a message is the frame's bytes for a binary one and its text
// for a text one.
func (l *limited) AddEventListener(event link.WebSocketEvent, listener func(data any)) (remove func()) {
	if event != link.WebSocketMessage {
		return l.socket.on(event, listener)
	}
	return l.socket.on(event, func(data any) {
		message, ok := data.(Message)
		switch {
		case !ok:
			listener(data)
		case message.Binary:
			listener(message.Data)
		default:
			listener(message.Text())
		}
	})
}
