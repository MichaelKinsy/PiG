package subprocess

import (
	"context"
	"net"
	"testing"
	"time"
)

// stalledOverlayConn is a started connection whose peer never reads: the writer holds one frame in its write and the queue takes
// cap(outCh) more, after which a send waits.
func stalledOverlayConn(t *testing.T) *Conn {
	t.Helper()
	peer, local := net.Pipe()
	conn := NewConn("stalled", local)
	conn.Start(context.Background())
	t.Cleanup(func() {
		_ = peer.Close()
		_ = conn.Close("test done")
	})
	return conn
}

// burstUntilParked sends 200 invalidation reports through host on a goroutine of its own, as the goroutine that delivers a
// component's input does, and returns once a sender is waiting for room in the queue: more frames are counted than the writer and
// the queue hold. done closes when all 200 calls returned.
func burstUntilParked(t *testing.T, conn *Conn, host *customOverlayHost) (done chan struct{}) {
	t.Helper()
	done = make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			host.OnInvalidate(1)
		}
	}()
	full := int64(cap(conn.outCh) + 2)
	deadline := time.Now().Add(20 * time.Second)
	for conn.queued.Load() < full {
		select {
		case <-done:
			t.Fatal("200 reports were taken by a connection that does not read")
		case <-conn.Done():
			t.Fatal("the connection ended before its queue filled")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("no sender waited: %d frames counted, the writer and queue hold %d", conn.queued.Load(), full-1)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("the burst ended with the queue full")
	default:
	}
	return done
}

// Conn.outCh holds 64 frames and then a send waits for the writer. The goroutine that delivers a component's input sends its
// invalidation reports (customOverlayHost.OnInvalidate), so an extension that does not read can park that goroutine and nothing
// else: never the TUI's owner loop, which only counts (internal/codingagent TestRemoteOverlayInvalidateOnlyCountsOnTheOwnerLoop,
// TestRemoteOverlayInvalidationsCoalesceWhileTheConnectionHasNoRoom). The parked send ends when the connection closes.
func TestCustomOverlayInvalidateIsReleasedWhenAStalledConnectionCloses(t *testing.T) {
	conn := stalledOverlayConn(t)
	host := &customOverlayHost{ctx: context.Background(), owner: conn, key: "custom-1"}
	done := burstUntilParked(t, conn, host)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = conn.Close("the extension does not read")
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("closing the connection did not release the sender")
	}
	select {
	case <-closed:
	case <-time.After(20 * time.Second):
		t.Fatal("Close did not return")
	}
}

// The parked send also ends when the component's own context ends (the ui.custom call was cancelled or the overlay's lifetime
// ended), with the connection still open.
func TestCustomOverlayInvalidateIsReleasedWhenTheComponentsContextEnds(t *testing.T) {
	conn := stalledOverlayConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := &customOverlayHost{ctx: ctx, owner: conn, key: "custom-1"}
	done := burstUntilParked(t, conn, host)
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("ending the component's context did not release the sender")
	}
	select {
	case <-conn.Done():
		t.Fatal("the connection ended with the component's context")
	default:
	}
}
