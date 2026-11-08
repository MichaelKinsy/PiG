package surfaces

import (
	"context"
	"sync"
	"sync/atomic"
)

// The connection loop (pi: packages/chord/src/surfaces/connection.ts; slice 11 §2, decision "Slice 11, P6"; p2's
// `keepConnected`): a client keeps one link to the server and replaces it when it ends. After a drop it reconnects after a
// growing pause (250 ms doubling to 5 s; a connection that stayed open 10 s resets it), at once on a wake (visible again,
// back online, network changed); it replaces an open socket silent past the server's ping interval on a wake, gives up a
// socket not open after 20 s, and stops when the app's `CanRetry` says no (an outdated client, a guest's ended share). The
// environment (time, timers, wakes) is given, so the same loop runs in a page and in a terminal's sandbox.
//
// Pi runs the loop on one thread: every event (a socket's, a timer's, a wake's, the retry answer) runs to its end before
// the next. The loop runs each event through an outbox, so the same holds here with no lock held while the app's callbacks
// (`Open`, `OnState`, a link's `Close`, the environment's methods) run, and one of them may call the loop back (Stop).
// `CanRetry` is the one blocking call: it runs on a goroutine of its own, its answer delivered as an event.

// The first retry pause after a failed or short-lived connection: 250 ms, doubling from there.
const RetryInitialMs = 250

// The longest retry pause: 5 s (and the pause while offline).
const RetryMaxMs = 5000

// A connection that stayed open this long counted as working: the next retry starts at the initial pause again.
const StableMs = 10_000

// A socket not open after this long is given up (browsers have no handshake timeout).
const ConnectTimeoutMs = 20_000

// The server's heartbeat pings every 15 s: an open socket silent for longer than this is presumed dead on a wake.
const StaleMs = 25_000

// After a wake, messages held while the client was frozen get this long to arrive before the socket is judged.
const WakeGraceMs = 1000

// ConnectionState is the connection as the app shows it: an attempt under way, open, or waiting for the next attempt.
type ConnectionState string

const (
	Connecting ConnectionState = "connecting"
	Connected  ConnectionState = "connected"
	Waiting    ConnectionState = "waiting"
)

// SocketEvents is what one socket reports to the loop.
type SocketEvents interface {
	Opened()
	// Received is any frame: the server's pings keep an open socket from looking silent.
	Received()
}

// LoopLink is one attempt's connection as the loop sees it: it closes once, and closing it ends it at once.
type LoopLink interface {
	// OnClose registers the listener the link calls once when it closes (also when the loop closed it).
	OnClose(listener func())
	Close()
}

// LoopEnvironment is what the loop runs in: the clock, timers, whether the device is online, and its wake events.
type LoopEnvironment struct {
	// Now is the clock in milliseconds.
	Now func() int64
	// After runs callback after ms milliseconds (on any goroutine); it returns its cancel.
	After func(ms int64, callback func()) (cancel func())
	// Online is false when the device is known to be offline.
	Online func() bool
	// OnWake calls listener when the client may have been away (visible again, online, network changed); it returns its removal.
	OnWake func(listener func()) (remove func())
}

// LoopOptions is what the loop takes.
type LoopOptions struct {
	// Open opens one socket and its link; the socket reports to events.
	Open    func(events SocketEvents) LoopLink
	OnState func(state ConnectionState)
	// CanRetry is asked after every close; false stops the loop. An error counts as true (unreachable, try again). Its context
	// ends at Stop.
	CanRetry    func(ctx context.Context) (bool, error)
	Environment LoopEnvironment
}

// Connection is a running connection loop.
type Connection struct{ loop *loop }

// KeepConnected keeps a client connected until Stop.
func KeepConnected(options LoopOptions) *Connection {
	//portlint:allow ctxbackground Pi's `keepConnected(options)` takes no signal: the loop's lifetime is its `stop()`, which cancels this context
	ctx, cancel := context.WithCancel(context.Background())
	l := &loop{options: options, env: options.Environment, ctx: ctx, cancel: cancel}
	l.box.run(func() {
		l.removeWake = l.env.OnWake(func() { l.box.run(l.wake) })
		l.connect()
	})
	return &Connection{loop: l}
}

// Stop stops the loop and closes the current link; no other is opened. It is safe to call from the loop's own callbacks
// (it then runs once they return).
func (c *Connection) Stop() { c.loop.box.run(c.loop.stop) }

// settled waits for the retry questions in flight (the tests', so a fake clock advances over a settled loop).
func (c *Connection) settled() { c.loop.asking.Wait() }

// attempt is one attempt: its link, when it started and opened, its last frame, its timeout's cancel.
type attempt struct {
	link          LoopLink
	opened        bool
	openedAt      int64
	lastFrame     int64
	cancelTimeout func()
}

// attemptEvents is one attempt's SocketEvents.
type attemptEvents struct {
	loop *loop
	at   *attempt
}

func (e attemptEvents) Opened() {
	e.loop.box.run(func() {
		l := e.loop
		// A stale open: the loop stopped (its link's close is queued behind this open) or the attempt ended.
		if l.stopped || l.current != e.at {
			return
		}
		e.at.opened = true
		e.at.openedAt = l.env.Now()
		e.at.lastFrame = e.at.openedAt
		e.at.cancelTimeout()
		l.options.OnState(Connected)
	})
}

func (e attemptEvents) Received() {
	e.loop.box.run(func() { e.at.lastFrame = e.loop.env.Now() })
}

type loop struct {
	options    LoopOptions
	env        LoopEnvironment
	box        outbox
	ctx        context.Context
	cancel     context.CancelFunc
	asking     sync.WaitGroup
	removeWake func()
	// The fields below are touched only by events, which run one at a time.
	failures         int
	current          *attempt
	cancelRetry      func()
	cancelStaleCheck func()
	// deciding is between a close and CanRetry's answer; a wake meanwhile retries at once.
	deciding          bool
	wokeWhileDeciding bool
	stopped           bool
}

// connect starts an attempt.
func (l *loop) connect() {
	l.cancelRetry = nil
	env := l.env
	at := &attempt{lastFrame: env.Now(), cancelTimeout: func() {}}
	l.current = at
	l.options.OnState(Connecting)
	link := l.options.Open(attemptEvents{loop: l, at: at})
	at.link = link
	at.cancelTimeout = l.after(ConnectTimeoutMs, func() {
		if !at.opened {
			link.Close()
		}
	})
	link.OnClose(func() { l.box.run(func() { l.closed(at) }) })
}

// stop stops the loop and closes the current link.
func (l *loop) stop() {
	if l.stopped {
		return
	}
	l.stopped = true
	l.cancel()
	l.removeWake()
	if l.cancelRetry != nil {
		l.cancelRetry()
		l.cancelRetry = nil
	}
	if l.current != nil && l.current.link != nil {
		l.current.link.Close()
	}
}

// closed is at's link closing: it asks CanRetry, then schedules the next attempt.
func (l *loop) closed(at *attempt) {
	if l.current != at {
		return
	}
	l.current = nil
	at.cancelTimeout()
	if l.cancelStaleCheck != nil {
		l.cancelStaleCheck()
		l.cancelStaleCheck = nil
	}
	if l.stopped {
		return
	}
	l.options.OnState(Waiting)
	if at.opened && l.env.Now()-at.openedAt >= StableMs {
		l.failures = 0
	}
	l.deciding = true
	l.wokeWhileDeciding = false
	l.asking.Go(func() {
		retry, err := l.options.CanRetry(l.ctx)
		if err != nil {
			retry = true
		}
		l.box.run(func() {
			l.deciding = false
			if retry {
				l.schedule(l.wokeWhileDeciding)
			}
		})
	})
}

// schedule schedules the next attempt: at once (now), else after the backoff (offline: the longest pause).
func (l *loop) schedule(now bool) {
	if l.stopped {
		return
	}
	backoff := int64(RetryMaxMs)
	if l.failures < 5 {
		backoff = min(int64(RetryMaxMs), int64(RetryInitialMs)<<l.failures)
	}
	l.failures++
	// Offline an attempt fails at once; the `online` wake retries sooner than this.
	delay := int64(0)
	switch {
	case now:
	case l.env.Online():
		delay = backoff
	default:
		delay = RetryMaxMs
	}
	l.cancelRetry = l.after(delay, l.connect)
}

// wake: a pending retry runs now; an open socket silent too long is replaced once its overdue ping is checked.
func (l *loop) wake() {
	if l.cancelRetry != nil {
		l.cancelRetry()
		l.connect()
		return
	}
	if l.deciding {
		l.wokeWhileDeciding = true
		return
	}
	at := l.current
	// A connecting attempt has its own timeout.
	if at == nil || !at.opened {
		return
	}
	if l.cancelStaleCheck != nil {
		l.cancelStaleCheck()
	}
	// After a network change the socket looks fine until the server's next ping is missing: check when it is due.
	due := max(int64(WakeGraceMs), at.lastFrame+StaleMs+1-l.env.Now())
	l.cancelStaleCheck = l.after(due, func() {
		l.cancelStaleCheck = nil
		if l.current == at && l.env.Now()-at.lastFrame > StaleMs {
			at.link.Close()
		}
	})
}

// after runs fn as an event after ms. Its cancel is as sure as Pi's: a timer already firing, its event queued behind the one
// that cancels it, does nothing.
func (l *loop) after(ms int64, fn func()) (cancel func()) {
	var cancelled atomic.Bool
	remove := l.env.After(ms, func() {
		l.box.run(func() {
			if !cancelled.Load() {
				fn()
			}
		})
	})
	return func() {
		cancelled.Store(true)
		remove()
	}
}
