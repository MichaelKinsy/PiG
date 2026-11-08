package host

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/link/websocket.test.ts › "WebSocket links over a loopback ws server" (the cases that need
// createHost; the fake-socket cases are in the link package). `ws` is gorilla/websocket with exSocket (example 13) as
// its WebSocketLike; `maxPayload` is the server connection's read limit.

// wsLoopback is Pi's `loopback()`: a server host serving Notes over a WebSocket server, each connection accepted as the owner.
func wsLoopback(t *testing.T, maxPayload int64) (server Host, url string) {
	t.Helper()
	server = newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal()})
	mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		_, err := serveNotes(h, nil)
		return err
	})}}, background))
	upgrader := websocket.Upgrader{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if maxPayload > 0 {
			conn.SetReadLimit(maxPayload)
		}
		socket := newExSocket(conn)
		if err := server.Accept(link.WebSocketLink(socket), LinkOptions{Principal: ownerPrincipal()}, background); err != nil {
			return
		}
		socket.start()
	}))
	t.Cleanup(httpServer.Close)
	return server, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

// connectingSocket is an exSocket whose ReadyState is 0 (connecting) until open, which then emits the `open` event.
type connectingSocket struct {
	*exSocket
	mu     sync.Mutex
	isOpen bool
	sent   int
}

func (s *connectingSocket) ReadyState() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isOpen {
		return 0
	}
	return s.exSocket.ReadyState()
}

func (s *connectingSocket) Send(payload []byte) error {
	s.mu.Lock()
	s.sent++
	s.mu.Unlock()
	return s.exSocket.Send(payload)
}

func (s *connectingSocket) sentFrames() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent
}

func (s *connectingSocket) open() {
	s.mu.Lock()
	s.isOpen = true
	s.mu.Unlock()
	s.emit(link.WebSocketOpen, nil)
}

func TestWebSocketLinksOverALoopbackServer(t *testing.T) {
	t.Run("carry a live reference both ways; the client's hello waits for open", func(t *testing.T) {
		_, url := wsLoopback(t, 0)
		conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
		must(t, err)
		must(t, resp.Body.Close())
		// The socket reports connecting until its open event: the client's hello is held back, not sent into a
		// socket that is not open.
		socket := &connectingSocket{exSocket: newExSocket(conn)}
		tab := newHost(t, HostOptions{})
		mustLoad(t)(tab.Load(nil, background))
		must(t, tab.Connect(link.WebSocketLink(socket), LinkOptions{Principal: serverPrincipal()}, background))
		socket.start()
		if sent := socket.sentFrames(); sent != 0 {
			t.Fatalf("%d frames sent before open", sent)
		}
		socket.open()
		notes, err := tab.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		if socket.sentFrames() == 0 {
			t.Fatal("the hello was not sent after open")
		}
		added, err := notes.Call("add", jsonObject("text", "over ws"), background)
		must(t, err)
		if added != float64(1) {
			t.Fatalf("add = %v, want 1", added)
		}
		rhWaitFor(t, func() bool {
			value, err := notes.Member("notes").Value()
			return err == nil && exText(value) == `[{"text":"over ws"}]`
		})
		must(t, tab.Close(background))
	})

	t.Run("close the connection on a frame above maxPayload (1009): references go loading (race 28)", func(t *testing.T) {
		_, url := wsLoopback(t, 4096)
		conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
		must(t, err)
		must(t, resp.Body.Close())
		code := make(chan int, 1)
		conn.SetCloseHandler(func(c int, _ string) error { code <- c; return nil })
		socket := newExSocket(conn)
		tab := newHost(t, HostOptions{})
		mustLoad(t)(tab.Load(nil, background))
		must(t, tab.Connect(link.WebSocketLink(socket), LinkOptions{Principal: serverPrincipal()}, background))
		socket.start()
		notes, err := tab.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		_, callErr := notes.Call("echo", jsonObject("data", make([]byte, 10_000)), background)
		select {
		case got := <-code:
			if got != websocket.CloseMessageTooBig {
				t.Fatalf("close code = %d, want 1009", got)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the connection was not closed")
		}
		requireChordError(t, callErr, data.CodeUnavailable, "")
		rhWaitFor(t, func() bool { return refStateKind(notes) == RefStateLoading })
		must(t, tab.Close(background))
	})
}
