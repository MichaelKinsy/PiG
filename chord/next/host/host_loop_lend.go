package host

import (
	"iter"
	"runtime"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// The first-suspension handoff of a method handler (DESIGN-go-layout.md section 10). Pi runs an async handler to its first
// `await` inside the section that admits its call. A Go handler is a blocking function, so it runs as a coroutine of that
// section (iter.Pull): the section's goroutine switches to the handler's, which runs until it returns or parks, and switches
// back. The two switches are direct, with no wake-up of a goroutine, and the handler's goroutine is reused by the next call.
// The rest of the handler (what Pi runs in later microtasks) runs once the section is over.

// lendRun is what a handler's coroutine runs: the call it was lent to, and nothing else once the call is over.
type lendRun struct {
	loop   *Loop
	signal *parking.Signal
	fn     func(abandoned bool)
	// goexited is set once fn ended its goroutine: the coroutine is spent, and its goroutine is left to finish ending.
	goexited bool
	// lent is whether the section was lent: the handler's goroutine owns the loop until it returns or parks.
	lent bool
	// busy is set from the lend until fn returned; a coroutine that yielded while busy parked.
	busy bool
	// yield gives control back to whoever resumed the coroutine: at the handler's park, and when it is idle.
	yield func()
}

// exec runs the call: fn is the handler and its settlement.
func (r *lendRun) exec() {
	loop := r.loop
	signal := r.signal
	fn := r.fn
	done := false
	defer func() {
		if !done {
			// fn did not return: it panicked (the panic goes on to whoever drives the coroutine, as iter.Pull carries it) or it
			// ended its goroutine with runtime.Goexit, which iter.Pull would carry to the goroutine that drives the handler: the
			// section's, or the one that ended it. A throw in Pi rejects the call and leaves the host's loop and its callers
			// as they were: so does this.
			if recovered := recover(); recovered != nil {
				r.finish()
				panic(recovered)
			}
			r.goexited = true
			// The loop's owner goes back to the section and the section goes on, as at the handler's park: what is left of the
			// handler is its rejection, which runs after the section as the settlement of a handler that returned does.
			signal.Return()
			fn(true)
			r.finish()
			// The goroutine is still ending: it stops here until stop lets it finish, and the goroutine that drives the
			// coroutine takes it as the end of the call (finish of the loop).
			r.yield()
			return
		}
		r.finish()
	}()
	id := goroutineid.Current()
	signal.Bind(id, r.yield)
	if r.lent {
		loop.owner.Store(id)
	}
	fn(false)
	done = true
}

// finish is the end of a call: an idle coroutine holds nothing of the call it served.
func (r *lendRun) finish() {
	loop := r.loop
	r.fn, r.signal, r.loop = nil, nil, nil
	r.busy = false
	loop.tasks.Done()
}

// coroutine is a handler's coroutine and its goroutine, as the loop keeps them. The goroutine reaches the run and nothing of
// the coroutine, so an idle one the loop dropped is collected, and its cleanup stops the goroutine.
type coroutine struct {
	run  *lendRun
	next func() (struct{}, bool)
	stop func()
	// cleanup stops the goroutine of a coroutine the loop dropped.
	cleanup runtime.Cleanup
}

func newCoroutine() *coroutine {
	run := &lendRun{}
	next, stop := iter.Pull(func(yield func(struct{}) bool) {
		run.yield = func() { yield(struct{}{}) }
		for {
			run.exec()
			if !yield(struct{}{}) {
				return
			}
		}
	})
	c := &coroutine{run: run, next: next, stop: stop}
	c.cleanup = runtime.AddCleanup(c, func(stop func()) { stop() }, stop)
	return c
}

// resumption is a handler that parked (or returned) inside a section: what is left of it runs once that section is over.
type resumption struct {
	c *coroutine
	// inline is whether it runs on the goroutine that ended the section: a handler that returned has only its settlement
	// left. One that parked waits for something, on a goroutine of its own.
	inline bool
}

// idleCoroutines is how many idle coroutines a loop keeps.
const idleCoroutines = 8

func (l *Loop) coroutine() *coroutine {
	l.mu.Lock()
	if n := len(l.idle); n > 0 {
		c := l.idle[n-1]
		l.idle[n-1] = nil
		l.idle = l.idle[:n-1]
		l.mu.Unlock()
		return c
	}
	l.mu.Unlock()
	return newCoroutine()
}

// recycle keeps a coroutine whose call is over for the next one, or stops it. A goroutine is kept once goroutineid has proved
// where the goroutine ID lives: until then Current formats the stack header on every call, and a loop that reuses a few
// goroutines would never end that (every section takes the ID of its goroutine).
func (l *Loop) recycle(c *coroutine) {
	if !goroutineid.Proving() {
		l.mu.Lock()
		if len(l.idle) < idleCoroutines {
			l.idle = append(l.idle, c)
			l.mu.Unlock()
			return
		}
		l.mu.Unlock()
	}
	c.stop()
}

// Lend runs fn as the synchronous part of an async function inside the running section: as a coroutine of the section's
// goroutine, which switches to it and waits until fn returns or raises signal (a Park of its ctx). Meanwhile the loop's
// owner is fn's goroutine: its calls to Run run inline, as nested synchronous calls do in Pi, and the rest of the section
// does not run. Raise gives the loop back to the section. What is left of fn runs after the section, as one of Pi's later
// microtasks: on the goroutine that ended the section when fn returned (Signal.Return), on a goroutine of its own when it
// parked. fn is an owned background task as Go's. Lend called outside a section lends nothing: the calls of fn to Run wait
// for the loop as any goroutine's do, and Lend returns when fn raises signal or returns.
//
// fn that ends its goroutine (runtime.Goexit, as testing.T.FailNow does) before it returns is a throw that nobody catches:
// Pi's awaited handler that throws rejects its call and leaves everything else as it was. iter.Pull would carry the Goexit
// to the goroutine that drives the coroutine, ending the caller's goroutine, and the call would never settle, with the loop
// owned by a goroutine that is gone. Instead Lend hands the section back as at a return, calls fn again with abandoned true
// (the rejection fn would have settled the call with) on the handler's goroutine, and lets only that goroutine end.
func (l *Loop) Lend(signal *parking.Signal, fn func(abandoned bool)) {
	section := goroutineid.Current()
	lent := l.owner.Load() == section
	if lent {
		signal.Lend(&l.owner, section)
	}
	c := l.coroutine()
	run := c.run
	run.loop, run.signal, run.fn, run.lent, run.busy = l, signal, fn, lent, true
	l.tasks.Add(1)
	l.switchTo(c)
	if !run.busy {
		// fn returned without raising the signal.
		if lent && !signal.Raised() {
			l.owner.Store(section)
		}
		l.recycle(c)
		return
	}
	if !lent {
		l.Go(func() { l.finish(c) })
		return
	}
	l.pending = append(l.pending, resumption{c: c, inline: signal.Returned()})
}

// switchTo runs the coroutine until it yields, and reports a handler that does not (parking.Guard).
func (l *Loop) switchTo(c *coroutine) {
	defer parking.Guard().Disarm()
	c.next()
}

// finish runs what is left of a handler that parked, then keeps its coroutine. One whose handler ended its goroutine is spent:
// its goroutine is still ending, and stop lets it finish, which iter.Pull reports to whoever stops the coroutine as the
// Goexit, so it is stopped by a task of the loop of its own, where the Goexit ends nothing else. The coroutine's cleanup is
// cancelled first (it holds the coroutine, so it has not run): the runtime's cleanup goroutine must not stop it as well, with
// the Goexit landing there and two stops of one iter.Pull at once.
func (l *Loop) finish(c *coroutine) {
	c.next()
	if c.run.goexited {
		c.cleanup.Stop()
		l.Go(func() { c.stop() })
		return
	}
	l.recycle(c)
}

// resumeAfter runs the handlers that parked or returned in the section that just ended, in the order they did.
func (l *Loop) resumeAfter(resumed []resumption) {
	for _, each := range resumed {
		if each.inline {
			l.finish(each.c)
			continue
		}
		l.Go(func() { l.finish(each.c) })
	}
}
