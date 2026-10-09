package experimental

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/client"
	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
)

// upstream: packages/coding-agent/test/experimental-remote-runtime.test.ts:153-166 intercepts only Client.connect. The static constructor, instance constructor and real byte handshake remain distinguishable.
func TestRejectNextExperimentalClientConnectPreservesInstancePath(t *testing.T) {
	rejectNextExperimentalClientConnect(t)
	var opens atomic.Int32
	options := client.ClientOptions{ServerId: "00000000-0000-4000-8000-000000000001", TransportFactory: func(_ context.Context, handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
		opens.Add(1)
		decoder, err := protocol.NewClientMessageDecoder(protocol.FrameDecoderOptions{})
		if err != nil {
			return nil, err
		}
		return &bindingFixturePeer{decoder: decoder, handlers: handlers}, nil
	}}
	instance, err := client.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := disposeServerClient(context.Background(), instance); err != nil {
			t.Error(err)
		}
	})
	if _, err := instance.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("instance connection opens=%d, want one real transport", got)
	}

	intercepted, err := connectRuntimeClient(t.Context(), options)
	failure, ok := err.(*client.ServerError) //nolint:errorlint // The upstream spy rejects with an outer ServerError; a wrapped error would exercise a different runtime branch.
	if intercepted != nil || !ok || failure.Code != "version" || failure.Message != "stale server" {
		t.Fatalf("first static Connect=%v, %#v", intercepted, err)
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("rejected static constructor opened transport: %d", got)
	}

	connected, err := connectRuntimeClient(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := disposeServerClient(context.Background(), connected); err != nil {
			t.Error(err)
		}
	})
	if !connected.Connected() || connected.ServerId() != options.ServerId {
		t.Fatalf("later static constructor did not use real client: %+v", connected.Hello())
	}
	if got := opens.Load(); got != 2 {
		t.Fatalf("later static constructor opens=%d, want second real transport", got)
	}
}
