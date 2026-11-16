package host

// A Go socket and its link close on goroutines of their own, at any point between the link's creation and the host's
// registration of its close handler. Pi's connect() and webSocketLink() run to completion before any socket event, so
// they never see a link closed while they set it up; Go has no such guarantee, so the setup reads the state it may
// have missed (Pi's Chord v2: packages/chord/src/next/host/host.ts:859 `if (parent.open) this.#parent.connected(parent)`
// and next/link/websocket.ts:52 `readyState !== CONNECTING && !== OPEN` give a closed link, both in the pinned v1.5.0).
// These tests build each interleaving instead of racing for it.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// upstreamOf is the connection the host would route a hold to, nil for none.
func upstreamOf(host *HostImpl) (kept *Connection) {
	host.run(func() { kept = host.upstream })
	return kept
}

// wantNoRoute checks that a hold taken now over host is unavailable at once, as a host with no upstream, no child of
// the name and no route has no way to the host it asks for (host.ts hold).
func wantNoRoute(t *testing.T, host *HostImpl) {
	t.Helper()
	denied, err := host.Hold(wsBoard, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	signal, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	_, err = host.Root(func(ctx Ctx) (any, error) { return callMember(denied, "add", "x", ctx) }, RootOptions{Signal: signal})
	if !IsChordError(err, CodeUnavailable) || !strings.Contains(err.Error(), "no route to server") {
		t.Fatalf("call = %v, want unavailable: no route to server", err)
	}
}

// host.ts connect (Pi 1.4.0 host.ts:953-963) keeps the connection it made for a link as the upstream, whatever the link's
// state. A link the embedder closed before it called Connect closed the connection inside newConnection (OnClose runs at
// once on a closed link) before the assignment, so nothing ever cleared it: holds bound to a dead connection and calls
// hung. The upstream of a host whose link ended is none, whether the link ended before Connect or after it.
func TestConnectOverALinkTheEmbedderClosedBeforeKeepsNoUpstreamAndAHoldHasNoRoute(t *testing.T) {
	client := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: KindClient, OnError: func(error) {}})
	t.Cleanup(func() { _ = client.Close() })
	_, up := MemoryLinkPair()
	up.Close("refused")
	client.Connect(up, ConnectOptions{Principal: kindServer()})
	if kept := upstreamOf(client); kept != nil {
		t.Fatalf("upstream = %p after Connect over a closed link, want none", kept)
	}
	wantNoRoute(t, client)
}

// A reference whose link was lost waits for the next Connect to hold again. A Connect over a link that is already closed
// bound it to the dead connection (hold != nil), so the next good Connect skipped it: the reference stayed dead for good.
func TestConnectAfterALinkClosedAtOnceHoldsTheReferencesAgainOverTheNextGoodLink(t *testing.T) {
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	serveCounter(t, server, nil)
	accept := func() *MemoryLink {
		down, up := MemoryLinkPair()
		if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "web"}, Kind: KindClient}); err != nil {
			t.Fatal(err)
		}
		return up
	}
	first := accept()
	client := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: first, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = client.Close() })
	counter := mustGet(t, client, "a")
	if got := mustCall(t, counter, "add", float64(1), nil); got != float64(1) {
		t.Fatalf("add = %v, want 1", got)
	}

	// The connection drops; the embedder's reconnect gets a socket that is refused at once.
	first.Close("dropped")
	waitFor(t, "the dropped upstream to clear", func() bool { return upstreamOf(client) == nil })
	_, refused := MemoryLinkPair()
	refused.Close("refused")
	client.Connect(refused, ConnectOptions{Principal: kindServer()})
	// The upstream is none (TestConnectOverALinkTheEmbedderClosedBeforeKeepsNoUpstreamAndAHoldHasNoRoute); what the
	// reference is bound to is the row here.
	var bound int
	client.run(func() {
		for _, binding := range client.bindings.Values() {
			if binding.hold != nil {
				bound++
			}
		}
	})
	if bound != 0 {
		t.Fatalf("%d references bound to the closed link, want none: they wait for the next link", bound)
	}

	client.Connect(accept(), ConnectOptions{Principal: kindServer()})
	signal, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	if _, err := client.Root(func(ctx Ctx) (any, error) { return callMember(counter, "add", float64(1), ctx) }, RootOptions{Signal: signal}); err != nil {
		t.Fatalf("add over the next good link = %v, want it held again", err)
	}
}

// closesAtListener is a socket that closed (its close event went to nobody) at the moment its link registered the close
// listener: its ReadyState reads open until then.
type closesAtListener struct{ *fakeWebSocket }

func (socket *closesAtListener) AddEventListener(typ string, listener func(WebSocketMessage)) {
	socket.fakeWebSocket.AddEventListener(typ, listener)
	if typ == "close" {
		socket.readyState = 3
	}
}

// websocket.ts (Pi v2 next/link/websocket.ts:52): a socket closing or closed already gives a closed link, and a socket that
// closed while the link registered its listeners does too: the state is read after the listeners are registered, so a
// close between the two is not lost. onClose on a closed link runs at once with "closed" (link.ts:46-48).
func TestWebSocketLinkOverASocketClosingOrClosedGivesAClosedLinkAlsoOneThatClosedAsItRegistered(t *testing.T) {
	for name, socket := range map[string]WebSocketLike{
		"closing":                       newFakeWebSocket(2),
		"closed":                        newFakeWebSocket(3),
		"closed as the link registered": &closesAtListener{newFakeWebSocket(webSocketOpen)},
	} {
		link := NewWebSocketLink(socket, WebSocketLinkOptions{})
		var reasons reasonLog
		link.OnClose(reasons.add)
		if got := reasons.list(); !link.Closed() || len(got) != 1 || got[0] != "closed" {
			t.Errorf("%s: closed = %v, close reasons = %q, want a closed link that tells a listener at once", name, link.Closed(), got)
		}
	}
}

// refusedOverARealSocket is the refused client of the test below with the refusal made to come at the step `before` names:
// `socket`: before the link wraps the socket, `link`: after it wraps the socket and before the host connects it. A browser's
// events always come after the code that made the socket; a Go socket's close can come between any two of its statements.
func refusedOverARealSocket(t *testing.T, before string) *HostImpl {
	t.Helper()
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	wss := startWebSocketServer(t)
	AcceptWebSockets[*http.Request](wss, server, WebSocketAcceptOptions[*http.Request]{
		Principal: func(*http.Request) (Principal, bool, error) { return nil, false, nil },
	})
	socket := dialWebSocket(wss.url(""))
	if before == "socket" {
		waitFor(t, "the server's refusal to close the socket", func() bool { return socket.ReadyState() == wsClosed })
	}
	link := NewWebSocketLink(socket, WebSocketLinkOptions{})
	if before == "link" {
		waitFor(t, "the server's refusal to close the link", link.Closed)
	}
	refused := CreateHost(HostOptions{Self: kindHost(),
		Name:     "browser",
		Kind:     KindClient,
		Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}},
		OnError:  func(error) {},
	})
	t.Cleanup(func() { _ = refused.Close() })
	return refused
}

func TestWebSocketLinksARefusalThatCameBeforeTheLinkWrappedTheSocketHasNoRoute(t *testing.T) {
	refused := refusedOverARealSocket(t, "socket")
	if kept := upstreamOf(refused); kept != nil {
		t.Fatalf("upstream = %p, want none: the socket was refused before its link was made", kept)
	}
	wantNoRoute(t, refused)
}

func TestWebSocketLinksARefusalThatCameBeforeTheHostConnectedTheLinkHasNoRoute(t *testing.T) {
	refused := refusedOverARealSocket(t, "link")
	if kept := upstreamOf(refused); kept != nil {
		t.Fatalf("upstream = %p, want none: the link was refused before the host connected it", kept)
	}
	wantNoRoute(t, refused)
}
