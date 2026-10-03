package client

import (
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// blockingConn is a net.Conn whose Read waits for eof and whose Write blocks until Close, so a write is in flight when the peer closes.
type blockingConn struct {
	eof       chan struct{}
	closed    chan struct{}
	writing   chan struct{}
	closeOnce sync.Once
}

func (conn *blockingConn) Read([]byte) (int, error) {
	select {
	case <-conn.eof:
		return 0, io.EOF
	case <-conn.closed:
		return 0, net.ErrClosed
	}
}
func (conn *blockingConn) Write([]byte) (int, error) {
	close(conn.writing)
	<-conn.closed
	return 0, net.ErrClosed
}
func (conn *blockingConn) Close() error {
	conn.closeOnce.Do(func() { close(conn.closed) })
	return nil
}
func (*blockingConn) LocalAddr() net.Addr              { return nil }
func (*blockingConn) RemoteAddr() net.Addr             { return nil }
func (*blockingConn) SetDeadline(time.Time) error      { return nil }
func (*blockingConn) SetReadDeadline(time.Time) error  { return nil }
func (*blockingConn) SetWriteDeadline(time.Time) error { return nil }

// upstream: packages/client/src/unix.ts:121-141 registers the socket close listener that calls handlers.onClose before UnixByteTransport#write registers its own (:215-218), so a remote close reaches the Connection before the in-flight write rejects with "Unix transport closed during write". Settling the write first failed discovery with that write error instead of omitting an endpoint that closed before its handshake (unix.test.ts:163).
func TestUnixTransportRemoteCloseNotifiesBeforeSettlingWrites(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		conn := &blockingConn{eof: make(chan struct{}), closed: make(chan struct{}), writing: make(chan struct{})}
		var mu sync.Mutex
		var order []string
		record := func(event string) {
			mu.Lock()
			order = append(order, event)
			mu.Unlock()
		}
		transport := newUnixByteTransport(conn, 1024, ByteTransportHandlers{
			OnData: func([]byte) {},
			OnClose: func() {
				// Let every other goroutine run: a write settled without waiting for this notification would record first.
				synctest.Wait()
				record("close")
			},
			OnError: func(err error) { record("error: " + err.Error()) },
		})
		transport.Send([]byte("hello"), func(err error) {
			if err == nil {
				record("sent")
				return
			}
			record("send: " + err.Error())
		})
		queued := make(chan error, 1)
		<-conn.writing
		transport.Send([]byte("queued"), func(err error) { queued <- err })
		go transport.read()
		close(conn.eof)
		<-transport.Done()
		if want := []string{"close", "send: Unix transport closed during write"}; !slices.Equal(order, want) {
			t.Fatalf("order = %q; want %q", order, want)
		}
		if err := <-queued; err == nil || err.Error() != "Unix transport is closed" {
			t.Fatalf("queued write = %v; want Unix transport is closed", err)
		}
	})
}

// upstream: packages/client/src/unix.ts:115-120 runs handlers.onClose inside the socket's end/close listener, and send() (:160-176) settles its rejection asynchronously, so no send rejection can be observed before the Connection saw the remote close. A Send racing a remote close whose handlers are still running settles after them.
func TestUnixTransportSendDuringRemoteCloseSettlesAfterHandlers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		conn := &blockingConn{eof: make(chan struct{}), closed: make(chan struct{}), writing: make(chan struct{})}
		var mu sync.Mutex
		var order []string
		record := func(event string) {
			mu.Lock()
			order = append(order, event)
			mu.Unlock()
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		transport := newUnixByteTransport(conn, 1024, ByteTransportHandlers{
			OnData: func([]byte) {},
			OnClose: func() {
				close(entered)
				<-release
				record("close")
			},
			OnError: func(err error) { record("error: " + err.Error()) },
		})
		go transport.read()
		close(conn.eof)
		<-entered
		transport.Send([]byte("late"), func(err error) {
			if err == nil {
				record("sent")
				return
			}
			record("send: " + err.Error())
		})
		synctest.Wait()
		close(release)
		<-transport.Done()
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if want := []string{"close", "send: Unix transport is closed"}; !slices.Equal(order, want) {
			t.Fatalf("order = %q; want %q", order, want)
		}
	})
}
