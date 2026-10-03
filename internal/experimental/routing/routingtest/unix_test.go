//go:build unix

package routingtest_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

func listenUnix(t *testing.T) (net.Listener, string) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "rt-")
	mustNoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "server.sock")
	listener, err := net.Listen("unix", path)
	mustNoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	return listener, path
}

// acceptOne delivers the listener's first connection, or nil when the listener closes first.
func acceptOne(t *testing.T, listener net.Listener) <-chan net.Conn {
	t.Helper()
	accepted := make(chan net.Conn, 1)
	go func() {
		defer close(accepted)
		if conn, err := listener.Accept(); err == nil {
			accepted <- conn
		}
	}()
	return accepted
}

// packages/server/src/testing/client.ts:152-174: socket data reaches the client, and the peer's close marks it closed and rejects pending waits.
func TestConnectUnixTestClientFollowsThePeer(t *testing.T) {
	ctx := boundedContext(t)
	listener, path := listenUnix(t)
	accepted := acceptOne(t, listener)
	client, err := routingtest.ConnectUnixTestClient(ctx, path)
	mustNoError(t, err)
	peer := <-accepted
	if peer == nil {
		t.Fatal("the listener accepted no connection")
	}
	frame := serverFrame(t, protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: serverID})
	if _, err := peer.Write(frame[:2]); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write(frame[2:]); err != nil {
		t.Fatal(err)
	}
	if message, err := client.Next(ctx, func(protocol.ServerMessage) bool { return true }); err != nil || message != (protocol.ServerHello{Version: protocol.ProtocolVersion, ServerId: serverID}) {
		t.Fatalf("received %#v, %v", message, err)
	}
	pending := make(chan error, 1)
	go func() {
		_, err := client.Next(ctx, responseTo("never"))
		pending <- err
	}()
	waitForWaiter(t, client)
	mustNoError(t, peer.Close())
	expectErrorText(t, <-pending, "Wire connection closed")
	mustNoError(t, client.WaitForClose(ctx))
	if !client.Closed() {
		t.Fatal("the client did not record the peer's close")
	}
	mustNoError(t, client.Close())
	if err := client.SendMessage(protocol.ClientHello{Version: protocol.ProtocolVersion}); err == nil {
		t.Fatal("a send on a closed socket succeeded")
	}
}

// packages/server/src/testing/client.ts:161-166: closing the client destroys the socket, waits for its close, and reports no socket error.
func TestConnectUnixTestClientCloseWaitsForTheSocket(t *testing.T) {
	ctx := boundedContext(t)
	listener, path := listenUnix(t)
	peer := acceptOne(t, listener)
	client, err := routingtest.ConnectUnixTestClient(ctx, path)
	mustNoError(t, err)
	t.Cleanup(func() {
		if conn := <-peer; conn != nil {
			_ = conn.Close()
		}
	})
	mustNoError(t, client.SendMessage(protocol.ClientHello{Version: protocol.ProtocolVersion}))
	pending := make(chan error, 1)
	go func() {
		_, err := client.Next(ctx, responseTo("never"))
		pending <- err
	}()
	waitForWaiter(t, client)
	mustNoError(t, client.Close())
	if !client.Closed() {
		t.Fatal("Close returned before the socket closed")
	}
	if err := <-pending; err == nil || err.Error() != "Wire connection closed" {
		t.Fatalf("pending wait after Close = %v, want only the close rejection", err)
	}
	mustNoError(t, client.Close())
	if _, err := routingtest.ConnectUnixTestClient(ctx, filepath.Join(filepath.Dir(path), "missing.sock")); err == nil {
		t.Fatal("connecting to a missing socket succeeded")
	}
}
