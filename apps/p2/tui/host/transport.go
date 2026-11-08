package host

// How the launcher reaches its server: HTTP requests and the chord WebSocket, directly with a cookie or through Radius (radius.go). Credentials stay
// inside a transport; callers name only paths on the server.
//
// pi: apps/p2/src/hosts/tui-transport.ts

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// MaxLinkFrame bounds Chord link frames and every queued message.
const MaxLinkFrame = 4 * 1024 * 1024

// Request is one HTTP request to a path of the server.
type Request struct {
	Method  string
	Headers map[string]string
	Body    io.Reader
}

// SocketEvents are the callbacks of one Socket; they run on the socket's reader goroutine.
type SocketEvents struct {
	Open    func()
	Message func(data []byte, binary bool)
	// Refused reports the HTTP status the server refused the WebSocket with; Close follows.
	Refused func(status int)
	Close   func()
}

// Socket is one WebSocket to the server.
type Socket interface {
	Open() bool
	Send(data []byte) error
	Terminate()
}

// Transport is the launcher's link to one server. Redirects are refused.
type Transport interface {
	Request(ctx context.Context, path string, request Request) (*http.Response, error)
	Socket(path string, events SocketEvents) Socket
}

type cookieTransport struct {
	base    *url.URL
	headers http.Header
}

// CookieTransport sends direct requests to the origin of target with the launcher's exact Origin and its cookie, if any.
func CookieTransport(target *url.URL, credentials Credentials) Transport {
	return &cookieTransport{base: target, headers: ConnectionHeaders(target, credentials)}
}

func (t *cookieTransport) Request(ctx context.Context, path string, request Request) (*http.Response, error) {
	next, err := t.base.Parse(path)
	if err != nil {
		return nil, err
	}
	method := request.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, next.String(), request.Body)
	if err != nil {
		return nil, err
	}
	for name, values := range t.headers {
		req.Header[name] = values
	}
	for name, value := range request.Headers {
		req.Header.Set(name, value)
	}
	// No client timeout: bodies stream; callers bound them with ctx.
	return noRedirects.Do(req)
}

var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

type webSocket struct {
	conn   *websocket.Conn
	open   atomic.Bool
	writeM sync.Mutex
}

func (s *webSocket) Open() bool { return s.open.Load() }

func (s *webSocket) Send(data []byte) error {
	s.writeM.Lock()
	defer s.writeM.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *webSocket) Terminate() {
	s.open.Store(false)
	_ = s.conn.Close()
}

// pendingSocket is a Socket whose dial has not finished.
type pendingSocket struct {
	mu        sync.Mutex
	socket    *webSocket
	cancelled bool
}

func (p *pendingSocket) Open() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.socket != nil && p.socket.Open()
}

func (p *pendingSocket) Send(data []byte) error {
	p.mu.Lock()
	socket := p.socket
	p.mu.Unlock()
	if socket == nil {
		return io.ErrClosedPipe
	}
	return socket.Send(data)
}

func (p *pendingSocket) Terminate() {
	p.mu.Lock()
	p.cancelled = true
	socket := p.socket
	p.mu.Unlock()
	if socket != nil {
		socket.Terminate()
	}
}

func (t *cookieTransport) Socket(path string, events SocketEvents) Socket {
	pending := &pendingSocket{}
	next, err := t.base.Parse(path)
	if err != nil {
		go events.Close()
		return pending
	}
	if next.Scheme == "https" {
		next.Scheme = "wss"
	} else {
		next.Scheme = "ws"
	}
	go func() {
		dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
		conn, response, err := dialer.Dial(next.String(), t.headers)
		if err != nil {
			if response != nil {
				_ = response.Body.Close()
				if events.Refused != nil {
					events.Refused(response.StatusCode)
				}
			}
			events.Close()
			return
		}
		conn.SetReadLimit(MaxLinkFrame)
		socket := &webSocket{conn: conn}
		socket.open.Store(true)
		pending.mu.Lock()
		pending.socket = socket
		cancelled := pending.cancelled
		pending.mu.Unlock()
		if cancelled {
			socket.Terminate()
			events.Close()
			return
		}
		events.Open()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				break
			}
			events.Message(data, kind == websocket.BinaryMessage)
		}
		socket.open.Store(false)
		_ = conn.Close()
		events.Close()
	}()
	return pending
}
