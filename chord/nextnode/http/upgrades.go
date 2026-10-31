package http

import (
	"context"
	"errors"
	nethttp "net/http"
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

// UpgradeAdmission is what admitted an upgrade: the allowed host or the relay it came through (local: neither), and its
// principal.
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
}

// defaultLinkPayload is a link socket's largest frame unless `links.options` says otherwise: 1 MiB.
const defaultLinkPayload = 1024 * 1024

// Upgrades are the upgraded connections of one HTTP component.
type Upgrades struct {
	mu    sync.Mutex
	open  map[*upgraded]struct{}
	after afterFunc
}

// NewUpgrades is an empty set of connections.
func NewUpgrades() *Upgrades { return &Upgrades{open: map[*upgraded]struct{}{}, after: realAfter} }

// Link upgrades request to the links endpoint: `host.accept` over the socket (its output capped) as the admitted principal,
// with options' limits; ctx is the parent of the work on the link.
func (u *Upgrades) Link(w nethttp.ResponseWriter, request *nethttp.Request, admission UpgradeAdmission, options LinkOptions, h host.Host, ctx context.Context) {
	maxPayload := options.MaxPayload
	if maxPayload == 0 {
		maxPayload = defaultLinkPayload
	}
	upgrade(w, request, maxPayload, func(socket *WebSocket) {
		end := link.WebSocketLink(limitedSocket(socket))
		u.keep(&upgraded{socket: socket, allowed: admission.Allowed, relay: admission.Relay, seal: end.Close})
		if err := h.Accept(end, host.LinkOptions{Principal: admission.Principal, Limits: options.Limits}, ctx); err != nil {
			socket.Terminate()
		}
		socket.Start()
	})
}

// Socket upgrades request to the plugin socket match: its handler gets the socket and request in a `host.root` scope acting
// as the admitted principal that lasts until the socket closes; a handler that fails is reported and closes it (1011).
func (u *Upgrades) Socket(w nethttp.ResponseWriter, r *nethttp.Request, admission UpgradeAdmission, match *SocketMatch, request HttpRequest, h host.Host, ctx context.Context, failed func(SocketFailure)) {
	upgrade(w, r, match.MaxPayload, func(socket *WebSocket) {
		closedCtx, closeSocket := chordctx.WithCancel(chordctx.Background())
		seal := func() { closeSocket(errors.New("http: the socket closed")) }
		socket.Once(link.WebSocketClose, func(any) { seal() })
		u.keep(&upgraded{socket: socket, allowed: admission.Allowed, relay: admission.Relay, handler: match.Handler, seal: seal})
		u.heartbeat(socket)
		scope := chordctx.WithAbortSignal(chordctx.WithoutAbortSignal(ctx), closedCtx)
		run := func(callCtx host.Ctx) (any, error) {
			err := tryHandler(match.Handler, socket, request, callCtx)
			if err != nil {
				failed(SocketFailure{Pattern: match.Pattern, Plugin: match.Plugin, Error: err})
				socket.Close(1011, "internal error")
				socket.Start()
				return nil, nil
			}
			socket.Start()
			<-closedCtx.Done()
			return nil, nil
		}
		// The scope sealed (its socket closed, the host closing) is no failure of the handler.
		go func() { _, _ = h.Root(run, scope, host.RootOptions{Principal: admission.Principal}) }()
	})
}

func tryHandler(handler *HttpSocket, socket *WebSocket, request HttpRequest, ctx host.Ctx) (err error) {
	defer recoverAs(&err)
	return handler.Handle(socket, request, ctx)
}

// revoke: the router changed: connections through a host or a relay no longer there, and plugin sockets whose handler left,
// are terminated at once and their scopes sealed (no close handshake a peer could ignore).
func (u *Upgrades) revoke(router *Router) {
	for _, each := range u.snapshot() {
		relayLeft := false
		if each.relay != nil {
			relayLeft = true
			for _, relay := range router.Relays {
				if relay == each.relay {
					relayLeft = false
					break
				}
			}
		}
		gone := (each.allowed != "" && !router.Hosts.Has(each.allowed)) || relayLeft
		if each.handler != nil {
			if _, ok := router.SocketHandlers[each.handler]; !ok {
				gone = true
			}
		}
		if gone {
			end(each)
		}
	}
}

// Close closes every connection (1001, "going away"); those still open when deadline is closed are terminated.
func (u *Upgrades) Close(deadline <-chan struct{}) {
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
		socket.Once(link.WebSocketClose, done)
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
	socket.On(link.WebSocketError, func(any) { socket.Terminate() })
	u.mu.Lock()
	u.open[connection] = struct{}{}
	u.mu.Unlock()
	socket.Once(link.WebSocketClose, func(any) {
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
	socket.On(link.WebSocketMessage, heard)
	socket.On(WebSocketPong, heard)
	socket.On(WebSocketPing, heard)
	socket.Once(link.WebSocketClose, func(any) {
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
	On(event link.WebSocketEvent, fn func(data any)) (remove func())
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

func (l *limited) AddEventListener(event link.WebSocketEvent, listener func(data any)) (remove func()) {
	return l.socket.On(event, listener)
}
