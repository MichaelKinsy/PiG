package experimental

import (
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var testAcceptBackoff = acceptBackoff{min: time.Millisecond, max: 2 * time.Millisecond, maxRepeats: 10}

// failingListener fails every Accept with the same error until closed.
type failingListener struct {
	scriptedListener
	err error
}

func (l *failingListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return nil, l.err
}

// scriptedListener returns the scripted Accept results in order, then net.ErrClosed.
type scriptedListener struct {
	mu      sync.Mutex
	results []acceptResult
	calls   int
}

type acceptResult struct {
	conn net.Conn
	err  error
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if len(l.results) == 0 {
		return nil, net.ErrClosed
	}
	next := l.results[0]
	l.results = l.results[1:]
	return next.conn, next.err
}

func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return &net.UnixAddr{Name: "scripted", Net: "unix"} }

// An accept failure on a listener that is still open must not end the accept loop: every later client would sit in the
// backlog until its own timeout, as the coordinator's public socket did when a Windows accept failed once.
func TestAcceptConnectionsSurvivesAcceptFailure(t *testing.T) {
	first, firstPeer := net.Pipe()
	second, secondPeer := net.Pipe()
	t.Cleanup(func() { _ = firstPeer.Close(); _ = secondPeer.Close(); _ = first.Close(); _ = second.Close() })
	listener := &scriptedListener{results: []acceptResult{
		{err: errors.New("accept failed once")},
		{conn: first},
		{err: errors.New("accept failed again")},
		{conn: second},
	}}
	var handled []net.Conn
	if err := acceptConnections(listener, "test", nil, testAcceptBackoff, func(conn net.Conn) bool {
		handled = append(handled, conn)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(handled) != 2 || handled[0] != first || handled[1] != second {
		t.Fatalf("handled %v, want both connections accepted after the failures", handled)
	}
	if listener.calls != 5 {
		t.Fatalf("%d Accept calls, want 5 (four scripted results, then the closed listener)", listener.calls)
	}
}

// handle returning false ends the loop, as the control socket does once the coordinator is shutting down.
func TestAcceptConnectionsStopsWhenHandlerDeclines(t *testing.T) {
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
	listener := &scriptedListener{results: []acceptResult{{conn: conn}, {conn: conn}}}
	if err := acceptConnections(listener, "test", nil, testAcceptBackoff, func(net.Conn) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if listener.calls != 1 {
		t.Fatalf("%d Accept calls, want 1", listener.calls)
	}
}

// A failure that repeats is not transient: the loop gives up after maxRepeats identical failures in a row and returns
// the failure, instead of spinning on a listener that cannot accept. A different failure restarts the count.
func TestAcceptConnectionsGivesUpOnRepeatedFailure(t *testing.T) {
	cause := errors.New("accept failed for good")
	listener := &failingListener{err: cause}
	err := acceptConnections(listener, "test", nil, testAcceptBackoff, func(net.Conn) bool { return true })
	if !errors.Is(err, cause) {
		t.Fatalf("error %v, want it to wrap %v", err, cause)
	}
	if listener.calls != testAcceptBackoff.maxRepeats {
		t.Fatalf("%d Accept calls, want %d", listener.calls, testAcceptBackoff.maxRepeats)
	}
}

func TestAcceptConnectionsRepeatCountRestartsOnDifferentFailureAndSuccess(t *testing.T) {
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = conn.Close(); _ = peer.Close() })
	var results []acceptResult
	for range testAcceptBackoff.maxRepeats - 1 {
		results = append(results, acceptResult{err: errors.New("one")})
	}
	results = append(results, acceptResult{err: errors.New("two")}, acceptResult{conn: conn})
	for range testAcceptBackoff.maxRepeats - 1 {
		results = append(results, acceptResult{err: errors.New("one")})
	}
	listener := &scriptedListener{results: results}
	handled := 0
	if err := acceptConnections(listener, "test", nil, testAcceptBackoff, func(net.Conn) bool { handled++; return true }); err != nil {
		t.Fatal(err)
	}
	if handled != 1 {
		t.Fatalf("handled %d connections, want 1", handled)
	}
}

// Shutdown interrupts the retry delay instead of waiting it out.
func TestAcceptConnectionsStopInterruptsBackoff(t *testing.T) {
	listener := &failingListener{err: errors.New("accept failed")}
	stop := make(chan struct{})
	slow := acceptBackoff{min: time.Hour, max: time.Hour, maxRepeats: 10}
	done := make(chan error, 1)
	go func() { done <- acceptConnections(listener, "test", stop, slow, func(net.Conn) bool { return true }) }()
	for {
		listener.mu.Lock()
		calls := listener.calls
		listener.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not interrupt the retry delay")
	}
}

// A listener that persistently cannot accept retires the coordinator: its sockets are removed so the next client starts
// a new one, rather than leaving clients queued on a listener nobody serves.
func TestCoordinatorRetiresWhenAcceptFailsPersistently(t *testing.T) {
	saved := coordinatorAcceptBackoff
	coordinatorAcceptBackoff = testAcceptBackoff
	t.Cleanup(func() { coordinatorAcceptBackoff = saved })
	dir := socketDir(t)
	public, control := filepath.Join(dir, "p"), filepath.Join(dir, "c")
	c, err := startCoordinator(public, control)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.shutdown)
	go c.acceptLoop(&failingListener{err: errors.New("accept failed for good")}, "test", func(net.Conn) bool { return true })
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator did not retire")
	}
}
