package host

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports the tests of main's chord/host/closed_link_registration_test.go (#525) to this host. A Go socket and its link close on
// goroutines of their own, at any point between the link's creation and the host's registration of its close handler. Pi's
// connect() and webSocketLink() run to completion before any socket event, so they never see a link closed while they set it
// up (host.ts:1059 `if (parent.open) this.#parent.connected(parent)` and link/websocket.ts:52 `readyState !== CONNECTING &&
// !== OPEN` give a closed link, both in the pinned v1.5.2). Go has no such guarantee, so the setup reads the state it may
// have missed. These tests build each interleaving instead of racing for it. Connect here is `if parent.open() {
// h.parent.connected(parent) }` (host_host.go Connect), so the first test is a PIN, not a fix; the websocket tests below
// pin the link package's re-read of the socket's state (link.WebSocketLink).

// parentOf is the link the host would route a hold to, nil for none.
func parentOf(host Host) (kept *hostLink) {
	h := host.(*chordHost)
	h.loop().Run(func() { kept = h.parent.link() })
	return kept
}

// A link closed before Connect registered it is no parent: the host keeps none, so the next Connect is taken instead of
// being refused with "connect: this host has an open parent link". chord/host (main's v1 port) kept the connection of such a
// link as the host's upstream, bound every hold to it and never re-held after the next good Connect.
func TestHostConnectOverALinkClosedBeforeKeepsNoParentSoTheNextConnectIsTaken(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		client := newHost(t, HostOptions{OnError: noopOnError})
		defer closeHosts(t, client)
		dead, _ := link.MemoryLinkPair()
		dead.Close()
		must(t, client.Connect(dead, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		if kept := parentOf(client); kept != nil {
			t.Fatalf("parent = %v after Connect over a closed link, want none", kept)
		}
		next, _ := link.MemoryLinkPair()
		must(t, client.Connect(next, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	})
}

// A reference whose link was lost waits for the next Connect to hold again. A Connect over a link that is already closed must
// not take it: the next good Connect holds the reference again over the new link and the reference is live.
func TestHostConnectAfterALinkClosedAtOnceHoldsTheReferencesAgainOverTheNextGoodLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		hosts := linked(t, func(host FacetHost) error {
			_, err := serveNotes(host, nil)
			return err
		}, linkedOptions{})
		notes, err := hosts.Client.Get(background, Notes, jsonObject("session", "S1"), GetOptions{})
		must(t, err)
		if got, err := notes.Call("add", jsonObject("text", "one"), background); err != nil || got != float64(1) {
			t.Fatalf("add = %v, %v; want 1", got, err)
		}

		// The connection drops; the embedder's reconnect gets a link that closed at once.
		hosts.Links[0].Close()
		transported(hosts.Links[0], hosts.Links[1])
		rhParentDown(t, hosts.Client)
		dead, _ := link.MemoryLinkPair()
		dead.Close()
		must(t, hosts.Client.Connect(dead, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		if kept := parentOf(hosts.Client); kept != nil {
			t.Fatalf("parent = %v after Connect over a closed link, want none", kept)
		}
		if kind := rhRefKind(notes); kind == RefStateLive {
			t.Fatalf("the reference is %v over a closed link, want it waiting for the next link", kind)
		}

		a, b := link.MemoryLinkPair()
		must(t, hosts.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		must(t, hosts.Client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		transported(a, b)
		rhWaitFor(t, func() bool { return rhRefKind(notes) == RefStateLive })
		// The object has no linger: the server made it anew, so only the call over the new link is the row.
		if _, err := notes.Call("add", jsonObject("text", "two"), background); err != nil {
			t.Fatalf("add over the next good link = %v, want it held again", err)
		}
	})
}

// peerClosedSocket is an exSocket whose ReadyState turns closed once the peer's close came: exSocket's reads closed only after
// its own Close, and a refusal is the peer's close.
type peerClosedSocket struct {
	*exSocket
	stateMu sync.Mutex
	gone    bool
}

func newPeerClosedSocket(conn *websocket.Conn) *peerClosedSocket {
	socket := &peerClosedSocket{exSocket: newExSocket(conn)}
	// Registered before the link's own, so the state reads closed when the link's handler runs.
	socket.AddEventListener(link.WebSocketClose, func(any) {
		socket.stateMu.Lock()
		socket.gone = true
		socket.stateMu.Unlock()
	})
	return socket
}

func (s *peerClosedSocket) ReadyState() int {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.gone {
		return 3
	}
	return s.exSocket.ReadyState()
}

// refusedOverARealSocket is a client whose server closed the WebSocket at once (a refusal), with the refusal made to come at
// the step `before` names: "socket" is before the link wraps the socket, "link" after it wraps the socket and before the host
// connects it. A browser's events always come after the code that made the socket; a Go socket's close can come between any
// two of its statements. The host's Connect was made over the refused link; the link is returned.
func refusedOverARealSocket(t *testing.T, before string) (client Host, refused link.Link) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	must(t, err)
	must(t, resp.Body.Close())
	socket := newPeerClosedSocket(conn)
	socket.start()
	if before == "socket" {
		rhWaitFor(t, func() bool { return socket.ReadyState() == 3 })
	}
	refused = link.WebSocketLink(socket)
	if before == "link" {
		rhWaitFor(t, func() bool { return refused.(interface{ Closed() bool }).Closed() })
	}
	client = newHost(t, HostOptions{OnError: noopOnError})
	t.Cleanup(func() { closeHosts(t, client) })
	must(t, client.Connect(refused, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	return client, refused
}

func TestHostConnectOverAWebSocketRefusedBeforeItsLinkWrappedItKeepsNoParent(t *testing.T) {
	client, refused := refusedOverARealSocket(t, "socket")
	rhWaitFor(t, func() bool { return refused.(interface{ Closed() bool }).Closed() })
	if kept := parentOf(client); kept != nil {
		t.Fatalf("parent = %v, want none: the socket was refused before its link was made", kept)
	}
	next, _ := link.MemoryLinkPair()
	must(t, client.Connect(next, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
}

func TestHostConnectOverAWebSocketRefusedBeforeTheHostConnectedItsLinkKeepsNoParent(t *testing.T) {
	client, _ := refusedOverARealSocket(t, "link")
	if kept := parentOf(client); kept != nil {
		t.Fatalf("parent = %v, want none: the link was refused before the host connected it", kept)
	}
	next, _ := link.MemoryLinkPair()
	must(t, client.Connect(next, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
}
