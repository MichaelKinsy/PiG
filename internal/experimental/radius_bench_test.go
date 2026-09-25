package experimental

import (
	"bytes"
	"context"
	"testing"
)

func BenchmarkRadiusHostControl(b *testing.B) {
	socket := newFakeRelaySocket(RadiusRelayHostSubprotocol)
	auth, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Gateway: "http://localhost", Input: &AuthInput{Type: "token", Token: "secret"}})
	if err != nil {
		b.Fatal(err)
	}
	host, err := NewRadiusRelayHost(RadiusRelayHostOptions{
		ServerID: testServerID, Auth: auth,
		Accept:           func(*RelayServerByteConnection) RelayByteConnectionHandler { return RelayByteConnectionHandler{} },
		WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) { return socket, nil },
	})
	if err != nil {
		b.Fatal(err)
	}
	host.Start(b.Context())
	defer host.Close()
	b.ReportAllocs()
	for b.Loop() {
		socket.control("ping", "")
		message := <-socket.sent
		if message.binary || string(message.data) != `{"version":1,"type":"pong"}` {
			b.Fatalf("reply = %+v", message)
		}
	}
}

func BenchmarkRadiusClientSend(b *testing.B) {
	socket := newFakeRelaySocket(RadiusRelayClientSubprotocol)
	auth, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Gateway: "http://localhost", Input: &AuthInput{Type: "token", Token: "secret"}})
	if err != nil {
		b.Fatal(err)
	}
	transport, err := CreateRadiusClientTransportFactory(RadiusClientTransportOptions{ServerID: testServerID, Auth: auth, WebSocketFactory: func(context.Context, RadiusRelayWebSocketOptions) (RadiusRelayWebSocket, error) { return socket, nil }})(b.Context(), RelayByteConnectionHandler{})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { transport.Close(); <-transport.Done() }()
	payload := bytes.Repeat([]byte{42}, 64*1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		if err := transport.Send(payload); err != nil {
			b.Fatal(err)
		}
		<-socket.sent
	}
}
