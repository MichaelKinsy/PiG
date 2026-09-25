package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCoordinatorExplicitEmptyGenerationID(t *testing.T) {
	server := NewCoordinatorConnection(CoordinatorConnectionOptions{ServerConnectionID: new("")})
	if server.ServerConnectionID() != "" {
		t.Fatal("explicit empty generation ID was replaced with a random ID")
	}
}

func TestCoordinatorConnectionLifecycle(t *testing.T) {
	for _, oracle := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream=%t", oracle), func(t *testing.T) {
			public, control, _ := startTestCoordinator(t, oracle)
			peer := controlDial(t, control)
			peer.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "worker"})
			peer.want(t, `{"type":"peer_registered","peerId":"worker"}`)
			server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: public + ".backend", ServerConnectionID: new("s1")})
			t.Cleanup(server.Close)
			if err := server.Send("worker", nil); err == nil {
				t.Fatal("send before registration")
			}
			events := make(chan CoordinatorConnectionEvent, 4)
			unsubscribe := server.OnEvent(NewCoordinatorConnectionListener(func(event CoordinatorConnectionEvent) { events <- event }))
			defer unsubscribe()
			if err := server.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			peer.want(t, `{"type":"server_connected","serverConnectionId":"s1"}`)
			if got := server.PeerIDs(); !reflect.DeepEqual(got, []string{"worker"}) {
				t.Fatal(got)
			}
			if err := server.Connect(t.Context()); err == nil {
				t.Fatal("double connect")
			}
			peer.send(t, map[string]any{"type": "send", "to": "server", "payload": map[string]any{"deep": []any{nil, "opaque"}}})
			select {
			case event := <-events:
				if event.Type != "message" || event.From != "worker" || string(event.Payload) != `{"deep":[null,"opaque"]}` {
					t.Fatalf("event %+v", event)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("message event missing")
			}
			if err := server.Send("worker", nil); err != nil {
				t.Fatal(err)
			}
			peer.want(t, `{"type":"message","from":"server","payload":null}`)
			if err := server.Broadcast("broadcast"); err != nil {
				t.Fatal(err)
			}
			peer.want(t, `{"type":"message","from":"server","payload":"broadcast"}`)
			second := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: public + ".b2", ServerConnectionID: new("s2")})
			t.Cleanup(second.Close)
			if err := second.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-server.Replaced():
			case <-time.After(5 * time.Second):
				t.Fatal("replacement promise unresolved")
			}
			if !server.WasReplaced() {
				t.Fatal("missing replacement state")
			}
			server.Close()
			if len(server.PeerIDs()) != 0 {
				t.Fatal("closed server retained peers")
			}
			if err := server.Broadcast(nil); err == nil {
				t.Fatal("closed server wrote")
			}
			second.Close()
			if second.WasReplaced() {
				t.Fatal("explicit close marked replaced")
			}
		})
	}
}

func TestCoordinatorSlowPeerDoesNotBlockOtherPeers(t *testing.T) {
	for _, oracle := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream=%t", oracle), func(t *testing.T) {
			public, control, _ := startTestCoordinator(t, oracle)
			for _, id := range []string{"slow", "fast"} {
				peer := controlDial(t, control)
				peer.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": id})
				peer.want(t, fmt.Sprintf(`{"type":"peer_registered","peerId":%q}`, id))
				if id == "fast" {
					server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: public + ".backend", ServerConnectionID: new("server")})
					defer server.Close()
					if err := server.Connect(t.Context()); err != nil {
						t.Fatal(err)
					}
					peer.want(t, `{"type":"server_connected","serverConnectionId":"server"}`)
					payload := strings.Repeat("x", 1024*1024)
					for range 4 {
						if err := server.Send("slow", payload); err != nil {
							t.Fatal(err)
						}
					}
					if err := server.Send("fast", "marker"); err != nil {
						t.Fatal(err)
					}
					peer.want(t, `{"type":"message","from":"server","payload":"marker"}`)
				}
			}
		})
	}
}

func TestCoordinatorListenerPanicDisconnects(t *testing.T) {
	public, control, _ := startTestCoordinator(t, false)
	server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: public + ".backend"})
	defer server.Close()
	server.OnEvent(NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) { panic("listener failed") }))
	if err := server.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	peer := controlDial(t, control)
	peer.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "worker"})
	select {
	case <-server.Replaced():
	case <-time.After(5 * time.Second):
		t.Fatal("listener error did not destroy the coordinator connection")
	}
}

func TestCoordinatorConnectionRejectsMalformedRegistration(t *testing.T) {
	for _, response := range []string{
		`{"type":"server_registered","serverConnectionId":"wrong","peers":[]}`,
		`{"type":"server_registered","serverConnectionId":"server","peers":[null]}`,
		`{"type":"server_registered","serverConnectionId":"server","peers":null}`,
		`{"type":"peer_connected","peerId":42}`,
		`{"type":"surprise"}`,
	} {
		t.Run(response, func(t *testing.T) {
			path := filepath.Join(socketDir(t), "c")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				_ = readControlLines(conn, func(_ json.RawMessage) error {
					_, err := conn.Write([]byte(response + "\n"))
					return errors.Join(errors.New("end fake"), err)
				})
			}()
			server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: path, Endpoint: "unused", ServerConnectionID: new("server")})
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := server.Connect(ctx); err == nil {
				t.Fatal("malformed registration accepted")
			}
			<-done
		})
	}
}

// Upstream iterates its live listener Set: removing a listener during delivery prevents that invocation.
func TestCoordinatorConnectionListenerRemovalDuringDelivery(t *testing.T) {
	server := NewCoordinatorConnection(CoordinatorConnectionOptions{})
	var remove func()
	server.OnEvent(NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) { remove() }))
	called := false
	remove = server.OnEvent(NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) { called = true }))
	if err := server.handleMessage(json.RawMessage(`{"type":"peer_connected","peerId":"p"}`)); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("unsubscribed listener called during same event")
	}
}

func TestCoordinatorConnectionConnectCancellation(t *testing.T) {
	path := filepath.Join(socketDir(t), "c")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
		close(accepted)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: path, Endpoint: "unused"})
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Connect(ctx) }()
	conn := <-accepted
	defer func() { _ = conn.Close() }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registration ignored cancellation")
	}
}
