package parking

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
)

// stall is what Watch installs: how long a section waits for its handler before it reports, and where the report goes.
type stall struct {
	after  time.Duration
	report func(text string)
}

// watching is the installed stall report; nil (the default, and every production binary) means Wait is the bare channel
// receive: no timer, no allocation.
var watching atomic.Pointer[stall]

// Watch makes every Wait that outlasts after report once to report, with the text and the stacks of every goroutine (the
// handler's among them), and go on waiting. It returns the function that restores what Watch replaced. A report changes
// nothing about the wait: the section keeps waiting, and a handler that parks late goes on as one that parks at once.
// Nothing in the host calls Watch: a test binary that wants misuse of the handler contract visible installs it in TestMain.
func Watch(after time.Duration, report func(text string)) (restore func()) {
	previous := watching.Swap(&stall{after: after, report: report})
	return func() { watching.Store(previous) }
}

// wait is Wait. A handler that holds the section for the Watch duration without returning or parking waits for something
// that is not a host primitive (a raw channel receive, a lock) before its first host call, which Pi's `await` would not do.
func (s *Signal) wait() {
	w := watching.Load()
	if w == nil {
		<-s.done
		return
	}
	timer := time.NewTimer(w.after)
	defer timer.Stop()
	select {
	case <-s.done:
		return
	case <-timer.C:
	}
	stacks := make([]byte, 1<<20)
	stacks = stacks[:runtime.Stack(stacks, true)]
	w.report(fmt.Sprintf("chord: a method handler has held the section that admitted its call for %s without returning or parking (waiting on a raw channel or a lock before its first host call?); use chordctx.Receive or chordctx.Await, or wait with a host primitive:\n%s\n", w.after, stacks))
	<-s.done
}
