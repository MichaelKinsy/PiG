package surfaces

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

// pi: packages/chord/test/surfaces/connection.test.ts (Pi 21e4cb80d). The fake clock and timers the test advances, the wakes it
// sends and the sockets the loop opened. Each test runs in a synctest bubble: Pi's `await settled()` (the promises settle) is
// synctest.Wait (every goroutine of the loop, the retry question's too, has run until it waits), so a question the test holds
// open stays open while the clock advances, as Pi's unresolved promise does.

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

// settled is Pi's `settled()`: what the loop was given has been handled (an answer of CanRetry has reached it).
func settled() { synctest.Wait() }

// advance advances the clock by ms, running due timers in order; the loop settles after each.
func (w *fakeWorld) advance(ms int64) {
	w.mu.Lock()
	end := w.now + ms
	w.mu.Unlock()
	for {
		settled()
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
	settled()
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

// openAt is when the loop opened its i-th socket (Pi's `world.opens[i]`).
func (w *fakeWorld) openAt(i int) int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.opens[i] }

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
	// beforeClose runs at the first Close, before the close listener: a socket goroutine whose open lands as the link closes.
	beforeClose func()
}

func (s *fakeSocket) OnClose(listener func()) { s.mu.Lock(); s.onClose = listener; s.mu.Unlock() }

func (s *fakeSocket) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	listener, before := s.onClose, s.beforeClose
	s.mu.Unlock()
	if before != nil {
		before()
	}
	if listener != nil {
		listener()
	}
}

func (s *fakeSocket) isClosed() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.closed }

// loopStates are the states a loop reported, in order.
type loopStates struct {
	mu   sync.Mutex
	list []ConnectionState
}

func (s *loopStates) add(state ConnectionState) {
	s.mu.Lock()
	s.list = append(s.list, state)
	s.mu.Unlock()
}

func (s *loopStates) all() []ConnectionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// last is Pi's `states.at(-1)`.
func (s *loopStates) last() ConnectionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list[len(s.list)-1]
}

// loopIn starts a loop in world recording its states; retry answers CanRetry.
func loopIn(w *fakeWorld, retry func(context.Context) (bool, error)) *loopStates {
	if retry == nil {
		retry = func(context.Context) (bool, error) { return true, nil }
	}
	states := new(loopStates)
	w.handle = KeepConnected(LoopOptions{
		Open:        w.open,
		OnState:     states.add,
		CanRetry:    retry,
		Environment: w.env,
	})
	return states
}

// questions are CanRetry's questions the test answers itself (Pi's `new Promise((resolve) => answers.push(resolve))`); a
// question nobody answered ends with the loop's context.
type questions struct {
	mu      sync.Mutex
	answers []chan questionAnswer
}

type questionAnswer struct {
	retry bool
	err   error
}

func (q *questions) canRetry(ctx context.Context) (bool, error) {
	answer := make(chan questionAnswer)
	q.mu.Lock()
	q.answers = append(q.answers, answer)
	q.mu.Unlock()
	select {
	case a := <-answer:
		return a.retry, a.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (q *questions) count() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.answers) }

// answer answers the i-th question (Pi's `answers[i](retry)`).
func (q *questions) answer(i int, a questionAnswer) {
	q.mu.Lock()
	answer := q.answers[i]
	q.mu.Unlock()
	answer <- a
}

// describe("reconnecting")

func TestReconnectsAfterAGrowingPauseAndReportsItsStates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		if got, want := states.all()[:4], []ConnectionState{"connecting", "waiting", "connecting", "waiting"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("states = %v, want %v", got, want)
		}
	})
}

// Cites packages/chord/src/surfaces/connection.ts:31 (SocketEvents.opened).
func TestStartsThePausesAgainAfterAConnectionThatStayedOpen10s(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		states := loopIn(w, nil)
		defer w.handle.Stop()
		for range 3 {
			w.last().Close()
			w.advance(5000)
		}
		w.last().events.Opened()
		if got := states.last(); got != Connected {
			t.Fatalf("last state = %q, want connected", got)
		}
		w.advance(StableMs)
		before := w.nowMs()
		w.last().Close()
		w.advance(5000)
		if got := w.lastOpen() - before; got != 250 {
			t.Fatalf("pause = %d, want 250", got)
		}
	})
}

func TestWaitsTheLongestPauseWhileOfflineAndRetriesAtOnceOnAWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		settled()
		if n := w.socketCount(); n != 2 {
			t.Fatalf("sockets = %d, want 2", n)
		}
	})
}

func TestGivesUpASocketNotOpenAfter20s(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

func TestStopsWhenCanRetrySaysNo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		states := loopIn(w, func(context.Context) (bool, error) { return false, nil })
		defer w.handle.Stop()
		w.last().Close()
		w.advance(60_000)
		if n := w.socketCount(); n != 1 {
			t.Fatalf("sockets = %d, want 1", n)
		}
		if got := states.last(); got != Waiting {
			t.Fatalf("last state = %q, want waiting", got)
		}
	})
}

// pi: "does not wait for canRetry: the next attempt goes ahead; a late no after it opened closes it, reported waiting" (Pi
// 21e4cb80d; it replaced "retries at once when woken while canRetry is deciding").
func TestDoesNotWaitForCanRetryTheNextAttemptGoesAheadALateNoAfterItOpenedClosesItReportedWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		var asked questions
		states := loopIn(w, asked.canRetry)
		defer w.handle.Stop()
		w.last().Close()
		w.advance(250)
		if n := w.socketCount(); n != 2 {
			t.Fatalf("sockets = %d, want 2", n)
		}
		w.last().events.Opened()
		if got := states.last(); got != Connected {
			t.Fatalf("last state = %q, want connected", got)
		}
		asked.answer(0, questionAnswer{retry: false})
		w.advance(60_000)
		if n, closed, state := w.socketCount(), w.last().isClosed(), states.last(); n != 2 || !closed || state != Waiting {
			t.Fatalf("sockets = %d, last closed = %v, last state = %q, want 2, true, waiting", n, closed, state)
		}
	})
}

// pi: "asks canRetry one question at a time: closes while it is open ask nothing more; the next close asks again" (Pi
// 21e4cb80d).
func TestAsksCanRetryOneQuestionAtATimeClosesWhileItIsOpenAskNothingMoreTheNextCloseAsksAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		var asked questions
		loopIn(w, asked.canRetry)
		defer w.handle.Stop()
		for range 8 {
			w.last().Close()
			w.advance(5000)
		}
		if sockets, answers := w.socketCount(), asked.count(); sockets != 9 || answers != 1 {
			t.Fatalf("sockets = %d, questions = %d, want 9 and 1", sockets, answers)
		}
		asked.answer(0, questionAnswer{retry: true})
		settled()
		w.last().Close()
		settled()
		if answers := asked.count(); answers != 2 {
			t.Fatalf("questions = %d, want 2", answers)
		}
	})
}

// pi: "retries at once a drop soon after a wake (the socket that was away), once per wake" (Pi 21e4cb80d).
func TestRetriesAtOnceADropSoonAfterAWakeTheSocketThatWasAwayOncePerWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		loopIn(w, nil)
		defer w.handle.Stop()
		w.last().events.Opened()
		w.advance(StableMs)
		// Back from the background: the wake comes first, then the close the frozen page had not handled yet.
		w.wakeUp()
		w.last().Close()
		w.advance(0)
		if n := w.socketCount(); n != 2 {
			t.Fatalf("sockets = %d, want 2", n)
		}
		// The server is down: the next drop waits the growing pause, no tight loop.
		closedAt := w.nowMs()
		w.last().Close()
		w.advance(5000)
		if got := w.lastOpen() - closedAt; got != 500 {
			t.Fatalf("pause = %d, want 500", got)
		}
	})
}

// A rejection of CanRetry counts as true (unreachable, try again): its error retries after the backoff.
func TestAnErrorFromCanRetryRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		loopIn(w, func(context.Context) (bool, error) { return false, context.DeadlineExceeded })
		defer w.handle.Stop()
		w.last().Close()
		w.advance(250)
		if n := w.socketCount(); n != 2 {
			t.Fatalf("sockets = %d, want 2", n)
		}
	})
}

// A late rejection of CanRetry (the next link already open) stops nothing, and the question is closed: the next close asks again.
// Cites packages/chord/src/surfaces/connection.ts:181 at Pi 21e4cb80d (`canRetry().then(answered, () => answered(true))`).
func TestALateErrorFromCanRetryKeepsTheOpenLinkAndTheNextCloseAsksAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		var asked questions
		states := loopIn(w, asked.canRetry)
		defer w.handle.Stop()
		w.last().Close()
		w.advance(250)
		w.last().events.Opened()
		asked.answer(0, questionAnswer{err: errors.New("unreachable")})
		w.advance(60_000)
		if n, closed, state := w.socketCount(), w.last().isClosed(), states.last(); n != 2 || closed || state != Connected {
			t.Fatalf("sockets = %d, last closed = %v, last state = %q, want 2, false, connected", n, closed, state)
		}
		w.last().Close()
		settled()
		if answers := asked.count(); answers != 2 {
			t.Fatalf("questions = %d, want 2", answers)
		}
	})
}

// A late no while the next attempt is still connecting ends that attempt: it is reported waiting once, its link is closed
// before the report, and the loop stays stopped (no retry, no wake).
// Cites packages/chord/src/surfaces/connection.ts:144 at Pi 21e4cb80d (stop: `if (attempt !== undefined) this.#options.onState("waiting")`).
func TestALateNoWhileTheNextAttemptIsConnectingClosesItThenReportsWaitingAndTheLoopStaysStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		var asked questions
		states := loopIn(w, asked.canRetry)
		defer w.handle.Stop()
		w.last().Close()
		w.advance(250)
		second := w.last()
		// The stop runs on the goroutine that brought the answer.
		atClose := make(chan []ConnectionState, 1)
		second.beforeClose = func() { atClose <- states.all() }
		asked.answer(0, questionAnswer{retry: false})
		settled()
		var got []ConnectionState
		select {
		case got = <-atClose:
		default:
		}
		if want := []ConnectionState{Connecting, Waiting, Connecting}; !reflect.DeepEqual(got, want) {
			t.Fatalf("states when the link closed = %v, want %v (closed first, then reported)", got, want)
		}
		if got, want := states.all(), []ConnectionState{Connecting, Waiting, Connecting, Waiting}; !second.isClosed() || !reflect.DeepEqual(got, want) {
			t.Fatalf("closed = %v, states = %v, want true and %v", second.isClosed(), got, want)
		}
		w.wakeUp()
		w.advance(60_000)
		if n, got := w.socketCount(), states.all(); n != 2 || len(got) != 4 {
			t.Fatalf("sockets = %d, states = %v, want 2 sockets and no further state", n, got)
		}
	})
}

// A drop is "soon after a wake" up to STALE_MS + WAKE_GRACE_MS after it (the stale check's reach); one millisecond later it
// waits the pause.
// Cites packages/chord/src/surfaces/connection.ts:163 at Pi 21e4cb80d (`now - this.#wokenAt <= STALE_MS + WAKE_GRACE_MS`).
func TestADropCountsAsSoonAfterAWakeForTheStaleChecksReachAndNoLonger(t *testing.T) {
	for _, each := range []struct {
		name  string
		after int64
		pause int64
	}{
		{"at STALE_MS + WAKE_GRACE_MS: at once", StaleMs + WakeGraceMs, 0},
		{"one millisecond later: the first pause", StaleMs + WakeGraceMs + 1, RetryInitialMs},
	} {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := newFakeWorld()
				loopIn(w, nil)
				defer w.handle.Stop()
				w.last().events.Opened()
				w.advance(StableMs)
				w.wakeUp()
				// A frame after the wake: the stale check the wake set finds the socket alive and leaves it open.
				w.advance(10_000)
				w.last().events.Received()
				w.advance(each.after - 10_000)
				if w.last().isClosed() {
					t.Fatal("the stale check closed a socket with a recent frame")
				}
				closedAt := w.nowMs()
				w.last().Close()
				w.advance(5000)
				if n, pause := w.socketCount(), w.lastOpen()-closedAt; n != 2 || pause != each.pause {
					t.Fatalf("sockets = %d, pause = %d, want 2 and %d", n, pause, each.pause)
				}
			})
		})
	}
}

// One wake of a client whose link was away, with a slow CanRetry (a page's base check over HTTP on a network just woken, 3 s
// here): the next attempt goes at the wake's close, beside the one check, not after it.
func TestOneWakeWithASlowCheckTheNextAttemptGoesAtOnceBesideTheOneCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		var asked questions
		states := loopIn(w, asked.canRetry)
		defer w.handle.Stop()
		w.last().events.Opened()
		w.advance(StableMs)
		w.wakeUp()
		woke := w.nowMs()
		w.last().Close()
		w.advance(3000)
		checks := asked.count()
		asked.answer(0, questionAnswer{retry: true})
		w.advance(5000)
		attempts := w.socketCount() - 1
		t.Logf("one wake, check answered after 3000 ms: next attempt %d ms after the wake, attempts %d, checks %d, states %v", w.openAt(1)-woke, attempts, checks, states.all())
		if at := w.openAt(1) - woke; at != 0 || attempts != 1 || checks != 1 {
			t.Fatalf("next attempt %d ms after the wake, attempts = %d, checks = %d, want 0, 1, 1", at, attempts, checks)
		}
	})
}

// describe("a silent socket")

// Cites packages/chord/src/surfaces/connection.ts:33 (SocketEvents.received).
func TestASilentSocketIsReplacedOnAWakeOnceItsNextPingIsOverdueAndOneWithRecentFramesStays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		// Its replacement goes at once (at the second wake's check, 1 s after it), not after a pause.
		if got, want := w.openAt(1), int64(10_000+StaleMs+1000); got != want {
			t.Fatalf("the replacement opened at %d, want %d", got, want)
		}
	})
}

func TestClosesTheLinkAtStopAndOpensNoOther(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
	})
}

// Stop reports the attempt it ended as waiting, once, after closing its link; a stop between attempts (the last state already
// waiting) and a second stop report nothing.
// Cites packages/chord/src/surfaces/connection.ts:144 at Pi 21e4cb80d (stop: `if (attempt !== undefined) this.#options.onState("waiting")`).
func TestStopReportsTheAttemptItEndedAsWaitingOnceAndNothingBetweenAttempts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		states := loopIn(w, nil)
		socket := w.last()
		socket.events.Opened()
		var atClose []ConnectionState
		socket.beforeClose = func() { atClose = states.all() }
		w.handle.Stop()
		w.handle.Stop()
		w.advance(60_000)
		if want := []ConnectionState{Connecting, Connected}; !reflect.DeepEqual(atClose, want) {
			t.Fatalf("states when the link closed = %v, want %v (closed first, then reported)", atClose, want)
		}
		if got, want := states.all(), []ConnectionState{Connecting, Connected, Waiting}; !reflect.DeepEqual(got, want) {
			t.Fatalf("states = %v, want %v", got, want)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		states := loopIn(w, nil)
		w.last().Close()
		settled()
		w.handle.Stop()
		w.advance(60_000)
		if got, want := states.all(), []ConnectionState{Connecting, Waiting}; w.socketCount() != 1 || !reflect.DeepEqual(got, want) {
			t.Fatalf("sockets = %d, states = %v, want 1 and %v", w.socketCount(), got, want)
		}
	})
}

// An open that lands as the loop stops (queued before the close of its link) is not reported: the loop ends with the waiting
// its stop reports for the attempt. Pi's LoopLink close ends synchronously, so it cannot reach this; a Go socket's goroutine can.
func TestAnOpenLandingAsTheLoopStopsReportsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newFakeWorld()
		states := loopIn(w, nil)
		socket := w.last()
		socket.beforeClose = socket.events.Opened
		w.handle.Stop()
		if !socket.isClosed() {
			t.Fatal("the link stayed open")
		}
		w.advance(60_000)
		if got, want := states.all(), []ConnectionState{Connecting, Waiting}; !reflect.DeepEqual(got, want) {
			t.Fatalf("states = %v, want %v (no connected)", got, want)
		}
	})
}

// Stop ends the question CanRetry is deciding: its context is cancelled and no goroutine is left behind.
func TestStopCancelsAPendingCanRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		noSurfacesGoroutines(t)
	})
}
