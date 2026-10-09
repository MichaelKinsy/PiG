package experimental

// pi: packages/coding-agent/src/experimental/radius-relay.ts

import (
	"bytes"
	"context"
	"testing"
	"testing/synctest"
)

// .upstream/v0.87.1/packages/coding-agent/test/experimental-radius-relay.test.ts:112 — bridges multiplexed host clients into independent server connections.
func TestRadiusHostUpstreamRemoteClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
		var accepted *RelayServerByteConnection
		var received [][]byte
		closes := 0
		var statuses []string
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
			ServerID: testServerID,
			Auth:     explicitAuth(t, "http://localhost"),
			WebSocketFactory: func(_ context.Context, options RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
				if options.Authorization != "Bearer secret" {
					t.Errorf("authorization = %q", options.Authorization)
				}
				return socket, nil
			},
			OnStatus: func(status RadiusRelayHostStatus) { statuses = append(statuses, status.Status) },
			Accept: func(connection *RelayServerByteConnection) RelayByteConnectionHandler {
				accepted = connection
				return RelayByteConnectionHandler{OnData: func(data []byte) { received = append(received, bytes.Clone(data)) }, OnClose: func() { closes++ }}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		defer host.Close()
		synctest.Wait()
		if len(statuses) != 2 || statuses[1] != "connected" {
			t.Fatalf("statuses = %v", statuses)
		}
		socket.control("connection_open", testConnectionID)
		synctest.Wait()
		if accepted == nil {
			t.Fatal("server did not accept connection")
		}
		frame, err := EncodeRelayDataFrame(testConnectionID, []byte{1, 2, 3})
		if err != nil {
			t.Fatal(err)
		}
		socket.incoming <- socketMessage{binary: true, data: frame}
		synctest.Wait()
		if len(received) != 1 || !bytes.Equal(received[0], []byte{1, 2, 3}) {
			t.Fatalf("received = %v", received)
		}
		if err := accepted.Send([]byte{4, 5, 6}); err != nil {
			t.Fatal(err)
		}
		outbound, ok := ParseRelayDataFrame((<-socket.sent).data)
		if !ok || outbound.ConnectionID != testConnectionID || !bytes.Equal(outbound.Payload, []byte{4, 5, 6}) {
			t.Fatalf("outbound = %+v", outbound)
		}
		socket.control("connection_close", testConnectionID)
		synctest.Wait()
		if closes != 1 {
			t.Fatalf("onClose calls = %d, want 1", closes)
		}
		host.Close()
	})
}
