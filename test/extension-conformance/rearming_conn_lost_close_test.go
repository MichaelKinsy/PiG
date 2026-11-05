package extensionconformance

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// lostCloseConn is a connection whose peer has closed, and whose first read is the one the close is not delivered to: it stays pending until its deadline, and a read posted again finds the end of file.
type lostCloseConn struct {
	net.Conn
	mu       sync.Mutex
	deadline time.Time
	reads    int
	// stallForever makes every read stall, as a peer that never closes does.
	stallForever bool
}

func (c *lostCloseConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }
func (c *lostCloseConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}
func (c *lostCloseConn) SetWriteDeadline(time.Time) error { return nil }

func (c *lostCloseConn) Read([]byte) (int, error) {
	c.mu.Lock()
	c.reads++
	first, deadline := c.reads == 1, c.deadline
	c.mu.Unlock()
	if !first && !c.stallForever {
		return 0, io.EOF
	}
	time.Sleep(time.Until(deadline))
	return 0, os.ErrDeadlineExceeded
}

// A read the peer's close was not delivered to ends with the end of file the script host waits for, in about one slice and not at the test's deadline.
func TestAScriptHostReadFindsThePeersCloseAPendingReadMissed(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	host := newRearmingConn(&lostCloseConn{Conn: client})
	if err := host.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := host.Read(make([]byte, 4)); !errors.Is(err, io.EOF) {
		t.Fatalf("read = %v, want the end of file of the closed peer", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the read took %s, want about one slice (%s) and not the test's deadline", took, peerReadSlice)
	}
}

// A peer that stays open keeps the read pending until the deadline the test set, which ends it as an unwrapped read ends: with a timeout.
func TestAScriptHostReadStillEndsAtTheTestsDeadline(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	host := newRearmingConn(&lostCloseConn{Conn: client, stallForever: true})
	if err := host.SetDeadline(time.Now().Add(1500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := host.Read(make([]byte, 4))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("read = %v, want a timeout", err)
	}
	if took := time.Since(started); took < time.Second || took > 4*time.Second {
		t.Fatalf("the read ended after %s, want the 1.5s deadline", took)
	}
}

// Bytes the peer sent are returned, not dropped by a slice that ends.
func TestAScriptHostReadReturnsWhatThePeerSentAfterASlice(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	host := newRearmingConn(client)
	if err := host.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(3 * peerReadSlice)
		_, _ = server.Write([]byte("ping"))
	}()
	got := make([]byte, 4)
	if _, err := io.ReadFull(host, got); err != nil || string(got) != "ping" {
		t.Fatalf("read %q, %v, want ping", got, err)
	}
}
