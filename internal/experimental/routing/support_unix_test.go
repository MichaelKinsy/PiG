//go:build unix

package routing_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

// unixTestClient is upstream connectUnixTestClient's ProtocolTestClient with the test's wait bound.
type unixTestClient struct {
	*routingtest.ProtocolTestClient
	ctx context.Context
}

func connectUnixTestClient(t *testing.T, path string) *unixTestClient {
	t.Helper()
	ctx := waitContext(t)
	client, err := routingtest.ConnectUnixTestClient(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &unixTestClient{ProtocolTestClient: client, ctx: ctx}
}

// hello sends the current protocol hello and returns the first hello or hello_error reply.
func (client *unixTestClient) hello(t *testing.T) protocol.ServerMessage {
	t.Helper()
	message, err := client.Hello(client.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
