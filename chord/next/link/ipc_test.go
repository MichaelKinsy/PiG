package link_test

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/link/ipc.test.ts. The Go form of a listener removal is the function the
// registration returns (a Go func has no identity to remove by). The forked-child test runs a goroutine child over a
// socket pair: Node's IPC channel and V8 "advanced" serialization have no Go counterpart, a `net.Pipe` carrying
// length-prefixed frames plays the channel and keeps the race of an exit against sends.

// fakePort plays a Node IPC channel by hand: what was sent, its listeners, and what the test makes happen.
type fakePort struct {
	mu           sync.Mutex
	connected    bool
	sent         [][]byte
	next         error
	backpressure bool
	callbacks    []func(error)
	disconnects  int
	message      map[int]func(any)
	disconnect   map[int]func()
	ids          int
}

func newFakePort() *fakePort {
	return &fakePort{connected: true, message: map[int]func(any){}, disconnect: map[int]func(){}}
}

func (port *fakePort) Send(message []byte, callback func(error)) (bool, error) {
	port.mu.Lock()
	defer port.mu.Unlock()
	if port.next != nil {
		return false, port.next
	}
	port.sent = append(port.sent, message)
	port.callbacks = append(port.callbacks, callback)
	return !port.backpressure, nil
}

func (port *fakePort) OnMessage(listener func(message any)) (remove func()) {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.ids++
	id := port.ids
	port.message[id] = listener
	return func() {
		port.mu.Lock()
		defer port.mu.Unlock()
		delete(port.message, id)
	}
}

func (port *fakePort) OnDisconnect(listener func()) (remove func()) {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.ids++
	id := port.ids
	port.disconnect[id] = listener
	return func() {
		port.mu.Lock()
		defer port.mu.Unlock()
		delete(port.disconnect, id)
	}
}

func (port *fakePort) Disconnect() {
	port.mu.Lock()
	port.disconnects++
	port.connected = false
	listeners := slices.Collect(mapValues(port.disconnect))
	port.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

func (port *fakePort) Connected() bool {
	port.mu.Lock()
	defer port.mu.Unlock()
	return port.connected
}

func (port *fakePort) receive(message any) {
	port.mu.Lock()
	listeners := slices.Collect(mapValues(port.message))
	port.mu.Unlock()
	for _, listener := range listeners {
		listener(message)
	}
}

func (port *fakePort) listeners() int {
	port.mu.Lock()
	defer port.mu.Unlock()
	return len(port.message) + len(port.disconnect)
}

func mapValues[K comparable, V any](m map[K]V) func(yield func(V) bool) {
	return func(yield func(V) bool) {
		for _, value := range m {
			if !yield(value) {
				return
			}
		}
	}
}

func TestIPCLinksOverAFakePort(t *testing.T) {
	t.Run("deliver []byte messages in order, queued until the listener is set; other messages are not the link's", func(t *testing.T) {
		port := newFakePort()
		end := link.IpcLink(port)
		port.receive([]byte{1})
		port.receive(map[string]any{"start": "the app's own"})
		port.receive([]byte{2})
		var received recorder[byte]
		end.OnMessage(func(message []byte) { received.add(message[0]) })
		port.receive([]byte{3})
		if !slices.Equal(received.all(), []byte{1, 2, 3}) {
			t.Fatalf("received = %v", received.all())
		}
	})

	t.Run("treat send returning false as backpressure, not a close", func(t *testing.T) {
		port := newFakePort()
		end := link.IpcLink(port)
		closed := 0
		end.OnClose(func() { closed++ })
		port.backpressure = true
		end.Send([]byte{1})
		end.Send([]byte{2})
		if len(port.sent) != 2 || closed != 0 {
			t.Fatalf("sent = %d, closed = %d", len(port.sent), closed)
		}
	})

	for name, fail := range map[string]func(*fakePort){
		"a thrown send":                 func(port *fakePort) { port.next = errors.New("channel closed") },
		"an error in the send callback": func(port *fakePort) { port.callbacks[0](errors.New("EPIPE")) },
		"a disconnect":                  func(port *fakePort) { port.Disconnect() },
	} {
		t.Run("close once on "+name+"; listeners removed, later messages dropped (race 27)", func(t *testing.T) {
			port := newFakePort()
			end := link.IpcLink(port)
			closed := 0
			var received recorder[[]byte]
			end.OnClose(func() { closed++ })
			end.OnMessage(func(message []byte) { received.add(message) })
			end.Send([]byte{1})
			fail(port)
			end.Send([]byte{2})
			port.Disconnect()
			port.receive([]byte{3})
			if closed != 1 || len(received.all()) != 0 || port.listeners() != 0 {
				t.Fatalf("closed = %d, received = %v, listeners = %d", closed, received.all(), port.listeners())
			}
		})
	}

	t.Run("disconnect the port on close, once; a port not connected gives a closed link", func(t *testing.T) {
		port := newFakePort()
		end := link.IpcLink(port)
		end.Close()
		end.Close()
		if port.disconnects != 1 {
			t.Fatalf("disconnects = %d", port.disconnects)
		}
		closed := false
		link.IpcLink(port).OnClose(func() { closed = true })
		if !closed {
			t.Fatal("a disconnected port gave an open link")
		}
	})
}

// framedPort is a port over a connection: a goroutine reads length-prefixed frames, a frame of a leading 0x00 is a
// message of the link (Uint8Array), anything else is the app's own.
type framedPort struct {
	conn net.Conn
	mu   sync.Mutex

	connected   bool
	messages    []func(any)
	disconnects []func()
}

func newFramedPort(conn net.Conn) *framedPort {
	port := &framedPort{conn: conn, connected: true}
	go port.read()
	return port
}

func (port *framedPort) read() {
	for {
		var length [4]byte
		if _, err := io.ReadFull(port.conn, length[:]); err != nil {
			port.ended()
			return
		}
		frame := make([]byte, binary.BigEndian.Uint32(length[:]))
		if _, err := io.ReadFull(port.conn, frame); err != nil {
			port.ended()
			return
		}
		port.mu.Lock()
		listeners := slices.Clone(port.messages)
		port.mu.Unlock()
		for _, listener := range listeners {
			if frame[0] == 0 {
				listener(frame[1:])
			} else {
				listener(string(frame[1:]))
			}
		}
	}
}

func (port *framedPort) ended() {
	port.mu.Lock()
	wasConnected := port.connected
	port.connected = false
	listeners := slices.Clone(port.disconnects)
	port.mu.Unlock()
	if wasConnected {
		for _, listener := range listeners {
			listener()
		}
	}
}

func (port *framedPort) Send(message []byte, callback func(error)) (bool, error) {
	frame := make([]byte, 4, 5+len(message))
	binary.BigEndian.PutUint32(frame, uint32(1+len(message)))
	frame = append(append(frame, 0), message...)
	_, err := port.conn.Write(frame)
	callback(err)
	return err == nil, nil
}

func (port *framedPort) OnMessage(listener func(message any)) func() {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.messages = append(port.messages, listener)
	return func() {}
}

func (port *framedPort) OnDisconnect(listener func()) func() {
	port.mu.Lock()
	defer port.mu.Unlock()
	port.disconnects = append(port.disconnects, listener)
	return func() {}
}

func (port *framedPort) Disconnect() {
	port.mu.Lock()
	port.connected = false
	port.mu.Unlock()
	_ = port.conn.Close()
}

func (port *framedPort) Connected() bool {
	port.mu.Lock()
	defer port.mu.Unlock()
	return port.connected
}

func TestIPCLinksToAChild(t *testing.T) {
	t.Run("carry []byte messages both ways and close when the child exits mid-send (race 27)", func(t *testing.T) {
		parentConn, childConn := net.Pipe()
		// The child: echoes every message of its parent link, and exits (closing its end) on 0xff or when the link closes.
		childPort := newFramedPort(childConn)
		child := link.IpcLink(childPort)
		child.OnMessage(func(message []byte) {
			if message[0] == 0xff {
				childPort.Disconnect()
				return
			}
			child.Send(message)
		})
		child.OnClose(func() { childPort.Disconnect() })

		parent := link.IpcLink(newFramedPort(parentConn))
		echoed := make(chan []byte, 1)
		parent.OnMessage(func(message []byte) { echoed <- slices.Clone(message) })
		var closes sync.WaitGroup
		closes.Add(1)
		var closeCount int
		var mu sync.Mutex
		parent.OnClose(func() {
			mu.Lock()
			closeCount++
			mu.Unlock()
			closes.Done()
		})
		parent.Send([]byte{1, 2, 3})
		select {
		case message := <-echoed:
			if !slices.Equal(message, []byte{1, 2, 3}) {
				t.Fatalf("echoed = %v", message)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no echo")
		}
		// The child exits on this one; the sends after it race the exit.
		parent.Send([]byte{0xff})
		for range 20 {
			parent.Send(make([]byte, 1024))
		}
		done := make(chan struct{})
		go func() { closes.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the link did not close")
		}
		mu.Lock()
		defer mu.Unlock()
		if closeCount != 1 {
			t.Fatalf("closes = %d", closeCount)
		}
	})
}
