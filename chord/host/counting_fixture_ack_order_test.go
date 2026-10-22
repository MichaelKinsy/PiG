package host

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// ackGateLink is a Link that takes each acknowledgement through a gate the test opens, and reports when one went in and
// when it came out: the order they come out in is the order a peer receives them.
type ackGateLink struct {
	mu        sync.Mutex
	gates     map[int]*requestsLatch
	entered   chan int
	committed chan int
}

func newAckGateLink() *ackGateLink {
	return &ackGateLink{gates: map[int]*requestsLatch{}, entered: make(chan int, 8), committed: make(chan int, 8)}
}

func (link *ackGateLink) gate(n int) *requestsLatch {
	link.mu.Lock()
	defer link.mu.Unlock()
	if link.gates[n] == nil {
		link.gates[n] = newRequestsLatch()
	}
	return link.gates[n]
}

func (link *ackGateLink) Send(frame Frame) (int, error) {
	if ack, ok := frame.(AckFrame); ok {
		link.entered <- ack.N
		link.gate(ack.N).wait()
		link.committed <- ack.N
	}
	return 0, nil
}

func (link *ackGateLink) Closed() bool { return false }

func (link *ackGateLink) Receive(func(Frame)) {}

func (link *ackGateLink) OnClose(func(reason string)) {}

func (link *ackGateLink) Close(...string) {}

// A peer takes acknowledgements as cumulative counts: a smaller one after a larger one is a protocol violation, and it
// closes the link ("ack 192 outside 193..194"; TestFlowUnitsWaitingOps... timed out on it once in about 12000 runs). The
// acknowledgement the paused link held is the older one, so releasing the link sends it before an acknowledgement that
// is sent while it is still on its way out.
func TestCountingLinkReleaseSendsTheHeldAcknowledgementBeforeALaterOne(t *testing.T) {
	inner := newAckGateLink()
	link := newCountingLink(inner)
	link.pause()
	if _, err := link.Send(AckFrame{N: 192}); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		link.release()
		close(released)
	}()
	if got := <-inner.entered; got != 192 {
		t.Fatalf("release sent acknowledgement %d, want the held 192", got)
	}
	// The held acknowledgement is in flight. A later one is sent now.
	later := make(chan struct{})
	go func() {
		_, _ = link.Send(AckFrame{N: 193})
		close(later)
	}()
	var order []int
	overtaking := 0
	select {
	case overtaking = <-inner.entered:
	case <-time.After(50 * time.Millisecond):
	}
	if overtaking != 0 {
		// The later acknowledgement went in beside the held one: the transport may carry it first.
		inner.gate(overtaking).open()
		order = append(order, <-inner.committed)
	}
	inner.gate(192).open()
	order = append(order, <-inner.committed)
	if overtaking == 0 {
		inner.gate(193).open()
		order = append(order, <-inner.committed)
	}
	<-released
	<-later
	if !slices.Equal(order, []int{192, 193}) {
		t.Fatalf("the peer received acknowledgements %v, want [192 193]", order)
	}
}
