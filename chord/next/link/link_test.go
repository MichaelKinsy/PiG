package link_test

import (
	"slices"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/link/link.test.ts. `flushed()` (a macrotask) is MemoryLink.Flushed: the pair's
// queue of microtasks drained. The last test (accepting a closed link keeps no host) needs createHost and stays
// pending on lane D5.

type recorder[T any] struct {
	mu    sync.Mutex
	items []T
}

func (r *recorder[T]) add(item T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, item)
}

func (r *recorder[T]) all() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.items)
}

func TestMemoryLinks(t *testing.T) {
	t.Run("deliver in order, one task later, and queue what arrives before the listener", func(t *testing.T) {
		a, b := link.MemoryLinkPair()
		a.Send([]byte{1})
		a.Send([]byte{2})
		a.Flushed()
		var got recorder[byte]
		b.OnMessage(func(message []byte) { got.add(message[0]) })
		if !slices.Equal(got.all(), []byte{1, 2}) {
			t.Fatalf("got = %v", got.all())
		}
		// Sent now, delivered a task later: the sender's code runs first.
		var release sync.Mutex
		release.Lock()
		a.Send([]byte{3})
		a.Flushed()
		if !slices.Equal(got.all(), []byte{1, 2, 3}) {
			t.Fatalf("got = %v", got.all())
		}
		release.Unlock()
	})

	t.Run("close this end at once and the peer one task later; send after close is a no-op", func(t *testing.T) {
		a, b := link.MemoryLinkPair()
		var closes recorder[string]
		var got recorder[byte]
		a.OnClose(func() { closes.add("a") })
		b.OnClose(func() { closes.add("b") })
		b.OnMessage(func(message []byte) { got.add(message[0]) })
		a.Close()
		if !slices.Equal(closes.all(), []string{"a"}) {
			t.Fatalf("closes = %v", closes.all())
		}
		a.Send([]byte{1})
		a.Flushed()
		if !slices.Equal(closes.all(), []string{"a", "b"}) {
			t.Fatalf("closes = %v", closes.all())
		}
		b.Send([]byte{2})
		a.Flushed()
		if len(got.all()) != 0 {
			t.Fatalf("got = %v", got.all())
		}
		var late recorder[string]
		a.OnClose(func() { late.add("late") })
		if !slices.Equal(late.all(), []string{"late"}) {
			t.Fatalf("late = %v", late.all())
		}
	})

	t.Run("drop messages still in flight to a closed end", func(t *testing.T) {
		a, b := link.MemoryLinkPair()
		var got recorder[byte]
		a.OnMessage(func(message []byte) { got.add(message[0]) })
		b.Send([]byte{1})
		a.Close()
		a.Flushed()
		if len(got.all()) != 0 {
			t.Fatalf("got = %v", got.all())
		}
	})

	t.Run("stop delivering queued messages once a listener closed the link", func(t *testing.T) {
		a, b := link.MemoryLinkPair()
		var got recorder[byte]
		a.Send([]byte{1})
		a.Send([]byte{2})
		a.Flushed()
		b.OnMessage(func(message []byte) {
			got.add(message[0])
			b.Close()
		})
		if !slices.Equal(got.all(), []byte{1}) {
			t.Fatalf("got = %v", got.all())
		}
	})

	t.Run("keep no listener once closed", func(t *testing.T) {
		a, b := link.MemoryLinkPair()
		type listener struct{ calls int }
		ref := func() weak.Pointer[listener] {
			held := &listener{}
			a.OnMessage(func([]byte) { held.calls++ })
			a.OnClose(func() { held.calls++ })
			return weak.Make(held)
		}()
		a.Close()
		a.Flushed()
		nexttest.ExpectCollected(t, ref)
		_ = b
	})
}

func TestAMemoryLinkTransfersNoRacyState(t *testing.T) {
	// Sends from several goroutines to one end arrive in each sender's order; the pair's queue is drained by one
	// goroutine at a time (the Go form of one event loop).
	a, b := link.MemoryLinkPair()
	var got recorder[int]
	b.OnMessage(func(message []byte) { got.add(int(message[0])<<8 | int(message[1])) })
	var wg sync.WaitGroup
	for sender := range 4 {
		wg.Go(func() {
			for index := range 100 {
				a.Send([]byte{byte(sender), byte(index)})
			}
		})
	}
	wg.Wait()
	a.Flushed()
	next := map[int]int{}
	for _, value := range got.all() {
		sender, index := value>>8, value&0xff
		if index != next[sender] {
			t.Fatalf("sender %d: got %d, want %d", sender, index, next[sender])
		}
		next[sender]++
	}
	if len(got.all()) != 400 {
		t.Fatalf("got %d messages", len(got.all()))
	}
}

// fifoQueue is a microtask queue of the test's own: jobs run in the order queued, jobs queued meanwhile after the ones
// queued before.
type fifoQueue struct{ jobs []func() }

func (q *fifoQueue) post(job func()) { q.jobs = append(q.jobs, job) }

func (q *fifoQueue) run() {
	for len(q.jobs) > 0 {
		job := q.jobs[0]
		q.jobs = q.jobs[1:]
		job()
	}
}

// A pair on a queue of the caller's delivers in that queue's order with its other jobs: what a listener queues while it
// handles the first message runs after the second message, which was queued before it (Pi's memory link on the one
// microtask queue).
func TestMemoryLinkPairOnDeliversInTheOrderOfTheCallersQueue(t *testing.T) {
	queue := &fifoQueue{}
	a, b := link.MemoryLinkPairOn(queue.post, queue.run)
	var order []string
	b.OnMessage(func(message []byte) {
		order = append(order, string(message))
		if string(message) == "m1" {
			queue.post(func() { order = append(order, "microtask of m1's handler") })
		}
	})
	a.Send([]byte("m1"))
	a.Send([]byte("m2"))
	if len(order) != 0 {
		t.Fatalf("delivered before the queue ran: %v", order)
	}
	a.Flushed()
	if want := []string{"m1", "m2", "microtask of m1's handler"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}
