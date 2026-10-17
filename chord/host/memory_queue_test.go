package host

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// stallHost keeps host's loop busy until the returned release is called (also at cleanup).
func stallHost(t *testing.T, host *HostImpl) (release func()) {
	t.Helper()
	busy, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(done) }) }
	t.Cleanup(release)
	go host.run(func() {
		close(busy)
		<-done
	})
	<-busy
	return release
}

// One shared queue orders memory-link deliveries (link.ts:90, 104-106), but a delivery only hands its frame to the
// receiving host's loop: a host whose loop is busy holds up neither the queue nor other hosts' links.
func TestMemoryLinksDeliverWhileAnotherReceiverHostIsBusy(t *testing.T) {
	stalled := requestsHost(t, HostOptions{Name: "stalled"})
	down, raw := MemoryLinkPair()
	if err := stalled.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	release := stallHost(t, stalled)
	_, _ = raw.Send(PingFrame{N: 1})

	server := requestsHost(t, HostOptions{Name: "server"})
	serveCounter(t, server, nil)
	down2, up2 := MemoryLinkPair()
	if err := server.Accept(down2, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up2, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	added := make(chan any, 1)
	go func() {
		counter, err := browser.Hold(linkCounter, map[string]any{"name": "c"}, nil)
		if err != nil {
			added <- err
			return
		}
		value, err := callMember(counter, "add", float64(1), nil)
		if err != nil {
			added <- err
			return
		}
		added <- value
	}()
	select {
	case value := <-added:
		if value != float64(1) {
			t.Fatalf("add = %v", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a busy receiver host held up another pair of hosts")
	}
	release()
}

// MemoryLink.flush returns once the peer host handled what was sent (Pi: the microtasks a closing host awaits,
// link.ts:103-112): it waits for the receiving host's loop, not only for the shared queue's hand-off.
func TestMemoryLinkFlushWaitsForThePeerHostToHandleFrames(t *testing.T) {
	host := requestsHost(t, HostOptions{Name: "server"})
	down, raw := MemoryLinkPair()
	if err := host.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	raw.Receive(func(Frame) {})
	release := stallHost(t, host)
	// A malformed frame makes the server's connection close its end (connection.ts:383).
	_, _ = raw.Send(MalformedFrame{Value: chordjson.ObjectOf("t", "call", "hold", float64(1))})
	flushed := make(chan struct{})
	go func() {
		raw.flush()
		close(flushed)
	}()
	select {
	case <-flushed:
		t.Fatal("flush returned while the receiving host had not handled the frame")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	<-flushed
	if !down.Closed() {
		t.Fatal("the server had not handled the frame when flush returned")
	}
}
