package experimental

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestRadiusWriterShutdownJoinsEveryQueuedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := newFakeRelaySocket(RadiusRelayClientSubprotocol)
		release := make(chan struct{})
		socket.onSend = func() { <-release }
		writer := newOrderedWebSocketWriter(socket)
		results := make(chan error, 2)
		go func() { results <- writer.send(true, []byte("active")) }()
		synctest.Wait()
		go func() { results <- writer.send(true, []byte("queued")) }()
		synctest.Wait()
		writer.close()
		joined := make(chan struct{})
		go func() { writer.wait(); close(joined) }()
		synctest.Wait()
		select {
		case <-joined:
			t.Error("shutdown returned while the first write still owns the socket")
		default:
		}
		close(release)
		<-joined
		<-results
		<-results
	})
}

func TestRadiusHostPingWriteFailureUsesTransportClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
		socket.sendErr = errors.New("write failed")
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"), Accept: func(*RelayServerByteConnection) RelayByteConnectionHandler { return RelayByteConnectionHandler{} }, WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) { return socket, nil }})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		defer host.Close()
		synctest.Wait()
		socket.control("ping", "")
		synctest.Wait()
		if got := <-socket.closing; got.code != 4001 || got.reason != "Radius relay send failed" {
			t.Fatalf("close = %+v", got)
		}
	})
}
