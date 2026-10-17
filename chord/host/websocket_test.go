package host_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/host"
)

// fakeWebSocketServer is a WebSocketServerLike that hands each connect call to the "connection" listeners.
type fakeWebSocketServer struct {
	mu        sync.Mutex
	listeners map[string][]func(host.WebSocketLike, string)
}

func (s *fakeWebSocketServer) On(event string, listener func(socket host.WebSocketLike, request string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listeners == nil {
		s.listeners = map[string][]func(host.WebSocketLike, string){}
	}
	s.listeners[event] = append(s.listeners[event], listener)
}

func (s *fakeWebSocketServer) connect(socket host.WebSocketLike, request string) {
	s.mu.Lock()
	listeners := append([]func(host.WebSocketLike, string){}, s.listeners["connection"]...)
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(socket, request)
	}
}

// fakeSocket is an open WebSocketLike that records what is sent and how it is closed.
type fakeSocket struct {
	mu     sync.Mutex
	state  int
	sent   []string
	closes []int
}

func (s *fakeSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *fakeSocket) Send(data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, ok := data.(string)
	if !ok {
		return errors.New("fakeSocket sends text messages only")
	}
	s.sent = append(s.sent, text)
	return nil
}

func (s *fakeSocket) Close(code int, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closes = append(s.closes, code)
	s.state = 3
}

func (s *fakeSocket) AddEventListener(string, func(event host.WebSocketMessage)) {}

func (s *fakeSocket) byeReasons(t *testing.T) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var reasons []string
	for _, data := range s.sent {
		var frame struct {
			T      string `json:"t"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			t.Fatalf("sent %q: %v", data, err)
		}
		if frame.T == "bye" {
			reasons = append(reasons, frame.Reason)
		}
	}
	return reasons
}

// Pi websocket.ts:23-33: acceptWebSockets wraps every connection's socket at once, resolves the authenticator after
// the connection listener returned, and closes the link with "unauthorized" when it yields no principal and with
// "authentication failed" when it rejects; an authenticated client is accepted and its link stays open.
func TestAcceptWebSocketsClosesRefusedAndFailedClientsAfterAuthenticating(t *testing.T) {
	server := &fakeWebSocketServer{}
	var reported []error
	var reportedMu sync.Mutex
	chord := host.CreateHost(host.HostOptions{Self: map[string]any{"kind": "host"}, Name: "server", OnError: func(err error) {
		reportedMu.Lock()
		reported = append(reported, err)
		reportedMu.Unlock()
	}})
	release := make(chan struct{})
	host.AcceptWebSockets[string](server, chord, host.WebSocketAcceptOptions[string]{
		Principal: func(request string) (host.Principal, bool, error) {
			<-release
			switch request {
			case "/?token=ok":
				return map[string]any{"guest": "web"}, true, nil
			case "/?token=bad":
				return nil, false, nil
			default:
				return nil, false, errors.New("token service down")
			}
		},
	})
	sockets := map[string]*fakeSocket{}
	for _, request := range []string{"/?token=ok", "/?token=bad", "/?token=error"} {
		socket := &fakeSocket{state: 1}
		sockets[request] = socket
		// The listener returns while the authenticator is still blocked: Pi settles it asynchronously.
		server.connect(socket, request)
	}
	for request, socket := range sockets {
		if reasons := socket.byeReasons(t); len(reasons) != 0 {
			t.Fatalf("%s closed before authenticating: %v", request, reasons)
		}
	}
	close(release)
	// Close drains the authentication tasks the host owns.
	if err := chord.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing the host may close the accepted link too, so only the first bye of each socket is the authenticator's.
	want := map[string]string{"/?token=bad": "unauthorized", "/?token=error": "authentication failed"}
	for request, socket := range sockets {
		got := socket.byeReasons(t)
		if request == "/?token=ok" {
			if len(got) > 0 && (got[0] == "unauthorized" || got[0] == "authentication failed") {
				t.Fatalf("%s was refused: %v", request, got)
			}
			continue
		}
		if len(got) == 0 || got[0] != want[request] {
			t.Fatalf("%s bye reasons = %v, want first %q", request, got, want[request])
		}
	}
	for _, request := range []string{"/?token=bad", "/?token=error"} {
		socket := sockets[request]
		socket.mu.Lock()
		closes := append([]int(nil), socket.closes...)
		socket.mu.Unlock()
		if len(closes) != 1 || closes[0] != 1000 {
			t.Fatalf("%s socket closes = %v, want [1000]", request, closes)
		}
	}
	reportedMu.Lock()
	defer reportedMu.Unlock()
	if len(reported) != 0 {
		t.Fatalf("reported %v", reported)
	}
}
