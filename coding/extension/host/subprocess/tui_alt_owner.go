package subprocess

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi runs everything of a TuiAltScreen on its one event loop: the input handler (tui.ts:995-997 hands the terminal's data to handleTerminalInput), the throttled
// frame (requestRender arms one setTimeout, :1066-1119) and the calls of the extension's own code, which are macrotasks of that loop, so a component's render and
// its handleInput never run at the same time. The interactive mode keeps that loop for the main screen: the renderer asks it for the frame deadline
// (SetRenderDeadline, run by RunDueRender) and for owned ticks (SetTickDispatcher), and the loop is the goroutine that runs them
// (internal/codingagent frame_timer.go, owner_loop.go). That loop must not wait on an extension's answer (AGENTS.md: no blocking extension IPC on the
// TUI input and render loop), and a screen that an extension builds belongs to no interactive mode, so each such screen has an owner of its own that serves the
// renderer through the same seams.
//
// The owner is one goroutine, started with the screen and ended with it (dispose, the death of the extension's connection, a shutdown). It runs, in order,
// the screen's frames, its owned ticks and every `tui.alt` call that is not served on a callback's own waiting goroutine (see callback and serveOnOwner): the
// component callbacks of a screen (render, input, mouse, focus, listeners, options, the terminal's own) are all sent by it, so the extension sees one at a time.
//
// Lock order, outermost first: the owner (the goroutine, not a lock) > the renderer's frame lock (mu) > the base container's lock > the renderer's writer lock
// (outMu) > this struct's mu, waitMu and the bridge's s.mu, which are leaves. Nothing that holds a renderer lock waits for the owner: the goroutines that wait
// for it (do) are the connection's request goroutines and the renderer's timer goroutines, which hold none, and the renderer calls armFrame with its frame lock
// held, which takes only o.mu and a non-blocking send.
type tuiAltOwner struct {
	screen *tui.TuiAltScreen
	// ctx ends the owner (the screen's context, ended by disposeTuiAlt); end is the cancel function that ends it, called when the owner returns, so a callback
	// that waits on the extension ends with the loop that sent it.
	ctx context.Context
	end context.CancelFunc
	// connDone is closed when the extension's connection has closed: the owner of a screen the extension never disposed ends with it. A nil channel never closes.
	connDone <-chan struct{}

	// gid is the owner goroutine's token (goroutineid.Token) while it runs, 0 outside it, which no token is (onOwner).
	gid atomic.Uintptr
	// wake is a one-place signal that tasks or the frame deadline changed.
	wake chan struct{}
	// exited is closed when the owner goroutine has returned.
	exited chan struct{}

	mu sync.Mutex
	// tasks are the posted tasks, in order. closed is set when the loop returns: a task posted after it is dropped and a caller that waits for one is released.
	tasks  []func()
	closed bool
	// closing ends the loop once the task it runs has returned (close).
	closing bool
	// due is the deadline of the renderer's throttled frame, zero when none is pending (armFrame).
	due time.Time
}

var errTuiAltScreenEnded = errors.New("tui.alt: the screen has ended")

func newTuiAltOwner(ctx context.Context, end context.CancelFunc, connDone <-chan struct{}, screen *tui.TuiAltScreen) *tuiAltOwner {
	return &tuiAltOwner{screen: screen, ctx: ctx, end: end, connDone: connDone, wake: make(chan struct{}, 1), exited: make(chan struct{})}
}

// start runs the owner and installs it as the renderer's frame, tick and owner loop. It is called once, before the screen is reachable by a call.
func (o *tuiAltOwner) start() {
	o.screen.SetRenderDeadline(o.armFrame)
	o.screen.SetTickDispatcher(func(fn func()) { o.post(fn) })
	//portlint:allow golifetime the owner is the screen's owned task: disposeTuiAlt and the dispose call end it through the screen's context (or close), and wait drains it
	go o.run()
}

// onOwner reports whether the caller is the owner goroutine. A nil owner (a screen state a test built without one) has none.
func (o *tuiAltOwner) onOwner() bool {
	return o != nil && o.gid.Load() == goroutineid.Token()
}

func (o *tuiAltOwner) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// armFrame moves the frame deadline to due; the zero time disarms it. The renderer calls it with its frame lock held, so it takes only o.mu and never waits.
func (o *tuiAltOwner) armFrame(due time.Time) {
	o.mu.Lock()
	o.due = due
	o.mu.Unlock()
	o.signal()
}

// post queues fn on the owner, after the tasks already queued, and reports whether the owner took it: a task posted to an owner that has returned is dropped.
func (o *tuiAltOwner) post(fn func()) bool {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return false
	}
	o.tasks = append(o.tasks, fn)
	o.mu.Unlock()
	o.signal()
	return true
}

// do runs fn on the owner and returns when it has run: the caller that is the owner runs it at once, as Pi's one thread does for a call from inside a task. It
// returns an error when the owner returned before it ran fn.
func (o *tuiAltOwner) do(fn func()) error {
	if o.onOwner() {
		fn()
		return nil
	}
	done := make(chan struct{})
	if !o.post(func() {
		defer close(done)
		fn()
	}) {
		return errTuiAltScreenEnded
	}
	select {
	case <-done:
		return nil
	case <-o.exited:
		// The task may have run just before the owner returned.
		select {
		case <-done:
			return nil
		default:
			return errTuiAltScreenEnded
		}
	}
}

// close ends the owner once the task that calls it has returned.
func (o *tuiAltOwner) close() {
	o.mu.Lock()
	o.closing = true
	o.mu.Unlock()
	o.signal()
}

// wait returns once the owner goroutine has returned. The owner itself does not wait for itself: it returns after the task that asks.
func (o *tuiAltOwner) wait() {
	if o == nil || o.onOwner() {
		return
	}
	<-o.exited
}

func (o *tuiAltOwner) run() {
	o.gid.Store(goroutineid.Token())
	defer func() {
		o.mu.Lock()
		o.closed = true
		clear(o.tasks)
		o.tasks = nil
		o.mu.Unlock()
		o.gid.Store(0)
		o.end()
		close(o.exited)
	}()
	// A stopped timer, made once: the loop arms it for the frame deadline and stops it when it wakes (a channel-based timer delivers no tick of a deadline that Stop or Reset replaced), as the main screen's frame timer does.
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	for o.ctx.Err() == nil {
		select {
		case <-o.connDone:
			return
		default:
		}
		o.mu.Lock()
		if o.closing {
			o.mu.Unlock()
			return
		}
		due := o.due
		var task func()
		if len(o.tasks) > 0 {
			task = o.tasks[0]
			o.tasks[0] = nil
			o.tasks = o.tasks[1:]
		}
		o.mu.Unlock()
		if !due.IsZero() && time.Until(due) <= 0 {
			// The renderer runs its due frame (RunDueRender checks its own clock); a frame requested while it ran armed a later deadline.
			o.screen.RunDueRender()
			o.mu.Lock()
			if o.due.Equal(due) {
				o.due = time.Time{}
			}
			o.mu.Unlock()
		}
		if task != nil {
			task()
			continue
		}
		o.mu.Lock()
		due, pending := o.due, len(o.tasks) > 0
		o.mu.Unlock()
		if pending {
			continue
		}
		var deadline <-chan time.Time
		if !due.IsZero() {
			timer.Reset(max(time.Until(due), 0))
			deadline = timer.C
		}
		select {
		case <-o.wake:
		case <-deadline:
		case <-o.ctx.Done():
		case <-o.connDone:
		}
		timer.Stop()
	}
}
