package routing_test

import (
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/protocol"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// controlledSocket is upstream ControlledSocket (unix-connection.test.ts:7-42): its first write stays pending until completeWrite, and its final frame and half-close are recorded.
type controlledSocket struct {
	net.Conn
	mu        sync.Mutex
	writes    [][]byte
	entered   chan struct{}
	release   chan struct{}
	ended     chan struct{}
	second    chan struct{}
	destroyed bool
	once      sync.Once
}

func newControlledSocket() *controlledSocket {
	return &controlledSocket{entered: make(chan struct{}), release: make(chan struct{}), ended: make(chan struct{}), second: make(chan struct{})}
}

func (socket *controlledSocket) Write(chunk []byte) (int, error) {
	socket.mu.Lock()
	socket.writes = append(socket.writes, append([]byte(nil), chunk...))
	first := len(socket.writes) == 1
	secondWrite := len(socket.writes) == 2
	socket.mu.Unlock()
	if first {
		close(socket.entered)
		<-socket.release
	} else if secondWrite {
		close(socket.second)
	}
	return len(chunk), nil
}

func (socket *controlledSocket) CloseWrite() error {
	socket.once.Do(func() { close(socket.ended) })
	return nil
}

func (socket *controlledSocket) Close() error {
	socket.mu.Lock()
	socket.destroyed = true
	socket.mu.Unlock()
	return nil
}

func (socket *controlledSocket) isDestroyed() bool {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	return socket.destroyed
}

func (socket *controlledSocket) hasEnded() bool {
	select {
	case <-socket.ended:
		return true
	default:
		return false
	}
}

func (*controlledSocket) Read([]byte) (int, error)         { return 0, errors.New("not readable") }
func (*controlledSocket) LocalAddr() net.Addr              { return &net.UnixAddr{Net: "unix"} }
func (*controlledSocket) RemoteAddr() net.Addr             { return &net.UnixAddr{Net: "unix"} }
func (*controlledSocket) SetDeadline(time.Time) error      { return nil }
func (*controlledSocket) SetReadDeadline(time.Time) error  { return nil }
func (*controlledSocket) SetWriteDeadline(time.Time) error { return nil }

// upstream: packages/server/test/unix-connection.test.ts:44 "queues a final protocol error behind pending output before closing"
func TestUnixByteConnectionQueuesAFinalProtocolErrorBehindPendingOutput(t *testing.T) {
	socket := newControlledSocket()
	connection := routing.NewUnixByteConnection(socket, time.Second, 64*1024)
	pending := make(chan error, 1)
	go func() { pending <- connection.Send([]byte{1, 2, 3}) }()
	select {
	case <-socket.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the pending write never reached the socket")
	}
	finalMessage := protocol.ServerHelloError{Error: protocol.ProtocolError{Code: "invalid_request", Message: "Protocol violation"}}
	final, err := protocol.EncodeServerMessage(finalMessage, protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	closing := make(chan error, 1)
	go func() { closing <- connection.Close(final) }()

	// The final frame stays queued for as long as the pending write is unfinished; this bounded wait can only pass early, never fail a correct queue.
	select {
	case <-socket.second:
		t.Fatal("the final frame was written before pending output completed")
	case <-time.After(200 * time.Millisecond):
	}
	// Checked after the window, once Close has had time to run: upstream asserts ended and destroyed are false synchronously after close().
	if socket.hasEnded() || socket.isDestroyed() {
		t.Fatal("the socket was ended or destroyed while output was still pending")
	}
	close(socket.release)
	if err := <-pending; err != nil {
		t.Fatalf("pending write = %v", err)
	}
	select {
	case <-socket.ended:
	case <-time.After(30 * time.Second):
		t.Fatal("the socket was never half-closed")
	}
	socket.mu.Lock()
	writes := socket.writes
	socket.mu.Unlock()
	if len(writes) != 2 {
		t.Fatalf("socket writes = %d, want the pending chunk then the final frame", len(writes))
	}
	decoder, err := protocol.NewServerMessageDecoder(protocol.FrameDecoderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := decoder.Push(writes[1])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(messages, []protocol.ServerMessage{finalMessage}) {
		t.Fatalf("final frame = %#v, want %#v", messages, finalMessage)
	}
	connection.MarkClosed()
	select {
	case err := <-closing:
		if err != nil {
			t.Fatalf("Close = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not settle after the socket close event")
	}
}
