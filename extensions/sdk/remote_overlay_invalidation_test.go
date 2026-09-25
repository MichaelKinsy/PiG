package sdk

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type invalidatingRemoteComponent struct {
	mu         sync.Mutex
	invalidate func()
	previous   func()
	renders    atomic.Int32
	started    chan struct{}
	release    chan struct{}
	startOnce  sync.Once
}

func (c *invalidatingRemoteComponent) Render(int) []string {
	render := c.renders.Add(1)
	if render == 1 && c.started != nil {
		c.startOnce.Do(func() { close(c.started) })
		<-c.release
	}
	return []string{"frame"}
}

func (*invalidatingRemoteComponent) HandleInput(string) (RemoteComponentResult, error) {
	return RemoteComponentResult{}, nil
}

func (c *invalidatingRemoteComponent) SetInvalidate(fn func()) {
	c.mu.Lock()
	if c.invalidate != nil {
		c.previous = c.invalidate
	}
	c.invalidate = fn
	c.mu.Unlock()
}

func (c *invalidatingRemoteComponent) requestRender() {
	c.mu.Lock()
	fn := c.invalidate
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (c *invalidatingRemoteComponent) requestDetachedRender() {
	c.mu.Lock()
	fn := c.previous
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func drainSDKFrames(conn net.Conn, done <-chan struct{}) {
	for {
		if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			return
		}
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			select {
			case <-done:
				return
			default:
				continue
			}
		}
		size := binary.BigEndian.Uint32(header[:])
		if _, err := io.CopyN(io.Discard, conn, int64(size)); err != nil {
			return
		}
	}
}

type panickingInvalidator struct{}

func (*panickingInvalidator) Render(int) []string { return nil }
func (*panickingInvalidator) HandleInput(string) (RemoteComponentResult, error) {
	return RemoteComponentResult{}, nil
}
func (*panickingInvalidator) SetInvalidate(func()) { panic("attach failed") }

func TestRemoteOverlayInvalidatorPanicIsContained(t *testing.T) {
	overlay := newRemoteOverlay(&panickingInvalidator{})
	err := overlay.start(nil, "custom-1", func() int { return 80 })
	if err == nil || err.Error() != "attach focused invalidation: attach failed" {
		t.Fatalf("start error = %v", err)
	}
	if !overlay.stop() {
		t.Fatal("overlay with failed invalidation attachment did not stop")
	}
}

func BenchmarkRemoteOverlayInvalidationCoalesced(b *testing.B) {
	overlay := &remoteOverlay{invalidate: make(chan struct{}, 1)}
	overlay.active.Store(true)
	overlay.requestRender()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		overlay.requestRender()
	}
}

func TestRemoteOverlayInvalidationCoalescesAndStopsBeforeDisposal(t *testing.T) {
	component := &invalidatingRemoteComponent{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	extensionConn, hostConn := net.Pipe()
	t.Cleanup(func() { _ = extensionConn.Close() })
	t.Cleanup(func() { _ = hostConn.Close() })
	conn := newConn(extensionConn)
	overlay := newRemoteOverlay(component)
	drainDone := make(chan struct{})
	defer close(drainDone)
	go drainSDKFrames(hostConn, drainDone)

	if err := overlay.start(conn, "custom-1", func() int { return 80 }); err != nil {
		t.Fatal(err)
	}
	component.requestRender()
	select {
	case <-component.started:
	case <-time.After(time.Second):
		t.Fatal("invalidation did not start a render")
	}

	for range 1000 {
		component.requestRender()
	}
	close(component.release)
	deadline := time.Now().Add(time.Second)
	for component.renders.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	overlay.stop()

	if got := component.renders.Load(); got != 2 {
		t.Fatalf("render count after an in-flight burst = %d, want exactly 2 coalesced renders", got)
	}
	component.requestDetachedRender()
	time.Sleep(20 * time.Millisecond)
	if got := component.renders.Load(); got != 2 {
		t.Fatalf("detached invalidation rendered after stop: count=%d", got)
	}
}
