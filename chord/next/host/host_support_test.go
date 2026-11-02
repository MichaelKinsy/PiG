package host

import (
	"sort"
	"sync"
	"time"
)

// fakeLoop is a deterministic eventLoop for helper tests, as vitest's fake timers are for Pi's: `fakeTimers()` fakes
// setTimeout, clearTimeout and Date from 0 (test/next/support.ts) and leaves microtasks real. The clock moves only
// through Advance and AdvanceAsync; Queue jobs run when Flush (or AdvanceAsync) runs them, as promise callbacks run at an
// await. Its state is locked (a blocking helper such as sleepOn runs on a goroutine of its own); jobs run unlocked.
type fakeLoop struct {
	mu     sync.Mutex
	now    int64
	seq    int
	timers []*fakeTimer
	queue  []func()
}

// fakeTimer is one pending timer of a fakeLoop.
type fakeTimer struct {
	loop    *fakeLoop
	at      int64
	seq     int
	job     func()
	stopped bool
	done    bool
}

var _ eventLoop = (*fakeLoop)(nil)

// newFakeLoop starts the clock at 0 ms.
func newFakeLoop() *fakeLoop { return &fakeLoop{} }

// Now is the fake Date.now().
func (l *fakeLoop) Now() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.now
}

// Queue queues a microtask for Flush.
func (l *fakeLoop) Queue(job func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queue = append(l.queue, job)
}

// AfterFunc is the fake setTimeout: Node's delay rule applies (nodeDelay), as the real Loop's.
func (l *fakeLoop) AfterFunc(d time.Duration, job func()) Timer {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	t := &fakeTimer{loop: l, at: l.now + nodeDelay(d).Milliseconds(), seq: l.seq, job: job}
	l.timers = append(l.timers, t)
	return t
}

// Stop is the fake clearTimeout.
func (t *fakeTimer) Stop() bool {
	t.loop.mu.Lock()
	defer t.loop.mu.Unlock()
	if t.stopped || t.done {
		return false
	}
	t.stopped = true
	t.loop.timers = removeTimer(t.loop.timers, t)
	return true
}

func removeTimer(timers []*fakeTimer, t *fakeTimer) []*fakeTimer {
	for i, each := range timers {
		if each == t {
			return append(timers[:i:i], timers[i+1:]...)
		}
	}
	return timers
}

// TimerCount is vi.getTimerCount(): the timers still pending.
func (l *fakeLoop) TimerCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.timers)
}

// Flush runs the queued microtasks, and those they queue, in order.
func (l *fakeLoop) Flush() {
	for {
		l.mu.Lock()
		if len(l.queue) == 0 {
			l.mu.Unlock()
			return
		}
		job := l.queue[0]
		l.queue = l.queue[1:]
		l.mu.Unlock()
		job()
	}
}

// Advance is vi.advanceTimersByTime(ms): the timers due within ms run in time order (ties in creation order) with the
// clock at their time, then the clock is at now+ms. Microtasks do not run.
func (l *fakeLoop) Advance(ms int64) { l.advance(ms, false) }

// AdvanceAsync is vi.advanceTimersByTimeAsync(ms): Advance with the microtasks flushed before each timer and at the end.
func (l *fakeLoop) AdvanceAsync(ms int64) { l.advance(ms, true) }

func (l *fakeLoop) advance(ms int64, flush bool) {
	l.mu.Lock()
	end := l.now + ms
	l.mu.Unlock()
	if flush {
		l.Flush()
	}
	for {
		l.mu.Lock()
		sort.SliceStable(l.timers, func(i, j int) bool {
			if l.timers[i].at != l.timers[j].at {
				return l.timers[i].at < l.timers[j].at
			}
			return l.timers[i].seq < l.timers[j].seq
		})
		if len(l.timers) == 0 || l.timers[0].at > end {
			l.now = end
			l.mu.Unlock()
			return
		}
		t := l.timers[0]
		l.timers = l.timers[1:]
		l.now = max(l.now, t.at)
		t.done = true
		l.mu.Unlock()
		t.job()
		if flush {
			l.Flush()
		}
	}
}
