package http

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	nethttp "net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Ports packages/chord/src/node/http/node.ts. Chord's own listener (decision "The HTTP server is a native object"; "The
// HTTP core is a fetch handler; Chord's own Node server is the easy path"): the listener's native, a net/http server that
// hands each request to the component as it reads requests (origin-form targets only), and writes its answer: whole, or
// pulled piece by piece as the socket drains. A body the route left unread is drained after the response when small, else
// the connection closes; the request's scope ends once the response is flushed or abandoned.
//
// pig divergence (D218): Go's net/http answers some malformed requests before the component does (see D218).
// Forced Go differences (Node's `http` module is Go's net/http):
//   - Node's limits stay its defaults on the listener, in Go's settings: `headersTimeout` 60 s is ReadHeaderTimeout,
//     `requestTimeout` 300 s ReadTimeout, 16 KiB of headers (431) MaxHeaderBytes less the request line's allowance Go adds,
//     keep-alive 5 s IdleTimeout.
//   - net/http answers some malformed requests itself, before any handler runs and with no default security headers: two
//     `Host` headers are 400 (net/http request.go "too many Host headers"; Node's parser keeps them and the component
//     answers 403), as is a `Host` with userinfo (server.go "malformed Host header": `@` is no host byte; Node's component
//     answers 403) and a malformed `Host`; `Fetch` and `NodeRequestOf` still refuse them as Pi does for a server that hands
//     the component the headers it read. No `http.Server` option reaches these checks.
//   - A Web `Request` is made only when something asks for it in Pi; Go's request exists already (incoming.go).
//   - The reason phrase of the status line is Go's (the answers' own text is Node's table, statuscodes.go); header names are
//     written in canonical case; an HTTP/1.0 request's answer has an `HTTP/1.0` status line (Node writes `HTTP/1.1`); an
//     upgrade's refusal also carries a `Date` header.
//   - net/http starts watching the connection for a disconnect only once the request's body is read (or at once without
//     one): a client that goes away while its body is unread is seen when the route reads or writes, where Node sees the
//     socket close at any time. The scope's abort reason is Pi's (`http: the client disconnected`), and a request context
//     ending when the handler returns is no disconnect.
//   - A small unread body is drained by net/http after the handler returns (up to 256 KiB); the component decides only
//     whether the connection closes (a declared length above 64 KiB, or chunked).
//   - `Upgrade` requests are handled by the component's Upgrade (Node's `upgrade` event) on the same connection.
//   - `closeIdleConnections` and `closeAllConnections` are the connections net/http reports idle and open.

// ListenAddress is where a listener listens.
type ListenAddress struct {
	Host string
	Port int
}

// Served is an answer and what its consumer calls once the body is written or abandoned (the request's scope then ends).
type Served struct {
	Answer   Answer
	Finished func()
}

// Handler is what the listener needs of the component.
type Handler interface {
	// Serve serves a request: it never fails.
	Serve(incoming Incoming, connection Connection) Served
	// Refusal is the answer for a request refused before it could be made one (default headers only).
	Refusal(status int) Answer
	// Upgrade handles a request asking for an upgrade (Node's `upgrade` event).
	Upgrade(w nethttp.ResponseWriter, request *nethttp.Request)
}

// NodeListener is the listener's native value: a listening server.
type NodeListener struct {
	Address  ListenAddress
	server   *nethttp.Server
	listener net.Listener
	served   chan struct{}
	mu       sync.Mutex
	idle     map[net.Conn]struct{}
}

// ListenNode is a server for handler listening at listen; it returns the listen error (a port in use).
func ListenNode(handler Handler, listen ListenAddress, ctx context.Context) (*NodeListener, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort(listen.Host, strconv.Itoa(listen.Port)))
	if err != nil {
		return nil, err
	}
	l := &NodeListener{listener: listener, served: make(chan struct{}), idle: map[net.Conn]struct{}{}}
	l.server = &nethttp.Server{
		Handler: nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) { serveNode(handler, w, r) }),
		// Node's defaults: headersTimeout 60 s, requestTimeout 300 s, 16 KiB of headers, keep-alive 5 s. net/http reads
		// 4096 bytes more than MaxHeaderBytes for the request line.
		ReadHeaderTimeout: 60 * time.Second,
		ReadTimeout:       300 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    16*1024 - 4096,
		// `OPTIONS *` reaches the handler: an asterisk-form target is refused with the other non-origin-form ones.
		DisableGeneralOptionsHandler: true,
		ConnState: func(conn net.Conn, state nethttp.ConnState) {
			l.mu.Lock()
			defer l.mu.Unlock()
			if state == nethttp.StateIdle {
				l.idle[conn] = struct{}{}
			} else {
				delete(l.idle, conn)
			}
		},
	}
	go func() {
		defer close(l.served)
		_ = l.server.Serve(listener)
	}()
	if ctx.Err() != nil {
		_ = l.server.Close()
		<-l.served
		return nil, context.Cause(ctx)
	}
	port := listen.Port
	if address, ok := listener.Addr().(*net.TCPAddr); ok {
		port = address.Port
	}
	l.Address = ListenAddress{Host: listen.Host, Port: port}
	return l, nil
}

// Stop stops accepting connections and closes the idle ones (the component's close runs next).
func (l *NodeListener) Stop() {
	_ = l.listener.Close()
	l.mu.Lock()
	idle := make([]net.Conn, 0, len(l.idle))
	for conn := range l.idle {
		idle = append(idle, conn)
	}
	l.mu.Unlock()
	for _, conn := range idle {
		_ = conn.Close()
	}
}

// Closed closes every connection still open and returns once the server closed.
func (l *NodeListener) Closed() {
	_ = l.server.Close()
	<-l.served
}

// serveNode is the server's handler: an upgrade, or one request served and its answer written.
func serveNode(handler Handler, w nethttp.ResponseWriter, r *nethttp.Request) {
	if isUpgrade(r) {
		handler.Upgrade(w, r)
		return
	}
	incoming, refusal := nodeIncomingOf(r)
	if refusal != nil {
		write(r, w, Served{Answer: handler.Refusal(int(refusal.Status)), Finished: func() {}})
		return
	}
	if r.Body != nil && r.Body != nethttp.NoBody {
		r.Body = &trackedBody{ReadCloser: r.Body}
	}
	served := handler.Serve(incoming, connectionOf(r))
	finished := served.Finished
	served.Finished = func() {
		incoming.finished.Store(true)
		finished()
	}
	write(r, w, served)
}

// isUpgrade is whether r asks for an upgrade (Node emits `upgrade` for a request with an `Upgrade` header and the
// `upgrade` connection token).
func isUpgrade(r *nethttp.Request) bool {
	if r.Header.Get("Upgrade") == "" {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for token := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

// nodeIncoming is a request as the component reads it on net/http's server: its URL parsed once.
type nodeIncoming struct {
	webIncoming
	// finished is set once its response was written: the request's context ending after that is no disconnect.
	finished atomic.Bool
}

// bodyless is whether the request has no body as the component reads it: Pi's Web `Request` for a GET or HEAD has none.
func (n *nodeIncoming) bodyless() bool {
	return n.request.Method == nethttp.MethodGet || n.request.Method == nethttp.MethodHead
}

// Cancelled calls abort when the client goes away before its answer is written (at once when it has).
func (n *nodeIncoming) Cancelled(abort func(reason error)) {
	gone := func() {
		if !n.finished.Load() {
			abort(errors.New("http: the client disconnected"))
		}
	}
	ctx := n.request.Context()
	if ctx.Err() != nil {
		gone()
		return
	}
	context.AfterFunc(ctx, gone)
}

// WireStatus is the status that refuses a request at the wire: Pi's closed union `400 | 403 | 501`.
type WireStatus int

const (
	// WireBadRequest is a target that does not parse (or a malformed `Host`).
	WireBadRequest WireStatus = 400
	// WireForbidden is an absolute-form target or a `Host` with userinfo.
	WireForbidden WireStatus = 403
	// WireNotImplemented is a method a Web `Request` cannot carry (`CONNECT`, `TRACE`, `TRACK`).
	WireNotImplemented WireStatus = 501
)

// WireRefusal is what refuses a request at the wire, with its status (400, 403 or 501).
type WireRefusal struct{ Status WireStatus }

// nodeIncomingOf is request as the component reads it, or what refuses it at the wire (the listener and `NodeRequest`
// alike): an absolute-form target 403, a target that does not parse 400, a `Host` with userinfo 403 (never a loopback or
// allowed name), a method a Web `Request` cannot carry (`CONNECT`, `TRACE`, `TRACK`) 501: served for no resource.
func nodeIncomingOf(r *nethttp.Request) (*nodeIncoming, *WireRefusal) {
	target := r.RequestURI
	// Only origin-form targets: an absolute-form one names an authority of its own, `*` none.
	if !strings.HasPrefix(target, "/") {
		return nil, &WireRefusal{Status: WireForbidden}
	}
	if UNCARRIED[r.Method] {
		return nil, &WireRefusal{Status: WireNotImplemented}
	}
	// The authority is the `Host` the client sent; admission checks it, so a strange one parses but is refused.
	host, ok := requestHeader(r, "host")
	if !ok {
		host = "invalid"
	}
	href, status := parseWireURL(host, target)
	if status != 0 {
		return nil, &WireRefusal{Status: WireStatus(status)}
	}
	return &nodeIncoming{request: r, url: href}, nil
}

// NodeRequestKind tells what a NodeRequest is.
type NodeRequestKind string

const (
	// NodeRequestRequest is a request to give `Fetch` (Pi's `"request"`).
	NodeRequestRequest NodeRequestKind = "request"
	// NodeRequestRefused is a request refused at the wire (Pi's `"refused"`, a WireRefusal).
	NodeRequestRefused NodeRequestKind = "refused"
)

// NodeRequest is a request as an app mounting `Fetch` on its own server gets it: a request, or refused. Pi's union of
// `{ kind: "request"; request; connection }` and `WireRefusal` (`{ kind: "refused"; status }`) is one struct tagged by Kind:
// Request and Connection are set for a request, Status for a refusal.
type NodeRequest struct {
	Kind NodeRequestKind
	// Request is the request to give `Fetch`, with Connection.
	Request    *nethttp.Request
	Connection Connection
	// Status is why the request is refused at the wire.
	Status WireStatus
}

// NodeRequestOf is Pi's `nodeRequest` (a Go function cannot share its name with the type NodeRequest): it converts r for `Fetch` on an app's own server, the whole conversion being the wire boundary: what the
// listener refuses is refused with the same status (nodeIncomingOf), never a panic. The request's `Host` is in its headers
// and its URL is the serialized target.
func NodeRequestOf(r *nethttp.Request) NodeRequest {
	incoming, refusal := nodeIncomingOf(r)
	if refusal != nil {
		return NodeRequest{Kind: NodeRequestRefused, Status: refusal.Status}
	}
	converted := r.Clone(r.Context())
	converted.Header = requestHeaders(r)
	// Pi's Web `Request` has no body for a GET or HEAD.
	if converted.Method == nethttp.MethodGet || converted.Method == nethttp.MethodHead {
		converted.Body = nethttp.NoBody
	}
	if parsed, err := neturl.Parse(incoming.url); err == nil {
		converted.URL = parsed
	}
	return NodeRequest{Kind: NodeRequestRequest, Request: converted, Connection: connectionOf(r)}
}

// connectionOf is what a connection knows of itself.
func connectionOf(r *nethttp.Request) Connection {
	var connection Connection
	if local, ok := r.Context().Value(nethttp.LocalAddrContextKey).(net.Addr); ok {
		if tcp, ok := local.(*net.TCPAddr); ok {
			connection.LocalAddress = tcp.IP.String()
			connection.LocalPort = tcp.Port
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		connection.RemoteAddress = host
	}
	return connection
}

// drainLimit is what `JSON` and `Body` leave unread that is drained after the response, keeping the connection: 64 KiB.
const drainLimit = 64 * 1024

// write writes served's answer to w; a body left unread longer than the drain limit closes the connection.
func write(r *nethttp.Request, w nethttp.ResponseWriter, served Served) {
	defer served.Finished()
	// A client gone before its answer: nothing to write.
	if r.Context().Err() != nil {
		return
	}
	answer := served.Answer
	closing := leftUnread(r)
	header := w.Header()
	for _, line := range answer.Headers.Lines() {
		header.Add(line.Name, latin1(line.Value))
	}
	if closing {
		header.Set("Connection", "close")
	} else if !r.Close && r.ProtoAtLeast(1, 1) {
		header.Set("Connection", "keep-alive")
		header.Set("Keep-Alive", "timeout=5")
	}
	w.WriteHeader(answer.Status)
	if answer.Stream != nil {
		pump(answer.Stream, w)
	} else if answer.Text != "" {
		_, _ = io.WriteString(w, answer.Text)
	}
	// The response is on the wire when `Finished` runs (Pi: `res` is flushed): net/http buffers the head and a small body until
	// the handler returns, and a `close` that waited for `Finished` must not be followed by a connection that cuts them.
	_ = nethttp.NewResponseController(w).Flush()
}

// leftUnread is whether the body the route left unread (more than the drain limit declared, or chunked) closes the
// connection.
func leftUnread(r *nethttp.Request) bool {
	if r.ContentLength == 0 {
		return false
	}
	if tracked, ok := r.Body.(*trackedBody); ok && tracked.complete() {
		return false
	}
	declared := math.Inf(1)
	if r.ContentLength > 0 {
		declared = float64(r.ContentLength)
	}
	return declared > drainLimit
}

// trackedBody is a request body that knows whether it was read to its end.
type trackedBody struct {
	io.ReadCloser
	mu  sync.Mutex
	eof bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.mu.Lock()
		b.eof = true
		b.mu.Unlock()
	}
	return n, err
}

func (b *trackedBody) complete() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.eof
}

// pump writes stream to w as the socket drains; a body that fails (reported by the component) destroys the connection.
func pump(stream *PulledBody, w nethttp.ResponseWriter) {
	defer func() { _ = stream.Close() }()
	flusher, _ := w.(nethttp.Flusher)
	buffer := make([]byte, 32*1024)
	for {
		n, err := stream.Read(buffer)
		if n > 0 {
			if _, werr := w.Write(buffer[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			panic(nethttp.ErrAbortHandler)
		}
	}
}

// latin1 is a header value as the wire carries it: each character up to U+00FF as one byte.
func latin1(value string) string {
	ascii := true
	for i := 0; i < len(value); i++ {
		if value[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return value
	}
	out := make([]byte, 0, len(value))
	for _, r := range value {
		out = append(out, byte(r))
	}
	return string(out)
}
