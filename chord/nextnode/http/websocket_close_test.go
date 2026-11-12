package http

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Go-only regression tests of the socket's close: no upstream test file. Pi's `ws` has no lock in its close: terminate()
// destroys the connection, its `close` event is emitted on a later turn (never inside terminate()), and a second terminate()
// never waits for it. A host's loop is held by the caller of Send, and a `close` listener (the link's end) takes that loop;
// a closer that waited for another closer's listeners closed the cycle (loop -> close -> loop).

// closeCycleBound is how long a closer that must not wait is given. Only a hang uses it up: the test releases the listener it
// blocked first, so a red run ends cleanly.
const closeCycleBound = 5 * time.Second

// returnsBefore reports whether run returned before release was resolved (within closeCycleBound).
func returnsBefore(run func(), release *gate) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		run()
	}()
	select {
	case <-done:
		return true
	case <-time.After(closeCycleBound):
		release.resolve()
		<-done
		return false
	}
}

// A close listener that blocks holds the closer that runs it; a second closer returns at once instead of waiting for it.
func TestWebSocketASecondCloserNeverWaitsForTheFirstClosersListeners(t *testing.T) {
	socket := serverSideSocket(t)
	entered, release := newGate(), newGate()
	t.Cleanup(release.resolve)
	var events []CloseEvent
	var mu sync.Mutex
	socket.OnClose(func(event CloseEvent) {
		entered.resolve()
		<-release.done()
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	first := make(chan struct{})
	go func() {
		defer close(first)
		socket.finish(CloseEvent{Code: 1000, Reason: "bye"})
	}()
	receive(t, entered.done(), "the first closer reaching its close listener")

	if !returnsBefore(func() { socket.closed(CloseEvent{Code: 1006}) }, release) {
		t.Fatal("a second closed() waited for the first closer's close listener")
	}
	select {
	case <-first:
		t.Fatal("the first closer returned before its listener was released")
	default:
	}
	release.resolve()
	receive(t, first, "the first closer returning")
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0] != (CloseEvent{Code: 1000, Reason: "bye"}) {
		t.Fatalf("close events = %+v, want the first closer's alone", events)
	}
}

// Pi's terminate() never emits `close` inside the call: the caller may hold a lock (the host's loop) that a close listener
// takes. The event follows on a later turn.
func TestWebSocketTerminateEmitsItsCloseOnALaterTurn(t *testing.T) {
	socket := serverSideSocket(t)
	entered, release := newGate(), newGate()
	t.Cleanup(release.resolve)
	events := make(chan CloseEvent, 2)
	socket.OnClose(func(event CloseEvent) {
		entered.resolve()
		<-release.done()
		events <- event
	})
	if !returnsBefore(socket.Terminate, release) {
		t.Fatal("Terminate ran the close listeners inside the call")
	}
	if got := socket.ReadyState(); got != WebSocketClosedState {
		t.Fatalf("ReadyState after Terminate = %d, want %d", got, WebSocketClosedState)
	}
	receive(t, entered.done(), "the close listener, on a later turn")
	release.resolve()
	if got := receive(t, events, "the close event"); got != (CloseEvent{Code: 1006}) {
		t.Fatalf("close event = %+v, want code 1006", got)
	}
	socket.Terminate()
	socket.closed(CloseEvent{Code: 1006})
	select {
	case got := <-events:
		t.Fatalf("a second close event %+v", got)
	default:
	}
}

// Whoever closes: `close` is emitted exactly once, what was queued fails with `WebSocket is not open` before it, and the
// close timer is stopped.
func TestWebSocketCloseEmitsOnceFailsWhatIsQueuedAndStopsTheTimer(t *testing.T) {
	socket := serverSideSocket(t)
	var fired atomic.Bool
	socket.mu.Lock()
	socket.timer = time.AfterFunc(time.Hour, func() { fired.Store(true) })
	var failures [2]error
	var failed atomic.Int32
	for i := range failures {
		socket.queue = append(socket.queue, queued{frame: 2, data: []byte("x"), byteCount: 1, done: func(err error) {
			failures[i] = err
			failed.Add(1)
		}})
	}
	socket.buffered = 2
	socket.mu.Unlock()
	var closes atomic.Int32
	closeSeen := make(chan int32, 4)
	socket.OnClose(func(CloseEvent) {
		// What was queued failed before the event.
		closeSeen <- failed.Load()
		closes.Add(1)
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { socket.closed(CloseEvent{Code: 1006}) })
	}
	wg.Go(func() { socket.finish(CloseEvent{Code: 1000}) })
	wg.Wait()
	if got := receive(t, closeSeen, "the close event"); got != 2 {
		t.Fatalf("%d queued jobs failed before the close event, want 2", got)
	}
	if got := closes.Load(); got != 1 {
		t.Fatalf("close emitted %d times, want once", got)
	}
	for i, err := range failures {
		if err == nil || err.Error() != "WebSocket is not open" {
			t.Fatalf("queued job %d failed with %v, want WebSocket is not open", i, err)
		}
	}
	if got := socket.BufferedAmount(); got != 0 {
		t.Fatalf("BufferedAmount = %d after the close, want 0", got)
	}
	if socket.timer.Stop() {
		t.Fatal("the close timer was still pending after the close")
	}
	if fired.Load() {
		t.Fatal("the close timer fired")
	}
}

// The cycle of the p2 stress hang, at the boundary where it closed: the goroutine that holds the host's loop sends on the link,
// the limited socket cuts a client that stopped reading (Terminate), while the socket's reader has begun closing it and its
// close listener (the link's end, which runs hostLink.Closed on the loop) waits for the loop. The sender must leave its section.
func TestWebSocketLinkSendTerminatingUnderTheLoopWhileTheReaderClosesDoesNotDeadlock(t *testing.T) {
	socket := serverSideSocket(t)
	loop := host.NewLoop()
	readerClosing := newGate()
	// Registered first: it runs when the closer has begun, before the link's listener asks for the loop.
	socket.OnClose(func(CloseEvent) { readerClosing.resolve() })
	limit := limitedSocket(socket)
	end := link.WebSocketLink(limit)
	linkClosed := newGate()
	end.OnClose(func() {
		// hostLink.Closed: a section of the host's loop.
		loop.Run(linkClosed.resolve)
	})
	inLoop := newGate()

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		// hostLink.Message: a section of the loop whose work sends on the link.
		loop.Run(func() {
			inLoop.resolve()
			<-readerClosing.done()
			// The closer dropped what was queued; a client that stopped reading has queued more since.
			socket.mu.Lock()
			socket.buffered = maxUnsentBytes + 1
			socket.mu.Unlock()
			// The link end's own Send is a no-op once its close began (it checks its socket first); the send of this section is
			// past that check, as the stress hang's was.
			_ = limit.Send([]byte("a frame"))
		})
	}()
	receive(t, inLoop.done(), "the sender in the loop")
	go socket.closed(CloseEvent{Code: 1006})

	select {
	case <-sent:
	case <-time.After(closeCycleBound):
		t.Fatal("the section holding the loop never returned: its send waited for the close the reader runs, which waits for the loop")
	}
	receive(t, linkClosed.done(), "the link's close, on the loop")
}
