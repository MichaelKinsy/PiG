//go:build unix

package routing_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// gatedServerServices holds AttachClient open until released, then counts attachment releases.
type gatedServerServices struct {
	routing.RoutedServerServiceHost
	entered  chan struct{}
	release  chan struct{}
	released atomic.Int32
}

func (gate *gatedServerServices) AttachClient(ctx context.Context, presentation routing.RoutedServerPresentation) (routing.RoutedServerServiceAttachment, error) {
	close(gate.entered)
	<-gate.release
	attachment, err := gate.RoutedServerServiceHost.AttachClient(ctx, presentation)
	return countingAttachment{attachment, &gate.released}, err
}

type countingAttachment struct {
	routing.RoutedServerServiceAttachment
	released *atomic.Int32
}

func (attachment countingAttachment) Release(ctx context.Context) error {
	attachment.released.Add(1)
	return attachment.RoutedServerServiceAttachment.Release(ctx)
}

func helloFrame(t *testing.T) []byte {
	t.Helper()
	frame, err := protocol.EncodeClientMessage(protocol.ClientHello{Version: protocol.ProtocolVersion}, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

// readUntilClosed returns every server frame, in order, until the server closes the connection.
func readUntilClosed(t *testing.T, conn net.Conn) []protocol.ServerMessage {
	t.Helper()
	decoder, err := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var messages []protocol.ServerMessage
	buffer := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, err := conn.Read(buffer)
		decoded, decodeErr := decoder.Push(buffer[:n])
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		messages = append(messages, decoded...)
		if err != nil {
			if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
				t.Fatalf("server did not close the connection; received %d messages", len(messages))
			}
			return messages
		}
	}
}

// upstream: packages/server/src/server.ts:239-245 and 264-285. A second hello fails the connection with invalid_request. A second hello that arrives while the first handshake is still attaching its
// server services makes finishHandshake observe state.stage !== "handshaking" after attachClient, release the services and send no hello (the wire differential's "hello then hello" when Node's handshake is slow).
// mutation-checked: negating the condition `err != nil` at unix.go:255 fails it.
func TestSecondHelloDuringHandshakeSendsOnlyTheError(t *testing.T) {
	path := shortDirectory(t, "pss-") + "/server.sock"
	listener, err := routing.CreateUnixListener(routing.UnixListenerOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	host := newTestServerHost()
	gate := &gatedServerServices{RoutedServerServiceHost: host.ServerServices, entered: make(chan struct{}), release: make(chan struct{})}
	host.ServerServices = gate
	reported := make(chan error, 8)
	server, err := routing.NewServer(host, routing.ServerOptions{Listeners: []routing.ServerListener{listener}, ServerId: testServerID, OnError: func(err error) { reported <- err }})
	if err != nil {
		t.Fatal(err)
	}
	if err := startError(server); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	hello := helloFrame(t)
	if _, err := conn.Write(hello); err != nil {
		t.Fatal(err)
	}
	<-gate.entered
	if _, err := conn.Write(hello); err != nil {
		t.Fatal(err)
	}
	// The second hello fails the connection while AttachClient is still held: the final hello_error and the half-close reach the
	// client before the attachment completes, so the handshake resumes on a connection that is already closing.
	messages := readUntilClosed(t, conn)
	close(gate.release)
	if len(messages) != 1 {
		t.Fatalf("server sent %d messages ; want only hello_error", len(messages))
	}
	failure, ok := messages[0].(protocol.ServerHelloError)
	if !ok || failure.Error.Code != "invalid_request" || failure.Error.Message != "hello may only be sent as the first message" {
		t.Fatalf("message = %#v", messages[0])
	}
	pollUntil(t, "the attached services are released", func() bool { return gate.released.Load() == 1 })
	// finishHandshake releases the late services itself; it never tries to send its hello on the closing connection.
	select {
	case err := <-reported:
		t.Fatalf("server reported %v", err)
	default:
	}
}
