package host

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A hold that its link handed to the host loop before the link closed can still be queued when Close takes its last
// snapshot of the objects: loop.run does not wait for the tasks posted before it, it runs first and drains them after.
// The hold then ran after the snapshot and after the connection had released everything it held, so it created an object
// nothing disposed and nothing released, and the resource open waiting for the abort a disposal raises kept waitTasks
// blocked for good (windows native-4, TestAwaitingAGetTimesOutAfterReadyTimeoutWithReadyTimeoutAndReleasesItsHold: Close
// in waitTasks for 29 minutes, the open of the Slow object's gate in its select for as long). Pi never sees a frame
// after its link closed (link.ts:82 `#deliverNow`: `if (this.#closed) return`); here the connection drops it.
//
// The state is built, not raced: a task is queued as post queues one whose drain goroutine has not been scheduled yet,
// at the point Close is in, after it closed its link and while it waits for the link's deliveries (flush), which a
// blocked delivery holds up.
func TestCloseDropsAHoldQueuedBeforeItsLinkClosed(t *testing.T) {
	gate := newRequestsLatch()
	t.Cleanup(gate.open)
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, awaitingSlow, awaitingSlowFactory(gate), ServeOptions{})
	down, _ := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	var connection *Connection
	server.run(func() { connection = server.connections.Keys()[0] })
	hold := HoldFrame{ID: 1, At: "server", Token: awaitingSlow.Description, Input: map[string]any{}}

	// A delivery that does not return holds up the flush of Close.
	inDelivery, endDelivery := make(chan struct{}), make(chan struct{})
	memoryDeliveries.then(func() {
		close(inDelivery)
		<-endDelivery
	})
	<-inDelivery
	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	waitFor(t, "Close to close the link", down.Closed)
	server.run(func() {}) // the section that closed the link, and the tasks it left, are done: Close waits for the flush
	server.mu.Lock()
	server.queue = append(server.queue, func() { connection.receive(hold) })
	server.scheduled = true
	server.mu.Unlock()
	close(endDelivery)

	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(testbudget.Wait(t)):
		gate.open() // frees the open nothing disposed, so the cleanup's Close returns
		t.Fatal("Close did not return: the hold queued before the link closed created an object nothing disposed")
	}
	var remaining int
	server.run(func() { remaining = server.objects.Len() })
	if remaining != 0 {
		t.Fatalf("%d objects left after Close", remaining)
	}
}
