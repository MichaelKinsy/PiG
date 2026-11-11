package main

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// socket is a WebSocket a client host's link runs over: a page has the browser's, a Go client this one, over gorilla/websocket. Frames
// that arrive before the link listens are kept for it.
type socket struct {
	conn *websocket.Conn

	mu        sync.Mutex
	listeners map[link.WebSocketEvent]map[int]func(any)
	next      int
	pending   [][]byte
	closed    bool
	send      sync.Mutex
}

// dial opens the server's links endpoint (`/chord`) with the cookie of a signed-in session.
func dial(ctx context.Context, origin string, cookie *http.Cookie) (*socket, error) {
	header := http.Header{}
	header.Add("Cookie", cookie.String())
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(origin, "http")+"/chord", header)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	s := &socket{conn: conn, listeners: map[link.WebSocketEvent]map[int]func(any){}}
	go s.read()
	return s, nil
}

func (s *socket) read() {
	for {
		kind, frame, err := s.conn.ReadMessage()
		if err != nil {
			s.mu.Lock()
			s.closed = true
			listeners := s.listeners[link.WebSocketClose]
			s.mu.Unlock()
			for _, listener := range listeners {
				listener(nil)
			}
			return
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		s.mu.Lock()
		listeners := s.listeners[link.WebSocketMessage]
		if len(listeners) == 0 {
			s.pending = append(s.pending, frame)
			s.mu.Unlock()
			continue
		}
		s.mu.Unlock()
		for _, listener := range listeners {
			listener(frame)
		}
	}
}

func (s *socket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 3
	}
	return 1
}

func (s *socket) SetBinaryType(string) {}

func (s *socket) Send(data []byte) error {
	s.send.Lock()
	defer s.send.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *socket) Close() { _ = s.conn.Close() }

func (s *socket) AddEventListener(event link.WebSocketEvent, listener func(any)) (remove func()) {
	s.mu.Lock()
	id := s.next
	s.next++
	if s.listeners[event] == nil {
		s.listeners[event] = map[int]func(any){}
	}
	s.listeners[event][id] = listener
	var queued [][]byte
	if event == link.WebSocketMessage {
		queued, s.pending = s.pending, nil
	}
	s.mu.Unlock()
	for _, frame := range queued {
		listener(frame)
	}
	return func() {
		s.mu.Lock()
		delete(s.listeners[event], id)
		s.mu.Unlock()
	}
}
