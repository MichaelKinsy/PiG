package subprocess

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stalledCloseConn is a socket whose peer's close never reaches a read that is pending: the read the Windows AF_UNIX stall leaves pending (peer_exit.go). Bytes the peer wrote are returned, and a read deadline and Close still end a read, as on the real socket.
type stalledCloseConn struct {
	net.Conn

	mu      sync.Mutex
	limit   time.Time
	changed chan struct{}
	closed  chan struct{}
	once    sync.Once
	// swallowed counts the end-of-file reports the conn has held back.
	swallowed atomic.Int64
}

func newStalledCloseConn(conn net.Conn) *stalledCloseConn {
	return &stalledCloseConn{Conn: conn, changed: make(chan struct{}), closed: make(chan struct{})}
}

func (c *stalledCloseConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.limit = t
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	return nil
}

func (c *stalledCloseConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *stalledCloseConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func (c *stalledCloseConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 || !errors.Is(err, io.EOF) {
		return n, err
	}
	// The peer has closed. The read does not see it.
	c.swallowed.Add(1)
	for {
		c.mu.Lock()
		limit, changed := c.limit, c.changed
		c.mu.Unlock()
		var expired <-chan time.Time
		if !limit.IsZero() {
			if !time.Now().Before(limit) {
				return 0, os.ErrDeadlineExceeded
			}
			timer := time.NewTimer(time.Until(limit))
			defer timer.Stop()
			expired = timer.C
		}
		select {
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		case <-changed:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
}

// peerExitBound is how long a connection may take to end its read once the process has exited: the grace for silence, and generous room for a loaded machine.
func peerExitBound(*testing.T) time.Duration { return peerExitGrace + 20*time.Second }

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func startWatchedConn(t *testing.T) (conn *Conn, peer net.Conn, stalled *stalledCloseConn, exited chan struct{}) {
	t.Helper()
	hostSide, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	stalled = newStalledCloseConn(hostSide)
	exited = make(chan struct{})
	conn = NewConn("watched", stalled)
	conn.watchPeerExit(exited)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	return conn, peer, stalled, exited
}

func requireReadEndedWithEOF(t *testing.T, conn *Conn) {
	t.Helper()
	select {
	case <-conn.Done():
	case <-time.After(peerExitBound(t)):
		t.Fatalf("the read did not end within %s of the process's exit", peerExitBound(t))
	}
	var transport *TransportError
	if err := conn.failureError(); !errors.As(err, &transport) || transport.Operation != "read" || !errors.Is(err, io.EOF) {
		t.Fatalf("failure = %v, want the read's end of file, which a closed socket reports", err)
	}
}

// An extension that exits while the host waits for it to register or sits idle is seen to be gone when its process is, though the peer's close never reaches the pending read: no request is in flight, so the heartbeat does not write.
func TestExtensionExitEndsAnIdleOrLoadingRead(t *testing.T) {
	conn, peer, stalled, exited := startWatchedConn(t)
	_ = peer.Close()
	close(exited)
	requireReadEndedWithEOF(t, conn)
	if stalled.swallowed.Load() == 0 {
		t.Fatal("the seam held back no end of file: the test did not simulate the stall")
	}
}

// A request in flight when the extension exits ends with the transport error a closed socket gives it.
func TestExtensionExitEndsARequestInFlight(t *testing.T) {
	conn, peer, _, exited := startWatchedConn(t)
	answered := make(chan error, 1)
	go func() {
		resp, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "command", Tool: "noop"}})
		if err == nil && resp != nil && resp.Response != nil && resp.Response.Error != nil {
			err = errors.New(resp.Response.Error.Message)
		}
		answered <- err
	}()
	// The extension reads what the host writes, the request first, and exits without answering it.
	requestRead := make(chan struct{})
	go func() {
		var once sync.Once
		_, _ = io.Copy(writerFunc(func(p []byte) (int, error) { once.Do(func() { close(requestRead) }); return len(p), nil }), peer)
	}()
	select {
	case <-requestRead:
	case <-time.After(peerExitBound(t)):
		t.Fatal("the host did not write the request")
	}
	_ = peer.Close()
	close(exited)
	select {
	case err := <-answered:
		if err == nil || !errors.Is(conn.failureError(), io.EOF) {
			t.Fatalf("request answered with %v (transport failure %v), want the transport's end of file", err, conn.failureError())
		}
	case <-time.After(peerExitBound(t)):
		t.Fatalf("the request in flight was not ended within %s of the process's exit", peerExitBound(t))
	}
}

// What the process wrote before it exited is read first: an error it reported and then exited with is not replaced by the exit.
func TestExtensionExitDoesNotDropWhatTheProcessWrote(t *testing.T) {
	conn, peer, _, exited := startWatchedConn(t)
	close(exited)
	body := []byte(`{"type":"register","register":{"name":"written-before-exit"}}`)
	go func() {
		frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
		_, _ = peer.Write(append(frame, body...))
		_ = peer.Close()
	}()
	select {
	case env, ok := <-conn.Incoming():
		if !ok || env.Type != MsgRegister || env.Register == nil || env.Register.Name != "written-before-exit" {
			t.Fatalf("first frame = %+v (open %v), want the register the process wrote", env, ok)
		}
	case <-time.After(peerExitBound(t)):
		t.Fatal("the frame the process wrote before it exited was not delivered")
	}
	requireReadEndedWithEOF(t, conn)
}

// An extension process that exits after it has registered is reported as a crash though its socket's close never reaches the host's pending read: the host's own record of the exit ends the read, through the isolated placement's load path.
func TestIsolatedExtensionExitIsReportedWhenTheSocketCloseIsNotSeen(t *testing.T) {
	modRoot := findModuleRoot(t)
	binPath := testExtensionBinaryPath(t.TempDir(), "crash-ext")
	cmd := exec.Command("go", "build", "-o", binPath, "./coding/extension/host/subprocess/testdata/crash-ext/")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build crash fixture: %v\n%s", err, out)
	}
	shortSockDir(t)

	h := newTestHost(t)
	t.Cleanup(func() { h.Shutdown("test done") })
	h.acceptGate = func(accept func() (net.Conn, error), _ <-chan struct{}) (net.Conn, error) {
		conn, err := accept()
		if err != nil {
			return nil, err
		}
		return newStalledCloseConn(conn), nil
	}
	reported := make(chan string, 4)
	h.SetCrashHandler(func(_ string, _ time.Duration, _ bool, reason string) { reported <- reason })

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if _, err := h.Load(ctx, ExtConfig{Name: "crash", Path: binPath, Enabled: true, SupervisorConfig: SupervisorConfig{MaxCrashes: 1}}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	select {
	case reason := <-reported:
		if reason == "" {
			t.Fatal("the crash was reported with no reason")
		}
	case <-time.After(peerExitBound(t) + 10*time.Second):
		t.Fatal("the extension's exit was not reported: the host's read never ended")
	}
}
