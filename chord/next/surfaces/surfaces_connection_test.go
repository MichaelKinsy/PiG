package surfaces

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// pi: packages/chord/test/surfaces/connection.test.ts. The fake clock and timers the test advances, the wakes it sends and the
// sockets the loop opened.

type fakeTimer struct {
	at        int64
	callback  func()
	cancelled bool
}

type fakeWorld struct {
	mu      sync.Mutex
	now     int64
	online  bool
	timers  []*fakeTimer
	wake    func()
	sockets []*fakeSocket
	opens   []int64
	handle  *Connection
	env     LoopEnvironment
}

func newFakeWorld() *fakeWorld {
	w := &fakeWorld{online: true}
	w.env = LoopEnvironment{
		Now: func() int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.now },
		After: func(ms int64, callback func()) func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			timer := &fakeTimer{at: w.now + ms, callback: callback}
			w.timers = append(w.timers, timer)
			return func() { w.mu.Lock(); timer.cancelled = true; w.mu.Unlock() }
		},
		Online: func() bool { w.mu.Lock(); defer w.mu.Unlock(); return w.online },
		OnWake: func(listener func()) func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.wake = listener
			return func() { w.mu.Lock(); w.wake = nil; w.mu.Unlock() }
		},
	}
	return w
}

func (w *fakeWorld) open(events SocketEvents) LoopLink {
	w.mu.Lock()
	defer w.mu.Unlock()
	socket := &fakeSocket{events: events}
	w.sockets = append(w.sockets, socket)
	w.opens = append(w.opens, w.now)
	return socket
}

// advance advances the clock by ms, running due timers in order; the retry question settles after each.
func (w *fakeWorld) advance(ms int64) {
	w.mu.Lock()
	end := w.now + ms
	w.mu.Unlock()
	for {
		w.handle.settled()
		w.mu.Lock()
		var due *fakeTimer
		for _, t := range w.timers {
			if !t.cancelled && t.at <= end && (due == nil || t.at < due.at) {
				due = t
			}
		}
		if due == nil {
			w.mu.Unlock()
			break
		}
		w.now = due.at
		due.cancelled = true
		w.mu.Unlock()
		due.callback()
	}
	w.mu.Lock()
	w.now = end
	w.mu.Unlock()
	w.handle.settled()
}

func (w *fakeWorld) wakeUp() {
	w.mu.Lock()
	wake := w.wake
	w.mu.Unlock()
	if wake != nil {
		wake()
	}
}

func (w *fakeWorld) last() *fakeSocket {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sockets[len(w.sockets)-1]
}

func (w *fakeWorld) socketCount() int { w.mu.Lock(); defer w.mu.Unlock(); return len(w.sockets) }

func (w *fakeWorld) nowMs() int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.now }

func (w *fakeWorld) lastOpen() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.opens[len(w.opens)-1]
}

// fakeSocket is one socket and its link: the test opens, feeds and closes it.
type fakeSocket struct {
	events  SocketEvents
	mu      sync.Mutex
	closed  bool
	onClose func()
}

func (s *fakeSocket) OnClose(listener func()) { s.mu.Lock(); s.onClose = listener; s.mu.Unlock() }

func (s *fakeSocket) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	listener := s.onClose
	s.mu.Unlock()
	if listener != nil {
		listener()
	}
}

func (s *fakeSocket) isClosed() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.closed }

// loopIn starts a loop in world recording its states; retry answers CanRetry.
func loopIn(w *fakeWorld, retry func(context.Context) (bool, error)) *[]ConnectionState {
	if retry == nil {
		retry = func(context.Context) (bool, error) { return true, nil }
	}
	var mu sync.Mutex
	states := new([]ConnectionState)
	w.handle = KeepConnected(LoopOptions{
		Open:        w.open,
		OnState:     func(s ConnectionState) { mu.Lock(); *states = append(*states, s); mu.Unlock() },
		CanRetry:    retry,
		Environment: w.env,
	})
	return states
}

func TestReconnectsAfterAGrowingPauseAndReportsItsStates(t *testing.T) {
	w := newFakeWorld()
	states := loopIn(w, nil)
	defer w.handle.Stop()
	var pauses []int64
	for range 7 {
		closedAt := w.nowMs()
		w.last().Close()
		w.advance(5000)
		pauses = append(pauses, w.lastOpen()-closedAt)
	}
	if want := []int64{250, 500, 1000, 2000, 4000, 5000, 5000}; !slices.Equal(pauses, want) {
		t.Fatalf("pauses = %v, want %v", pauses, want)
	}
	if want := []ConnectionState{"connecting", "waiting", "connecting", "waiting"}; !reflect.DeepEqual((*states)[:4], want) {
		t.Fatalf("states = %v, want %v", (*states)[:4], want)
	}
}

func TestStartsThePausesAgainAfterAConnectionThatStayedOpen10s(t *testing.T) {
	w := newFakeWorld()
	states := loopIn(w, nil)
	defer w.handle.Stop()
	for range 3 {
		w.last().Close()
		w.advance(5000)
	}
	w.last().events.Opened()
	if got := (*states)[len(*states)-1]; got != Connected {
		t.Fatalf("last state = %q, want connected", got)
	}
	w.advance(StableMs)
	before := w.nowMs()
	w.last().Close()
	w.advance(5000)
	if got := w.lastOpen() - before; got != 250 {
		t.Fatalf("pause = %d, want 250", got)
	}
}

func TestWaitsTheLongestPauseWhileOfflineAndRetriesAtOnceOnAWake(t *testing.T) {
	w := newFakeWorld()
	loopIn(w, nil)
	defer w.handle.Stop()
	w.mu.Lock()
	w.online = false
	w.mu.Unlock()
	w.last().Close()
	w.advance(1000)
	if n := w.socketCount(); n != 1 {
		t.Fatalf("sockets = %d, want 1", n)
	}
	w.wakeUp()
	w.handle.settled()
	if n := w.socketCount(); n != 2 {
		t.Fatalf("sockets = %d, want 2", n)
	}
}

func TestGivesUpASocketNotOpenAfter20s(t *testing.T) {
	w := newFakeWorld()
	loopIn(w, nil)
	defer w.handle.Stop()
	first := w.last()
	w.advance(ConnectTimeoutMs - 1)
	if first.isClosed() {
		t.Fatal("closed before the timeout")
	}
	w.advance(1)
	if !first.isClosed() {
		t.Fatal("not closed at the timeout")
	}
}

func TestStopsWhenCanRetrySaysNo(t *testing.T) {
	w := newFakeWorld()
	states := loopIn(w, func(context.Context) (bool, error) { return false, nil })
	defer w.handle.Stop()
	w.last().Close()
	w.advance(60_000)
	if n := w.socketCount(); n != 1 {
		t.Fatalf("sockets = %d, want 1", n)
	}
	if got := (*states)[len(*states)-1]; got != Waiting {
		t.Fatalf("last state = %q, want waiting", got)
	}
}

func TestRetriesAtOnceWhenWokenWhileCanRetryIsDeciding(t *testing.T) {
	w := newFakeWorld()
	asked := make(chan struct{})
	answer := make(chan bool)
	loopIn(w, func(context.Context) (bool, error) {
		close(asked)
		return <-answer, nil
	})
	defer w.handle.Stop()
	w.last().Close()
	<-asked
	w.wakeUp()
	answer <- true
	w.advance(0)
	if n := w.socketCount(); n != 2 {
		t.Fatalf("sockets = %d, want 2", n)
	}
}

// A rejection of CanRetry counts as true (unreachable, try again): its error retries after the backoff.
func TestAnErrorFromCanRetryRetries(t *testing.T) {
	w := newFakeWorld()
	loopIn(w, func(context.Context) (bool, error) { return false, context.DeadlineExceeded })
	defer w.handle.Stop()
	w.last().Close()
	w.advance(250)
	if n := w.socketCount(); n != 2 {
		t.Fatalf("sockets = %d, want 2", n)
	}
}

func TestASilentSocketIsReplacedOnAWakeOnceItsNextPingIsOverdueAndOneWithRecentFramesStays(t *testing.T) {
	w := newFakeWorld()
	loopIn(w, nil)
	defer w.handle.Stop()
	first := w.last()
	first.events.Opened()
	w.advance(10_000)
	first.events.Received()
	w.wakeUp()
	w.advance(StaleMs)
	if first.isClosed() {
		t.Fatal("a socket with recent frames was replaced")
	}
	w.wakeUp()
	w.advance(StaleMs + 1000)
	if !first.isClosed() {
		t.Fatal("a silent socket stayed")
	}
}

func TestClosesTheLinkAtStopAndOpensNoOther(t *testing.T) {
	w := newFakeWorld()
	loopIn(w, nil)
	w.last().events.Opened()
	w.handle.Stop()
	if !w.last().isClosed() {
		t.Fatal("the link stayed open")
	}
	w.advance(60_000)
	if n := w.socketCount(); n != 1 {
		t.Fatalf("sockets = %d, want 1", n)
	}
}

// Stop ends the question CanRetry is deciding: its context is cancelled and no goroutine is left behind.
func TestStopCancelsAPendingCanRetry(t *testing.T) {
	w := newFakeWorld()
	asked := make(chan struct{})
	loopIn(w, func(ctx context.Context) (bool, error) {
		close(asked)
		<-ctx.Done()
		return false, ctx.Err()
	})
	w.last().Close()
	<-asked
	w.handle.Stop()
	w.advance(60_000)
	if n := w.socketCount(); n != 1 {
		t.Fatalf("sockets = %d, want 1", n)
	}
}
