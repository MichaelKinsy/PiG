package host

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// The host's event loop. Pi runs a host, its objects, references, links and routines on one JavaScript event loop: a
// synchronous section is atomic, queueMicrotask jobs run after it in order, a setTimeout callback is a later section, and
// Date.now() is unix milliseconds. Loop is the Go form of that loop (DESIGN-go-layout.md section 3). It ports no Pi file.

// Timer is a pending AfterFunc: Stop is clearTimeout.
type Timer interface {
	// Stop prevents the timer's job from running; it reports whether it was still pending.
	Stop() bool
}

// eventLoop is what the helpers of this package ask of the host's loop: the clock, the microtask queue and the timers.
// *Loop is the host's; a test drives virtual time through its own (fakeLoop, as vitest's fake timers).
type eventLoop interface {
	// Now is Date.now(): unix milliseconds.
	Now() int64
	// Queue is queueMicrotask: job runs after the running section, in order.
	Queue(job func())
	// AfterFunc is setTimeout: job runs as a section of the loop once d has elapsed, unless the timer is stopped first.
	AfterFunc(d time.Duration, job func()) Timer
}

// Loop is a host's event loop: one section at a time, a microtask queue drained at the end of each section, and timers
// whose jobs run as sections. The zero value is not usable; NewLoop makes one.
type Loop struct {
	// exec is held by the goroutine running a section.
	exec  sync.Mutex
	owner atomic.Uint64

	mu    sync.Mutex
	queue []func()
	// scheduled is set while a goroutine is on its way to drain a queue filled from outside the loop.
	scheduled bool

	tasks sync.WaitGroup
}

// NewLoop returns an idle loop.
func NewLoop() *Loop { return &Loop{} }

var _ eventLoop = (*Loop)(nil)

// onLoop reports whether the caller is running a section of the loop.
func (l *Loop) onLoop() bool { return l.owner.Load() == goroutineid.Current() }

// Run runs work as one synchronous section: inline when the caller is already on the loop (nested synchronous calls
// nest, as in Pi), otherwise once the loop is free. The microtasks queued meanwhile run before an outermost Run returns.
func (l *Loop) Run(work func()) {
	id := goroutineid.Current()
	if l.owner.Load() == id {
		work()
		return
	}
	l.exec.Lock()
	l.owner.Store(id)
	defer func() {
		l.owner.Store(0)
		l.exec.Unlock()
	}()
	work()
	l.drain()
}

// Queue is queueMicrotask: job runs after the running section, before the loop takes another, in queueing order. It is
// safe from any goroutine; from outside the loop the job runs on a section of its own.
func (l *Loop) Queue(job func()) {
	l.mu.Lock()
	l.queue = append(l.queue, job)
	start := !l.scheduled && !l.onLoop()
	if start {
		l.scheduled = true
	}
	l.mu.Unlock()
	if start {
		l.Go(func() { l.Run(func() {}) })
	}
}

// drain runs the queued jobs until none is left. The caller runs a section.
func (l *Loop) drain() {
	for {
		l.mu.Lock()
		if len(l.queue) == 0 {
			l.scheduled = false
			l.mu.Unlock()
			return
		}
		job := l.queue[0]
		l.queue[0] = nil
		l.queue = l.queue[1:]
		l.mu.Unlock()
		job()
	}
}

// Go runs fn as an owned background task (an intentionally unawaited Promise of Pi): Wait joins it.
func (l *Loop) Go(fn func()) {
	//portlint:allow golifetime joined by Loop.Wait through l.tasks; the host's close waits for its background tasks
	l.tasks.Go(fn)
}

// Wait blocks until every task started with Go (and every timer job already running) has returned.
func (l *Loop) Wait() { l.tasks.Wait() }

// Now is Date.now(): unix milliseconds.
func (l *Loop) Now() int64 { return time.Now().UnixMilli() }

// AfterFunc is setTimeout: job runs as a section once d has elapsed (see nodeDelay), unless the timer is stopped first.
func (l *Loop) AfterFunc(d time.Duration, job func()) Timer {
	t := &loopTimer{}
	t.timer = time.AfterFunc(nodeDelay(d), func() {
		l.Run(func() {
			if t.state.CompareAndSwap(timerPending, timerFired) {
				job()
			}
		})
	})
	return t
}

const (
	timerPending int32 = iota
	timerFired
	timerStopped
)

// loopTimer is a Loop timer. A job that was due but had not entered the loop when Stop ran does not run: Node runs a
// timer's callback only from its timer phase, after any clearTimeout before it.
type loopTimer struct {
	state atomic.Int32
	timer *time.Timer
}

// Stop implements Timer.
func (t *loopTimer) Stop() bool {
	if !t.state.CompareAndSwap(timerPending, timerStopped) {
		return false
	}
	t.timer.Stop()
	return true
}

// nodeTimeoutMax is Node's TIMEOUT_MAX (lib/internal/timers.js), the longest delay a timer honors: 2^31-1 ms.
const nodeTimeoutMax = (1<<31 - 1) * time.Millisecond

// nodeDelay is the wait Node's setTimeout gives a delay of d: a delay outside [1 ms, TIMEOUT_MAX] runs after 1 ms, and the
// rest is truncated to whole milliseconds. Every timer this package ports waits nodeDelay of its delay.
func nodeDelay(d time.Duration) time.Duration {
	if d < time.Millisecond || d > nodeTimeoutMax {
		return time.Millisecond
	}
	return d.Truncate(time.Millisecond)
}
