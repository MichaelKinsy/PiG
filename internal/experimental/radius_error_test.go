package experimental

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

// Upstream webSocketError supplies the same fallback for opening and established socket failures.
func TestRadiusEstablishedErrorsHaveUsefulDetails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientSocket := newFakeRelaySocket(RadiusRelayClientSubprotocol)
		failures := make(chan string, 1)
		client, err := CreateRadiusClientTransportFactory(RadiusClientTransportOptions{ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"), WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
			return clientSocket, nil
		}})(t.Context(), RelayByteConnectionHandler{OnError: func(err error) { failures <- err.Error() }})
		if err != nil {
			t.Fatal(err)
		}
		clientSocket.incoming <- socketMessage{err: errors.New("")}
		<-client.Done()
		if got := <-failures; got != "Radius WebSocket connection failed" {
			t.Errorf("client error = %q", got)
		}

		hostSocket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
		statuses := make(chan RadiusRelayHostStatus, 4)
		host, err := NewRadiusRelayHost(RadiusRelayHostOptions{ServerID: testServerID, Auth: explicitAuth(t, "http://localhost"), Accept: func(*RelayServerByteConnection) RelayByteConnectionHandler { return RelayByteConnectionHandler{} }, OnStatus: func(status RadiusRelayHostStatus) { statuses <- status }, WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) {
			return hostSocket, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		host.Start(t.Context())
		defer host.Close()
		synctest.Wait()
		hostSocket.incoming <- socketMessage{err: errors.New(" \n")}
		synctest.Wait()
		<-statuses
		<-statuses
		if got := <-statuses; got.Status != "retrying" || got.Error != "Radius WebSocket connection failed" {
			t.Errorf("host error = %+v", got)
		}
	})
}
