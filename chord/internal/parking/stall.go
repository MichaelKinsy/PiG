package parking

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
)

// stall is what Watch installs: how long a section waits for its handler before it reports, and what the report does.
type stall struct {
	after time.Duration
	// fire is the report, made once for every guard.
	fire func()
}

// watching is the installed stall report; nil (the default, and every production binary) means Guard arms nothing: no timer,
// no allocation.
var watching atomic.Pointer[stall]

// Watch makes every Guard that outlasts after report once to report, with the text and the stacks of every goroutine (the
// handler's among them). It returns the function that restores what Watch replaced. A report changes nothing about the wait:
// the section keeps waiting, and a handler that parks late goes on as one that parks at once. Nothing in the host calls
// Watch: a test binary that wants misuse of the handler contract visible installs it in TestMain.
func Watch(after time.Duration, report func(text string)) (restore func()) {
	w := &stall{after: after}
	w.fire = func() {
		stacks := make([]byte, 1<<20)
		stacks = stacks[:runtime.Stack(stacks, true)]
		report(fmt.Sprintf("chord: a method handler has held the section that admitted its call for %s without returning or parking (waiting on a raw channel or a lock before its first host call?); use chordctx.Receive or chordctx.Await, or wait with a host primitive:\n%s\n", after, stacks))
	}
	previous := watching.Swap(w)
	return func() { watching.Store(previous) }
}

// Armed is a guard that is armed, or the nothing Guard returns without Watch.
type Armed struct{ timer *time.Timer }

// Guard arms the stall report for a section that is about to wait for its handler to return or park; Disarm ends it. A
// handler that holds the section for the Watch duration without returning or parking waits for something that is not a host
// primitive (a raw channel receive, a lock) before its first host call, which Pi's `await` would not do. Without Watch it is
// one atomic load.
func Guard() Armed {
	w := watching.Load()
	if w == nil {
		return Armed{}
	}
	return Armed{timer: time.AfterFunc(w.after, w.fire)}
}

// Disarm stops the report of a guard that has not fired.
func (a Armed) Disarm() {
	if a.timer != nil {
		a.timer.Stop()
	}
}
